package ui

import (
	"math"

	"github.com/gdamore/tcell/v2"
)

// The stereo field plots the newest window of the tap with the left channel
// across and the right channel up, inside the largest square the pane holds.
// A mono signal draws the rising diagonal; a wide mix fills a cloud; a
// channel out of phase leans the other way.
const stereoWindow = 1024 // frames per frame, about 23 ms

func (a *Analyzer) drawStereo(s tcell.Screen, r Rect) {
	a.cv.Clear()
	dotW, dotH := a.cv.DotW(), a.cv.DotH()
	side := min(dotW, dotH)
	ox, oy := dotW/2, dotH/2 // centre
	half := float64(side/2 - 1)
	for i := range a.left {
		l, rr := clamp1(a.left[i]), clamp1(a.right[i])
		x := ox + int(math.Round(l*half))
		y := oy - int(math.Round(rr*half))
		swing := int(max(abs1(l), abs1(rr)) * float64(dotH))
		a.cv.Set(x, y, colorFor(swing, dotH, false))
	}
	a.paint(s, r)
}

func clamp1(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}

func abs1(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
