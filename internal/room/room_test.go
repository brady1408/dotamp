package room

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/radio"
	"github.com/brady1408/dotamp/internal/stream"
)

var ctx = context.Background()

type udp struct{}

func (udp) Write(p []byte) (int, error) { return len(p), nil }
func (udp) Read(p []byte) (int, error)  { time.Sleep(5 * time.Millisecond); return 0, nil }

type conn struct{ closed atomic.Int32 }

func (c *conn) UDP() stream.UDP       { return udp{} }
func (c *conn) Close(context.Context) { c.closed.Add(1) }

// lib serves the 2 s sine fixture for every track and suggests s1..s9.
type lib struct {
	library.Library
	url     string
	similar []library.Track
	fail    atomic.Bool
}

func (l *lib) Stream(context.Context, library.Track) (library.Stream, error) {
	return library.Stream{URL: l.url + "/sine440-44k.flac", Codec: "flac"}, nil
}
func (l *lib) Similar(context.Context, library.Track, int) ([]library.Track, error) {
	if l.fail.Load() {
		return nil, errors.New("plex down")
	}
	return l.similar, nil
}
func (l *lib) RecentAlbums(context.Context, int, int) ([]library.Album, error) {
	if l.fail.Load() {
		return nil, errors.New("plex down")
	}
	return []library.Album{{ID: "al"}}, nil
}
func (l *lib) AlbumTracks(context.Context, string) ([]library.Track, error) {
	return []library.Track{{ID: "cold", Title: "Cold", Artist: "Z", Duration: 2 * time.Second}}, nil
}

func tracks(prefix string, n int) []library.Track {
	var out []library.Track
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("%s%d", prefix, i)
		out = append(out, library.Track{ID: id, Title: id, Artist: "artist " + id, Duration: 2 * time.Second})
	}
	return out
}

type harness struct {
	r         *Room
	lib       *lib
	listeners atomic.Int32
	mu        sync.Mutex
	posts     []string
	now       time.Time
	dir       string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir("../audio/testdata")))
	t.Cleanup(srv.Close)
	h := &harness{lib: &lib{url: srv.URL, similar: tracks("s", 9)}, dir: t.TempDir(), now: time.Now()}
	h.listeners.Store(2)
	h.r = New(ctx, Config{Guild: 1, Library: h.lib, History: history.NewWriter(h.dir), HistoryDir: h.dir,
		Autoplay: true, IdleLeave: 5 * time.Minute, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deps: Deps{
			Open:      func(context.Context, snowflake.ID) (Conn, error) { return &conn{}, nil },
			Listeners: func(snowflake.ID) int { return int(h.listeners.Load()) },
			Post:      func(_ snowflake.ID, m string) { h.mu.Lock(); h.posts = append(h.posts, m); h.mu.Unlock() },
			Now:       func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		}})
	t.Cleanup(func() { h.r.Leave(context.Background()) })
	return h
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) current() (library.Track, bool) { return h.r.Controller().Current() }

func TestJoinStartsRadioWhenTheQueueIsEmpty(t *testing.T) {
	h := newHarness(t)
	if err := h.r.Join(ctx, 10, "a-team"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { cur, ok := h.current(); return ok && cur.ID == "cold" })
	if _, name := h.r.Controller().RequesterOf(h.r.Controller().Index()); name != radio.RadioBy {
		t.Fatalf("radio row tagged %q", name)
	}
	if ch, name := h.r.Where(); ch != 10 || name != "a-team" {
		t.Fatalf("where = %d %q", ch, name)
	}
}

func TestRadioFollowsTheLastSongAndKeepsGoing(t *testing.T) {
	h := newHarness(t)
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { cur, ok := h.current(); return ok && cur.ID == "cold" })
	waitFor(t, func() bool { return len(h.r.Controller().Tracks()) >= 2 })
	_ = h.r.Controller().Next(ctx)
	waitFor(t, func() bool { cur, _ := h.current(); return len(cur.ID) > 0 && cur.ID[0] == 's' })
}

func TestARequestDisplacesAFedRadioPick(t *testing.T) {
	h := newHarness(t)
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { return len(h.r.Controller().Tracks()) >= 2 })
	h.r.Request(ctx, "u1", "Alice", []library.Track{{ID: "req", Title: "Req", Duration: 2 * time.Second}})
	ts := h.r.Controller().Tracks()
	i := h.r.Controller().Index()
	if ts[i+1].ID != "req" {
		t.Fatalf("next = %s; queue %v", ts[i+1].ID, ts)
	}
	for j := i + 1; j < len(ts); j++ {
		if _, name := h.r.Controller().RequesterOf(j); name == radio.RadioBy {
			t.Fatalf("a radio row survived the request at %d: %v", j, ts)
		}
	}
}

func TestSkipVotesAreDistinctAndResetPerSong(t *testing.T) {
	h := newHarness(t)
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { _, ok := h.current(); return ok })
	first, _ := h.current()
	if msg, _ := h.r.Skip(ctx, "u1", "Alice", false); msg != "Alice voted to skip; one more skips it" {
		t.Fatalf("msg = %q", msg)
	}
	if msg, _ := h.r.Skip(ctx, "u1", "Alice", false); msg != "Alice voted to skip; one more skips it" {
		t.Fatalf("second vote by the same person counted: %q", msg)
	}
	if msg, _ := h.r.Skip(ctx, "u2", "Bob", false); msg != "Skipped "+first.Title {
		t.Fatalf("msg = %q", msg)
	}
	waitFor(t, func() bool { cur, ok := h.current(); return ok && cur.ID != first.ID })
	if msg, _ := h.r.Skip(ctx, "u2", "Bob", false); msg != "Bob voted to skip; one more skips it" {
		t.Fatalf("votes carried over: %q", msg)
	}
}

func TestOwnSongAloneAndDJSkipAtOnce(t *testing.T) {
	h := newHarness(t)
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { _, ok := h.current(); return ok })
	h.r.Request(ctx, "u1", "Alice", []library.Track{{ID: "mine", Title: "Mine", Duration: 2 * time.Second}})
	_ = h.r.Controller().Next(ctx)
	waitFor(t, func() bool { cur, _ := h.current(); return cur.ID == "mine" })
	if msg, _ := h.r.Skip(ctx, "u1", "Alice", false); msg != "Skipped Mine" {
		t.Fatalf("own song: %q", msg)
	}
	waitFor(t, func() bool { cur, ok := h.current(); return ok && cur.ID != "mine" })
	cur, _ := h.current()
	if msg, _ := h.r.Skip(ctx, "u9", "Dee", true); msg != "Skipped "+cur.Title {
		t.Fatalf("dj: %q", msg)
	}
	h.listeners.Store(1)
	waitFor(t, func() bool { c, ok := h.current(); return ok && c.ID != cur.ID })
	cur, _ = h.current()
	if msg, _ := h.r.Skip(ctx, "u3", "Cy", false); msg != "Skipped "+cur.Title {
		t.Fatalf("alone: %q", msg)
	}
}

func TestIdleLeaveStopsAndStopsLogging(t *testing.T) {
	h := newHarness(t)
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { _, ok := h.current(); return ok })
	h.listeners.Store(0)
	h.r.Tick(ctx) // starts the idle clock
	if ch, _ := h.r.Where(); ch == 0 {
		t.Fatal("left before the timeout")
	}
	h.mu.Lock()
	h.now = h.now.Add(6 * time.Minute)
	h.mu.Unlock()
	h.r.Tick(ctx)
	if ch, _ := h.r.Where(); ch != 0 {
		t.Fatal("still joined after the idle timeout")
	}
	if _, ok := h.current(); ok || len(h.r.Controller().Tracks()) != 0 {
		t.Fatal("queue survived leaving")
	}
	es, _ := history.Read(h.dir, "1")
	n := len(es)
	time.Sleep(300 * time.Millisecond)
	es, _ = history.Read(h.dir, "1")
	if len(es) != n {
		t.Fatal("history written after leaving")
	}
}

func TestRadioGivesUpAfterThreeFailures(t *testing.T) {
	h := newHarness(t)
	h.lib.fail.Store(true)
	h.r.Join(ctx, 10, "a-team")
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 5; i++ {
		h.r.Tick(ctx)
	}
	h.mu.Lock()
	posts := append([]string(nil), h.posts...)
	h.mu.Unlock()
	n := 0
	for _, p := range posts {
		if p == "Can't reach the library; queue a song to try again" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("posts = %v", posts)
	}
}

func TestAutoplayOffStaysQuietAndResetsOnLeave(t *testing.T) {
	h := newHarness(t)
	h.r.SetAutoplay(false)
	h.r.Join(ctx, 10, "a-team")
	time.Sleep(200 * time.Millisecond)
	if _, ok := h.current(); ok {
		t.Fatal("radio played with autoplay off")
	}
	h.r.Leave(ctx)
	if !h.r.Autoplay() {
		t.Fatal("autoplay did not reset to the config default on leave")
	}
}
