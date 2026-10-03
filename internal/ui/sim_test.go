package ui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func sim(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	t.Cleanup(s.Fini)
	return s
}

func rows(s tcell.SimulationScreen) []string {
	cells, w, h := s.GetContents()
	out := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			b.WriteString(string(cells[y*w+x].Runes))
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}
