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
	eng := NewEngine(out)
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
	eng := NewEngine(out)
	defer eng.Close()
	c := NewController(&fakeLib{url: srv.URL}, eng, func(string) {})
	ctx := context.Background()
	_ = c.PlayTracks(ctx, []library.Track{{ID: "a"}}, 0)
	c.SeekBy(-10 * time.Second)
	if p := eng.Position(); p != 0 {
		t.Fatalf("position = %v", p)
	}
}
