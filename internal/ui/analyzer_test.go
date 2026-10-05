package ui

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
)

type sineSpectrum struct{ hz float64 }

func (s sineSpectrum) Spectrum(n int, dst []float64) int {
	for i := 0; i < n; i++ {
		dst[i] = math.Sin(2 * math.Pi * s.hz * float64(i) / 44100)
	}
	return n
}

type silent struct{}

func (silent) Spectrum(n int, dst []float64) int { return 0 }
func (silent) Stereo(n int, l, r []float64) int  { return 0 }

func (s sineSpectrum) Stereo(n int, l, r []float64) int {
	s.Spectrum(n, l)
	copy(r, l)
	return n
}

func countBraille(s tcell.SimulationScreen) (lit int, maxColorRed bool) {
	cells, _, _ := s.GetContents()
	for _, c := range cells {
		if len(c.Runes) == 1 && c.Runes[0] > 0x2800 && c.Runes[0] <= 0x28FF {
			lit++
			fg, _, _ := c.Style.Decompose()
			if fg == tcell.PaletteColor(1) || fg == tcell.PaletteColor(9) {
				maxColorRed = true
			}
		}
	}
	return
}

func TestAnalyzerSilenceDrawsNothing(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(silent{})
	a.Update(80, 8)
	a.Draw(s, Rect{0, 0, 80, 8})
	s.Show()
	if lit, _ := countBraille(s); lit != 0 {
		t.Fatalf("lit cells on silence = %d", lit)
	}
}

func TestAnalyzerSineLightsOneRegionAndReachesRed(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(sineSpectrum{hz: 44100.0 / 2048 * 46}) // bin-centred ≈ 990 Hz: no scalloping loss
	r := Rect{0, 0, 80, 8}
	for i := 0; i < 5; i++ { // let the caps settle
		a.Update(r.W, r.H)
	}
	a.Draw(s, r)
	s.Show()
	lit, red := countBraille(s)
	if lit == 0 || lit > 80*8/4 {
		t.Fatalf("lit cells = %d; a single tone should light a narrow region", lit)
	}
	if !red {
		t.Fatal("a full-scale tone should reach the red band")
	}
}

func TestAnalyzerResizes(t *testing.T) {
	s := sim(t, 120, 10)
	a := NewAnalyzer(sineSpectrum{hz: 440})
	a.Update(80, 8)
	a.Draw(s, Rect{0, 0, 80, 8})
	a.Update(120, 10)
	a.Draw(s, Rect{0, 0, 120, 10}) // must not panic on a wider bar count
}
