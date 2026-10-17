package audio

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

// newTestController is an idle controller: fixture library, no queue.
func newTestController(t *testing.T) (*Controller, *fakeLib) {
	t.Helper()
	srv := fixtureServer(t)
	lib := &fakeLib{url: srv.URL}
	eng := NewEngine(&fakeOutput{}, OutRate)
	t.Cleanup(eng.Close)
	return NewController(lib, eng, func(string) {}), lib
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var ctx = context.Background()

func TestLanesTakeTurnsWithTheDJ(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2")...)
	if _, err := c.Request(ctx, "a", "Alice", named("a1", "a2", "a3")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Request(ctx, "b", "Bob", named("b1")); err != nil {
		t.Fatal(err)
	}
	if err := c.Jump(ctx, 0); err != nil {
		t.Fatal(err)
	}
	var order []string
	for i := 0; i < 6; i++ {
		cur, _ := c.Current()
		order = append(order, cur.ID)
		if err := c.Next(ctx); err != nil && i < 5 {
			t.Fatal(err)
		}
	}
	want := []string{"dj1", "a1", "b1", "dj2", "a2", "a3"}
	if strings.Join(order, " ") != strings.Join(want, " ") {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestOnlyOneRequestIsFedAhead(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2", "dj3")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1", "a2", "a3"))
	ts := c.Tracks()
	fed := 0
	for i := range ts {
		if _, name := c.RequesterOf(i); name != "" {
			fed++
		}
	}
	if fed != 1 || ts[1].ID != "a1" {
		t.Fatalf("fed %d rows: %v", fed, ids(ts))
	}
	if p := c.Pending(); len(p) != 1 || len(p[0].Tracks) != 2 {
		t.Fatalf("pending = %+v", p)
	}
}

func TestRequestOnAnIdleQueueStartsPlaying(t *testing.T) {
	c, lib := newTestController(t)
	if _, err := c.Request(ctx, "a", "Alice", named("a1")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { cur, ok := c.Current(); return ok && cur.ID == "a1" })
	if opens := lib.streamOpens(); len(opens) != 1 {
		t.Fatalf("opened %v", opens)
	}
}

func TestDJTurnIsSkippedWhenTheDJHasNothingLeft(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1", "a2"))
	_ = c.Next(ctx) // a1
	_ = c.Next(ctx) // the DJ has nothing after dj1: a2 plays, no silence
	cur, ok := c.Current()
	if !ok || cur.ID != "a2" {
		t.Fatalf("current = %v %v", cur, ok)
	}
}

func TestTwoRequestsInOneTrackTakeOneTurn(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1"))
	c.Request(ctx, "b", "Bob", named("b1"))
	_ = c.Next(ctx) // a1 was fed
	_ = c.Next(ctx) // Bob's turn now
	_ = c.Next(ctx) // then the DJ's dj2
	cur, _ := c.Current()
	if cur.ID != "dj2" {
		t.Fatalf("DJ's turn was skipped: current %s, queue %v", cur.ID, ids(c.Tracks()))
	}
}

func TestRemovingTheFedRowLeavesTheLaneAlone(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1", "a2"))
	_ = c.Remove(ctx, 1) // the fed a1
	if ts := c.Tracks(); len(ts) != 2 {
		t.Fatalf("queue = %v", ids(ts))
	}
	if p := c.Pending(); len(p[0].Tracks) != 1 || p[0].Tracks[0].ID != "a2" {
		t.Fatalf("lane = %+v", p)
	}
	_ = c.Next(ctx) // dj2 starts: Alice's turn again, a2 fed
	if ts := c.Tracks(); len(ts) != 3 || ts[2].ID != "a2" {
		t.Fatalf("after next = %v", ids(ts))
	}
}

func TestClearDropsTheLanes(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1", "a2", "a3"))
	if n := c.Clear(); n != 3 { // a1 fed + a2, a3 waiting
		t.Fatalf("dropped %d", n)
	}
	if len(c.Pending()) != 0 || len(c.Tracks()) != 0 {
		t.Fatal("something survived Clear")
	}
}

func TestCapRefusesWithTheCount(t *testing.T) {
	c, _ := newTestController(t)
	c.SetMaxPending(2)
	c.Enqueue(named("dj1")...)
	_ = c.Jump(ctx, 0)
	if _, err := c.Request(ctx, "a", "Alice", named("a1", "a2")); err != nil {
		t.Fatal(err)
	}
	n, err := c.Request(ctx, "a", "Alice", named("a3"))
	if !errors.Is(err, ErrTooManyPending) || n != 2 {
		t.Fatalf("got %d %v", n, err)
	}
}

func TestTrackEndReportsPlayedAndRequester(t *testing.T) {
	c, _ := newTestController(t)
	var got []string
	c.SetTrackEnd(func(t library.Track, played time.Duration, by, name string) {
		got = append(got, t.ID+"/"+name)
	})
	c.Enqueue(named("dj1")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1"))
	_ = c.Next(ctx) // dj1 ended by skip
	_ = c.Next(ctx) // a1 ended by skip, queue exhausted
	if strings.Join(got, " ") != "dj1/ a1/Alice" {
		t.Fatalf("ends = %v", got)
	}
}
