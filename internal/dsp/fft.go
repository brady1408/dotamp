package dsp

import (
	"math"
	"math/bits"
)

// fft performs an in-place iterative radix-2 FFT. len(re) must be a power of two.
func fft(re, im []float64) {
	n := len(re)
	shift := bits.UintSize - uint(bits.Len(uint(n-1)))
	for i := 0; i < n; i++ {
		j := int(bits.Reverse(uint(i)) >> shift)
		if j > i {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		step := -2 * math.Pi / float64(size)
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				wr, wi := math.Cos(step*float64(k)), math.Sin(step*float64(k))
				a, b := start+k, start+k+half
				tr := wr*re[b] - wi*im[b]
				ti := wr*im[b] + wi*re[b]
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}

// Magnitudes windows samples, runs the FFT, and returns |X[k]| normalised by
// the window's coherent gain, so a full-scale sine reads 1.0 whatever the
// window. len(samples) must be a power of two; len(window) must equal it.
func Magnitudes(samples, window []float64) []float64 {
	n := len(samples)
	re := make([]float64, n)
	im := make([]float64, n)
	gain := 0.0
	for i := range samples {
		re[i] = samples[i] * window[i]
		gain += window[i]
	}
	fft(re, im)
	out := make([]float64, n/2)
	scale := 2 / gain
	for k := range out {
		out[k] = math.Hypot(re[k], im[k]) * scale
	}
	return out
}
