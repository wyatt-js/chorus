// Package calibrate measures each output's acoustic latency so the pipeline can
// time-align them. It plays a short test chirp on one device at a time (via the
// Broadcaster's Probe), records it on the Mac's mic, and cross-correlates the
// recording against the emitted chirp to recover the time-of-arrival. The
// per-device latencies then set the fan-out offsets (offset_i = max − latency_i).
package calibrate

import (
	"math"
	"time"

	"github.com/wyattjs/chorus/internal/audio"
)

// Chirp default parameters. A 400Hz–4kHz exponential sweep stays in the warm
// mid-band — clear of low-frequency room rumble, but capped below the piercing
// highs that make a wider sweep harsh on the ears — while remaining easy for the
// matched filter to find. ~0.8s keeps a strong, sharp correlation peak.
const (
	ChirpF0       = 400.0
	ChirpF1       = 4000.0
	ChirpDuration = 800 * time.Millisecond
	chirpFade     = 30 * time.Millisecond // gentle raised-cosine onset/decay
	// chirpAmp is the emission peak (0–1). Kept low so the tone is gentle on the
	// ears; the matched filter's processing gain still detects it well below the
	// level of the surrounding audio.
	chirpAmp = 0.2
)

// Chirp is a generated test tone: PCM ready to emit, plus the mono reference
// (one sample per output frame) the recording is correlated against.
type Chirp struct {
	PCM        []byte    // interleaved s16le in the given format, ready for Probe
	Reference  []float64 // mono, normalized to [-1,1], len == frames
	SampleRate int
}

// GenerateChirp builds an exponential sine sweep from f0 to f1 over dur in the
// given format (the emitted PCM is the mono sweep copied to every channel).
func GenerateChirp(format audio.Format, f0, f1 float64, dur time.Duration) Chirp {
	sr := format.SampleRate
	frames := int(float64(sr) * dur.Seconds())
	if frames < 1 {
		frames = 1
	}
	ref := make([]float64, frames)

	// Exponential sweep: instantaneous frequency f(t) = f0·(f1/f0)^(t/T), whose
	// integral gives the phase below. k = f1/f0.
	T := float64(frames) / float64(sr)
	k := f1 / f0
	lnk := math.Log(k)
	fadeN := int(float64(sr) * chirpFade.Seconds())
	for n := range frames {
		t := float64(n) / float64(sr)
		phase := 2 * math.Pi * f0 * T / lnk * (math.Pow(k, t/T) - 1)
		s := math.Sin(phase)
		// Raised-cosine fade in/out to avoid a step (click) at the edges.
		if fadeN > 0 {
			if n < fadeN {
				s *= 0.5 * (1 - math.Cos(math.Pi*float64(n)/float64(fadeN)))
			} else if rem := frames - 1 - n; rem < fadeN {
				s *= 0.5 * (1 - math.Cos(math.Pi*float64(rem)/float64(fadeN)))
			}
		}
		ref[n] = s
	}

	pcm := pcmFromMono(ref, format, chirpAmp)
	return Chirp{PCM: pcm, Reference: ref, SampleRate: sr}
}

// DefaultChirp is GenerateChirp with the package defaults.
func DefaultChirp(format audio.Format) Chirp {
	return GenerateChirp(format, ChirpF0, ChirpF1, ChirpDuration)
}

// pcmFromMono renders a mono [-1,1] signal to interleaved little-endian s16
// across all channels at the given peak amplitude (0–1).
func pcmFromMono(mono []float64, format audio.Format, amp float64) []byte {
	bytesPerSample := format.BitDepth / 8
	out := make([]byte, len(mono)*format.Channels*bytesPerSample)
	i := 0
	for _, s := range mono {
		v := int16(math.Round(s * amp * math.MaxInt16))
		for c := 0; c < format.Channels; c++ {
			out[i] = byte(v)
			out[i+1] = byte(v >> 8)
			i += 2
		}
	}
	return out
}
