package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// heatCells returns, per row, how many cells carry a lit half-block, and the
// set of columns lit anywhere.
func heatCells(s tcell.SimulationScreen) (perRow []int, cols map[int]bool) {
	cells, w, h := s.GetContents()
	perRow = make([]int, h)
	cols = map[int]bool{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) == 1 && c.Runes[0] == '▀' {
				perRow[y]++
				cols[x] = true
			}
		}
	}
	return
}

func TestSpectrogramSilenceDrawsNothing(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(silent{})
	a.SetMode(ModeSpectrogram)
	for i := 0; i < 10; i++ {
		a.Update(80, 8)
	}
	a.Draw(s, Rect{0, 0, 80, 8})
	s.Show()
	if _, cols := heatCells(s); len(cols) != 0 {
		t.Fatalf("silence lit %d columns", len(cols))
	}
}

func TestSpectrogramToneIsANarrowHorizontalStreak(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(sineSpectrum{hz: 44100.0 / 2048 * 46})
	a.SetMode(ModeSpectrogram)
	for i := 0; i < 2*80; i++ { // enough ticks to fill every column
		a.Update(80, 8)
	}
	a.Draw(s, Rect{0, 0, 80, 8})
	s.Show()
	perRow, cols := heatCells(s)
	litRows := 0
	for _, n := range perRow {
		if n > 0 {
			litRows++
		}
	}
	if litRows == 0 || litRows > 2 {
		t.Fatalf("a single tone should occupy one or two rows, got %v", perRow)
	}
	if len(cols) != 80 {
		t.Fatalf("after enough ticks every column should carry the tone, got %d", len(cols))
	}
}

func TestSpectrogramScrollsWithNewestOnTheRight(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(sineSpectrum{hz: 44100.0 / 2048 * 46})
	a.SetMode(ModeSpectrogram)
	for i := 0; i < 20; i++ {
		a.Update(80, 8)
	}
	a.src = silent{}
	for i := 0; i < 6; i++ { // three columns of silence arrive
		a.Update(80, 8)
	}
	a.Draw(s, Rect{0, 0, 80, 8})
	s.Show()
	_, cols := heatCells(s)
	if cols[79] || cols[78] || cols[77] {
		t.Fatalf("the newest columns should be dark: %v", cols)
	}
	if !cols[76] {
		t.Fatalf("the tone should sit just left of the silence: %v", cols)
	}
	if cols[0] {
		t.Fatal("twenty ticks cannot have reached the far left")
	}
}

func TestVCyclesThroughAllThreeViews(t *testing.T) {
	app, _, _ := newApp(t)
	key(app, tcell.KeyRune, 'v')
	key(app, tcell.KeyRune, 'v')
	if app.an.Mode() != ModeSpectrogram {
		t.Fatalf("mode = %v", app.an.Mode())
	}
	key(app, tcell.KeyRune, 'v')
	if app.an.Mode() != ModeBars {
		t.Fatalf("mode = %v", app.an.Mode())
	}
	app.SetVisual("spectrogram")
	if app.an.Mode() != ModeSpectrogram {
		t.Fatal("SetVisual should know the spectrogram")
	}
}
