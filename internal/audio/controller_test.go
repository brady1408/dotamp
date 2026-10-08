package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

type fakeLib struct {
	url   string
	fails map[string]int // track ID -> remaining failures to inject
	mu    sync.Mutex
	opens []string // track IDs in the order Stream was asked for them
}

func (f *fakeLib) streamOpens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opens...)
}

func (f *fakeLib) Search(context.Context, string) (library.SearchResult, error) {
	return library.SearchResult{}, nil
}
func (f *fakeLib) RecentAlbums(context.Context, int, int) ([]library.Album, error) { return nil, nil }
func (f *fakeLib) AlbumTracks(context.Context, string) ([]library.Track, error)    { return nil, nil }
func (f *fakeLib) ArtistAlbums(context.Context, string) ([]library.Album, error)   { return nil, nil }
func (f *fakeLib) Artists(context.Context, int, int) ([]library.Artist, int, error) {
	return nil, 0, nil
}
func (f *fakeLib) ArtistIndex(context.Context) ([]library.Letter, error) { return nil, nil }
func (f *fakeLib) Playlists(context.Context) ([]library.Playlist, error) { return nil, nil }
func (f *fakeLib) PlaylistTracks(context.Context, string) ([]library.Track, error) {
	return nil, nil
}
func (f *fakeLib) CreatePlaylist(context.Context, string, []library.Track) (library.Playlist, error) {
	return library.Playlist{}, nil
}
func (f *fakeLib) Stream(_ context.Context, t library.Track) (library.Stream, error) {
	f.mu.Lock()
	f.opens = append(f.opens, t.ID)
	f.mu.Unlock()
	if f.fails[t.ID] > 0 {
		f.fails[t.ID]--
		return library.Stream{URL: f.url + "/missing.flac", Codec: "flac"}, nil
	}
	return library.Stream{URL: f.url + "/sine440-44k.flac", Codec: "flac"}, nil
}

func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile("testdata/sine440-44k.flac")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sine440-44k.flac" {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "a.flac", time.Time{}, bytesReader(data))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestControllerAdvancesAndSkipsBrokenTrack(t *testing.T) {
	srv := fixtureServer(t)
	lib := &fakeLib{url: srv.URL, fails: map[string]int{"b": 2}} // b fails first try and the retry
	out := &fakeOutput{}
	eng := NewEngine(out, OutRate)
	defer eng.Close()
	var nmu sync.Mutex
	var notices []string
	c := NewController(lib, eng, func(s string) { nmu.Lock(); notices = append(notices, s); nmu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	ts := []library.Track{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}}
	if err := c.PlayTracks(ctx, ts, 0); err != nil {
		t.Fatal(err)
	}
	if cur, _ := c.Current(); cur.ID != "a" {
		t.Fatalf("current = %s", cur.ID)
	}
	// Pull two seconds of output: track a (2 s) ends, b fails twice and is skipped, c starts.
	deadline := time.Now().Add(10 * time.Second)
	for {
		out.p.pull(4410)
		if cur, _ := c.Current(); cur.ID == "c" {
			break
		}
		if time.Now().After(deadline) {
			cur, _ := c.Current()
			t.Fatalf("never reached track c; current=%s err=%v", cur.ID, eng.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	nmu.Lock()
	defer nmu.Unlock()
	if len(notices) == 0 {
		t.Fatal("expected a notice about skipping b")
	}
}

func TestControllerSeekByClampsAtZero(t *testing.T) {
	srv := fixtureServer(t)
	out := &fakeOutput{}
	eng := NewEngine(out, OutRate)
	defer eng.Close()
	c := NewController(&fakeLib{url: srv.URL}, eng, func(string) {})
	ctx := context.Background()
	_ = c.PlayTracks(ctx, []library.Track{{ID: "a"}}, 0)
	c.SeekBy(-10 * time.Second)
	if p := eng.Position(); p != 0 {
		t.Fatalf("position = %v", p)
	}
}

func TestQueueRemoveAdjustsTheCursor(t *testing.T) {
	var q Queue
	q.Replace([]library.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}}, 1)
	if cur, last := q.Remove(0); cur || last || q.Index() != 0 || len(q.Tracks()) != 2 {
		t.Fatalf("removing before the cursor: cur=%v last=%v idx=%d n=%d", cur, last, q.Index(), len(q.Tracks()))
	}
	if cur, last := q.Remove(1); cur || last || q.Index() != 0 || q.Tracks()[0].ID != "b" {
		t.Fatalf("removing after the cursor: cur=%v last=%v idx=%d", cur, last, q.Index())
	}
	q.Replace([]library.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}}, 1)
	if cur, last := q.Remove(1); !cur || last || q.Index() != 1 || q.Tracks()[1].ID != "c" {
		t.Fatalf("removing the current track points at the next: cur=%v last=%v idx=%d", cur, last, q.Index())
	}
	q.Replace([]library.Track{{ID: "a"}, {ID: "b"}}, 1)
	if cur, last := q.Remove(1); !cur || !last || q.Index() != 0 {
		t.Fatalf("removing the current last track: cur=%v last=%v idx=%d", cur, last, q.Index())
	}
	if cur, last := q.Remove(5); cur || last || len(q.Tracks()) != 1 {
		t.Fatal("an index out of range removes nothing")
	}
}

func playing(t *testing.T) (*Controller, *fakeLib, *Engine, context.Context) {
	t.Helper()
	srv := fixtureServer(t)
	lib := &fakeLib{url: srv.URL}
	eng := NewEngine(&fakeOutput{}, OutRate)
	t.Cleanup(eng.Close)
	c := NewController(lib, eng, func(string) {})
	ctx := context.Background()
	ts := []library.Track{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}}
	if err := c.PlayTracks(ctx, ts, 0); err != nil {
		t.Fatal(err)
	}
	return c, lib, eng, ctx
}

func TestControllerClearStopsAndEmpties(t *testing.T) {
	c, _, eng, _ := playing(t)
	c.Clear()
	if _, ok := c.Current(); ok || len(c.Tracks()) != 0 || c.Index() != -1 || eng.Playing() {
		t.Fatalf("after clear: tracks=%d playing=%v", len(c.Tracks()), eng.Playing())
	}
}

func TestControllerRemovePlayingTrackMovesOn(t *testing.T) {
	c, lib, eng, ctx := playing(t)
	if err := c.Remove(ctx, 0); err != nil {
		t.Fatal(err)
	}
	cur, ok := c.Current()
	if !ok || cur.ID != "b" || c.Index() != 0 || len(c.Tracks()) != 2 || !eng.Playing() {
		t.Fatalf("after removing a: cur=%+v ok=%v idx=%d n=%d", cur, ok, c.Index(), len(c.Tracks()))
	}
	if opens := lib.streamOpens(); opens[len(opens)-1] != "b" {
		t.Fatalf("b should have been opened: %v", opens)
	}
}

func TestControllerRemoveOtherTrackKeepsPlaying(t *testing.T) {
	c, lib, eng, ctx := playing(t)
	if err := c.Remove(ctx, 2); err != nil {
		t.Fatal(err)
	}
	cur, _ := c.Current()
	if cur.ID != "a" || len(c.Tracks()) != 2 || !eng.Playing() || len(lib.streamOpens()) != 1 {
		t.Fatalf("removing c must not touch playback: cur=%s n=%d opens=%v", cur.ID, len(c.Tracks()), lib.streamOpens())
	}
}

func TestControllerRemoveLastPlayingTrackStops(t *testing.T) {
	c, _, eng, ctx := playing(t)
	_ = c.Jump(ctx, 2) // play c, the last
	if err := c.Remove(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Current(); ok || eng.Playing() || len(c.Tracks()) != 2 || c.Index() != 1 {
		t.Fatalf("removing the last playing track stops: n=%d idx=%d playing=%v", len(c.Tracks()), c.Index(), eng.Playing())
	}
}

func TestControllerRemoveDropsAPrefetchedNext(t *testing.T) {
	c, _, eng, ctx := playing(t)
	c.maybePrefetch(ctx) // the fixture is 2 s long, so the next track is due already
	if eng.PeekNext() == nil {
		t.Fatal("b should be prefetched")
	}
	if err := c.Remove(ctx, 1); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	nextID := c.nextID
	c.mu.Unlock()
	if eng.PeekNext() != nil || nextID != "" {
		t.Fatalf("the prefetched b must be discarded: peek=%v nextID=%q", eng.PeekNext(), nextID)
	}
	c.maybePrefetch(ctx)
	c.mu.Lock()
	nextID = c.nextID
	c.mu.Unlock()
	if nextID != "c" {
		t.Fatalf("the new next should be c, got %q", nextID)
	}
}
