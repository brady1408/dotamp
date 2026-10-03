package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// flakyServer serves the FLAC fixture. Requests whose 1-based ordinal is in
// cut are closed after a third of the body; those in reject get a 503.
func flakyServer(t *testing.T, cut, reject map[int]bool) (*httptest.Server, func() []string) {
	t.Helper()
	data, err := os.ReadFile("testdata/sine440-44k.flac")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var ranges []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		n := len(ranges)
		mu.Unlock()
		switch {
		case reject[n]:
			http.Error(w, "busy", http.StatusServiceUnavailable)
		case cut[n]:
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(200)
			_, _ = w.Write(data[:len(data)/3])
			if h, ok := w.(http.Hijacker); ok {
				conn, _, _ := h.Hijack()
				conn.Close()
			}
		default:
			http.ServeContent(w, r, "a.flac", time.Time{}, bytesReader(data))
		}
	}))
	t.Cleanup(s.Close)
	return s, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), ranges...) }
}

// A connection that drops mid-stream is reopened at the same byte offset
// without the decoder noticing: the track plays whole.
func TestHTTPFileReconnectsAfterPrematureEOF(t *testing.T) {
	srv, ranges := flakyServer(t, map[int]bool{1: true}, nil)
	hf, err := OpenHTTP(context.Background(), srv.URL+"/a.flac", nil)
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewFLAC(hf)
	if err != nil {
		t.Fatal(err)
	}
	frames, _, _ := drain(t, src)
	if frames < 88000 || frames > 88400 {
		t.Fatalf("frames = %d, want the whole 2 s", frames)
	}
	rs := ranges()
	if len(rs) < 2 || rs[1] == "" {
		t.Fatalf("expected a Range reconnect, got requests %q", rs)
	}
}

// Three rejected reconnects in a row give up with an error, so the controller's
// own retry still has something to catch.
func TestHTTPFileGivesUpAfterRepeatedRejections(t *testing.T) {
	srv, _ := flakyServer(t, map[int]bool{1: true}, map[int]bool{2: true, 3: true, 4: true})
	hf, err := OpenHTTP(context.Background(), srv.URL+"/a.flac", nil)
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewFLAC(hf)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 4096)
	for {
		if _, err := src.Read(buf); err != nil {
			if err.Error() == "EOF" {
				t.Fatal("a premature end must not read as a clean EOF")
			}
			return
		}
	}
}

// A seek that interrupts a network read must not leave that interruption
// behind as a decode error: the next track end would take the error path
// and lose its gapless handover.
func TestEngineSeekLeavesNoStaleError(t *testing.T) {
	data, _ := os.ReadFile("testdata/sine440-44k.flac")
	hold := make(chan struct{})
	first := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first { // 16 KB then stall: the decoder blocks inside Read
			first = false
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(200)
			_, _ = w.Write(data[:16384])
			w.(http.Flusher).Flush()
			<-hold
			return
		}
		http.ServeContent(w, r, "a.flac", time.Time{}, bytesReader(data))
	}))
	defer srv.Close()
	defer close(hold)
	hf, err := OpenHTTP(context.Background(), srv.URL+"/a.flac", nil)
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewFLAC(hf)
	if err != nil {
		t.Fatal(err)
	}
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	e.Play(src)
	time.Sleep(100 * time.Millisecond) // decoder is now blocked on the stalled body
	e.Seek(time.Second)
	if err := e.Err(); err != nil {
		t.Fatalf("stale error after seek: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := out.p.pull(1024); n[0] == 0 && n[1] == 0 && n[2] == 0 && n[3] == 0 {
		t.Fatal("no audio after seeking away from a stalled read")
	}
}

// Seeking to the end with a next source queued hands over to it instead of
// ending the track and waiting for the controller.
func TestEngineSeekToEndChainsIntoNext(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	a := &fakeSource{rate: 44100, frames: 44100}
	b := &constSource{fakeSource: fakeSource{rate: 44100, frames: 44100}, value: 0.25}
	e.Play(a)
	e.SetNext(b)
	e.Seek(time.Hour)
	time.Sleep(50 * time.Millisecond)
	s := pullSlowly(out.p, 2048)
	const bVal = int16(8191)
	seen := 0
	for _, v := range s {
		if v == bVal {
			seen++
		}
	}
	if seen < 1500 {
		t.Fatalf("B frames after seek-to-end = %d of 2048", seen)
	}
	select {
	case <-e.Handover():
	default:
		t.Fatal("Handover should have fired")
	}
	select {
	case <-e.Done():
		t.Fatal("Done must not fire when a next was queued")
	default:
	}
}
