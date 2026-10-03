package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/library"
)

type Row struct {
	Text, Right string
	Header      bool
	Album       *library.Album
	Track       *library.Track
	Artist      *library.Artist
	Playing     bool
}

type List struct {
	Rows     []Row
	Sel, Top int
}

func (l *List) SetRows(rows []Row) {
	l.Rows, l.Top = rows, 0
	l.Sel = -1
	for i, r := range rows {
		if !r.Header {
			l.Sel = i
			break
		}
	}
}

func (l *List) Selected() *Row {
	if l.Sel < 0 || l.Sel >= len(l.Rows) {
		return nil
	}
	return &l.Rows[l.Sel]
}

func (l *List) Move(delta int) {
	if l.Sel < 0 {
		return
	}
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	cur := l.Sel
	for delta > 0 {
		next := cur + step
		for next >= 0 && next < len(l.Rows) && l.Rows[next].Header {
			next += step
		}
		if next < 0 || next >= len(l.Rows) {
			break
		}
		cur = next
		delta--
	}
	l.Sel = cur
}

func (l *List) Draw(s tcell.Screen, r Rect, focused bool) {
	if r.H <= 0 {
		return
	}
	if l.Sel >= 0 {
		if l.Sel < l.Top {
			l.Top = l.Sel
		}
		if l.Sel >= l.Top+r.H {
			l.Top = l.Sel - r.H + 1
		}
	}
	for y := 0; y < r.H; y++ {
		i := l.Top + y
		if i >= len(l.Rows) {
			PutStr(s, r.X, r.Y+y, Fit("", r.W), tcell.StyleDefault)
			continue
		}
		row := l.Rows[i]
		style := tcell.StyleDefault
		text := " " + row.Text
		switch {
		case row.Header:
			style = style.Bold(true).Dim(true)
			text = row.Text
		case i == l.Sel && focused:
			style = style.Reverse(true)
		case row.Playing:
			style = style.Foreground(tcell.PaletteColor(10))
		}
		right := row.Right
		if right != "" {
			right += " "
		}
		left := Fit(text, r.W-len([]rune(right)))
		PutStr(s, r.X, r.Y+y, left, style)
		PutStr(s, r.X+r.W-len([]rune(right)), r.Y+y, right, style)
	}
}

func (l *List) RowAt(r Rect, y int) int {
	i := l.Top + (y - r.Y)
	if y < r.Y || y >= r.Y+r.H || i < 0 || i >= len(l.Rows) {
		return -1
	}
	return i
}

func QueueRows(ts []library.Track, playing int) []Row {
	rows := make([]Row, len(ts))
	for i := range ts {
		t := &ts[i]
		rows[i] = Row{
			Text:    fmt.Sprintf("%d. %s — %s", i+1, t.Artist, t.Title),
			Right:   Clock(t.Duration),
			Track:   t,
			Playing: i == playing,
		}
		if t.Artist == "" {
			rows[i].Text = fmt.Sprintf("%d. %s", i+1, t.Title)
		}
	}
	return rows
}
