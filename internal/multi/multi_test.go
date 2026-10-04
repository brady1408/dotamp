package multi

import (
	"context"
	"errors"
	"testing"

	"github.com/brady1408/dotamp/internal/library"
)

// fake is one server's library; every item it returns is tagged with its id.
type fake struct {
	id     string
	tracks map[string][]library.Track
	calls  []string
	fail   bool
}

func (f *fake) tag(a library.Artist) library.Artist { a.Server = f.id; return a }

func (f *fake) Search(_ context.Context, q string) (library.SearchResult, error) {
	f.calls = append(f.calls, "search:"+q)
	if f.fail {
		return library.SearchResult{}, errors.New("down")
	}
	return library.SearchResult{
		Artists: []library.Artist{f.tag(library.Artist{ID: "a-" + f.id, Name: "Artist " + f.id})},
		Albums:  []library.Album{{ID: "al-" + f.id, Title: "Album " + f.id, Server: f.id}},
		Tracks:  []library.Track{{ID: "t-" + f.id, Title: "Track " + f.id, Server: f.id}},
	}, nil
}
func (f *fake) RecentAlbums(context.Context, int, int) ([]library.Album, error) {
	f.calls = append(f.calls, "recent")
	return []library.Album{{ID: "r-" + f.id, Server: f.id}}, nil
}
func (f *fake) AlbumTracks(_ context.Context, id string) ([]library.Track, error) {
	f.calls = append(f.calls, "tracks:"+id)
	return []library.Track{{ID: "t1", AlbumID: id, Server: f.id}}, nil
}
func (f *fake) ArtistAlbums(_ context.Context, id string) ([]library.Album, error) {
	f.calls = append(f.calls, "albums:"+id)
	return nil, nil
}
func (f *fake) Artists(context.Context, int, int) ([]library.Artist, int, error) {
	f.calls = append(f.calls, "artists")
	return nil, 0, nil
}
func (f *fake) ArtistIndex(context.Context) ([]library.Letter, error) {
	f.calls = append(f.calls, "index")
	return nil, nil
}
func (f *fake) Stream(_ context.Context, t library.Track) (library.Stream, error) {
	f.calls = append(f.calls, "stream:"+t.ID)
	return library.Stream{URL: "http://" + f.id + "/" + t.ID}, nil
}

func TestSearchFansOutAndTagsByServer(t *testing.T) {
	a, b := &fake{id: "A"}, &fake{id: "B", fail: true}
	m := New()
	m.Add(Server{ID: "A", Name: "Mine", Owned: true}, a)
	m.Add(Server{ID: "B", Name: "Friend"}, b)
	res, err := m.Search(context.Background(), "x")
	if err != nil {
		t.Fatal(err) // one server down is not an error for the search
	}
	if len(res.Albums) != 1 || res.Albums[0].Server != "A" || len(res.Tracks) != 1 {
		t.Fatalf("results = %+v", res)
	}
	if len(a.calls) != 1 || len(b.calls) != 1 {
		t.Fatalf("both servers must be asked: %v %v", a.calls, b.calls)
	}
	if _, err := New().Search(context.Background(), "x"); err == nil {
		t.Fatal("no servers at all is an error")
	}
}

func TestRoutingFollowsTheItemServer(t *testing.T) {
	a, b := &fake{id: "A"}, &fake{id: "B"}
	m := New()
	m.Add(Server{ID: "A", Name: "Mine", Owned: true}, a)
	m.Add(Server{ID: "B", Name: "Friend"}, b)
	ts, err := m.AlbumTracks(context.Background(), "B:al-B")
	if err != nil || len(ts) != 1 || ts[0].Server != "B" || ts[0].AlbumID != "B:al-B" || ts[0].ID != "B:t1" {
		t.Fatalf("tracks=%+v err=%v", ts, err)
	}
	if b.calls[0] != "tracks:al-B" || len(a.calls) != 0 {
		t.Fatalf("B should be asked, not A: %v %v", b.calls, a.calls)
	}
	st, err := m.Stream(context.Background(), library.Track{ID: "t9", Server: "B"})
	if err != nil || st.URL != "http://B/t9" {
		t.Fatalf("stream=%+v err=%v", st, err)
	}
	if _, err := m.Stream(context.Background(), library.Track{ID: "t9", Server: "Z"}); err == nil {
		t.Fatal("unknown server must be an error")
	}
}

func TestBrowsingUsesTheCurrentServer(t *testing.T) {
	a, b := &fake{id: "A"}, &fake{id: "B"}
	m := New()
	m.Add(Server{ID: "A", Name: "Mine", Owned: true}, a)
	m.Add(Server{ID: "B", Name: "Friend"}, b)
	if cur := m.Current(); cur.ID != "A" {
		t.Fatalf("first added is current by default: %+v", cur)
	}
	if _, err := m.RecentAlbums(context.Background(), 0, 10); err != nil || len(a.calls) != 1 {
		t.Fatalf("recent should go to A: %v", a.calls)
	}
	if !m.SetCurrent("B") || m.SetCurrent("nope") {
		t.Fatal("SetCurrent should succeed for B and fail for an unknown id")
	}
	_, _, _ = m.Artists(context.Background(), 0, 10)
	_, _ = m.ArtistIndex(context.Background())
	if len(b.calls) != 2 {
		t.Fatalf("artists and index should go to B: %v", b.calls)
	}
	servers := m.Servers()
	if len(servers) != 2 || servers[0].Name != "Mine" || !servers[0].Owned {
		t.Fatalf("servers = %+v", servers)
	}
}

func TestQualifiedIDsRoundTrip(t *testing.T) {
	a := &fake{id: "A"}
	m := New()
	m.Add(Server{ID: "A", Name: "Mine"}, a)
	res, _ := m.Search(context.Background(), "x")
	// IDs handed to the UI are qualified so a later call can route them.
	if res.Albums[0].ID != "A:al-A" || res.Artists[0].ID != "A:a-A" {
		t.Fatalf("ids = %q %q", res.Albums[0].ID, res.Artists[0].ID)
	}
	_, _ = m.ArtistAlbums(context.Background(), res.Artists[0].ID)
	if a.calls[len(a.calls)-1] != "albums:a-A" {
		t.Fatalf("the raw id must reach the server: %v", a.calls)
	}
}
