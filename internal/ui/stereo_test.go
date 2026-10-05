package ui

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// stereoFake feeds left and right separately; phase is the right channel's
// lag in radians: 0 is mono, π/2 is a full-width circle.
type stereoFake struct{ phase float64 }

func (f stereoFake) Spectrum(n int, dst []float64) int {
	for i := 0; i < n; i++ {
		dst[i] = math.Sin(2 * math.Pi * 440 * float64(i) / 44100)
	}
	return n
}

func (f stereoFake) Stereo(n int, l, r []float64) int {
	for i := 0; i < n; i++ {
		th := 2 * math.Pi * 440 * float64(i) / 44100
		l[i] = math.Sin(th)
		r[i] = math.Sin(th + f.phase)
	}
	return n
}

func litPositions(s tcell.SimulationScreen) [][2]int {
	cells, w, h := s.GetContents()
	var out [][2]int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) == 1 && c.Runes[0] > 0x2800 && c.Runes[0] <= 0x28FF {
				out = append(out, [2]int{x, y})
			}
		}
	}
	return out
}

func TestStereoMonoIsADiagonal(t *testing.T) {
	s := sim(t, 40, 10)
	a := NewAnalyzer(stereoFake{phase: 0})
	a.SetMode(ModeStereo)
	a.Update(40, 10)
	a.Draw(s, Rect{0, 0, 40, 10})
	s.Show()
	pos := litPositions(s)
	if len(pos) < 8 {
		t.Fatalf("too few lit cells for a trace: %d", len(pos))
	}
	// The square is centred; a mono signal maps to x == y within the square.
	// Each cell is 2 dots wide and 4 tall, so compare in dot space.
	for _, p := range pos {
		dx := float64(p[0]*2+1) - 40.0 // dots from the horizontal centre (80 dots wide)
		dy := 20.0 - float64(p[1]*4+2) // dots from the vertical centre (40 dots tall)
		if math.Abs(dx-dy) > 6 {
			t.Fatalf("cell %v is off the diagonal (dx=%.0f dy=%.0f)", p, dx, dy)
		}
	}
}

func TestStereoWideSignalFillsAShape(t *testing.T) {
	s := sim(t, 40, 10)
	a := NewAnalyzer(stereoFake{phase: math.Pi / 2})
	a.SetMode(ModeStereo)
	a.Update(40, 10)
	a.Draw(s, Rect{0, 0, 40, 10})
	s.Show()
	pos := litPositions(s)
	cols := map[int]bool{}
	for _, p := range pos {
		cols[p[0]] = true
	}
	if len(cols) < 12 {
		t.Fatalf("a quadrature signal should spread across the square, got %d columns", len(cols))
	}
}

func TestStereoSilenceIsAtMostACentreDot(t *testing.T) {
	s := sim(t, 40, 10)
	a := NewAnalyzer(silent{})
	a.SetMode(ModeStereo)
	a.Update(40, 10)
	a.Draw(s, Rect{0, 0, 40, 10})
	s.Show()
	if pos := litPositions(s); len(pos) > 1 {
		t.Fatalf("silence lit %d cells", len(pos))
	}
}
