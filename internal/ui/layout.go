package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

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

// PutStr writes text at x,y one grapheme cluster at a time, advancing by each
// cluster's display width, and returns the cells used. It does not clip;
// callers pass text already cut to width (see Fit).
func PutStr(s tcell.Screen, x, y int, text string, style tcell.Style) int {
	n := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		rs := g.Runes()
		w := g.Width()
		s.SetContent(x+n, y, rs[0], rs[1:], style)
		n += w
	}
	return n
}

// Fit cuts or pads text to exactly width cells. A wide glyph that would
// straddle the cut is replaced by a space.
func Fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		w := g.Width()
		if used+w > width {
			break
		}
		b.WriteString(g.Str())
		used += w
	}
	for used < width {
		b.WriteByte(' ')
		used++
	}
	return b.String()
}
