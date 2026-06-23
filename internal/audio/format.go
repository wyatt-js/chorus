// Package audio holds shared PCM format types.
package audio

// Format describes an interleaved, little-endian PCM stream.
type Format struct {
	SampleRate int // Hz
	Channels   int
	BitDepth   int // bits per sample
}

// BytesPerFrame is the size of one frame (one sample across all channels).
func (f Format) BytesPerFrame() int {
	return f.Channels * f.BitDepth / 8
}

// StereoCD is the format chorus runs the pipeline in: 48kHz, 16-bit, stereo.
// (Name kept for call-site stability; it is no longer 44.1kHz.) 48kHz is macOS's
// native system-audio rate, so capturing at 48k means audiotee does NO
// sample-rate conversion — eliminating a per-chunk resampler that spliced an
// audible click at every chunk boundary onto every output. The BT device, the
// Samsung TV, and most CoreAudio outputs are natively 48k too, so nothing
// downstream resamples either. Every sink (AirPlay 2, the CoreAudio render
// helper, the live WAV Cast stream) is configured to match.
var StereoCD = Format{SampleRate: 48000, Channels: 2, BitDepth: 16}
