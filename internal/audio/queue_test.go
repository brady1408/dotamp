package audio

import (
	"testing"

	"github.com/brady1408/dotamp/internal/library"
)

func tracks(n int) []library.Track {
	out := make([]library.Track, n)
	for i := range out {
		out[i] = library.Track{ID: string(rune('a' + i))}
	}
	return out
}

func TestQueueLinear(t *testing.T) {
	var q Queue
	if _, ok := q.Current(); ok || q.Index() != -1 {
		t.Fatal("empty queue must have no current")
	}
	q.Replace(tracks(3), 0)
	cur, _ := q.Current()
	if cur.ID != "a" {
		t.Fatalf("current = %s", cur.ID)
	}
	if !q.Next() || !q.Next() || q.Next() {
		t.Fatal("Next should succeed twice then fail at the end")
	}
	cur, _ = q.Current()
	if cur.ID != "c" {
		t.Fatalf("after end current = %s, must stay on last", cur.ID)
	}
	if !q.Prev() {
		t.Fatal("Prev should succeed")
	}
	q.Append(tracks(1)...)
	if len(q.Tracks()) != 4 {
		t.Fatal("append failed")
	}
	if q.Jump(9) || !q.Jump(3) {
		t.Fatal("jump bounds")
	}
}

func TestQueueRepeatWraps(t *testing.T) {
	var q Queue
	q.Replace(tracks(2), 1)
	q.Repeat = true
	if !q.Next() || q.Index() != 0 {
		t.Fatalf("repeat should wrap to 0, index = %d", q.Index())
	}
	if !q.Prev() || q.Index() != 1 {
		t.Fatal("repeat Prev should wrap to the end")
	}
}

func TestQueueShuffleVisitsEveryTrackOnce(t *testing.T) {
	var q Queue
	q.Replace(tracks(6), 2)
	q.Shuffle = true
	seen := map[string]int{}
	cur, _ := q.Current()
	seen[cur.ID]++
	for q.Next() {
		cur, _ = q.Current()
		seen[cur.ID]++
	}
	if len(seen) != 6 {
		t.Fatalf("shuffle visited %d of 6", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("track %s visited %d times", id, n)
		}
	}
}
