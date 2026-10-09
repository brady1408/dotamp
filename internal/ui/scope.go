package ui

import (
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
	scopeGhostColor    = 22  // palette dark green: last frame's trace, like phosphor afterglow
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
		// Deliberately lopsided rounding, kept on purpose. The top half
		// rounds normally; the bottom half truncates toward the centre, so
		// the negative side sits one dot high and its smallest swings
		// flatten onto the centre line. It started as a rounding bug, but
		// it gives the trace a natural feel: music is never perfectly
		// still, and a scope that drew every last flicker read as nervous,
		// while a mathematically perfect gate drew a ruler-straight line
		// that was just as distracting. This one-sided softness is what
		// makes quiet passages look the way they sound. Do not "fix" it;
		// TestScopeRoundingIsLopsidedOnPurpose guards it.
		y := centre - int(v*swing+0.5)
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
	// Last frame's trace first, dim, so the live one overwrites where they
	// meet: the afterglow of a phosphor screen, which turns two frames the
	// eye would blend into a single moving line with a soft tail.
	if a.Afterglow && len(a.ghost) == len(trace) {
		a.drawTrace(a.ghost, func(int) int { return scopeGhostColor })
	}
	a.drawTrace(trace, func(yy int) int {
		swing := yy - centre
		if swing < 0 {
			swing = -swing
		}
		return colorFor(swing*2, dotH, false)
	})
	a.ghost = append(a.ghost[:0], trace...)
	a.paint(s, r)
}

// drawTrace joins neighbouring columns so a steep edge is a line, not dots.
func (a *Analyzer) drawTrace(trace []int, color func(y int) int) {
	prev := trace[0]
	for x, y := range trace {
		lo, hi := min(prev, y), max(prev, y)
		for yy := lo; yy <= hi; yy++ {
			a.cv.Set(x, yy, color(yy))
		}
		prev = y
	}
}
