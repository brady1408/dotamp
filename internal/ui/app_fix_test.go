package ui

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestAppSingleClickSelectsDoubleClickActivates(t *testing.T) {
	app, _, lib := newApp(t)
	key(app, tcell.KeyTab, 0)   // Library root menu
	key(app, tcell.KeyDown, 0)  // Recently added
	key(app, tcell.KeyEnter, 0) // "Albums" header at row 0, albums below
	app.Draw()
	y := app.layout.Pane.Y + 1 + 2 // second album row
	app.Handle(tcell.NewEventMouse(5, y, tcell.Button1, 0))
	app.Handle(tcell.NewEventMouse(5, y, tcell.ButtonNone, 0))
	if len(lib.played) != 0 {
		t.Fatal("a single click must only select")
	}
	if sel := app.browser.List().Selected(); sel == nil || sel.Album == nil || sel.Album.ID != "2" {
		t.Fatalf("single click should select the row: %+v", sel)
	}
	app.Handle(tcell.NewEventMouse(5, y, tcell.Button1, 0))
	if app.browser.Title() != "Recent Two" {
		t.Fatalf("a double click must activate (open the album); title = %q", app.browser.Title())
	}
	// A click long after the previous one is a new single click: it selects
	// the track row under it and plays nothing.
	app.lastClick = time.Now().Add(-time.Second)
	app.Handle(tcell.NewEventMouse(5, y, tcell.Button1, 0))
	if len(lib.played) != 0 {
		t.Fatal("a slow click must not activate")
	}
}

func TestAnalyzerDecaysOnTicksNotDraws(t *testing.T) {
	s := sim(t, 80, 8)
	a := NewAnalyzer(sineSpectrum{hz: 44100.0 / 2048 * 46})
	r := Rect{0, 0, 80, 8}
	a.Update(r.W, r.H)
	a.src = silent{}
	before := append([]float64(nil), a.smooth.Bars...)
	for i := 0; i < 10; i++ {
		a.Draw(s, r)
	}
	for i := range before {
		if a.smooth.Bars[i] != before[i] {
			t.Fatal("Draw must not advance the smoother")
		}
	}
	a.Update(r.W, r.H)
	moved := false
	for i := range before {
		if a.smooth.Bars[i] < before[i] {
			moved = true
		}
	}
	if !moved {
		t.Fatal("Update should decay the bars")
	}
}
