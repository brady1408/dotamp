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

func named(ids ...string) []library.Track {
	out := make([]library.Track, len(ids))
	for i, id := range ids {
		out[i] = library.Track{ID: id, Title: id}
	}
	return out
}

func ids(ts []library.Track) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func TestInsertNextGoesRightAfterTheCursorEvenWhenShuffled(t *testing.T) {
	var q Queue
	q.Replace(named("a", "b", "c", "d"), 1)
	q.Shuffle = true
	i := q.InsertNext(library.Track{ID: "r"}, "u1", "Alice")
	if i != 2 || q.Tracks()[2].ID != "r" {
		t.Fatalf("inserted at %d: %v", i, ids(q.Tracks()))
	}
	if by, name := q.Tag(2); by != "u1" || name != "Alice" {
		t.Fatalf("tag = %q %q", by, name)
	}
	if !q.Next() || q.Tracks()[q.Index()].ID != "r" {
		t.Fatalf("shuffle did not play the request next: idx %d", q.Index())
	}
}

func TestInsertNextOnAnEmptyQueueBecomesCurrent(t *testing.T) {
	var q Queue
	q.InsertNext(library.Track{ID: "r"}, "u1", "Alice")
	cur, ok := q.Current()
	if !ok || cur.ID != "r" || q.Index() != 0 {
		t.Fatalf("current = %v %v idx %d", cur, ok, q.Index())
	}
}

func TestTagsFollowRemoveAndReplace(t *testing.T) {
	var q Queue
	q.Replace(named("a", "b"), 0)
	q.InsertNext(library.Track{ID: "r"}, "u1", "Alice")
	q.Remove(0)
	if _, name := q.Tag(0); name != "Alice" {
		t.Fatalf("tag after remove = %q", name)
	}
	q.Append(library.Track{ID: "c"})
	if _, name := q.Tag(2); name != "" {
		t.Fatalf("appended row tagged %q", name)
	}
	q.Replace(named("x"), 0)
	if _, name := q.Tag(0); name != "" {
		t.Fatalf("replace kept a tag %q", name)
	}
}

func TestHasNextIgnoresRepeat(t *testing.T) {
	var q Queue
	q.Replace(named("a", "b"), 1)
	q.Repeat = true
	if q.HasNext() {
		t.Fatal("HasNext true at the end with Repeat")
	}
	q.Jump(0)
	if !q.HasNext() {
		t.Fatal("HasNext false with a track after the cursor")
	}
}
