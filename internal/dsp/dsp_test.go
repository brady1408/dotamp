package dsp

import (
	"math"
	"testing"
)

func sine(n int, rate, hz float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.Sin(2 * math.Pi * hz * float64(i) / rate)
	}
	return out
}

func TestHannEdgesAndPeak(t *testing.T) {
	w := Hann(8)
	if len(w) != 8 || w[0] > 1e-9 || math.Abs(w[4]-1) > 1e-9 {
		t.Fatalf("hann = %v", w)
	}
}

func TestMagnitudesPeakBin(t *testing.T) {
	const n, rate = 2048, 44100.0
	hz := rate / n * 100 // exactly bin 100
	mag := Magnitudes(sine(n, rate, hz), Hann(n))
	if len(mag) != n/2 {
		t.Fatalf("len = %d", len(mag))
	}
	best := 0
	for i := range mag {
		if mag[i] > mag[best] {
			best = i
		}
	}
	if best != 100 {
		t.Fatalf("peak bin = %d, want 100", best)
	}
	if mag[100] < 0.95 || mag[100] > 1.05 { // normalised for the window's gain: full scale reads 1
		t.Fatalf("peak magnitude = %f", mag[100])
	}
}

func TestBandsCoverAndAreMonotonic(t *testing.T) {
	b := NewBands(40, 1024, 44100, 40, 16000)
	prevHi := -1
	for i, r := range b.ranges {
		if r[0] > r[1] || (i > 0 && r[0] <= prevHi) {
			t.Fatalf("bar %d range %v after %d", i, r, prevHi)
		}
		if r[1] >= 1024 {
			t.Fatalf("bar %d exceeds bins: %v", i, r)
		}
		prevHi = r[1]
	}
	if b.ranges[0][0] < 1 {
		t.Fatal("first bar must skip the DC bin")
	}
}

func TestLevelsDbScale(t *testing.T) {
	b := NewBands(4, 16, 1000, 50, 400)
	mag := make([]float64, 16)
	out := make([]float64, 4)
	b.Levels(mag, out)
	for _, v := range out {
		if v != 0 {
			t.Fatalf("silence should be 0, got %v", out)
		}
	}
	for i := range mag {
		mag[i] = 1
	}
	b.Levels(mag, out)
	for _, v := range out {
		if math.Abs(v-1) > 1e-9 {
			t.Fatalf("full scale should be 1, got %v", out)
		}
	}
	for i := range mag {
		mag[i] = 0.001 // -60 dB
	}
	b.Levels(mag, out)
	if out[0] > 1e-9 {
		t.Fatalf("-60 dB should clamp to 0, got %v", out[0])
	}
}

func TestSmootherAttackDecayAndCaps(t *testing.T) {
	s := NewSmoother(1, 0.5, 2, 0.1)
	s.Update([]float64{1})
	if s.Bars[0] != 1 || s.Caps[0] != 1 {
		t.Fatalf("attack: %+v", s)
	}
	s.Update([]float64{0})
	if math.Abs(s.Bars[0]-0.5) > 1e-9 {
		t.Fatalf("decay: %v", s.Bars[0])
	}
	if s.Caps[0] != 1 {
		t.Fatalf("cap should hold: %v", s.Caps[0])
	}
	s.Update([]float64{0})
	s.Update([]float64{0})
	if math.Abs(s.Caps[0]-0.9) > 1e-9 {
		t.Fatalf("cap should fall by 0.1 after hold: %v", s.Caps[0])
	}
	s.Update([]float64{0, 0, 0})
	if len(s.Bars) != 3 || len(s.Caps) != 3 {
		t.Fatal("smoother must resize to new bar count")
	}
}
