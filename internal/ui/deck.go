package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

type DeckState struct {
	Playing          bool
	Position, Length time.Duration
	Artist, Title    string
	Codec            string
	SampleRate       int
	BitDepth         int
	Bitrate          int
	Volume           float64
	Shuffle, Repeat  bool
	Notice           string
	Tick             int
}

func Clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Marquee returns a width-cell window onto text. Text that fits is padded;
// longer text scrolls one grapheme every 4 ticks with a gap before it repeats.
func Marquee(text string, width, tick int) string {
	if uniseg.StringWidth(text) <= width {
		return Fit(text, width)
	}
	var loop []string
	g := uniseg.NewGraphemes(text + "   •   ")
	for g.Next() {
		loop = append(loop, g.Str())
	}
	off := (tick / 4) % len(loop)
	var b strings.Builder
	for i := 0; i < len(loop); i++ {
		b.WriteString(loop[(off+i)%len(loop)])
	}
	return Fit(b.String(), width)
}

func DrawDeck(s tcell.Screen, r Rect, st DeckState) {
	base := tcell.StyleDefault
	dim := base.Dim(true)
	bold := base.Bold(true)

	// Row 1: state, clock, marquee.
	glyph := "■"
	if st.Playing {
		glyph = "▶"
	}
	left := fmt.Sprintf(" %s  %s / %s   ", glyph, Clock(st.Position), Clock(st.Length))
	x := r.X + PutStr(s, r.X, r.Y, left, bold)
	title := st.Title
	if st.Artist != "" {
		title = st.Artist + " — " + st.Title
	}
	PutStr(s, x, r.Y, Marquee(title, r.W-(x-r.X)-1, st.Tick), base)

	// Row 2: format readout, or a notice.
	if st.Notice != "" {
		PutStr(s, r.X, r.Y+1, Fit(" "+st.Notice, r.W), base.Foreground(tcell.PaletteColor(11)))
	} else {
		info := fmt.Sprintf(" %s  %s  %dbit  %dk", strings.ToUpper(st.Codec), khz(st.SampleRate), st.BitDepth, st.Bitrate)
		if st.BitDepth == 0 {
			info = fmt.Sprintf(" %s  %s  %dk", strings.ToUpper(st.Codec), khz(st.SampleRate), st.Bitrate)
		}
		vol := volumeBar(st.Volume, 10)
		flags := "  "
		if st.Shuffle {
			flags = " ⇄"
		}
		if st.Repeat {
			flags += " ↻"
		}
		right := "vol " + vol + flags + " "
		rw := uniseg.StringWidth(right)
		PutStr(s, r.X, r.Y+1, Fit(info, r.W-rw), dim)
		PutStr(s, r.X+r.W-rw, r.Y+1, right, dim)
	}

	// Row 3: seek bar.
	w := r.W - 2
	pos := 0
	if st.Length > 0 {
		pos = int(float64(w-1) * float64(st.Position) / float64(st.Length))
	}
	var b strings.Builder
	b.WriteRune(' ')
	for i := 0; i < w; i++ {
		switch {
		case i == pos:
			b.WriteRune('●')
		case i < pos:
			b.WriteRune('━')
		default:
			b.WriteRune('─')
		}
	}
	PutStr(s, r.X, r.Y+2, b.String(), base)
}

func khz(rate int) string {
	if rate == 0 {
		return "—"
	}
	if rate%1000 == 0 {
		return fmt.Sprintf("%dk", rate/1000)
	}
	return fmt.Sprintf("%.1fk", float64(rate)/1000)
}

func volumeBar(v float64, width int) string {
	on := int(v*float64(width) + 0.5)
	return strings.Repeat("█", on) + strings.Repeat("░", width-on)
}

// SeekBarHit maps a click at column x on the seek row to a fraction of the
// track, or -1 if the click is outside the bar.
func SeekBarHit(r Rect, x int) float64 {
	w := r.W - 2
	i := x - r.X - 1
	if i < 0 || i >= w {
		return -1
	}
	return float64(i) / float64(w-1)
}
