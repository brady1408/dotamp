package ui

import "github.com/gdamore/tcell/v2"

type SearchField struct {
	Text string
	Open bool
}

// Key feeds a key event to the field. submit is true on Enter with text,
// cancel on Escape; both close the field.
func (f *SearchField) Key(ev *tcell.EventKey) (submit, cancel bool) {
	switch ev.Key() {
	case tcell.KeyEnter:
		f.Open = false
		return f.Text != "", f.Text == ""
	case tcell.KeyEscape:
		f.Open, f.Text = false, ""
		return false, true
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		rs := []rune(f.Text)
		if len(rs) > 0 {
			f.Text = string(rs[:len(rs)-1])
		}
	case tcell.KeyRune:
		f.Text += string(ev.Rune())
	}
	return false, false
}

func (f *SearchField) Draw(s tcell.Screen, r Rect) {
	PutStr(s, r.X, r.Y, Fit(" / "+f.Text+"▏", r.W), tcell.StyleDefault.Bold(true))
}
