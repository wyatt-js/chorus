// chorusaudio — a small CoreAudio helper for chorus.
//
//   chorusaudio list
//       Print output-capable audio devices as TSV: uid<TAB>transport<TAB>name
//
//   chorusaudio bt-list
//       Print paired Bluetooth audio devices as TSV:
//       address<TAB>connected(0|1)<TAB>name
//
//   chorusaudio bt-connect --address <addr>
//       Open a connection to a paired Bluetooth device (so it comes online as a
//       CoreAudio output). Exits 0 once connected, non-zero on failure.
//
//   chorusaudio render --device-uid <uid>
//       Read s16le / 44100Hz / stereo PCM from stdin and play it to the device
//       with the given UID (e.g. a paired Bluetooth soundbar).
//
//   chorusaudio record [--seconds <n>]
//       Capture the default input device (the Mac's mic) as s16le/48000/mono to
//       stdout for n seconds (default 5). Used by acoustic calibration.
//
// Rendering uses an AUHAL output unit aimed at a specific device with a render
// callback — the reliable way to play to a non-default device on macOS.

import AudioToolbox
import CoreAudio
import Darwin
import Foundation
import IOBluetooth

// MARK: - CoreAudio property helpers

func systemDevices() -> [AudioDeviceID] {
  var addr = AudioObjectPropertyAddress(
    mSelector: kAudioHardwarePropertyDevices,
    mScope: kAudioObjectPropertyScopeGlobal,
    mElement: kAudioObjectPropertyElementMain)
  var dataSize: UInt32 = 0
  guard AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &dataSize) == noErr else {
    return []
  }
  let count = Int(dataSize) / MemoryLayout<AudioDeviceID>.size
  var ids = [AudioDeviceID](repeating: 0, count: count)
  guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &dataSize, &ids) == noErr else {
    return []
  }
  return ids
}

func stringProperty(_ dev: AudioDeviceID, _ selector: AudioObjectPropertySelector) -> String? {
  var addr = AudioObjectPropertyAddress(
    mSelector: selector, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
  var size = UInt32(MemoryLayout<CFString?>.size)
  var value: CFString? = nil
  let status = withUnsafeMutablePointer(to: &value) {
    AudioObjectGetPropertyData(dev, &addr, 0, nil, &size, $0)
  }
  guard status == noErr, let s = value else { return nil }
  return s as String
}

func hasOutputStreams(_ dev: AudioDeviceID) -> Bool {
  var addr = AudioObjectPropertyAddress(
    mSelector: kAudioDevicePropertyStreams,
    mScope: kAudioObjectPropertyScopeOutput,
    mElement: kAudioObjectPropertyElementMain)
  var size: UInt32 = 0
  guard AudioObjectGetPropertyDataSize(dev, &addr, 0, nil, &size) == noErr else { return false }
  return size > 0
}

func transportString(_ dev: AudioDeviceID) -> String {
  var addr = AudioObjectPropertyAddress(
    mSelector: kAudioDevicePropertyTransportType,
    mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain)
  var transport: UInt32 = 0
  var size = UInt32(MemoryLayout<UInt32>.size)
  guard AudioObjectGetPropertyData(dev, &addr, 0, nil, &size, &transport) == noErr else { return "unknown" }
  switch transport {
  case kAudioDeviceTransportTypeBuiltIn: return "builtin"
  case kAudioDeviceTransportTypeBluetooth, kAudioDeviceTransportTypeBluetoothLE: return "bluetooth"
  case kAudioDeviceTransportTypeUSB: return "usb"
  case kAudioDeviceTransportTypeHDMI: return "hdmi"
  case kAudioDeviceTransportTypeAirPlay: return "airplay"
  case kAudioDeviceTransportTypeVirtual, kAudioDeviceTransportTypeAggregate: return "virtual"
  default: return "other"
  }
}

func deviceID(forUID uid: String) -> AudioDeviceID? {
  for dev in systemDevices() where hasOutputStreams(dev) {
    if stringProperty(dev, kAudioDevicePropertyDeviceUID) == uid { return dev }
  }
  return nil
}

func die(_ msg: String) -> Never {
  FileHandle.standardError.write("chorusaudio: \(msg)\n".data(using: .utf8)!)
  exit(1)
}

// MARK: - Ring buffer (byte FIFO shared between the stdin reader and the render callback)

final class RingBuffer {
  private var buf: [UInt8]
  private var head = 0
  private var tail = 0
  private var filled = 0
  private var lock = os_unfair_lock()

  init(capacity: Int) { buf = [UInt8](repeating: 0, count: capacity) }

  // write copies up to n bytes into the ring without overwriting unread data,
  // returning the count actually written (less than n when the ring is full).
  // The render pump applies backpressure on a short write — never dropping — so
  // an upstream delay (prepended silence for a per-device offset) accumulates and
  // plays out instead of being discarded here. (The mic-capture path ignores the
  // count: a full ring there just skips the newest frames.)
  @discardableResult
  func write(_ src: UnsafePointer<UInt8>, _ n: Int) -> Int {
    os_unfair_lock_lock(&lock)
    defer { os_unfair_lock_unlock(&lock) }
    let cap = buf.count
    let toWrite = min(n, cap - filled)
    for i in 0..<toWrite {
      buf[tail] = src[i]
      tail = (tail + 1) % cap
      filled += 1
    }
    return toWrite
  }

  // read copies up to n bytes into dst, returning the count actually copied.
  func read(_ dst: UnsafeMutablePointer<UInt8>, _ n: Int) -> Int {
    os_unfair_lock_lock(&lock)
    defer { os_unfair_lock_unlock(&lock) }
    let cap = buf.count
    let toCopy = min(n, filled)
    for i in 0..<toCopy {
      dst[i] = buf[head]
      head = (head + 1) % cap
    }
    filled -= toCopy
    return toCopy
  }
}

// MARK: - Input capture (realtime thread): pull mic frames out of the unit into the ring

// RecordContext carries what the input callback needs across the C boundary: the
// unit to render from and the ring to stash captured frames in, plus a reusable
// scratch buffer so the realtime thread never allocates.
final class RecordContext {
  let unit: AudioUnit
  let ring: RingBuffer
  let scratch: UnsafeMutableRawPointer
  let scratchSize: Int
  init(unit: AudioUnit, ring: RingBuffer) {
    self.unit = unit
    self.ring = ring
    self.scratchSize = 1 << 16  // far larger than any input buffer slice
    self.scratch = UnsafeMutableRawPointer.allocate(byteCount: scratchSize, alignment: 16)
  }
}

let inputCallback: AURenderCallback = { (inRefCon, ioActionFlags, inTimeStamp, inBusNumber, inNumberFrames, _) -> OSStatus in
  let ctx = Unmanaged<RecordContext>.fromOpaque(inRefCon).takeUnretainedValue()
  let bytesNeeded = Int(inNumberFrames) * 2  // mono s16 = 2 bytes/frame
  if bytesNeeded > ctx.scratchSize { return noErr }
  var abl = AudioBufferList(
    mNumberBuffers: 1,
    mBuffers: AudioBuffer(mNumberChannels: 1, mDataByteSize: UInt32(bytesNeeded), mData: ctx.scratch))
  let status = AudioUnitRender(ctx.unit, ioActionFlags, inTimeStamp, inBusNumber, inNumberFrames, &abl)
  if status == noErr {
    ctx.ring.write(ctx.scratch.assumingMemoryBound(to: UInt8.self), bytesNeeded)
  }
  return status
}

// MARK: - Render callback (realtime thread): pull PCM from the ring into the unit's buffer

let renderCallback: AURenderCallback = { (inRefCon, _, _, _, inNumberFrames, ioData) -> OSStatus in
  let ring = Unmanaged<RingBuffer>.fromOpaque(inRefCon).takeUnretainedValue()
  guard let abl = ioData else { return noErr }
  let buffers = UnsafeMutableAudioBufferListPointer(abl)
  let bytesNeeded = Int(inNumberFrames) * 4  // 16-bit stereo = 4 bytes/frame
  let buf = buffers[0]
  guard let raw = buf.mData else { return noErr }
  let dst = raw.assumingMemoryBound(to: UInt8.self)
  let got = ring.read(dst, bytesNeeded)
  if got < bytesNeeded {
    memset(dst + got, 0, bytesNeeded - got)  // underrun -> silence
  }
  return noErr
}

// writeBlocking writes all n bytes into the ring, waiting (a couple ms at a time)
// for the realtime render callback to free space when it's full. Blocking here
// propagates backpressure up the pipe to the Go feeder, so a per-device offset's
// prepended silence accumulates upstream and delays playback instead of being
// dropped — the realtime callback paces the whole chain.
func writeBlocking(_ ring: RingBuffer, _ src: UnsafePointer<UInt8>, _ n: Int) {
  var off = 0
  while off < n {
    off += ring.write(src + off, n - off)
    if off < n { usleep(2000) }
  }
}

// MARK: - Commands

func runList() {
  for dev in systemDevices() where hasOutputStreams(dev) {
    let uid = stringProperty(dev, kAudioDevicePropertyDeviceUID) ?? ""
    let name = stringProperty(dev, kAudioObjectPropertyName) ?? "(unknown)"
    print("\(uid)\t\(transportString(dev))\t\(name)")
  }
}

// MARK: - Bluetooth (IOBluetooth)

// NameProbe collects the addresses of devices that answer a baseband name
// request — i.e. are powered on and in range. The async name-request callback
// (target/selector) fires on the run loop the probe drives.
final class NameProbe: NSObject {
  private(set) var reachable = Set<String>()
  private var pending = 0

  // probe fires a name request at each device and runs the run loop until every
  // request completes or the deadline passes. A reachable device answers quickly;
  // an off/out-of-range one consumes its full page timeout. The controller
  // serializes paging, so the overall budget scales with the device count — that
  // keeps a reachable device late in the queue from being cut off.
  func probe(_ devices: [IOBluetoothDevice], perDevice: TimeInterval) {
    // BluetoothHCIPageTimeout is in 0.625ms units; clamp to its UInt16 range.
    let units = min(Double(UInt16.max), max(1, perDevice / 0.000625))
    let pageTO = BluetoothHCIPageTimeout(UInt16(units))
    for d in devices {
      // The fixed callback remoteNameRequestComplete(_:status:) fires on self.
      if d.remoteNameRequest(self, withPageTimeout: pageTO) == kIOReturnSuccess {
        pending += 1
      }
    }
    let budget = perDevice * Double(pending) + 2.0
    let deadline = Date().addingTimeInterval(budget)
    while pending > 0 && Date() < deadline {
      RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.2))
    }
  }

  @objc func remoteNameRequestComplete(_ device: IOBluetoothDevice, status: IOReturn) {
    if status == kIOReturnSuccess, let addr = device.addressString {
      reachable.insert(addr)
    }
    pending -= 1
  }
}

// runBTList prints paired Bluetooth audio devices and whether each is connected.
// When perDeviceTimeout > 0, devices that are not currently connected are pinged
// with a name request and only the ones that answer (powered on, in range) are
// printed — so stale pairings for devices nowhere near the Mac are hidden.
func runBTList(perDeviceTimeout: TimeInterval) {
  guard let paired = IOBluetoothDevice.pairedDevices() else { return }
  let audio = paired.compactMap { $0 as? IOBluetoothDevice }
    .filter { $0.deviceClassMajor == UInt32(kBluetoothDeviceClassMajorAudio) }

  var reachable: Set<String>? = nil
  if perDeviceTimeout > 0 {
    let probe = NameProbe()
    probe.probe(audio.filter { !$0.isConnected() }, perDevice: perDeviceTimeout)
    reachable = probe.reachable
  }

  for d in audio {
    let addr = d.addressString ?? ""
    // Connected devices are reachable by definition; otherwise require a probe hit.
    if let reachable, !d.isConnected(), !reachable.contains(addr) {
      continue
    }
    let name = d.name ?? "(unknown)"
    let connected = d.isConnected() ? "1" : "0"
    print("\(addr)\t\(connected)\t\(name)")
  }
}

// runBTConnect opens a baseband connection to the device, which brings a paired
// audio device online as a CoreAudio output. Blocks until connected or fails.
func runBTConnect(address: String) {
  guard let d = IOBluetoothDevice(addressString: address) else {
    die("no Bluetooth device with address \(address)")
  }
  if d.isConnected() { return }
  let res = d.openConnection()
  if res != kIOReturnSuccess {
    die("openConnection to \(address) failed (IOReturn \(res))")
  }
}

// runBTDisconnect drops the baseband connection to the device, so it stops being
// a CoreAudio output. A no-op if it's already disconnected.
func runBTDisconnect(address: String) {
  guard let d = IOBluetoothDevice(addressString: address) else {
    die("no Bluetooth device with address \(address)")
  }
  if !d.isConnected() { return }
  let res = d.closeConnection()
  if res != kIOReturnSuccess {
    die("closeConnection to \(address) failed (IOReturn \(res))")
  }
}

func runRender(uid: String) {
  guard let dev = deviceID(forUID: uid) else { die("no output device with UID \(uid)") }

  let ring = RingBuffer(capacity: 48000 * 4)  // ~1s

  // Instantiate an AUHAL output unit.
  var desc = AudioComponentDescription(
    componentType: kAudioUnitType_Output,
    componentSubType: kAudioUnitSubType_HALOutput,
    componentManufacturer: kAudioUnitManufacturer_Apple,
    componentFlags: 0, componentFlagsMask: 0)
  guard let comp = AudioComponentFindNext(nil, &desc) else { die("no HAL output component") }
  var unitOpt: AudioUnit?
  guard AudioComponentInstanceNew(comp, &unitOpt) == noErr, let unit = unitOpt else {
    die("AudioComponentInstanceNew failed")
  }

  // Aim it at the requested device.
  var devID = dev
  if AudioUnitSetProperty(unit, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0,
    &devID, UInt32(MemoryLayout<AudioDeviceID>.size)) != noErr {
    die("could not set output device")
  }

  // Tell the unit our client format (s16le/48000/stereo, interleaved); the unit
  // converts to the device's hardware format. Must match audio.StereoCD (48k).
  var asbd = AudioStreamBasicDescription(
    mSampleRate: 48000,
    mFormatID: kAudioFormatLinearPCM,
    mFormatFlags: kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked,
    mBytesPerPacket: 4, mFramesPerPacket: 1, mBytesPerFrame: 4,
    mChannelsPerFrame: 2, mBitsPerChannel: 16, mReserved: 0)
  if AudioUnitSetProperty(unit, kAudioUnitProperty_StreamFormat, kAudioUnitScope_Input, 0,
    &asbd, UInt32(MemoryLayout<AudioStreamBasicDescription>.size)) != noErr {
    die("could not set stream format")
  }

  // Wire the render callback.
  var cb = AURenderCallbackStruct(inputProc: renderCallback, inputProcRefCon: Unmanaged.passUnretained(ring).toOpaque())
  if AudioUnitSetProperty(unit, kAudioUnitProperty_SetRenderCallback, kAudioUnitScope_Input, 0,
    &cb, UInt32(MemoryLayout<AURenderCallbackStruct>.size)) != noErr {
    die("could not set render callback")
  }

  if AudioUnitInitialize(unit) != noErr { die("AudioUnitInitialize failed") }

  let chunk = 8192
  var tmp = [UInt8](repeating: 0, count: chunk)
  var eof = false

  // Pre-buffer before starting playback. The render callback pulls at the device
  // clock from the moment the unit starts; if the ring is empty (or hovering near
  // empty, as it does when the upstream feed arrives in bursts) it underruns to
  // silence — an audible click every time a burst lands late. Prime a cushion
  // first so the callback always has a backlog to draw on. The unit isn't running
  // yet, so nothing drains the ring during priming and we can count bytes directly.
  // Mirrors the AirPlay path, which fills its buffer before it begins streaming.
  let primeTarget = 48000 * 2  // ~0.5s of the ~1s ring
  var primed = 0
  while primed < primeTarget {
    let n = tmp.withUnsafeMutableBytes { read(0, $0.baseAddress, chunk) }
    if n <= 0 { eof = true; break }
    tmp.withUnsafeBufferPointer { writeBlocking(ring, $0.baseAddress!, n) }
    primed += n
  }

  if AudioOutputUnitStart(unit) != noErr { die("AudioOutputUnitStart failed") }

  // Pump the rest of stdin -> ring until EOF, then stop. writeBlocking applies
  // backpressure when the ring is full, so the upstream offset buffer accrues.
  while !eof {
    let n = tmp.withUnsafeMutableBytes { read(0, $0.baseAddress, chunk) }
    if n <= 0 { break }
    tmp.withUnsafeBufferPointer { writeBlocking(ring, $0.baseAddress!, n) }
  }

  AudioOutputUnitStop(unit)
  AudioUnitUninitialize(unit)
  AudioComponentInstanceDispose(unit)
}

// runRecord captures the default input device (the Mac's mic) as s16le/48000/mono
// to stdout for `seconds`. Used by acoustic calibration to hear the test chirp.
// It flushes promptly (small chunks) so the reader's arrival timestamps stay
// tight — calibration relies on a near-constant capture-to-stdout latency.
func runRecord(seconds: Double) {
  var addr = AudioObjectPropertyAddress(
    mSelector: kAudioHardwarePropertyDefaultInputDevice,
    mScope: kAudioObjectPropertyScopeGlobal,
    mElement: kAudioObjectPropertyElementMain)
  var devID = AudioDeviceID(0)
  var size = UInt32(MemoryLayout<AudioDeviceID>.size)
  guard AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &size, &devID) == noErr,
    devID != 0
  else {
    die("no default input device")
  }

  var desc = AudioComponentDescription(
    componentType: kAudioUnitType_Output,
    componentSubType: kAudioUnitSubType_HALOutput,
    componentManufacturer: kAudioUnitManufacturer_Apple,
    componentFlags: 0, componentFlagsMask: 0)
  guard let comp = AudioComponentFindNext(nil, &desc) else { die("no HAL output component") }
  var unitOpt: AudioUnit?
  guard AudioComponentInstanceNew(comp, &unitOpt) == noErr, let unit = unitOpt else {
    die("AudioComponentInstanceNew failed")
  }

  // Enable input (bus 1), disable output (bus 0) — this HAL unit captures.
  var one: UInt32 = 1
  var zero: UInt32 = 0
  if AudioUnitSetProperty(unit, kAudioOutputUnitProperty_EnableIO, kAudioUnitScope_Input, 1,
    &one, UInt32(MemoryLayout<UInt32>.size)) != noErr {
    die("could not enable input")
  }
  if AudioUnitSetProperty(unit, kAudioOutputUnitProperty_EnableIO, kAudioUnitScope_Output, 0,
    &zero, UInt32(MemoryLayout<UInt32>.size)) != noErr {
    die("could not disable output")
  }

  // Aim at the default input device.
  var d = devID
  if AudioUnitSetProperty(unit, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0,
    &d, UInt32(MemoryLayout<AudioDeviceID>.size)) != noErr {
    die("could not set input device")
  }

  // Our client format on the OUTPUT scope of the input bus: s16le/48000/mono.
  // The unit converts from the mic's hardware format to this.
  var asbd = AudioStreamBasicDescription(
    mSampleRate: 48000,
    mFormatID: kAudioFormatLinearPCM,
    mFormatFlags: kAudioFormatFlagIsSignedInteger | kAudioFormatFlagIsPacked,
    mBytesPerPacket: 2, mFramesPerPacket: 1, mBytesPerFrame: 2,
    mChannelsPerFrame: 1, mBitsPerChannel: 16, mReserved: 0)
  if AudioUnitSetProperty(unit, kAudioUnitProperty_StreamFormat, kAudioUnitScope_Output, 1,
    &asbd, UInt32(MemoryLayout<AudioStreamBasicDescription>.size)) != noErr {
    die("could not set input client format")
  }

  let ring = RingBuffer(capacity: 48000 * 2 * 4)  // ~4s of mono s16
  let ctx = RecordContext(unit: unit, ring: ring)
  var cb = AURenderCallbackStruct(inputProc: inputCallback, inputProcRefCon: Unmanaged.passUnretained(ctx).toOpaque())
  if AudioUnitSetProperty(unit, kAudioOutputUnitProperty_SetInputCallback, kAudioUnitScope_Global, 0,
    &cb, UInt32(MemoryLayout<AURenderCallbackStruct>.size)) != noErr {
    die("could not set input callback")
  }

  if AudioUnitInitialize(unit) != noErr { die("AudioUnitInitialize failed") }
  if AudioOutputUnitStart(unit) != noErr { die("AudioOutputUnitStart failed") }

  let deadline = Date().addingTimeInterval(seconds)
  let chunk = 4096
  var tmp = [UInt8](repeating: 0, count: chunk)
  while Date() < deadline {
    let n = tmp.withUnsafeMutableBufferPointer { ring.read($0.baseAddress!, chunk) }
    if n > 0 {
      tmp.withUnsafeBufferPointer { _ = fwrite($0.baseAddress, 1, n, stdout) }
      fflush(stdout)
    } else {
      usleep(2000)  // ring empty; let the callback get ahead
    }
  }

  AudioOutputUnitStop(unit)
  AudioUnitUninitialize(unit)
  AudioComponentInstanceDispose(unit)
}

// MARK: - Entry

let args = Array(CommandLine.arguments.dropFirst())
switch args.first {
case "list":
  runList()
case "bt-list":
  var timeout: TimeInterval = 0
  var i = 1
  while i < args.count {
    if args[i] == "--reachable-timeout", i + 1 < args.count {
      timeout = TimeInterval(args[i + 1]) ?? 0
      i += 2
    } else {
      i += 1
    }
  }
  runBTList(perDeviceTimeout: timeout)
case "bt-connect":
  var address: String?
  var i = 1
  while i < args.count {
    if args[i] == "--address", i + 1 < args.count {
      address = args[i + 1]
      i += 2
    } else {
      i += 1
    }
  }
  guard let a = address else { die("usage: chorusaudio bt-connect --address <addr>") }
  runBTConnect(address: a)
case "bt-disconnect":
  var address: String?
  var i = 1
  while i < args.count {
    if args[i] == "--address", i + 1 < args.count {
      address = args[i + 1]
      i += 2
    } else {
      i += 1
    }
  }
  guard let a = address else { die("usage: chorusaudio bt-disconnect --address <addr>") }
  runBTDisconnect(address: a)
case "render":
  var uid: String?
  var i = 1
  while i < args.count {
    if args[i] == "--device-uid", i + 1 < args.count {
      uid = args[i + 1]
      i += 2
    } else {
      i += 1
    }
  }
  guard let u = uid else { die("usage: chorusaudio render --device-uid <uid>") }
  runRender(uid: u)
case "record":
  var seconds = 5.0
  var i = 1
  while i < args.count {
    if args[i] == "--seconds", i + 1 < args.count {
      seconds = Double(args[i + 1]) ?? seconds
      i += 2
    } else {
      i += 1
    }
  }
  runRecord(seconds: seconds)
default:
  FileHandle.standardError.write("usage: chorusaudio (list | bt-list [--reachable-timeout <sec>] | bt-connect --address <addr> | bt-disconnect --address <addr> | render --device-uid <uid> | record [--seconds <n>])\n".data(using: .utf8)!)
  exit(2)
}
