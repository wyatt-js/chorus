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

// StereoCD is the format airtooth runs the Phase 0 pipeline in: 44.1kHz, 16-bit,
// stereo. It is what audiotee emits with `--sample-rate 44100 --stereo` and
// exactly what libraop's RAOP sender expects, so no conversion is needed.
var StereoCD = Format{SampleRate: 44100, Channels: 2, BitDepth: 16}
