package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// litByRow counts braille cells with any dot set, per screen row.
func litByRow(s tcell.SimulationScreen) []int {
	cells, w, h := s.GetContents()
	out := make([]int, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) == 1 && c.Runes[0] > 0x2800 && c.Runes[0] <= 0x28FF {
				out[y]++
			}
		}
	}
	return out
}

func TestScopeSilenceIsAFlatLineAcrossTheMiddle(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(silent{})
	a.SetMode(ModeScope)
	a.Update(80, 8)
	a.Draw(s, Rect{0, 0, 80, 8})
	s.Show()
	rows := litByRow(s)
	mid := 8 / 2
	if rows[mid-1]+rows[mid] < 80 {
		t.Fatalf("the centre line should span the width: %v", rows)
	}
	for y, n := range rows {
		if y != mid-1 && y != mid && n != 0 {
			t.Fatalf("silence must not light row %d: %v", y, rows)
		}
	}
}

func TestScopeSineSwingsAboveAndBelowCentre(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(sineSpectrum{hz: 440})
	a.SetMode(ModeScope)
	a.Update(80, 8)
	a.Draw(s, Rect{0, 0, 80, 8})
	s.Show()
	rows := litByRow(s)
	if rows[0] == 0 || rows[7] == 0 {
		t.Fatalf("a full-scale sine should reach the top and bottom rows: %v", rows)
	}
	lit, _ := countBraille(s)
	if lit < 80 {
		t.Fatalf("the trace must be continuous across the width, lit = %d", lit)
	}
}

func TestScopeTriggersOnARisingZeroCrossing(t *testing.T) {
	// Two draws of the same steady tone must start at the same phase, or the
	// trace would jitter at 30 Hz.
	a := NewAnalyzer(sineSpectrum{hz: 440})
	a.SetMode(ModeScope)
	a.Update(80, 8)
	first := append([]int(nil), a.scopeTrace(160)...)
	a.Update(80, 8)
	second := a.scopeTrace(160)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("trace differs at dot %d: %d vs %d", i, first[i], second[i])
		}
	}
	if first[0] < 14 || first[0] > 18 { // starts at the centre line (dot 16 of 32) on the way up
		t.Fatalf("trace should start at a zero crossing, got dot %d", first[0])
	}
}

func TestVKeyCyclesModesAndReportsIt(t *testing.T) {
	app, _, _ := newApp(t)
	var saved []string
	app.OnVisual = func(name string) { saved = append(saved, name) }
	if app.an.Mode() != ModeBars {
		t.Fatal("bars by default")
	}
	key(app, tcell.KeyRune, 'v')
	if app.an.Mode() != ModeScope || len(saved) != 1 || saved[0] != "scope" {
		t.Fatalf("mode=%v saved=%v", app.an.Mode(), saved)
	}
	key(app, tcell.KeyRune, 'v')
	if app.an.Mode() != ModeSpectrogram || saved[1] != "spectrogram" {
		t.Fatalf("mode=%v saved=%v", app.an.Mode(), saved)
	}
	key(app, tcell.KeyRune, 'v')
	if app.an.Mode() != ModeStereo || saved[2] != "stereo" {
		t.Fatalf("mode=%v saved=%v", app.an.Mode(), saved)
	}
	key(app, tcell.KeyRune, 'v')
	if app.an.Mode() != ModeBars || saved[3] != "bars" {
		t.Fatalf("mode=%v saved=%v", app.an.Mode(), saved)
	}
	app.SetVisual("scope")
	if app.an.Mode() != ModeScope {
		t.Fatal("SetVisual should select the scope")
	}
	app.SetVisual("nonsense")
	if app.an.Mode() != ModeScope {
		t.Fatal("an unknown name must leave the mode alone")
	}
}

func TestScopeFillsAWideWindow(t *testing.T) {
	const w = 320 // 640 dots × 4 samples = 2560 samples, more than the FFT buffer holds
	s := sim(t, w, 8)
	a := NewAnalyzer(sineSpectrum{hz: 440})
	a.SetMode(ModeScope)
	a.Update(w, 8)
	a.Draw(s, Rect{0, 0, w, 8})
	s.Show()
	cells, cw, ch := s.GetContents()
	// The rightmost 40 columns must still swing to the top and bottom rows.
	top, bottom := 0, 0
	for x := cw - 40; x < cw; x++ {
		if r := cells[0*cw+x].Runes; len(r) == 1 && r[0] > 0x2800 {
			top++
		}
		if r := cells[(ch-1)*cw+x].Runes; len(r) == 1 && r[0] > 0x2800 {
			bottom++
		}
	}
	if top == 0 || bottom == 0 {
		t.Fatalf("right edge is flat: top=%d bottom=%d lit cells in the last 40 columns", top, bottom)
	}
}

// TestScopeRoundingIsLopsidedOnPurpose: the top half rounds normally, the
// bottom half truncates toward the centre. That leaves -1 one dot short
// of the edge and flattens the smallest negative swings, which is what
// keeps the trace calm; Brady preferred it to symmetric rounding and to
// a symmetric gate (which drew a solid centre line). Do not "fix" it.
func TestScopeRoundingIsLopsidedOnPurpose(t *testing.T) {
	a := NewAnalyzer(silent{})
	a.SetMode(ModeScope)
	a.Update(4, 2) // centre row 4, swing 3 dots
	dotW := a.cv.DotW()
	a.trace = make([]float64, dotW*scopeSamplesPerDot+scopeTriggerSearch)
	vals := []float64{1, -1, 0.4 / 3, -1.4 / 3} // full up, full down, a small up, a small down
	a.trace[0] = -1                             // the rising crossing is at index 1
	for i := 1; i < len(a.trace); i++ {
		a.trace[i] = vals[((i-1)/scopeSamplesPerDot)%len(vals)]
	}
	got := a.scopeTrace(dotW)
	if got[0] != 1 || got[1] != 6 {
		t.Fatalf("+1 reaches row 1, -1 stops at row 6: %v", got[:4])
	}
	if got[2] != 4 || got[3] != 4 {
		t.Fatalf("small swings below half a dot up or 1.5 dots down sit on the centre: %v", got[:4])
	}
}
