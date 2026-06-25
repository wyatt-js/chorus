package calibrate

import (
	"math"
	"math/rand"
	"testing"

	"github.com/wyattjs/chorus/internal/audio"
)

func TestFFTRoundTrip(t *testing.T) {
	x := make([]complex128, 8)
	for i := range x {
		x[i] = complex(float64(i+1), 0)
	}
	orig := append([]complex128(nil), x...)
	fft(x, false)
	fft(x, true)
	for i := range x {
		if math.Abs(real(x[i])-real(orig[i])) > 1e-9 || math.Abs(imag(x[i])) > 1e-9 {
			t.Fatalf("round-trip mismatch at %d: got %v want %v", i, x[i], orig[i])
		}
	}
}

func TestMatchedFilterRecoversDelay(t *testing.T) {
	c := DefaultChirp(audio.StereoCD)
	ref := c.Reference

	const delay = 12345 // samples
	rng := rand.New(rand.NewSource(1))
	signal := make([]float64, delay+len(ref)+20000)
	for i := range signal {
		signal[i] = 0.3 * (rng.Float64()*2 - 1) // background noise
	}
	for i, v := range ref {
		signal[delay+i] += 0.5 * v // the chirp arrives, attenuated
	}

	lag, score := matchedFilter(signal, ref)
	if lag != delay {
		// Allow ±1 sample for floating-point rounding in the FFT.
		if d := lag - delay; d < -1 || d > 1 {
			t.Errorf("recovered lag = %d, want %d", lag, delay)
		}
	}
	if score < 6 {
		t.Errorf("detection score = %.1f, want a clear peak (>= 6)", score)
	}
}

func TestMatchedFilterRejectsNoise(t *testing.T) {
	c := DefaultChirp(audio.StereoCD)
	rng := rand.New(rand.NewSource(2))
	signal := make([]float64, len(c.Reference)+40000)
	for i := range signal {
		signal[i] = rng.Float64()*2 - 1 // pure noise, no chirp
	}
	if _, score := matchedFilter(signal, c.Reference); score >= 6 {
		t.Errorf("pure noise scored %.1f; should be below the detection threshold", score)
	}
}
