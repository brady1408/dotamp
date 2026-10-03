package ui

import (
	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/canvas"
	"github.com/brady1408/dotamp/internal/dsp"
)

const (
	fftN    = 2048
	bandLo  = 40.0
	bandHi  = 16000.0
	decay   = 0.85
	capHold = 15
	capFall = 0.03
)

type SpectrumSource interface {
	Spectrum(n int, dst []float64) int
}

type Analyzer struct {
	src     SpectrumSource
	window  []float64
	samples []float64
	bands   dsp.Bands
	nbars   int
	levels  []float64
	smooth  *dsp.Smoother
	cv      *canvas.Braille
}

func NewAnalyzer(src SpectrumSource) *Analyzer {
	return &Analyzer{
		src:     src,
		window:  dsp.Hann(fftN),
		samples: make([]float64, fftN),
		smooth:  dsp.NewSmoother(0, decay, capHold, capFall),
	}
}

func (a *Analyzer) resize(w, h int) {
	if a.cv != nil && a.cv.W == w && a.cv.H == h {
		return
	}
	a.cv = canvas.NewBraille(w, h)
	a.nbars = w
	a.bands = dsp.NewBands(a.nbars, fftN/2, 44100, bandLo, bandHi)
	a.levels = make([]float64, a.nbars)
}

func (a *Analyzer) Draw(s tcell.Screen, r Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	a.resize(r.W, r.H)
	if n := a.src.Spectrum(fftN, a.samples); n == fftN {
		mag := dsp.Magnitudes(a.samples, a.window)
		a.bands.Levels(mag, a.levels)
	} else {
		for i := range a.levels {
			a.levels[i] = 0
		}
	}
	a.smooth.Update(a.levels)

	a.cv.Clear()
	dotH := a.cv.DotH()
	for i := 0; i < a.nbars; i++ {
		h := int(a.smooth.Bars[i]*float64(dotH) + 0.5)
		x := i * 2
		for y := 0; y < h; y++ {
			c := colorFor(y, dotH, false)
			a.cv.Set(x, dotH-1-y, c)
			a.cv.Set(x+1, dotH-1-y, c)
		}
		if top := int(a.smooth.Caps[i]*float64(dotH) + 0.5); top > 0 {
			y := dotH - top
			c := colorFor(top-1, dotH, true)
			a.cv.Set(x, y, c)
			a.cv.Set(x+1, y, c)
		}
	}
	for cy := 0; cy < r.H; cy++ {
		for cx := 0; cx < r.W; cx++ {
			ru, col := a.cv.Cell(cx, cy)
			style := tcell.StyleDefault
			if col >= 0 {
				style = style.Foreground(tcell.PaletteColor(col))
			}
			s.SetContent(r.X+cx, r.Y+cy, ru, nil, style)
		}
	}
}

// colorFor picks the palette index for a dot at height y of dotH: green below
// 60 %, yellow to 85 %, red above. bright selects the cap variants.
func colorFor(y, dotH int, bright bool) int {
	f := float64(y) / float64(dotH)
	switch {
	case f < 0.6:
		if bright {
			return 10
		}
		return 2
	case f < 0.85:
		if bright {
			return 11
		}
		return 3
	default:
		if bright {
			return 9
		}
		return 1
	}
}
