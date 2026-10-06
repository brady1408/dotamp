package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/rivo/uniseg"
)

func TestClock(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0:00", 83 * time.Second: "1:23", 3600*time.Second + 5*time.Second: "1:00:05",
	} {
		if got := Clock(d); got != want {
			t.Errorf("Clock(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestMarqueeFitsAndScrollsRuneSafe(t *testing.T) {
	if got := Marquee("abc", 5, 7); got != "abc  " {
		t.Fatalf("fit = %q", got)
	}
	long := "宇宙戦艦ヤマト — 真っ赤なスカーフ"
	seen := map[string]bool{}
	for tick := 0; tick < 60; tick++ {
		m := Marquee(long, 10, tick)
		if w := uniseg.StringWidth(m); w != 10 {
			t.Fatalf("tick %d: %d cells %q", tick, w, m)
		}
		if strings.ContainsRune(m, '�') {
			t.Fatalf("tick %d sliced a rune: %q", tick, m)
		}
		seen[m] = true
	}
	if len(seen) < 10 {
		t.Fatal("marquee did not scroll")
	}
}

func TestDrawDeckRows(t *testing.T) {
	s := sim(t, 80, 3)
	DrawDeck(s, Rect{0, 0, 80, 3}, DeckState{
		Playing: true, Position: 83 * time.Second, Length: 291 * time.Second,
		Artist: "*NSYNC", Title: "Tearin' Up My Heart",
		Codec: "flac", SampleRate: 44100, BitDepth: 16, Bitrate: 978, Volume: 0.7,
	})
	s.Show()
	r := rows(s)
	if !strings.Contains(r[0], "1:23 / 4:51") || !strings.Contains(r[0], "*NSYNC — Tearin' Up My Heart") {
		t.Fatalf("row0 = %q", r[0])
	}
	if !strings.Contains(r[1], "FLAC") || !strings.Contains(r[1], "44.1k") || !strings.Contains(r[1], "978k") {
		t.Fatalf("row1 = %q", r[1])
	}
	if len([]rune(r[2])) < 70 || !strings.ContainsRune(r[2], '●') {
		t.Fatalf("seek bar row = %q", r[2])
	}
}

func TestDrawDeckNoticeReplacesRow2(t *testing.T) {
	s := sim(t, 80, 3)
	DrawDeck(s, Rect{0, 0, 80, 3}, DeckState{Notice: "Skipped X: network error", Codec: "flac"})
	s.Show()
	r := rows(s)
	if !strings.Contains(r[1], "Skipped X") || strings.Contains(r[1], "FLAC") {
		t.Fatalf("row1 = %q", r[1])
	}
}

func TestDrawDeckShowsTheAlbum(t *testing.T) {
	s := sim(t, 80, 3)
	DrawDeck(s, Rect{0, 0, 80, 3}, DeckState{
		Playing: true, Artist: "*NSYNC", Title: "Bye Bye Bye", Album: "No Strings Attached",
		Codec: "flac", SampleRate: 44100, BitDepth: 16, Bitrate: 978, Volume: 0.7,
	})
	s.Show()
	r := rows(s)
	if !strings.Contains(r[1], "No Strings Attached") || !strings.Contains(r[1], "FLAC") {
		t.Fatalf("row1 should carry the format and the album: %q", r[1])
	}
	// A long album name is cut rather than pushing the volume bar off the row.
	DrawDeck(s, Rect{0, 0, 80, 3}, DeckState{
		Artist: "A", Title: "B", Album: strings.Repeat("Very Long Album Name ", 6),
		Codec: "flac", SampleRate: 44100, BitDepth: 16, Bitrate: 978, Volume: 0.7,
	})
	s.Show()
	r = rows(s)
	if !strings.Contains(r[1], "vol ") || uniseg.StringWidth(r[1]) > 80 {
		t.Fatalf("row1 overflowed: %q", r[1])
	}
}
