package ui

import "github.com/gdamore/tcell/v2"

var helpLines = []string{
	"space      play / pause",
	"n / p      next / previous track",
	"← / →      seek 5 s",
	"+ / -      volume",
	"Tab        switch Queue / Library",
	"/          search the library",
	"Enter      play track, or play album",
	"a          add track or album to the queue",
	"Backspace  back (Library)",
	"A-Z / #    jump to letter (Artists)",
	"s / r      shuffle / repeat",
	"v          bars / scope / spectrogram / stereo",
	"?          this help",
	"q          quit",
}

func DrawHelp(s tcell.Screen, r Rect) {
	w := 44
	h := len(helpLines) + 2
	x := r.X + max((r.W-w)/2, 0)
	y := r.Y + max((r.H-h)/2, 0)
	style := tcell.StyleDefault.Reverse(true)
	for i := 0; i < h; i++ {
		line := ""
		if i > 0 && i <= len(helpLines) {
			line = "  " + helpLines[i-1]
		}
		PutStr(s, x, y+i, Fit(line, w), style)
	}
}
