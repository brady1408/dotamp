package ui

import (
	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/dsp"
)

// The spectrogram keeps one column of band levels per two ticks and paints
// them as a heat map: time runs left to right with the newest column at the
// right edge, frequency runs bottom to top on the bars' log scale, and
// loudness is colour. Each cell holds two bands, drawn as a half block with
// the upper band in the foreground and the lower in the background.
const spectrogramTicksPerColumn = 2

type spectrogram struct {
	w, h    int         // pane size the history was built for
	bands   dsp.Bands   // h*2 bands
	levels  []float64   // scratch: the current tick's levels
	column  []float64   // the column being accumulated (max over its ticks)
	ticks   int         // ticks accumulated into column so far
	history [][]float64 // ring of columns, len w
	head    int         // index of the oldest column in history
}

func (sg *spectrogram) resize(w, h, rate int) {
	if sg.w == w && sg.h == h {
		return
	}
	sg.w, sg.h = w, h
	sg.bands = dsp.NewBands(h*2, fftN/2, rate, bandLo, bandHi)
	sg.levels = make([]float64, h*2)
	sg.column = make([]float64, h*2)
	sg.ticks = 0
	sg.history = make([][]float64, w)
	for i := range sg.history {
		sg.history[i] = make([]float64, h*2)
	}
	sg.head = 0
}

// update folds one tick's magnitudes into the column under construction and
// commits it to the history every spectrogramTicksPerColumn ticks.
func (sg *spectrogram) update(mag []float64) {
	if mag == nil {
		for i := range sg.levels {
			sg.levels[i] = 0
		}
	} else {
		sg.bands.Levels(mag, sg.levels)
	}
	for i, v := range sg.levels {
		if v > sg.column[i] {
			sg.column[i] = v
		}
	}
	sg.ticks++
	if sg.ticks < spectrogramTicksPerColumn {
		return
	}
	copy(sg.history[sg.head], sg.column)
	sg.head = (sg.head + 1) % len(sg.history)
	for i := range sg.column {
		sg.column[i] = 0
	}
	sg.ticks = 0
}

// heat maps a level to a palette colour, or -1 for off.
func heat(v float64) int {
	switch {
	case v < 0.15:
		return -1
	case v < 0.4:
		return 2 // green
	case v < 0.6:
		return 10 // bright green
	case v < 0.8:
		return 11 // yellow
	default:
		return 9 // red
	}
}

func (sg *spectrogram) draw(s tcell.Screen, r Rect) {
	for x := 0; x < r.W; x++ {
		col := sg.history[(sg.head+x)%len(sg.history)] // oldest at the left
		for cy := 0; cy < r.H; cy++ {
			lower := (r.H - 1 - cy) * 2
			top, bottom := heat(col[lower+1]), heat(col[lower])
			if top < 0 && bottom < 0 {
				s.SetContent(r.X+x, r.Y+cy, ' ', nil, tcell.StyleDefault)
				continue
			}
			style := tcell.StyleDefault
			if top >= 0 {
				style = style.Foreground(tcell.PaletteColor(top))
			}
			if bottom >= 0 {
				style = style.Background(tcell.PaletteColor(bottom))
			}
			s.SetContent(r.X+x, r.Y+cy, '▀', nil, style)
		}
	}
}
