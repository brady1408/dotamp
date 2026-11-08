package audio

import (
	"errors"
	"strings"
	"testing"
)

// aliceWaiting: the DJ is on dj1, Alice's a1 is fed next, a2 a3 wait.
func aliceWaiting(t *testing.T) *Controller {
	t.Helper()
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2", "dj3")...)
	if err := c.Jump(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Request(ctx, "a", "Alice", named("a1", "a2", "a3")); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMineIsTheFedSongThenTheLane(t *testing.T) {
	c := aliceWaiting(t)
	c.Request(ctx, "b", "Bob", named("b1"))
	songs, fed := c.Mine("a")
	if !fed || strings.Join(ids(songs), " ") != "a1 a2 a3" {
		t.Fatalf("mine = %v fed=%v", ids(songs), fed)
	}
	if songs, fed := c.Mine("b"); fed || strings.Join(ids(songs), " ") != "b1" {
		t.Fatalf("bob = %v fed=%v", ids(songs), fed)
	}
	if songs, _ := c.Mine("nobody"); len(songs) != 0 {
		t.Fatalf("nobody = %v", ids(songs))
	}
}

func TestRemoveMineFromTheLane(t *testing.T) {
	c := aliceWaiting(t)
	if err := c.RemoveMine(ctx, "a", 2, "a3"); err != nil {
		t.Fatal(err)
	}
	if songs, _ := c.Mine("a"); strings.Join(ids(songs), " ") != "a1 a2" {
		t.Fatalf("mine = %v", ids(songs))
	}
	if err := c.RemoveMine(ctx, "a", 1, "a3"); !errors.Is(err, ErrMoved) {
		t.Fatalf("stale pick: err = %v", err)
	}
	if err := c.RemoveMine(ctx, "b", 1, "a2"); !errors.Is(err, ErrMoved) {
		t.Fatalf("someone else's song: err = %v", err)
	}
}

func TestRemoveMineTheFedSongLinesUpTheNext(t *testing.T) {
	c := aliceWaiting(t)
	if err := c.RemoveMine(ctx, "a", 0, "a1"); err != nil {
		t.Fatal(err)
	}
	songs, fed := c.Mine("a")
	if !fed || strings.Join(ids(songs), " ") != "a2 a3" {
		t.Fatalf("mine = %v fed=%v", ids(songs), fed)
	}
	if ts := c.Tracks(); strings.Join(ids(ts), " ") != "dj1 a2 dj2 dj3" {
		t.Fatalf("queue = %v", ids(ts))
	}
}

func TestTopMineTakesTheFedSlot(t *testing.T) {
	c := aliceWaiting(t)
	if err := c.TopMine(ctx, "a", 2, "a3"); err != nil {
		t.Fatal(err)
	}
	if songs, _ := c.Mine("a"); strings.Join(ids(songs), " ") != "a3 a1 a2" {
		t.Fatalf("mine = %v", ids(songs))
	}
	if ts := c.Tracks(); strings.Join(ids(ts), " ") != "dj1 a3 dj2 dj3" {
		t.Fatalf("queue = %v", ids(ts))
	}
	if by, name := c.RequesterOf(1); by != "a" || name != "Alice" {
		t.Fatalf("fed row tag = %q %q", by, name)
	}
	if err := c.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if cur, _ := c.Current(); cur.ID != "a3" {
		t.Fatalf("playing %s, want a3", cur.ID)
	}
}

func TestTopMineWithoutAFedSongMovesToTheFrontOfTheLane(t *testing.T) {
	c := aliceWaiting(t)
	c.Request(ctx, "b", "Bob", named("b1", "b2", "b3"))
	if err := c.TopMine(ctx, "b", 2, "b3"); err != nil {
		t.Fatal(err)
	}
	if songs, fed := c.Mine("b"); fed || strings.Join(ids(songs), " ") != "b3 b1 b2" {
		t.Fatalf("bob = %v fed=%v", ids(songs), fed)
	}
	if err := c.TopMine(ctx, "b", 0, "b1"); !errors.Is(err, ErrMoved) {
		t.Fatalf("stale pick: err = %v", err)
	}
}

// The decoder reads the next source up to half a second before the
// handover; past that point the prefetch cannot be dropped.
func startedPrefetch(t *testing.T, c *Controller, want string) {
	t.Helper()
	waitFor(t, func() bool { return c.eng.Length() > 0 })
	c.maybePrefetch(ctx)
	c.mu.Lock()
	got := c.nextID
	c.mu.Unlock()
	if got != want {
		t.Fatalf("prefetched %q, want %q", got, want)
	}
	c.eng.mu.Lock()
	c.eng.nextStarted = true
	c.eng.mu.Unlock()
}

func TestTopMineAfterTheDecoderStartedTheNextMovesOnToTheNewPick(t *testing.T) {
	c := aliceWaiting(t)
	startedPrefetch(t, c, "a1")
	if err := c.TopMine(ctx, "a", 2, "a3"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { cur, _ := c.Current(); return cur.ID == "a3" })
	if !decoderRunning(c.eng) {
		t.Fatal("the decoder was halted: the bot goes silent")
	}
}

func TestRemoveMineAfterTheDecoderStartedTheNextMovesOn(t *testing.T) {
	c := aliceWaiting(t)
	startedPrefetch(t, c, "a1")
	if err := c.RemoveMine(ctx, "a", 0, "a1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { cur, _ := c.Current(); return cur.ID == "a2" })
	if !decoderRunning(c.eng) {
		t.Fatal("the decoder was halted: the bot goes silent")
	}
}

func TestAPrefetchOpeningDuringTopMineIsNotKept(t *testing.T) {
	c, lib := newTestController(t)
	c.Enqueue(named("dj1", "dj2")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1", "a2", "a3"))
	waitFor(t, func() bool { return c.eng.Length() > 0 })
	lib.mu.Lock()
	lib.onStream = func(id string) {
		if id == "a1" { // the press lands while a1's stream is opening
			if err := c.TopMine(ctx, "a", 2, "a3"); err != nil {
				t.Error(err)
			}
		}
	}
	lib.mu.Unlock()
	c.maybePrefetch(ctx)
	c.mu.Lock()
	got := c.nextID
	c.mu.Unlock()
	if got == "a1" {
		t.Fatal("a1 was prefetched after it was moved back into the lane")
	}
}
