package ui

import "github.com/gdamore/tcell/v2"

type Rect struct{ X, Y, W, H int }

type Layout struct {
	Deck, Analyzer, Pane Rect
	TooSmall             bool
}

const (
	MinW, MinH = 80, 24
	deckRows   = 3
)

func Compute(w, h int) Layout {
	l := Layout{TooSmall: w < MinW || h < MinH}
	rest := max(h-deckRows, 0)
	an := max(rest/3, 4)
	if an > rest {
		an = rest
	}
	l.Deck = Rect{0, 0, w, deckRows}
	l.Analyzer = Rect{0, deckRows, w, an}
	l.Pane = Rect{0, deckRows + an, w, max(rest-an, 0)}
	return l
}

func DrawTooSmall(s tcell.Screen, w, h int) {
	s.Clear()
	msg := "dotamp needs at least 80×24"
	x := max((w-len([]rune(msg)))/2, 0)
	PutStr(s, x, h/2, msg, tcell.StyleDefault)
}

// PutStr writes text at x,y and returns the number of cells used. It does not
// clip; callers pass text already cut to width (see Fit).
func PutStr(s tcell.Screen, x, y int, text string, style tcell.Style) int {
	n := 0
	for _, r := range text {
		s.SetContent(x+n, y, r, nil, style)
		n++
	}
	return n
}

// Fit cuts or pads text to exactly width runes.
func Fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	rs := []rune(text)
	if len(rs) >= width {
		return string(rs[:width])
	}
	for len(rs) < width {
		rs = append(rs, ' ')
	}
	return string(rs)
}
