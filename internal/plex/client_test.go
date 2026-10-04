package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, routes map[string]string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		for prefix, file := range routes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				b, err := os.ReadFile("testdata/" + file)
				if err != nil {
					t.Fatal(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(s.Close)
	return s, &seen
}

func TestMusicSectionAndHeaders(t *testing.T) {
	s, seen := serve(t, map[string]string{"/library/sections": "sections.json"})
	c := New(s.URL, "tok", "cid")
	id, err := c.MusicSection(context.Background())
	if err != nil || id != "3" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	h := (*seen)[0].Header
	for k, want := range map[string]string{
		"X-Plex-Token": "tok", "X-Plex-Client-Identifier": "cid",
		"X-Plex-Product": "dotamp", "Accept": "application/json",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if h.Get("X-Plex-Version") == "" {
		t.Error("missing X-Plex-Version")
	}
}

func TestSearchWithMissingHub(t *testing.T) {
	s, seen := serve(t, map[string]string{"/hubs/search": "search.json"})
	c := New(s.URL, "tok", "cid")
	c.SetSection("3")
	res, err := c.Search(context.Background(), "nsync")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artists) != 1 || len(res.Albums) != 1 || len(res.Tracks) != 0 {
		t.Fatalf("got %+v", res)
	}
	q := (*seen)[0].URL.Query()
	if q.Get("query") != "nsync" || q.Get("sectionId") != "3" {
		t.Fatalf("query = %v", q)
	}
	if res.Albums[0].ArtistID != "100" || res.Albums[0].TrackCount != 12 {
		t.Fatalf("album = %+v", res.Albums[0])
	}
}

func TestRecentAlbumsPagesAndSorts(t *testing.T) {
	s, seen := serve(t, map[string]string{"/library/sections/3/all": "albums.json"})
	c := New(s.URL, "tok", "cid")
	c.SetSection("3")
	albums, err := c.RecentAlbums(context.Background(), 50, 25)
	if err != nil || len(albums) != 2 {
		t.Fatalf("albums=%v err=%v", albums, err)
	}
	q := (*seen)[0].URL.Query()
	if q.Get("type") != "9" || q.Get("sort") != "addedAt:desc" ||
		q.Get("X-Plex-Container-Start") != "50" || q.Get("X-Plex-Container-Size") != "25" {
		t.Fatalf("query = %v", q)
	}
}

func TestAlbumTracksAndStream(t *testing.T) {
	s, _ := serve(t, map[string]string{"/library/metadata/200/children": "children.json"})
	c := New(s.URL, "tok", "cid")
	tracks, err := c.AlbumTracks(context.Background(), "200")
	if err != nil || len(tracks) != 2 {
		t.Fatalf("tracks=%v err=%v", tracks, err)
	}
	tr := tracks[0]
	if tr.Codec != "flac" || tr.SampleRate != 44100 || tr.BitDepth != 16 ||
		tr.Duration != 200*time.Second || tr.Artist != "*NSYNC" || tr.Index != 1 {
		t.Fatalf("track = %+v", tr)
	}
	st, err := c.Stream(context.Background(), tr)
	if err != nil || st.Codec != "flac" || st.URL != s.URL+"/library/parts/1/1/file.flac" {
		t.Fatalf("stream = %+v err=%v", st, err)
	}
	// The token travels in a header only, so it never lands in a URL that a
	// log line or an error message could carry.
	if strings.Contains(st.URL, "tok") || st.Headers["X-Plex-Token"] != "tok" {
		t.Fatalf("token must be a header, not a query parameter: %+v", st)
	}
	st2, _ := c.Stream(context.Background(), tracks[1])
	if st2.Codec != "mp3" || !strings.Contains(st2.URL, "/music/:/transcode/universal/start.mp3?") ||
		!strings.Contains(st2.URL, "path=%2Flibrary%2Fmetadata%2F301") || strings.Contains(st2.URL, "tok") {
		t.Fatalf("transcode stream = %+v", st2)
	}
}

func TestErrorStatus(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer s.Close()
	c := New(s.URL, "bad", "cid")
	if _, err := c.MusicSection(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestArtistsPagedInSortOrderAndLetterIndex(t *testing.T) {
	s, seen := serve(t, map[string]string{
		"/library/sections/3/all":            "artists.json",
		"/library/sections/3/firstCharacter": "firstchar.json",
	})
	c := New(s.URL, "tok", "cid")
	c.SetSection("3")
	artists, total, err := c.Artists(context.Background(), 500, 2)
	if err != nil || total != 1792 || len(artists) != 2 || artists[1].Name != "Dope Lemon" {
		t.Fatalf("artists=%v total=%d err=%v", artists, total, err)
	}
	q := (*seen)[0].URL.Query()
	if q.Get("type") != "8" || q.Get("sort") != "titleSort" ||
		q.Get("X-Plex-Container-Start") != "500" || q.Get("X-Plex-Container-Size") != "2" {
		t.Fatalf("query = %v", q)
	}
	idx, err := c.ArtistIndex(context.Background())
	if err != nil || len(idx) != 3 || idx[0].Letter != "#" || idx[0].Count != 22 || idx[2].Letter != "B" || idx[2].Count != 159 {
		t.Fatalf("index=%v err=%v", idx, err)
	}
	if q := (*seen)[1].URL.Query(); q.Get("type") != "8" {
		t.Fatalf("index query = %v", q)
	}
}

func TestItemsCarryServerID(t *testing.T) {
	s, _ := serve(t, map[string]string{"/hubs/search": "search.json", "/library/metadata/200/children": "children.json"})
	c := New(s.URL, "tok", "cid")
	c.SetSection("3")
	c.SetServer("srv1", "Ressikan")
	res, _ := c.Search(context.Background(), "nsync")
	if res.Artists[0].Server != "srv1" || res.Albums[0].Server != "srv1" {
		t.Fatalf("search results must carry the server id: %+v", res)
	}
	tracks, _ := c.AlbumTracks(context.Background(), "200")
	if tracks[0].Server != "srv1" {
		t.Fatalf("tracks must carry the server id: %+v", tracks[0])
	}
}

func TestStreamTranscodesOnlyWhenTheRelayCannotCarryTheFile(t *testing.T) {
	s, _ := serve(t, map[string]string{"/library/metadata/200/children": "children.json"})
	c := New(s.URL, "tok", "cid")
	tracks, _ := c.AlbumTracks(context.Background(), "200")
	flac := tracks[0] // 978 kbps
	// Direct connection: always the original.
	st, _ := c.Stream(context.Background(), flac)
	if st.Codec != "flac" {
		t.Fatalf("direct must be original: %+v", st)
	}
	// Relay with a Plex Pass cap of 2000 kbps: a CD FLAC fits.
	c.SetConnection(false, true, 2000)
	st, _ = c.Stream(context.Background(), flac)
	if st.Codec != "flac" {
		t.Fatalf("978 kbps fits under 2000: %+v", st)
	}
	// Relay with a free-account cap of 1000 kbps: it does not.
	c.SetConnection(false, true, 1000)
	st, _ = c.Stream(context.Background(), flac)
	if st.Codec != "mp3" || !strings.Contains(st.URL, "musicBitrate=320") || !strings.Contains(st.URL, "path=%2Flibrary%2Fmetadata%2F300") {
		t.Fatalf("over the cap must transcode: %+v", st)
	}
	// A forced remote bitrate transcodes on any non-local connection.
	c.SetConnection(false, false, 0)
	c.SetRemoteBitrate(192)
	st, _ = c.Stream(context.Background(), flac)
	if st.Codec != "mp3" || !strings.Contains(st.URL, "musicBitrate=192") {
		t.Fatalf("forced remote bitrate must transcode: %+v", st)
	}
	c.SetConnection(true, false, 0) // but not on a local connection
	st, _ = c.Stream(context.Background(), flac)
	if st.Codec != "flac" {
		t.Fatalf("local stays original: %+v", st)
	}
}
