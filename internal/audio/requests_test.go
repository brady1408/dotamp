package audio

import (
	"context"
	"errors"
	"strings"
	"sync"
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

func TestRequestKeepsPlayingAfterTheCallersContextEnds(t *testing.T) {
	c, _ := newTestController(t)
	cctx, cancel := context.WithCancel(context.Background())
	if _, err := c.Request(cctx, "a", "Alice", named("a1")); err != nil {
		t.Fatal(err)
	}
	cancel() // the slash command's reply has gone out; its context is done
	waitFor(t, func() bool { cur, ok := c.Current(); return ok && cur.ID == "a1" && c.eng.Length() > 0 })
	c.eng.Seek(1500 * time.Millisecond) // past the buffered bytes: a fresh HTTP request on the source's context
	time.Sleep(300 * time.Millisecond)
	if err := c.eng.Err(); err != nil {
		t.Fatalf("stream died with the caller's context: %v", err)
	}
}

func TestRequestAfterTheQueuePlayedOutPlaysTheRequest(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1")...)
	_ = c.Jump(ctx, 0)
	_ = c.Next(ctx) // nothing after dj1: stopped, cursor still on dj1
	if _, ok := c.Current(); ok {
		t.Fatal("still current after the queue played out")
	}
	c.Request(ctx, "a", "Alice", named("a1"))
	waitFor(t, func() bool { cur, ok := c.Current(); return ok && cur.ID == "a1" })
	if got := strings.Join(ids(c.Tracks()), " "); got != "dj1 a1" {
		t.Fatalf("queue = %q", got)
	}
}

func TestRequestDiscardsAPrefetchedNextTrack(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2")...)
	_ = c.Jump(ctx, 0)
	waitFor(t, func() bool { return c.eng.Length() > 0 })
	c.maybePrefetch(ctx) // the 2 s fixture is inside prefetchAhead
	c.mu.Lock()
	pre := c.nextID
	c.mu.Unlock()
	if pre != "dj2" {
		t.Fatalf("prefetched %q", pre)
	}
	c.Request(ctx, "a", "Alice", named("a1"))
	c.mu.Lock()
	after := c.nextID
	c.mu.Unlock()
	if after != "" || c.eng.PeekNext() != nil {
		t.Fatalf("prefetch kept: %q", after)
	}
}

func TestTrackEndIsReportedOnceWhenTheNextTrackFails(t *testing.T) {
	c, lib := newTestController(t)
	lib.fails = map[string]int{"b": 2}
	var got []string
	c.SetTrackEnd(func(t library.Track, _ time.Duration, _, _ string) { got = append(got, t.ID) })
	c.Enqueue(named("a", "b")...)
	_ = c.Jump(ctx, 0)
	_ = c.Next(ctx)
	if strings.Join(got, " ") != "a" {
		t.Fatalf("ends = %v", got)
	}
}

// decoderRunning reports whether the engine's decode goroutine is alive.
func decoderRunning(e *Engine) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stop != nil
}

func TestRequestDropsThePrefetchWithoutStoppingTheDecoder(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2")...)
	_ = c.Jump(ctx, 0)
	waitFor(t, func() bool { return c.eng.Length() > 0 })
	c.maybePrefetch(ctx)
	c.Request(ctx, "a", "Alice", named("a1"))
	if !decoderRunning(c.eng) {
		t.Fatal("the decoder was halted mid-track")
	}
}

func TestRemovingThePrefetchedNextKeepsPlaying(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("a", "b")...)
	_ = c.Jump(ctx, 0)
	waitFor(t, func() bool { return c.eng.Length() > 0 })
	c.maybePrefetch(ctx)
	if err := c.Remove(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if c.eng.PeekNext() != nil {
		t.Fatal("prefetched b survived its removal")
	}
	if !decoderRunning(c.eng) {
		t.Fatal("the decoder was halted mid-track")
	}
}

func TestFedRowBehindTheCursorIsMovedAhead(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2", "dj3")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1")) // [dj1 a1 dj2 dj3]
	_ = c.Jump(ctx, 2)                        // a handover that skipped the request lands on dj2
	if got := strings.Join(ids(c.Tracks()), " "); got != "dj1 dj2 a1 dj3" {
		t.Fatalf("queue = %q", got)
	}
	if _, name := c.RequesterOf(2); name != "Alice" {
		t.Fatalf("tag did not travel: %q", name)
	}
}

func TestEnqueueAndPlayStartsWhenIdleAndOnlyAppendsOtherwise(t *testing.T) {
	c, _ := newTestController(t)
	if err := c.EnqueueAndPlay(ctx, named("a", "b")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { cur, ok := c.Current(); return ok && cur.ID == "a" })
	if err := c.EnqueueAndPlay(ctx, named("c")); err != nil {
		t.Fatal(err)
	}
	if cur, _ := c.Current(); cur.ID != "a" {
		t.Fatalf("appending while playing changed the current track to %s", cur.ID)
	}
	if got := strings.Join(ids(c.Tracks()), " "); got != "a b c" {
		t.Fatalf("queue = %q", got)
	}
	_ = c.Clear()
	if err := c.EnqueueAndPlay(ctx, named("d")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { cur, ok := c.Current(); return ok && cur.ID == "d" })
}

func TestAPlayedRequestIsNotFedAgain(t *testing.T) {
	c, _ := newTestController(t)
	c.Enqueue(named("dj1", "dj2", "dj3")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "g", "Gentry", named("g1")) // fed at 1
	var played []string
	for i := 0; i < 4; i++ {
		cur, _ := c.Current()
		played = append(played, cur.ID)
		_ = c.Next(ctx)
	}
	if got := strings.Join(played, " "); got != "dj1 g1 dj2 dj3" {
		t.Fatalf("played %q; Gentry's one song must play once", got)
	}
	if got := strings.Join(ids(c.Tracks()), " "); got != "dj1 g1 dj2 dj3" {
		t.Fatalf("queue = %q", got)
	}
}

func TestTrackStartIsReportedWithTheRequester(t *testing.T) {
	c, _ := newTestController(t)
	var got []string
	var mu sync.Mutex
	c.SetTrackStart(func(t library.Track, by, name string) { mu.Lock(); got = append(got, t.ID+"/"+name); mu.Unlock() })
	c.Enqueue(named("dj1")...)
	_ = c.Jump(ctx, 0)
	c.Request(ctx, "a", "Alice", named("a1"))
	_ = c.Next(ctx)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, " ") != "dj1/ a1/Alice" {
		t.Fatalf("starts = %v", got)
	}
}

func TestASecondPrevSoonAfterARestartGoesBack(t *testing.T) {
	old := restartAfter
	restartAfter = 300 * time.Millisecond // the 2 s fixture is shorter than the real 3 s
	t.Cleanup(func() { restartAfter = old })
	srv := fixtureServer(t)
	eng := NewEngine(NewSilentOutput(OutRate), OutRate) // a clock that moves in real time
	t.Cleanup(eng.Close)
	c := NewController(&fakeLib{url: srv.URL}, eng, func(string) {})
	c.Enqueue(named("a", "b")...)
	_ = c.Jump(ctx, 1)
	waitFor(t, func() bool { return c.eng.Position() > 500*time.Millisecond })
	_ = c.Prev(ctx) // restart b
	if cur, _ := c.Current(); cur.ID != "b" {
		t.Fatalf("first p moved to %s", cur.ID)
	}
	waitFor(t, func() bool { return c.eng.Position() > 500*time.Millisecond }) // past the restart window by the clock
	_ = c.Prev(ctx)
	if cur, _ := c.Current(); cur.ID != "a" {
		t.Fatalf("second p soon after a restart stayed on %s", cur.ID)
	}
}
