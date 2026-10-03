package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

// truncatingServer serves the FLAC fixture, but the first request for it is
// cut off midway: a connection that drops during playback.
func truncatingServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	data, err := os.ReadFile("testdata/sine440-44k.flac")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(200)
			_, _ = w.Write(data[:len(data)/3])
			if h, ok := w.(http.Hijacker); ok { // close the TCP connection mid-body
				conn, _, _ := h.Hijack()
				conn.Close()
			}
			return
		}
		http.ServeContent(w, r, "a.flac", time.Time{}, bytesReader(data))
	}))
	t.Cleanup(s.Close)
	return s, &requests
}

// A stream that fails mid-track is reopened once from the current position;
// the track keeps playing and the queue does not advance.
func TestControllerRetriesMidTrackFailureFromPosition(t *testing.T) {
	srv, requests := truncatingServer(t)
	lib := &fakeLib{url: srv.URL}
	out := &fakeOutput{}
	eng := NewEngine(out)
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
			t.Fatalf("never reached b; current=%s err=%v requests=%d", cur.ID, eng.Err(), *requests)
		}
	}
	if got := strings.Join(lib.streamOpens(), ","); got != "a,a,b" {
		t.Fatalf("stream opens = %s, want a,a,b (a retried once, then b)", got)
	}
	if *requests < 3 {
		t.Fatalf("expected a retry request, got %d", *requests)
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
	eng := NewEngine(out)
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
