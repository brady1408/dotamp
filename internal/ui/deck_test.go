package ui

import (
	"strings"
	"testing"
	"time"
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
		if n := len([]rune(m)); n != 10 {
			t.Fatalf("tick %d: %d runes %q", tick, n, m)
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
