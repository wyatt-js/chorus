package calibrate

import "math/cmplx"

// fft computes the in-place iterative radix-2 Cooley–Tukey FFT of x, whose
// length must be a power of two. inverse selects the IFFT (with 1/N scaling).
// Hand-rolled to keep the build pure-Go with no new dependency.
func fft(x []complex128, inverse bool) {
	n := len(x)
	if n <= 1 {
		return
	}

	// Bit-reversal permutation.
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}

	sign := -1.0
	if inverse {
		sign = 1.0
	}
	for length := 2; length <= n; length <<= 1 {
		ang := sign * 2 * pi / float64(length)
		wlen := cmplx.Rect(1, ang)
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			half := length >> 1
			for k := 0; k < half; k++ {
				u := x[i+k]
				v := x[i+k+half] * w
				x[i+k] = u + v
				x[i+k+half] = u - v
				w *= wlen
			}
		}
	}

	if inverse {
		inv := complex(1/float64(n), 0)
		for i := range x {
			x[i] *= inv
		}
	}
}

const pi = 3.14159265358979323846

// nextPow2 returns the smallest power of two >= n.
func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
