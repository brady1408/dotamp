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
	Spectrum(n int, dst []float64) int // mono frames at the playhead
	Stereo(n int, l, r []float64) int  // the same frames, channels apart
	Rate() int                         // frames per second those are
}

type Analyzer struct {
	mode    Mode
	src     SpectrumSource
	window  []float64
	samples []float64
	bands   dsp.Bands
	nbars   int
	levels  []float64
	smooth  *dsp.Smoother
	cv      *canvas.Braille
	sg      spectrogram
	left    []float64 // the stereo field's window
	right   []float64
	trace   []float64 // the scope's window: as many samples as the width needs, plus room to find a trigger
}

func NewAnalyzer(src SpectrumSource) *Analyzer {
	return &Analyzer{
		src:     src,
		window:  dsp.Hann(fftN),
		samples: make([]float64, fftN),
		smooth:  dsp.NewSmoother(0, decay, capHold, capFall),
	}
}

func (a *Analyzer) Mode() Mode     { return a.mode }
func (a *Analyzer) SetMode(m Mode) { a.mode = m }
func (a *Analyzer) NextMode() Mode { a.mode = (a.mode + 1) % Mode(len(modeNames)); return a.mode }

func (a *Analyzer) resize(w, h int) {
	if a.cv != nil && a.cv.W == w && a.cv.H == h {
		return
	}
	a.cv = canvas.NewBraille(w, h)
	a.nbars = w
	a.bands = dsp.NewBands(a.nbars, fftN/2, a.src.Rate(), bandLo, bandHi)
	a.levels = make([]float64, a.nbars)
	a.smooth.Update(a.levels) // size the smoother to the new bar count before any Draw
}

// Update samples the tap and advances the smoother by one tick. The app calls
// it on the 30 Hz tick only, so decay runs on time, not on key repeat.
func (a *Analyzer) Update(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	a.resize(w, h)
	var mag []float64
	if n := a.src.Spectrum(fftN, a.samples); n == fftN {
		mag = dsp.Magnitudes(a.samples, a.window)
		a.bands.Levels(mag, a.levels)
	} else {
		for i := range a.levels {
			a.levels[i] = 0
		}
		for i := range a.samples {
			a.samples[i] = 0
		}
	}
	a.smooth.Update(a.levels)
	switch a.mode {
	case ModeScope:
		need := w*2*scopeSamplesPerDot + scopeTriggerSearch
		if len(a.trace) != need {
			a.trace = make([]float64, need)
		}
		if n := a.src.Spectrum(need, a.trace); n != need {
			for i := range a.trace {
				a.trace[i] = 0
			}
		}
	case ModeSpectrogram:
		a.sg.resize(w, h, a.src.Rate())
		a.sg.update(mag)
	case ModeStereo:
		if a.left == nil {
			a.left, a.right = make([]float64, stereoWindow), make([]float64, stereoWindow)
		}
		if n := a.src.Stereo(stereoWindow, a.left, a.right); n != stereoWindow {
			for i := range a.left {
				a.left[i], a.right[i] = 0, 0
			}
		}
	}
}

// Draw paints the current mode's view of the last Update.
func (a *Analyzer) Draw(s tcell.Screen, r Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	a.resize(r.W, r.H)
	switch a.mode {
	case ModeScope:
		a.drawScope(s, r)
		return
	case ModeSpectrogram:
		a.sg.resize(r.W, r.H, a.src.Rate())
		a.sg.draw(s, r)
		return
	case ModeStereo:
		a.drawStereo(s, r)
		return
	}
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
	a.paint(s, r)
}

// paint copies the canvas to the screen.
func (a *Analyzer) paint(s tcell.Screen, r Rect) {
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
