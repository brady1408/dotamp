package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/brady1408/dotamp/internal/library"
)

func TestPlaylistsListsAudioPlaylistsAndTagsServer(t *testing.T) {
	s, seen := serve(t, map[string]string{"/playlists": "playlists.json"})
	c := New(s.URL, "tok", "cid")
	c.SetServer("srv1", "Ressikan")
	ps, err := c.Playlists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != "901" || ps[0].Name != "Road Trip" || ps[0].TrackCount != 14 || ps[0].Server != "srv1" {
		t.Fatalf("playlists = %+v", ps)
	}
	if q := (*seen)[0].URL.Query(); q.Get("playlistType") != "audio" {
		t.Fatalf("must ask for audio playlists only: %v", q)
	}
}

func TestPlaylistTracksReuseTheTrackMapping(t *testing.T) {
	s, _ := serve(t, map[string]string{"/playlists/901/items": "playlist-items.json"})
	c := New(s.URL, "tok", "cid")
	c.SetServer("srv1", "Ressikan")
	ts, err := c.PlaylistTracks(context.Background(), "901")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].ID != "300" || ts[0].Artist != "*NSYNC" || ts[0].Codec != "flac" || ts[0].SampleRate != 44100 || ts[1].Server != "srv1" {
		t.Fatalf("tracks = %+v", ts)
	}
}

// createServer answers /identity and records every POST/PUT to /playlists.
func createServer(t *testing.T) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/identity":
			b, _ := os.ReadFile("testdata/identity.json")
			_, _ = w.Write(b)
		case r.Method == http.MethodPost && r.URL.Path == "/playlists":
			seen = append(seen, r)
			b, _ := os.ReadFile("testdata/playlist-created.json")
			_, _ = w.Write(b)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/playlists/903/items"):
			seen = append(seen, r)
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s, &seen
}

func TestCreatePlaylistPostsNameAndKeysInOrder(t *testing.T) {
	s, seen := createServer(t)
	c := New(s.URL, "tok", "cid")
	c.SetServer("manual", "configured server") // not a machine id: must fetch /identity
	tracks := []library.Track{{ID: "301"}, {ID: "300"}}
	p, err := c.CreatePlaylist(context.Background(), "Tea & Toast — été", tracks)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "903" || p.Name != "Tea & Toast — été" || p.TrackCount != 2 || p.Server != "manual" {
		t.Fatalf("playlist = %+v", p)
	}
	if len(*seen) != 1 {
		t.Fatalf("one POST expected, saw %d", len(*seen))
	}
	q := (*seen)[0].URL.Query()
	if q.Get("type") != "audio" || q.Get("smart") != "0" || q.Get("title") != "Tea & Toast — été" {
		t.Fatalf("query = %v", q)
	}
	if want := "server://abc123machine/com.plexapp.plugins.library/library/metadata/301,300"; q.Get("uri") != want {
		t.Fatalf("uri = %q, want %q", q.Get("uri"), want)
	}
	if (*seen)[0].Header.Get("X-Plex-Token") != "tok" || strings.Contains((*seen)[0].URL.String(), "tok") {
		t.Fatal("token must be in the header only")
	}
}

func TestCreatePlaylistBatchesAbove500(t *testing.T) {
	s, seen := createServer(t)
	c := New(s.URL, "tok", "cid")
	tracks := make([]library.Track, 1200)
	for i := range tracks {
		tracks[i] = library.Track{ID: "k" + strconv.Itoa(i)}
	}
	if _, err := c.CreatePlaylist(context.Background(), "Big", tracks); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 3 || (*seen)[0].Method != http.MethodPost || (*seen)[1].Method != http.MethodPut || (*seen)[2].Method != http.MethodPut {
		t.Fatalf("want POST, PUT, PUT; got %d requests", len(*seen))
	}
	first := (*seen)[0].URL.Query().Get("uri")
	if n := strings.Count(first, ",") + 1; n != 500 {
		t.Fatalf("first batch should carry 500 ids, carried %d", n)
	}
	last := (*seen)[2].URL.Query().Get("uri")
	if !strings.HasSuffix(last, ",k1199") || strings.Count(last, ",")+1 != 200 {
		t.Fatalf("last batch = %q", last)
	}
}

func TestCreatePlaylistRejectsNoTracks(t *testing.T) {
	c := New("http://127.0.0.1:9", "tok", "cid")
	if _, err := c.CreatePlaylist(context.Background(), "Empty", nil); err == nil {
		t.Fatal("an empty playlist must be an error before any request")
	}
}

func TestMachineIDIsFetchedOnce(t *testing.T) {
	s, _ := createServer(t)
	c := New(s.URL, "tok", "cid")
	hits := 0
	c.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/identity" {
			hits++
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	_, _ = c.CreatePlaylist(context.Background(), "A", []library.Track{{ID: "1"}})
	_, _ = c.CreatePlaylist(context.Background(), "B", []library.Track{{ID: "2"}})
	if hits != 1 {
		t.Fatalf("/identity fetched %d times, want 1", hits)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// editServer answers /identity and the items listing, and records every
// PUT and DELETE under /playlists/901.
func editServer(t *testing.T) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/identity":
			b, _ := os.ReadFile("testdata/identity.json")
			_, _ = w.Write(b)
		case r.Method == http.MethodGet && r.URL.Path == "/playlists/901/items":
			b, _ := os.ReadFile("testdata/playlist-items-ids.json")
			_, _ = w.Write(b)
		case (r.Method == http.MethodPut || r.Method == http.MethodDelete) && strings.HasPrefix(r.URL.Path, "/playlists/901/items"):
			seen = append(seen, r)
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s, &seen
}

func TestAddToPlaylistPutsKeysInOrder(t *testing.T) {
	s, seen := editServer(t)
	c := New(s.URL, "tok", "cid")
	if err := c.AddToPlaylist(context.Background(), "901", []library.Track{{ID: "7"}, {ID: "8"}}); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 || (*seen)[0].Method != http.MethodPut {
		t.Fatalf("one PUT expected: %d", len(*seen))
	}
	if uri := (*seen)[0].URL.Query().Get("uri"); !strings.HasSuffix(uri, "/library/metadata/7,8") {
		t.Fatalf("uri = %q", uri)
	}
}

func TestRemoveFromPlaylistDeletesTheItemAtIndex(t *testing.T) {
	s, seen := editServer(t)
	c := New(s.URL, "tok", "cid")
	if err := c.RemoveFromPlaylist(context.Background(), "901", 1); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 || (*seen)[0].Method != http.MethodDelete || (*seen)[0].URL.Path != "/playlists/901/items/9002" {
		t.Fatalf("expected DELETE of item 9002, got %+v", *seen)
	}
	if err := c.RemoveFromPlaylist(context.Background(), "901", 3); err == nil || !strings.Contains(err.Error(), "3 entries") {
		t.Fatalf("out of range must fail before any write: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatal("no write for an out-of-range index")
	}
}

func TestMovePlaylistTrackNamesThePredecessor(t *testing.T) {
	s, seen := editServer(t)
	c := New(s.URL, "tok", "cid")
	// Move the last entry (9003) up one: it should sit after 9001.
	if err := c.MovePlaylistTrack(context.Background(), "901", 2, 1); err != nil {
		t.Fatal(err)
	}
	r := (*seen)[0]
	if r.Method != http.MethodPut || r.URL.Path != "/playlists/901/items/9003/move" || r.URL.Query().Get("after") != "9001" {
		t.Fatalf("move = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}
	// Move the second entry (9002) to the top: no after.
	if err := c.MovePlaylistTrack(context.Background(), "901", 1, 0); err != nil {
		t.Fatal(err)
	}
	r = (*seen)[1]
	if r.URL.Path != "/playlists/901/items/9002/move" || r.URL.Query().Has("after") {
		t.Fatalf("to top = %s?%s", r.URL.Path, r.URL.RawQuery)
	}
	// Move the first entry (9001) down one: it should sit after 9002.
	if err := c.MovePlaylistTrack(context.Background(), "901", 0, 1); err != nil {
		t.Fatal(err)
	}
	if r = (*seen)[2]; r.URL.Path != "/playlists/901/items/9001/move" || r.URL.Query().Get("after") != "9002" {
		t.Fatalf("down = %s?%s", r.URL.Path, r.URL.RawQuery)
	}
	if err := c.MovePlaylistTrack(context.Background(), "901", 0, 5); err == nil {
		t.Fatal("out of range must fail")
	}
}

func TestRenameAndDeletePlaylist(t *testing.T) {
	var seen []*http.Request
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MediaContainer":{"size":0}}`))
	}))
	defer s.Close()
	c := New(s.URL, "tok", "cid")
	if err := c.RenamePlaylist(context.Background(), "901", "Tea & Toast — été"); err != nil {
		t.Fatal(err)
	}
	if r := seen[0]; r.Method != http.MethodPut || r.URL.Path != "/playlists/901" || r.URL.Query().Get("title") != "Tea & Toast — été" {
		t.Fatalf("rename = %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}
	if err := c.DeletePlaylist(context.Background(), "901"); err != nil {
		t.Fatal(err)
	}
	if r := seen[1]; r.Method != http.MethodDelete || r.URL.Path != "/playlists/901" {
		t.Fatalf("delete = %s %s", r.Method, r.URL.Path)
	}
}
