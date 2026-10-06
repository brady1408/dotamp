package audio

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

// A stream that fails mid-track is reopened once from the current position;
// the track keeps playing and the queue does not advance.
func TestControllerRetriesMidTrackFailureFromPosition(t *testing.T) {
	// Request 1 is cut mid-body; the stream's own three reconnects (2–4) are
	// rejected, so the error reaches the controller, whose reopen (5) succeeds.
	srv, ranges := flakyServer(t, map[int]bool{1: true}, map[int]bool{2: true, 3: true, 4: true})
	requests := func() int { return len(ranges()) }
	lib := &fakeLib{url: srv.URL}
	out := &fakeOutput{}
	eng := NewEngine(out, OutRate)
	defer eng.Close()
	var nmu sync.Mutex
	var notices []string
	c := NewController(lib, eng, func(s string) { nmu.Lock(); notices = append(notices, s); nmu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	ts := []library.Track{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}
	if err := c.PlayTracks(ctx, ts, 0); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out.p.pull(2048)
		time.Sleep(2 * time.Millisecond)
		cur, _ := c.Current()
		if cur.ID == "b" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never reached b; current=%s err=%v requests=%d", cur.ID, eng.Err(), requests())
		}
	}
	if got := strings.Join(lib.streamOpens(), ","); got != "a,a,b" {
		t.Fatalf("stream opens = %s, want a,a,b (a retried once, then b)", got)
	}
	if requests() < 5 {
		t.Fatalf("expected the controller's reopen after the stream gave up, got %d requests", requests())
	}
	nmu.Lock()
	defer nmu.Unlock()
	for _, n := range notices {
		if strings.Contains(n, "Skipped A") {
			t.Fatalf("A should have been retried, not skipped: %v", notices)
		}
	}
}

// With Repeat on and every track failing, the controller gives up after one
// pass through the queue instead of looping forever.
func TestControllerGivesUpWhenEveryTrackFailsUnderRepeat(t *testing.T) {
	srv := fixtureServer(t)
	lib := &fakeLib{url: srv.URL, fails: map[string]int{"a": 1 << 30, "b": 1 << 30}}
	out := &fakeOutput{}
	eng := NewEngine(out, OutRate)
	defer eng.Close()
	c := NewController(lib, eng, func(string) {})
	c.ToggleRepeat()
	done := make(chan error, 1)
	go func() { done <- c.PlayTracks(context.Background(), []library.Track{{ID: "a"}, {ID: "b"}}, 0) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when nothing can play")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PlayTracks looped forever under Repeat")
	}
	if _, has := c.Current(); has {
		t.Fatal("nothing should be current")
	}
}
