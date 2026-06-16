// airtoothaudio — a small CoreAudio helper for airtooth.
//
//   airtoothaudio list
//       Print output-capable audio devices as TSV: uid<TAB>transport<TAB>name
//
//   airtoothaudio render --device-uid <uid>
//       Read s16le / 44100Hz / stereo PCM from stdin and play it to the device
//       with the given UID (e.g. a paired Bluetooth soundbar).
//
// Rendering uses an AUHAL output unit aimed at a specific device with a render
// callback — the reliable way to play to a non-default device on macOS.

import AudioToolbox
import CoreAudio
import Darwin
import Foundation

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
  FileHandle.standardError.write("airtoothaudio: \(msg)\n".data(using: .utf8)!)
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

  func write(_ src: UnsafePointer<UInt8>, _ n: Int) {
    os_unfair_lock_lock(&lock)
    defer { os_unfair_lock_unlock(&lock) }
    let cap = buf.count
    for i in 0..<n {
      if filled == cap {  // overflow: drop oldest
        head = (head + 1) % cap
        filled -= 1
      }
      buf[tail] = src[i]
      tail = (tail + 1) % cap
      filled += 1
    }
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

// MARK: - Commands

func runList() {
  for dev in systemDevices() where hasOutputStreams(dev) {
    let uid = stringProperty(dev, kAudioDevicePropertyDeviceUID) ?? ""
    let name = stringProperty(dev, kAudioObjectPropertyName) ?? "(unknown)"
    print("\(uid)\t\(transportString(dev))\t\(name)")
  }
}

func runRender(uid: String) {
  guard let dev = deviceID(forUID: uid) else { die("no output device with UID \(uid)") }

  let ring = RingBuffer(capacity: 44100 * 4)  // ~1s

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

  // Tell the unit our client format (s16le/44100/stereo, interleaved); the unit
  // converts to the device's hardware format.
  var asbd = AudioStreamBasicDescription(
    mSampleRate: 44100,
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
  if AudioOutputUnitStart(unit) != noErr { die("AudioOutputUnitStart failed") }

  // Pump stdin -> ring until EOF, then stop.
  let chunk = 8192
  var tmp = [UInt8](repeating: 0, count: chunk)
  while true {
    let n = tmp.withUnsafeMutableBytes { read(0, $0.baseAddress, chunk) }
    if n <= 0 { break }
    tmp.withUnsafeBufferPointer { ring.write($0.baseAddress!, n) }
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
  guard let u = uid else { die("usage: airtoothaudio render --device-uid <uid>") }
  runRender(uid: u)
default:
  FileHandle.standardError.write("usage: airtoothaudio (list | render --device-uid <uid>)\n".data(using: .utf8)!)
  exit(2)
}
