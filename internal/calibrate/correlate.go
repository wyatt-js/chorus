package calibrate

import "math"

// matchedFilter cross-correlates signal against reference (the known chirp) and
// returns the sample lag of the best alignment — i.e. where the chirp starts in
// signal — together with a detection score: the peak magnitude divided by the
// RMS of the whole correlation. A clean, unambiguous arrival scores high (the
// peak towers over the noise floor); a score below ~6 means the tone wasn't
// clearly heard. Correlation is done by FFT so a multi-second search window is
// cheap.
func matchedFilter(signal, reference []float64) (lag int, score float64) {
	if len(signal) < len(reference) || len(reference) == 0 {
		return 0, 0
	}
	size := nextPow2(len(signal) + len(reference))

	a := make([]complex128, size)
	for i, v := range signal {
		a[i] = complex(v, 0)
	}
	b := make([]complex128, size)
	for i, v := range reference {
		b[i] = complex(v, 0)
	}
	fft(a, false)
	fft(b, false)
	for i := range a {
		a[i] *= conj(b[i]) // cross-correlation = signal ⋆ reference
	}
	fft(a, true)

	// Search only lags where the chirp fits entirely inside the signal; circular
	// wrap-around beyond that is an artifact, not a real arrival.
	maxLag := len(signal) - len(reference)
	peakIdx, peakVal := 0, -1.0
	var sumSq float64
	for i := 0; i <= maxLag; i++ {
		v := real(a[i])
		sumSq += v * v
		if av := math.Abs(v); av > peakVal {
			peakVal, peakIdx = av, i
		}
	}
	rms := math.Sqrt(sumSq / float64(maxLag+1))
	if rms == 0 {
		return peakIdx, 0
	}
	return peakIdx, peakVal / rms
}

func conj(c complex128) complex128 { return complex(real(c), -imag(c)) }
