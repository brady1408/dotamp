package ui

import "testing"

func TestLayout80x24(t *testing.T) {
	l := Compute(80, 24)
	if l.TooSmall {
		t.Fatal("80x24 is the minimum and must not be too small")
	}
	if l.Deck != (Rect{0, 0, 80, 3}) {
		t.Fatalf("deck = %+v", l.Deck)
	}
	if l.Analyzer != (Rect{0, 3, 80, 7}) { // (24-3)/3 = 7
		t.Fatalf("analyzer = %+v", l.Analyzer)
	}
	if l.Pane != (Rect{0, 10, 80, 14}) {
		t.Fatalf("pane = %+v", l.Pane)
	}
}

func TestLayoutAnalyzerMinimum(t *testing.T) {
	l := Compute(100, 14) // too small by height, but rects must still be sane
	if !l.TooSmall || l.Analyzer.H < 4 || l.Pane.H < 0 {
		t.Fatalf("%+v", l)
	}
	s := sim(t, 40, 10)
	DrawTooSmall(s, 40, 10)
	s.Show()
	found := false
	for _, r := range rows(s) {
		if len(r) > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("too-small notice must draw something")
	}
}
