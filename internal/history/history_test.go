package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

func entry(id string, at time.Time, played, dur float64, name string) Entry {
	return Entry{At: at, Channel: "a-team", Name: name, Played: played,
		Track: library.Track{ID: "s:" + id, Title: id, Duration: time.Duration(dur * float64(time.Second))}}
}

func TestAppendThenReadRoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history") // Append creates it
	w := NewWriter(dir)
	now := time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC)
	if err := w.Append("g1", entry("a", now, 200, 240, "Alice")); err != nil {
		t.Fatal(err)
	}
	if err := w.Append("g1", entry("b", now.Add(time.Minute), 30, 240, "")); err != nil {
		t.Fatal(err)
	}
	es, err := Read(dir, "g1")
	if err != nil || len(es) != 2 || es[0].Track.ID != "s:a" || es[1].Played != 30 || es[0].Name != "Alice" {
		t.Fatalf("read = %+v, %v", es, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "g1.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestNightsCountsDistinctDatesPerGuild(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir)
	d1 := time.Date(2026, 10, 10, 22, 0, 0, 0, time.Local)
	d2 := time.Date(2026, 10, 17, 1, 0, 0, 0, time.Local)
	w.Append("g1", entry("a", d1, 240, 240, ""))
	w.Append("g1", entry("b", d1.Add(time.Hour), 240, 240, ""))
	w.Append("g1", entry("c", d2, 240, 240, ""))
	w.Append("g2", entry("a", d1, 240, 240, ""))
	ns, err := Nights(dir)
	if err != nil || len(ns) != 2 || ns[0].Guild != "g1" || ns[0].Nights != 2 || ns[1].Nights != 1 {
		t.Fatalf("nights = %+v, %v", ns, err)
	}
	if ns, err := Nights(filepath.Join(dir, "missing")); err != nil || ns != nil {
		t.Fatalf("missing dir: %v %v", ns, err)
	}
}

func TestScoreWeighsRecencyAndPunishesSkips(t *testing.T) {
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	es := []Entry{
		entry("old", now.Add(-10*7*24*time.Hour), 240, 240, ""), // 0.9^10 ≈ 0.35
		entry("new", now.Add(-time.Hour), 240, 240, ""),         // ≈ 1
		entry("skip", now.Add(-time.Hour), 60, 240, ""),         // played < half: -0.5
		entry("skip", now.Add(-time.Hour), 240, 240, ""),        // +1 → net 0.5
	}
	s := Score(es, now)
	if len(s) != 3 || s[0].Track.ID != "s:new" || s[1].Track.ID != "s:skip" || s[2].Track.ID != "s:old" {
		t.Fatalf("order = %+v", s)
	}
	if s[1].Score < 0.49 || s[1].Score > 0.51 {
		t.Fatalf("skip score = %v", s[1].Score)
	}
}

func TestScoreTreatsUnknownDurationAsPlayedThrough(t *testing.T) {
	now := time.Now()
	e := entry("x", now, 5, 0, "")
	if s := Score([]Entry{e}, now); len(s) != 1 || s[0].Score < 0.99 {
		t.Fatalf("score = %+v", s)
	}
}

func TestRadioPlaysWeighHalf(t *testing.T) {
	now := time.Now()
	es := []Entry{
		{At: now, Played: 240, Track: library.Track{ID: "r", Duration: 240 * time.Second}, By: "radio"},
		{At: now, Played: 240, Track: library.Track{ID: "q", Duration: 240 * time.Second}, By: "u1"},
	}
	s := Score(es, now)
	if s[0].Track.ID != "q" || s[1].Score > 0.51 || s[1].Score < 0.49 {
		t.Fatalf("scores = %+v", s)
	}
}
