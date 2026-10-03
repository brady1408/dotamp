package dsp

import "math"

// Bands groups FFT bins into bars spaced evenly on a log frequency axis.
type Bands struct {
	ranges [][2]int // inclusive bin ranges, one per bar
}

func NewBands(nbars, nbins, sampleRate int, lo, hi float64) Bands {
	binHz := float64(sampleRate) / float64(2*nbins)
	b := Bands{ranges: make([][2]int, nbars)}
	last := 0
	for i := 0; i < nbars; i++ {
		f0 := lo * math.Pow(hi/lo, float64(i)/float64(nbars))
		f1 := lo * math.Pow(hi/lo, float64(i+1)/float64(nbars))
		b0 := int(f0 / binHz)
		b1 := int(f1/binHz) - 1
		if b0 < 1 {
			b0 = 1
		}
		if b0 <= last {
			b0 = last + 1
		}
		if b1 < b0 {
			b1 = b0
		}
		if b1 > nbins-1 {
			b1 = nbins - 1
		}
		if b0 > nbins-1 {
			b0 = nbins - 1
		}
		b.ranges[i] = [2]int{b0, b1}
		last = b1
	}
	return b
}

const floorDb = -60.0

// Levels writes one value in [0,1] per bar: the loudest bin in the bar's range,
// in dB relative to full scale, with -60 dB and below reading 0.
func (b Bands) Levels(mag []float64, out []float64) {
	for i, r := range b.ranges {
		peak := 0.0
		for k := r[0]; k <= r[1] && k < len(mag); k++ {
			if mag[k] > peak {
				peak = mag[k]
			}
		}
		if peak <= 0 {
			out[i] = 0
			continue
		}
		db := 20 * math.Log10(peak)
		v := 1 - db/floorDb
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		out[i] = v
	}
}
