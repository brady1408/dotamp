package room

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	delay   atomic.Int64 // Similar sleeps this long (ns)
	first   sync.Once
	asked   atomic.Value // the first track Similar was asked about (library.Track)
}

func (l *lib) Stream(context.Context, library.Track) (library.Stream, error) {
	if l.fail.Load() {
		return library.Stream{}, errors.New("plex down")
	}
	return library.Stream{URL: l.url + "/sine440-44k.flac", Codec: "flac"}, nil
}
func (l *lib) Similar(_ context.Context, t library.Track, _ int) ([]library.Track, error) {
	l.first.Do(func() { l.asked.Store(t) })
	time.Sleep(time.Duration(l.delay.Load()))
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
	opens     atomic.Int32
	openDelay atomic.Int64
	conns     []*conn
}

func newHarness(t *testing.T) *harness { return newHarnessWith(t, nil) }

// newHarnessWith lets a test write history before the room loads it.
func newHarnessWith(t *testing.T, before func(dir string)) *harness {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir("../audio/testdata")))
	t.Cleanup(srv.Close)
	h := &harness{lib: &lib{url: srv.URL, similar: tracks("s", 9)}, dir: t.TempDir(), now: time.Now()}
	h.listeners.Store(2)
	if before != nil {
		before(h.dir)
	}
	h.r = h.newRoom()
	t.Cleanup(func() { h.r.Leave(context.Background()) })
	return h
}

// newRoom builds a room over the harness's library, clock and directories,
// as a restarted bot would.
func (h *harness) newRoom() *Room {
	return New(ctx, Config{Guild: 1, Library: h.lib, History: history.NewWriter(h.dir), HistoryDir: h.dir, StateDir: filepath.Join(h.dir, "state"),
		Autoplay: true, IdleLeave: 5 * time.Minute, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Deps: Deps{
			Open: func(context.Context, snowflake.ID) (Conn, error) {
				h.opens.Add(1)
				time.Sleep(time.Duration(h.openDelay.Load()))
				c := &conn{}
				h.mu.Lock()
				h.conns = append(h.conns, c)
				h.mu.Unlock()
				return c, nil
			},
			Listeners: func(snowflake.ID) int { return int(h.listeners.Load()) },
			Post:      func(_ snowflake.ID, m string) { h.mu.Lock(); h.posts = append(h.posts, m); h.mu.Unlock() },
			Now:       func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		}})
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

func radioAhead(h *harness) []string {
	var out []string
	ts, i := h.r.Controller().Tracks(), h.r.Controller().Index()
	for j := i + 1; j < len(ts); j++ {
		if _, name := h.r.Controller().RequesterOf(j); name == radio.RadioBy {
			out = append(out, ts[j].ID)
		}
	}
	for _, l := range h.r.Controller().Pending() {
		if l.By == radio.RadioBy {
			for _, t := range l.Tracks {
				out = append(out, t.ID)
			}
		}
	}
	return out
}

func TestARequestDuringAPickKeepsTheRadioOut(t *testing.T) {
	h := newHarness(t)
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { return len(h.r.Controller().Tracks()) >= 2 })
	h.lib.delay.Store(int64(300 * time.Millisecond))
	_ = h.r.Controller().Next(ctx) // the start hook begins a slow pick
	time.Sleep(50 * time.Millisecond)
	h.r.Request(ctx, "u1", "Alice", []library.Track{{ID: "a1", Title: "A1", Duration: 2 * time.Second}, {ID: "a2", Title: "A2", Duration: 2 * time.Second}})
	time.Sleep(500 * time.Millisecond)
	if got := radioAhead(h); len(got) != 0 {
		t.Fatalf("radio picks waiting while a request does: %v", got)
	}
}

func TestLeaveDuringAPickStaysQuiet(t *testing.T) {
	h := newHarness(t)
	h.lib.delay.Store(int64(300 * time.Millisecond))
	h.r.Join(ctx, 10, "a-team")
	time.Sleep(20 * time.Millisecond)
	h.r.Leave(ctx)
	time.Sleep(500 * time.Millisecond)
	if _, ok := h.current(); ok || len(h.r.Controller().Tracks()) != 0 {
		t.Fatalf("a pick started after leaving: %v", h.r.Controller().Tracks())
	}
}

func TestConcurrentJoinsOpenOnce(t *testing.T) {
	h := newHarness(t)
	h.openDelay.Store(int64(100 * time.Millisecond))
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); h.r.Join(ctx, 10, "a-team") }()
	}
	wg.Wait()
	if n := h.opens.Load(); n != 1 {
		t.Fatalf("opened %d connections for one channel", n)
	}
}

func TestLibraryDownWithHistoryPostsOnce(t *testing.T) {
	h := newHarnessWith(t, func(dir string) {
		w := history.NewWriter(dir)
		for i := 0; i < 8; i++ {
			w.Append("1", history.Entry{At: time.Now().Add(-48 * time.Hour), Played: 200, Track: library.Track{ID: fmt.Sprintf("h%d", i), Title: "H", Artist: fmt.Sprintf("a%d", i), Duration: 200 * time.Second}})
		}
	})
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

func TestClearHoldsTheRadioUntilTheNextRequest(t *testing.T) {
	h := newHarness(t)
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { _, ok := h.current(); return ok })
	h.r.Clear()
	for i := 0; i < 3; i++ {
		h.r.Tick(ctx)
	}
	time.Sleep(200 * time.Millisecond)
	if _, ok := h.current(); ok || len(h.r.Controller().Tracks()) != 0 {
		t.Fatalf("radio refilled after a clear: %v", h.r.Controller().Tracks())
	}
	h.r.Request(ctx, "u1", "Alice", []library.Track{{ID: "fresh", Title: "Fresh", Duration: 2 * time.Second}})
	waitFor(t, func() bool { cur, ok := h.current(); return ok && cur.ID == "fresh" })
	waitFor(t, func() bool { return len(radioAhead(h)) > 0 }) // the radio follows the fresh request
}

func TestAColdJoinStartsFromAFavouriteNotTheLastLine(t *testing.T) {
	now := time.Now()
	h := newHarnessWith(t, func(dir string) {
		w := history.NewWriter(dir)
		for i := 0; i < 3; i++ { // the room plays this through, night after night
			w.Append("1", history.Entry{At: now.Add(-time.Duration(48+i) * time.Hour), Played: 240, Name: "Necrious", By: "n1",
				Track: library.Track{ID: "fav", Title: "Fav", Artist: "Linkin Park", Duration: 240 * time.Second}})
		}
		// the last line is a radio pick that was skipped at once
		w.Append("1", history.Entry{At: now.Add(-time.Hour), Played: 10, By: radio.RadioBy, Name: radio.RadioBy,
			Track: library.Track{ID: "junk", Title: "Junk", Artist: "Pitbull", Duration: 200 * time.Second}})
	})
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { v := h.lib.asked.Load(); return v != nil })
	if got := h.lib.asked.Load().(library.Track); got.ID != "fav" {
		t.Fatalf("radio started from %q, not the room's favourite", got.ID)
	}
}

func (h *harness) stateFile() string { return filepath.Join(h.dir, "state", "1.json") }

// playing joins and queues a requested song so there is something to save.
func (h *harness) playing(t *testing.T) {
	t.Helper()
	h.r.Join(ctx, 10, "a-team")
	waitFor(t, func() bool { _, ok := h.current(); return ok })
	h.r.Request(ctx, "u1", "Alice", []library.Track{{ID: "req", Title: "Req", Duration: 2 * time.Second}, {ID: "req2", Title: "Req2", Duration: 2 * time.Second}})
}

func TestShutdownSavesAndKeepsTheQueue(t *testing.T) {
	h := newHarness(t)
	h.playing(t)
	n := len(h.r.Controller().Tracks())
	h.r.Shutdown(ctx)
	if _, err := os.Stat(h.stateFile()); err != nil {
		t.Fatalf("no state file: %v", err)
	}
	if len(h.r.Controller().Tracks()) != n {
		t.Fatal("shutdown cleared the queue")
	}
	h.mu.Lock()
	closed := h.conns[len(h.conns)-1].closed.Load()
	h.mu.Unlock()
	if closed == 0 {
		t.Fatal("voice not closed on shutdown")
	}
}

func TestResumeRejoinsAndPicksUp(t *testing.T) {
	h := newHarness(t)
	h.playing(t)
	before := h.r.Controller().Snapshot()
	cur := before.Tracks[before.Index]
	h.r.Shutdown(ctx)
	r2 := h.newRoom()
	t.Cleanup(func() { r2.Leave(context.Background()) })
	ok, err := r2.Resume(ctx)
	if err != nil || !ok {
		t.Fatalf("resume = %v %v", ok, err)
	}
	if ch, name := r2.Where(); ch != 10 || name != "a-team" {
		t.Fatalf("rejoined %d %q", ch, name)
	}
	waitFor(t, func() bool { c, ok := r2.Controller().Current(); return ok && c.ID == cur.ID })
	got := r2.Controller().Snapshot()
	if len(got.Tracks) != len(before.Tracks) || got.Tags[before.Index+1] != before.Tags[before.Index+1] {
		t.Fatalf("queue or requesters lost: %+v", got.Tags)
	}
	h.mu.Lock()
	posts := strings.Join(h.posts, " | ")
	h.mu.Unlock()
	if !strings.Contains(posts, "Back, picking up "+cur.Title) {
		t.Fatalf("posts = %q", posts)
	}
}

func TestResumeWithNobodyKeepsTheQueueForJoin(t *testing.T) {
	h := newHarness(t)
	h.playing(t)
	before := h.r.Controller().Snapshot()
	h.r.Shutdown(ctx)
	h.listeners.Store(0)
	r2 := h.newRoom()
	t.Cleanup(func() { r2.Leave(context.Background()) })
	if ok, _ := r2.Resume(ctx); ok {
		t.Fatal("rejoined an empty channel")
	}
	if ch, _ := r2.Where(); ch != 0 {
		t.Fatal("joined anyway")
	}
	h.listeners.Store(2)
	r2.Join(ctx, 11, "airwolf")
	cur := before.Tracks[before.Index]
	waitFor(t, func() bool { c, ok := r2.Controller().Current(); return ok && c.ID == cur.ID })
}

func TestStaleStateIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.playing(t)
	h.r.Shutdown(ctx)
	h.mu.Lock()
	h.now = h.now.Add(31 * time.Minute)
	h.mu.Unlock()
	r2 := h.newRoom()
	t.Cleanup(func() { r2.Leave(context.Background()) })
	if ok, _ := r2.Resume(ctx); ok || len(r2.Controller().Tracks()) != 0 {
		t.Fatal("a stale state was resumed")
	}
}

func TestCorruptStateIsSetAside(t *testing.T) {
	h := newHarness(t)
	os.MkdirAll(filepath.Dir(h.stateFile()), 0o700)
	os.WriteFile(h.stateFile(), []byte("{not json"), 0o600)
	if ok, err := h.r.Resume(ctx); ok || err != nil {
		t.Fatalf("resume = %v %v", ok, err)
	}
	if _, err := os.Stat(h.stateFile() + ".bad"); err != nil {
		t.Fatal("corrupt file not set aside")
	}
}

func TestLeaveLeavesNothingToResume(t *testing.T) {
	h := newHarness(t)
	h.playing(t)
	h.r.Save()
	h.r.Leave(ctx)
	r2 := h.newRoom()
	t.Cleanup(func() { r2.Leave(context.Background()) })
	if ok, _ := r2.Resume(ctx); ok || len(r2.Controller().Tracks()) != 0 {
		t.Fatal("a deliberate leave came back")
	}
}

func TestHoldSurvivesARestart(t *testing.T) {
	h := newHarness(t)
	h.playing(t)
	h.r.Clear()
	h.r.Request(ctx, "u1", "Alice", []library.Track{{ID: "solo", Title: "Solo", Duration: 2 * time.Second}})
	h.r.Clear() // held again, empty queue, still in the channel
	h.r.Shutdown(ctx)
	r2 := h.newRoom()
	t.Cleanup(func() { r2.Leave(context.Background()) })
	r2.Resume(ctx)
	time.Sleep(200 * time.Millisecond)
	if _, ok := r2.Controller().Current(); ok {
		t.Fatal("radio refilled after a restart that followed a clear")
	}
}
