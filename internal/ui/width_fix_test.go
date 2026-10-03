package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

func TestPutStrAdvancesByCellWidth(t *testing.T) {
	s := sim(t, 20, 1)
	n := PutStr(s, 0, 0, "宇多田ヒカル", tcell.StyleDefault)
	s.Show()
	if n != 12 {
		t.Fatalf("cells used = %d, want 12", n)
	}
	if r := rows(s)[0]; !strings.Contains(r, "宇多田ヒカル") {
		t.Fatalf("row = %q", r)
	}
}

func TestFitMeasuresCells(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
	}{{"宇多田ヒカル", 5}, {"abc", 6}, {"宇多田", 7}, {"宇多田ヒカル — 光", 9}} {
		got := Fit(tc.in, tc.width)
		if w := uniseg.StringWidth(got); w != tc.width {
			t.Errorf("Fit(%q, %d) = %q, width %d", tc.in, tc.width, got, w)
		}
	}
}

func TestMarqueeWindowIsCellWidth(t *testing.T) {
	long := "宇宙戦艦ヤマト — 真っ赤なスカーフ"
	for tick := 0; tick < 80; tick++ {
		if w := uniseg.StringWidth(Marquee(long, 10, tick)); w != 10 {
			t.Fatalf("tick %d: width %d", tick, w)
		}
	}
}
