import AudioTeeCore
import Foundation

/// CLI-specific output handler that writes raw PCM audio to stdout
/// and lifecycle messages to stderr via the logger.
///
/// IMPORTANT: `handleAudioData` is called on the Core Audio real-time IO proc
/// thread. Writing to the stdout pipe can *block* whenever the downstream
/// consumer (chorus) is briefly slow to drain it (the pipe's kernel buffer
/// fills). Blocking on the audio thread makes the IO proc miss its deadline,
/// so Core Audio drops the next capture buffers — an audible gap on *every*
/// output at once, recurring in bursts whenever the consumer hiccups.
///
/// So the audio thread never writes to stdout. It only copies the chunk and
/// hands it to a dedicated serial writer queue; that queue absorbs any pipe
/// blocking off the real-time thread. A bounded backlog guards memory: under
/// sustained backpressure we drop the oldest chunk (a rare, logged gap) rather
/// than grow without limit.
class BinaryAudioOutputHandler: AudioOutputHandler {
  private let fd = STDOUT_FILENO

  /// Dedicated thread for the (potentially blocking) stdout writes. Serial, so
  /// PCM stays in order.
  private let writeQueue = DispatchQueue(label: "audiotee.stdout-writer", qos: .userInitiated)

  /// Guards `pendingBytes`/`droppedChunks`. Held only briefly.
  private let lock = NSLock()
  private var pendingBytes = 0
  /// ~1 MB ≈ 3 s at 48 kHz/16-bit/stereo. Caps the backlog so a stalled or
  /// dead consumer can't grow memory unbounded.
  private let maxPendingBytes = 1 << 20
  private var droppedChunks = 0

  func handleAudioData(_ pointer: UnsafeRawPointer, count: Int) {
    // Real-time audio thread: must not block. Drop under sustained backpressure
    // instead of queuing without bound.
    lock.lock()
    if pendingBytes + count > maxPendingBytes {
      droppedChunks += 1
      let dropped = droppedChunks
      lock.unlock()
      if dropped == 1 || dropped % 50 == 0 {
        AudioTeeLogging.logger.error(
          "stdout consumer too slow; dropped audio chunk",
          context: ["dropped_chunks": String(dropped)])
      }
      return
    }
    pendingBytes += count
    lock.unlock()

    // The Core Audio pointer is only valid for this call, so copy it out.
    let data = Data(bytes: pointer, count: count)
    writeQueue.async { [weak self] in
      guard let self = self else { return }
      self.writeAll(data)
      self.lock.lock()
      self.pendingBytes -= count
      self.lock.unlock()
    }
  }

  /// Blocking write of one chunk to stdout — runs on `writeQueue`, never the
  /// audio thread.
  private func writeAll(_ data: Data) {
    data.withUnsafeBytes { (raw: UnsafeRawBufferPointer) in
      guard let base = raw.baseAddress else { return }
      var written = 0
      while written < data.count {
        let result = write(fd, base.advanced(by: written), data.count - written)
        if result >= 0 {
          written += result
        } else if errno == EINTR {
          continue
        } else {
          break  // EPIPE, EIO, etc — consumer gone or real error
        }
      }
    }
  }

  func handleMetadata(_ metadata: AudioStreamMetadata) {
    AudioTeeLogging.logger.writeMessage(.metadata, data: metadata)
  }

  func handleStreamStart() {
    AudioTeeLogging.logger.writeMessage(.streamStart, data: Optional<String>.none)
  }

  func handleStreamStop() {
    // Drain any queued writes before signalling stop so the tail isn't lost.
    writeQueue.sync {}
    AudioTeeLogging.logger.writeMessage(.streamStop, data: Optional<String>.none)
  }
}
