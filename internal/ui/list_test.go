package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

func TestListSkipsHeadersAndClamps(t *testing.T) {
	var l List
	l.SetRows([]Row{{Text: "Albums", Header: true}, {Text: "A"}, {Text: "B"}, {Text: "Tracks", Header: true}})
	if l.Sel != 1 {
		t.Fatalf("initial sel = %d", l.Sel)
	}
	l.Move(1)
	if l.Sel != 2 {
		t.Fatalf("sel = %d", l.Sel)
	}
	l.Move(1) // only a header below: stay
	if l.Sel != 2 {
		t.Fatalf("sel past trailing header = %d", l.Sel)
	}
	l.Move(-5)
	if l.Sel != 1 {
		t.Fatalf("sel after big up = %d", l.Sel)
	}
}

func TestListEmptyGroupRenders(t *testing.T) {
	var l List
	l.SetRows([]Row{{Text: "Artists", Header: true}, {Text: "Albums", Header: true}, {Text: "Tracks", Header: true}})
	if l.Selected() != nil {
		t.Fatal("no selectable rows -> nil")
	}
	s := sim(t, 40, 5)
	l.Draw(s, Rect{0, 0, 40, 5}, true)
	s.Show()
	if r := rows(s); !strings.Contains(r[0], "Artists") || !strings.Contains(r[2], "Tracks") {
		t.Fatalf("rows = %q", r)
	}
}

func TestListScrollKeepsSelectionVisible(t *testing.T) {
	var l List
	var rs []Row
	for i := 0; i < 30; i++ {
		rs = append(rs, Row{Text: "row"})
	}
	l.SetRows(rs)
	l.Move(25)
	s := sim(t, 40, 5)
	r := Rect{0, 0, 40, 5}
	l.Draw(s, r, true)
	if l.Top > l.Sel || l.Sel >= l.Top+r.H {
		t.Fatalf("sel %d not within top %d + %d", l.Sel, l.Top, r.H)
	}
	if got := l.RowAt(r, 4); got != l.Top+4 {
		t.Fatalf("RowAt = %d", got)
	}
}

func TestQueueRowsMarksPlaying(t *testing.T) {
	rs := QueueRows([]library.Track{{Title: "A", Duration: 61 * time.Second}, {Title: "B"}}, 1)
	if rs[0].Playing || !rs[1].Playing || rs[0].Right != "1:01" || !strings.HasPrefix(rs[0].Text, "1.") {
		t.Fatalf("rows = %+v", rs)
	}
}
