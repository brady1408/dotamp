package subsonic

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

// fakeNavidrome checks the Subsonic auth on every call and answers the
// handful of endpoints dotamp uses.
func fakeNavidrome(t *testing.T) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	ok := func(body any) []byte {
		b, _ := json.Marshal(map[string]any{"subsonic-response": merge(map[string]any{"status": "ok", "version": "1.16.1", "type": "navidrome", "openSubsonic": true}, body)})
		return b
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		q := r.URL.Query()
		sum := md5.Sum([]byte("secret" + q.Get("s")))
		if q.Get("u") != "brady" || q.Get("t") != hex.EncodeToString(sum[:]) || q.Get("s") == "" || q.Get("v") == "" || q.Get("c") != "dotamp" || q.Get("f") != "json" {
			w.Write([]byte(`{"subsonic-response":{"status":"failed","version":"1.16.1","error":{"code":40,"message":"Wrong username or password"}}}`))
			return
		}
		switch r.URL.Path {
		case "/rest/ping":
			w.Write(ok(nil))
		case "/rest/search3":
			w.Write(ok(map[string]any{"searchResult3": map[string]any{
				"artist": []map[string]any{{"id": "ar1", "name": "*NSYNC", "albumCount": 3}},
				"album":  []map[string]any{{"id": "al1", "name": "No Strings Attached", "artist": "*NSYNC", "artistId": "ar1", "year": 2000, "songCount": 12, "created": "2025-09-04T10:00:00Z"}},
				"song": []map[string]any{{"id": "s1", "title": "Bye Bye Bye", "album": "No Strings Attached", "albumId": "al1", "artist": "*NSYNC", "artistId": "ar1",
					"track": 1, "duration": 200, "bitRate": 978, "suffix": "flac", "contentType": "audio/flac", "samplingRate": 44100, "bitDepth": 16}},
			}}))
		case "/rest/getArtists":
			w.Write(ok(map[string]any{"artists": map[string]any{"index": []map[string]any{
				{"name": "#", "artist": []map[string]any{{"id": "ar0", "name": "311"}}},
				{"name": "N", "artist": []map[string]any{{"id": "ar1", "name": "*NSYNC"}, {"id": "ar2", "name": "Nirvana"}}},
			}}}))
		case "/rest/getAlbumList2":
			if q.Get("type") != "newest" {
				http.Error(w, "type", 400)
				return
			}
			w.Write(ok(map[string]any{"albumList2": map[string]any{"album": []map[string]any{
				{"id": "al9", "name": "Newest", "artist": "Someone", "artistId": "ar9", "year": 2026, "songCount": 1},
			}}}))
		case "/rest/getAlbum":
			w.Write(ok(map[string]any{"album": map[string]any{"id": q.Get("id"), "name": "No Strings Attached", "song": []map[string]any{
				{"id": "s1", "title": "Bye Bye Bye", "track": 1, "duration": 200, "suffix": "flac", "bitRate": 978},
				{"id": "s2", "title": "It's Gonna Be Me", "track": 2, "duration": 191, "suffix": "m4a", "bitRate": 256},
			}}}))
		case "/rest/getArtist":
			w.Write(ok(map[string]any{"artist": map[string]any{"id": q.Get("id"), "name": "*NSYNC", "album": []map[string]any{
				{"id": "al1", "name": "No Strings Attached", "artist": "*NSYNC", "artistId": "ar1", "year": 2000, "songCount": 12},
			}}}))
		case "/rest/getPlaylists":
			w.Write(ok(map[string]any{"playlists": map[string]any{"playlist": []map[string]any{
				{"id": "p1", "name": "Road Trip", "songCount": 2, "owner": "brady"},
				{"id": "p2", "name": "Tea & Toast — été", "songCount": 0, "owner": "brady"},
			}}}))
		case "/rest/getPlaylist":
			w.Write(ok(map[string]any{"playlist": map[string]any{"id": q.Get("id"), "name": "Road Trip", "songCount": 2, "entry": []map[string]any{
				{"id": "s2", "title": "It's Gonna Be Me", "album": "No Strings Attached", "albumId": "al1", "artist": "*NSYNC", "track": 2, "duration": 191, "suffix": "m4a", "bitRate": 256},
				{"id": "s1", "title": "Bye Bye Bye", "album": "No Strings Attached", "albumId": "al1", "artist": "*NSYNC", "track": 1, "duration": 200, "suffix": "flac", "bitRate": 978},
			}}}))
		case "/rest/createPlaylist":
			if q.Get("name") == "nobody" { // a server that creates but returns nothing
				w.Write(ok(nil))
				return
			}
			id := "p9"
			if q.Get("playlistId") != "" {
				id = q.Get("playlistId")
			}
			w.Write(ok(map[string]any{"playlist": map[string]any{"id": id, "name": q.Get("name"), "songCount": len(q["songId"])}}))
		case "/rest/updatePlaylist", "/rest/deletePlaylist":
			w.Write(ok(nil))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s, &seen
}

func merge(a map[string]any, b any) map[string]any {
	if m, ok := b.(map[string]any); ok {
		for k, v := range m {
			a[k] = v
		}
	}
	return a
}

func TestPingSignsEveryRequest(t *testing.T) {
	s, seen := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	if err := c.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	q := (*seen)[0].URL.Query()
	if q.Get("t") == "" || q.Get("s") == "" || strings.Contains((*seen)[0].URL.String(), "secret") {
		t.Fatalf("auth must be the salted token, never the password: %s", (*seen)[0].URL)
	}
	bad := New(s.URL, "brady", "wrong")
	if err := bad.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "Wrong username") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchMapsAndTags(t *testing.T) {
	s, _ := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	c.SetServer("nd", "Navidrome")
	res, err := c.Search(context.Background(), "nsync")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Artists) != 1 || res.Artists[0].Server != "nd" || len(res.Albums) != 1 || res.Albums[0].TrackCount != 12 || res.Albums[0].ArtistID != "ar1" {
		t.Fatalf("res = %+v", res)
	}
	tr := res.Tracks[0]
	if tr.Codec != "flac" || tr.Duration != 200*time.Second || tr.Bitrate != 978 || tr.SampleRate != 44100 || tr.BitDepth != 16 || tr.Index != 1 || tr.AlbumID != "al1" || tr.Server != "nd" {
		t.Fatalf("track = %+v", tr)
	}
}

func TestArtistsComeWithTheirIndex(t *testing.T) {
	s, seen := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	artists, total, err := c.Artists(context.Background(), 1, 2)
	if err != nil || total != 3 || len(artists) != 2 || artists[0].Name != "*NSYNC" || artists[1].Name != "Nirvana" {
		t.Fatalf("artists=%+v total=%d err=%v", artists, total, err)
	}
	idx, err := c.ArtistIndex(context.Background())
	if err != nil || len(idx) != 2 || idx[0].Letter != "#" || idx[0].Count != 1 || idx[1].Letter != "N" || idx[1].Count != 2 {
		t.Fatalf("index=%+v err=%v", idx, err)
	}
	calls := 0
	for _, r := range *seen {
		if r.URL.Path == "/rest/getArtists" {
			calls++
		}
	}
	if calls != 1 {
		t.Fatalf("the artist list should be fetched once and cached, got %d calls", calls)
	}
}

func TestRecentAlbumsAndChildren(t *testing.T) {
	s, _ := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	albums, err := c.RecentAlbums(context.Background(), 0, 100)
	if err != nil || len(albums) != 1 || albums[0].Title != "Newest" {
		t.Fatalf("albums=%+v err=%v", albums, err)
	}
	tracks, err := c.AlbumTracks(context.Background(), "al1")
	if err != nil || len(tracks) != 2 || tracks[0].Album != "No Strings Attached" || tracks[1].Codec != "m4a" || tracks[1].AlbumID != "al1" {
		t.Fatalf("tracks=%+v err=%v", tracks, err)
	}
	arts, err := c.ArtistAlbums(context.Background(), "ar1")
	if err != nil || len(arts) != 1 || arts[0].Artist != "*NSYNC" {
		t.Fatalf("artist albums=%+v err=%v", arts, err)
	}
}

func TestStreamRawOrTranscoded(t *testing.T) {
	s, _ := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	tracks, _ := c.AlbumTracks(context.Background(), "al1")
	st, err := c.Stream(context.Background(), tracks[0])
	if err != nil || st.Codec != "flac" || !strings.Contains(st.URL, "/rest/stream?") || !strings.Contains(st.URL, "id=s1") || !strings.Contains(st.URL, "format=raw") {
		t.Fatalf("flac stream = %+v err=%v", st, err)
	}
	if strings.Contains(st.URL, "secret") {
		t.Fatal("the password must never be in a URL")
	}
	st, _ = c.Stream(context.Background(), tracks[1]) // m4a: not decodable, ask for mp3
	if st.Codec != "mp3" || !strings.Contains(st.URL, "format=mp3") || !strings.Contains(st.URL, "maxBitRate=320") {
		t.Fatalf("m4a stream = %+v", st)
	}
	c.SetRemoteBitrate(192)
	c.SetLocal(false)
	st, _ = c.Stream(context.Background(), tracks[0])
	if st.Codec != "mp3" || !strings.Contains(st.URL, "maxBitRate=192") {
		t.Fatalf("remote forced bitrate = %+v", st)
	}
	c.SetLocal(true)
	st, _ = c.Stream(context.Background(), tracks[0])
	if st.Codec != "flac" {
		t.Fatalf("local stays original: %+v", st)
	}
}

func TestPlaylistsAndTheirTracks(t *testing.T) {
	s, _ := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	c.SetServer("navidrome", "Navidrome")
	ps, err := c.Playlists(context.Background())
	if err != nil || len(ps) != 2 || ps[0].ID != "p1" || ps[0].TrackCount != 2 || ps[1].Name != "Tea & Toast — été" || ps[0].Server != "navidrome" {
		t.Fatalf("playlists=%+v err=%v", ps, err)
	}
	ts, err := c.PlaylistTracks(context.Background(), "p1")
	if err != nil || len(ts) != 2 || ts[0].ID != "s2" || ts[1].ID != "s1" || ts[0].Album != "No Strings Attached" || ts[1].Server != "navidrome" {
		t.Fatalf("tracks=%+v err=%v", ts, err)
	}
}

func TestCreatePlaylistRepeatsSongIDsInOrder(t *testing.T) {
	s, seen := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	c.SetServer("navidrome", "Navidrome")
	p, err := c.CreatePlaylist(context.Background(), "Tea & Toast — été", []library.Track{{ID: "s2"}, {ID: "s1"}})
	if err != nil || p.ID != "p9" || p.Name != "Tea & Toast — été" || p.TrackCount != 2 || p.Server != "navidrome" {
		t.Fatalf("playlist=%+v err=%v", p, err)
	}
	last := (*seen)[len(*seen)-1]
	if last.URL.Path != "/rest/createPlaylist" {
		t.Fatalf("last call = %s", last.URL.Path)
	}
	if ids := last.URL.Query()["songId"]; len(ids) != 2 || ids[0] != "s2" || ids[1] != "s1" {
		t.Fatalf("songId = %v", ids)
	}
	if last.URL.Query().Get("name") != "Tea & Toast — été" {
		t.Fatalf("name = %q", last.URL.Query().Get("name"))
	}
}

func TestCreatePlaylistFallsBackToTheListing(t *testing.T) {
	s, seen := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	// The fake returns no playlist for the name "nobody"; the client must
	// then list playlists and find one by name. Our listing has no "nobody",
	// so the result is an error that names the problem, not a panic.
	_, err := c.CreatePlaylist(context.Background(), "nobody", []library.Track{{ID: "s1"}})
	if err == nil || !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("err = %v", err)
	}
	if (*seen)[len(*seen)-1].URL.Path != "/rest/getPlaylists" {
		t.Fatal("the client should have looked the playlist up by name")
	}
}

func TestCreatePlaylistRejectsNoTracks(t *testing.T) {
	c := New("http://127.0.0.1:9", "brady", "secret")
	if _, err := c.CreatePlaylist(context.Background(), "Empty", nil); err == nil {
		t.Fatal("an empty playlist must be an error before any request")
	}
}

func TestAddAndRemoveUseUpdatePlaylist(t *testing.T) {
	s, seen := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	if err := c.AddToPlaylist(context.Background(), "p1", []library.Track{{ID: "s7"}, {ID: "s8"}}); err != nil {
		t.Fatal(err)
	}
	q := (*seen)[len(*seen)-1].URL.Query()
	if (*seen)[len(*seen)-1].URL.Path != "/rest/updatePlaylist" || q.Get("playlistId") != "p1" || strings.Join(q["songIdToAdd"], ",") != "s7,s8" {
		t.Fatalf("add query = %v", q)
	}
	if err := c.RemoveFromPlaylist(context.Background(), "p1", 1); err != nil {
		t.Fatal(err)
	}
	q = (*seen)[len(*seen)-1].URL.Query()
	if q.Get("playlistId") != "p1" || q.Get("songIndexToRemove") != "1" {
		t.Fatalf("remove query = %v", q)
	}
}

func TestMoveResendsTheReorderedList(t *testing.T) {
	s, seen := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	// the fake's playlist is [s2, s1]; moving entry 1 to 0 gives [s1, s2]
	if err := c.MovePlaylistTrack(context.Background(), "p1", 1, 0); err != nil {
		t.Fatal(err)
	}
	last := (*seen)[len(*seen)-1]
	q := last.URL.Query()
	if last.URL.Path != "/rest/createPlaylist" || q.Get("playlistId") != "p1" || strings.Join(q["songId"], ",") != "s1,s2" {
		t.Fatalf("move = %s %v", last.URL.Path, q)
	}
	if err := c.MovePlaylistTrack(context.Background(), "p1", 0, 4); err == nil || !strings.Contains(err.Error(), "2 entries") {
		t.Fatalf("out of range: %v", err)
	}
}

func TestRenameAndDeleteUseTheSubsonicCalls(t *testing.T) {
	s, seen := fakeNavidrome(t)
	c := New(s.URL, "brady", "secret")
	if err := c.RenamePlaylist(context.Background(), "p1", "Tea & Toast — été"); err != nil {
		t.Fatal(err)
	}
	last := (*seen)[len(*seen)-1]
	if last.URL.Path != "/rest/updatePlaylist" || last.URL.Query().Get("playlistId") != "p1" || last.URL.Query().Get("name") != "Tea & Toast — été" {
		t.Fatalf("rename = %s %v", last.URL.Path, last.URL.Query())
	}
	if err := c.DeletePlaylist(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	last = (*seen)[len(*seen)-1]
	if last.URL.Path != "/rest/deletePlaylist" || last.URL.Query().Get("id") != "p1" {
		t.Fatalf("delete = %s %v", last.URL.Path, last.URL.Query())
	}
}
