package ui

import (
	"math"

	"github.com/gdamore/tcell/v2"
)

// Mode selects what the analyzer pane draws from the tap.
type Mode int

const (
	ModeBars        Mode = iota // the spectrum analyzer
	ModeScope                   // the oscilloscope
	ModeSpectrogram             // the waterfall
	ModeStereo                  // the stereo field
)

var modeNames = map[Mode]string{ModeBars: "bars", ModeScope: "scope", ModeSpectrogram: "spectrogram", ModeStereo: "stereo"}

func (m Mode) String() string { return modeNames[m] }

// ParseMode returns the mode for a saved name, and whether the name was known.
func ParseMode(name string) (Mode, bool) {
	for m, n := range modeNames {
		if n == name {
			return m, true
		}
	}
	return ModeBars, false
}

const (
	scopeSamplesPerDot = 4   // 80 columns show about 15 ms at 44.1 kHz
	scopeTriggerSearch = 512 // samples ahead of the window in which a trigger is looked for
)

// scopeTrace returns one vertical dot position per horizontal dot: the most
// recent window of the tap, started at a rising zero crossing so a steady
// tone holds still on screen instead of jittering with every tick. The
// window is a.trace, sized by Update for this width.
func (a *Analyzer) scopeTrace(dotW int) []int {
	dotH := a.cv.DotH()
	centre := dotH / 2
	swing := float64(centre - 1)
	window := dotW * scopeSamplesPerDot
	src := a.trace
	if len(src) < window+1 { // not sampled at this width yet
		src = make([]float64, window+1)
	}
	start := 0
	for i := 1; i+window <= len(src); i++ {
		if src[i-1] < 0 && src[i] >= 0 {
			start = i
			break
		}
	}
	trace := make([]int, dotW)
	for x := range trace {
		i := start + x*scopeSamplesPerDot
		if i >= len(src) {
			i = len(src) - 1
		}
		v := src[i]
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		y := centre - int(math.Round(v*swing))
		if y < 0 {
			y = 0
		}
		if y >= dotH {
			y = dotH - 1
		}
		trace[x] = y
	}
	return trace
}

// drawScope paints the trace as a connected line, coloured by how far it
// swings from the centre with the same bands the bars use.
func (a *Analyzer) drawScope(s tcell.Screen, r Rect) {
	a.cv.Clear()
	dotH := a.cv.DotH()
	centre := dotH / 2
	trace := a.scopeTrace(a.cv.DotW())
	prev := trace[0]
	for x, y := range trace {
		lo, hi := min(prev, y), max(prev, y)
		for yy := lo; yy <= hi; yy++ {
			swing := yy - centre
			if swing < 0 {
				swing = -swing
			}
			a.cv.Set(x, yy, colorFor(swing*2, dotH, false))
		}
		prev = y
	}
	a.paint(s, r)
}
