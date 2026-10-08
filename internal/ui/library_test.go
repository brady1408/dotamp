package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/library"
)

type stubLib struct{}

func (stubLib) Search(_ context.Context, q string) (library.SearchResult, error) {
	return library.SearchResult{Albums: []library.Album{{ID: "200", Title: "No Strings Attached", Artist: "*NSYNC"}}}, nil
}
func (stubLib) RecentAlbums(context.Context, int, int) ([]library.Album, error) {
	return []library.Album{{ID: "1", Title: "Recent One"}, {ID: "2", Title: "Recent Two"}}, nil
}
func (stubLib) AlbumTracks(_ context.Context, id string) ([]library.Track, error) {
	return []library.Track{{ID: "300", Title: "Bye Bye Bye", AlbumID: id, Index: 1}, {ID: "301", Title: "It's Gonna Be Me", AlbumID: id, Index: 2}}, nil
}
func (stubLib) Artists(context.Context, int, int) ([]library.Artist, int, error) { return nil, 0, nil }
func (stubLib) ArtistIndex(context.Context) ([]library.Letter, error)            { return nil, nil }
func (stubLib) Playlists(context.Context) ([]library.Playlist, error) {
	return []library.Playlist{{ID: "p1", Name: "Road Trip", TrackCount: 3}, {ID: "p2", Name: "Empty", TrackCount: 0}}, nil
}
func (stubLib) PlaylistTracks(_ context.Context, id string) ([]library.Track, error) {
	if id == "p2" {
		return nil, nil
	}
	// count says 3, the server has deleted one since: two come back
	return []library.Track{{ID: "301", Title: "It's Gonna Be Me", Artist: "*NSYNC", Index: 2}, {ID: "300", Title: "Bye Bye Bye", Artist: "*NSYNC", Index: 1}}, nil
}
func (stubLib) CreatePlaylist(_ context.Context, name string, ts []library.Track) (library.Playlist, error) {
	return library.Playlist{ID: "new", Name: name, TrackCount: len(ts)}, nil
}
func (stubLib) AddToPlaylist(context.Context, string, []library.Track) error  { return nil }
func (stubLib) RemoveFromPlaylist(context.Context, string, int) error         { return nil }
func (stubLib) MovePlaylistTrack(context.Context, string, int, int) error     { return nil }
func (stubLib) ArtistAlbums(context.Context, string) ([]library.Album, error) { return nil, nil }
func (stubLib) Stream(context.Context, library.Track) (library.Stream, error) {
	return library.Stream{}, nil
}

func TestBrowserRecentSearchOpenBack(t *testing.T) {
	b := NewBrowser(stubLib{})
	ctx := context.Background()
	if err := b.LoadRoot(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.LoadRecent(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.List().Rows) != 3 || b.List().Rows[1].Album == nil { // header + 2 albums
		t.Fatalf("recent rows = %+v", b.List().Rows)
	}
	if err := b.Search(ctx, "nsync"); err != nil {
		t.Fatal(err)
	}
	if b.Title() != "Search: nsync" {
		t.Fatalf("title = %q", b.Title())
	}
	sel := b.List().Selected()
	if sel == nil || sel.Album == nil {
		t.Fatalf("selected = %+v", sel)
	}
	if err := b.OpenAlbum(ctx, *sel.Album); err != nil {
		t.Fatal(err)
	}
	if tr := b.List().Selected(); tr == nil || tr.Track == nil || tr.Track.ID != "300" {
		t.Fatalf("album view selected = %+v", tr)
	}
	if !b.Back(ctx) || b.Title() != "Search: nsync" {
		t.Fatal("back should return to search results")
	}
	if !b.Back(ctx) || b.Title() != "Recently added" {
		t.Fatal("back should return to recent")
	}
	if !b.Back(ctx) || b.Title() != "Library" {
		t.Fatal("back should return to the root menu")
	}
	if b.Back(ctx) {
		t.Fatal("back at root must be false")
	}
}

func TestSearchFieldKeys(t *testing.T) {
	var f SearchField
	f.Open = true
	for _, r := range "ab" {
		f.Key(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	f.Key(tcell.NewEventKey(tcell.KeyBackspace2, 0, 0))
	if f.Text != "a" {
		t.Fatalf("text = %q", f.Text)
	}
	if submit, _ := f.Key(tcell.NewEventKey(tcell.KeyEnter, 0, 0)); !submit {
		t.Fatal("enter should submit")
	}
	if _, cancel := f.Key(tcell.NewEventKey(tcell.KeyEscape, 0, 0)); !cancel || f.Open {
		t.Fatal("escape should cancel and close")
	}
}

func TestSearchTrackRowsNameTheAlbum(t *testing.T) {
	b := NewBrowser(albumLib{})
	if err := b.Search(context.Background(), "bye"); err != nil {
		t.Fatal(err)
	}
	for _, r := range b.List().Rows {
		if r.Track != nil {
			if !strings.Contains(r.Text, "No Strings Attached") {
				t.Fatalf("track row should name its album: %q", r.Text)
			}
			return
		}
	}
	t.Fatal("no track row")
}

// albumLib returns a track search hit with its album filled in.
type albumLib struct{ stubLib }

func (albumLib) Search(context.Context, string) (library.SearchResult, error) {
	return library.SearchResult{Tracks: []library.Track{{ID: "300", Title: "Bye Bye Bye", Artist: "*NSYNC", Album: "No Strings Attached"}}}, nil
}

func TestRootMenuHasPlaylists(t *testing.T) {
	b := NewBrowser(stubLib{})
	_ = b.LoadRoot(context.Background())
	rows := b.List().Rows
	if len(rows) != 3 || rows[2].Menu != MenuPlaylists || rows[2].Text != "Playlists" {
		t.Fatalf("root rows = %+v", rows)
	}
}

func TestPlaylistsViewListsAndOpens(t *testing.T) {
	b := NewBrowser(stubLib{})
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	if err := b.OpenPlaylists(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Title() != "Playlists" {
		t.Fatalf("title = %q", b.Title())
	}
	rows := b.List().Rows
	if len(rows) != 2 || rows[0].Playlist == nil || rows[0].Text != "Road Trip" || rows[0].Right != "3" {
		t.Fatalf("rows = %+v", rows)
	}
	if err := b.OpenPlaylist(ctx, *rows[0].Playlist); err != nil {
		t.Fatal(err)
	}
	rows = b.List().Rows
	// header, then the two tracks that still exist, numbered by position
	if len(rows) != 3 || !rows[0].Header || rows[1].Text != " 1. *NSYNC — It's Gonna Be Me" || rows[2].Track.ID != "300" {
		t.Fatalf("playlist rows = %+v", rows)
	}
	ts, ok := b.AlbumContext()
	if !ok || len(ts) != 2 || ts[0].ID != "301" {
		t.Fatalf("a playlist view carries its tracks like an album view: %+v %v", ts, ok)
	}
	if sel := b.List().Selected(); sel == nil || sel.Track == nil || sel.Track.ID != "301" {
		t.Fatalf("first track should be selected: %+v", sel)
	}
}

func TestEmptyPlaylistOpensToItsHeaderOnly(t *testing.T) {
	b := NewBrowser(stubLib{})
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	_ = b.OpenPlaylists(ctx)
	if err := b.OpenPlaylist(ctx, *b.List().Rows[1].Playlist); err != nil {
		t.Fatal(err)
	}
	rows := b.List().Rows
	if len(rows) != 1 || !rows[0].Header {
		t.Fatalf("rows = %+v", rows)
	}
	if ts, ok := b.AlbumContext(); !ok || len(ts) != 0 {
		t.Fatalf("empty context must still be an (empty) album context: %v %v", ts, ok)
	}
}

type noPlaylists struct{ stubLib }

func (noPlaylists) Playlists(context.Context) ([]library.Playlist, error) { return nil, nil }

func TestNoPlaylistsShowsOneDimRow(t *testing.T) {
	b := NewBrowser(noPlaylists{})
	_ = b.LoadRoot(context.Background())
	_ = b.OpenPlaylists(context.Background())
	rows := b.List().Rows
	if len(rows) != 1 || !rows[0].Header || rows[0].Text != "No playlists" {
		t.Fatalf("rows = %+v", rows)
	}
}

type taggedLib struct{ stubLib }

func (taggedLib) Playlists(context.Context) ([]library.Playlist, error) {
	return []library.Playlist{{ID: "A:p1", Name: "Road Trip", TrackCount: 3, Server: "A"}, {ID: "B:p7", Name: "Theirs", TrackCount: 9, Server: "B"}}, nil
}

func TestPlaylistsGroupByServerWithHeaders(t *testing.T) {
	b := NewBrowser(taggedLib{})
	b.SetSwitcher(twoServers())
	_ = b.LoadRoot(context.Background())
	_ = b.OpenPlaylists(context.Background())
	rows := b.List().Rows
	want := []string{"Ressikan", "Road Trip", "Friend (relay)", "Theirs"}
	if len(rows) != 4 {
		t.Fatalf("rows = %+v", rows)
	}
	for i, w := range want {
		if rows[i].Text != w || rows[i].Header != (i%2 == 0) {
			t.Fatalf("row %d = %+v, want %q", i, rows[i], w)
		}
	}
	if sel := b.List().Selected(); sel == nil || sel.Header || sel.Text != "Road Trip" {
		t.Fatalf("the first playlist, not a header, should be selected: %+v", sel)
	}
}

func TestReloadPlaylistsOnlyWhenOnTop(t *testing.T) {
	b := NewBrowser(stubLib{})
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	_ = b.OpenPlaylists(ctx)
	b.List().Sel = 1
	b.ReloadPlaylists(ctx)
	if b.Title() != "Playlists" || b.List().Sel != 1 || len(b.stack) != 2 {
		t.Fatalf("reload should rebuild in place and keep the selection: %q sel=%d depth=%d", b.Title(), b.List().Sel, len(b.stack))
	}
	_ = b.OpenPlaylist(ctx, *b.List().Rows[0].Playlist)
	depth := len(b.stack)
	b.ReloadPlaylists(ctx)
	if len(b.stack) != depth || b.Title() != "Road Trip" {
		t.Fatal("reload must leave a deeper view alone")
	}
}

// countingLib counts AlbumTracks calls and answers with a FLAC track.
type countingLib struct {
	stubLib
	mu    sync.Mutex
	calls []string
}

func (c *countingLib) AlbumTracks(_ context.Context, id string) ([]library.Track, error) {
	c.mu.Lock()
	c.calls = append(c.calls, id)
	c.mu.Unlock()
	if id == "2" {
		return []library.Track{{ID: "t2", Codec: "mp3", Bitrate: 320}}, nil
	}
	return []library.Track{{ID: "t1", Codec: "flac", SampleRate: 44100, BitDepth: 16}}, nil
}

func (c *countingLib) count() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.calls) }

func TestAlbumRowsGainAQualityTagOnce(t *testing.T) {
	lib := &countingLib{}
	b := NewBrowser(lib)
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	_ = b.LoadRecent(ctx) // header + Recent One (id 1) + Recent Two (id 2)
	if r := b.List().Rows[1].Right; strings.Contains(r, "FLAC") {
		t.Fatalf("tags are fetched lazily, not at load: %q", r)
	}
	b.Prepare(ctx, 10)
	deadline := time.Now().Add(3 * time.Second)
	for {
		b.Prepare(ctx, 10)
		r1, r2 := b.List().Rows[1].Right, b.List().Rows[2].Right
		if strings.Contains(r1, "FLAC 16/44.1") && strings.Contains(r2, "MP3 320k") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tags never arrived: %q %q", r1, r2)
		}
		time.Sleep(5 * time.Millisecond)
	}
	before := lib.count()
	b.Prepare(ctx, 10)
	b.Prepare(ctx, 10)
	time.Sleep(20 * time.Millisecond)
	if lib.count() != before || before != 2 {
		t.Fatalf("each album is asked once: %d then %d", before, lib.count())
	}
	// A later view of the same album reuses the cache.
	_ = b.Search(ctx, "nsync") // the stub's search album has id 200, unknown so far
	b.Prepare(ctx, 10)
	_ = b.Back(ctx)
	_ = b.LoadRecent(ctx)
	b.Prepare(ctx, 10)
	if r := b.List().Rows[1].Right; !strings.Contains(r, "FLAC 16/44.1") {
		t.Fatalf("cached tag should apply at once: %q", r)
	}
}

func TestSearchListsMatchingPlaylists(t *testing.T) {
	b := NewBrowser(stubLib{}) // playlists: "Road Trip" (3) and "Empty" (0)
	if err := b.Search(context.Background(), "trip"); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range b.List().Rows {
		if r.Playlist != nil {
			names = append(names, r.Text+"|"+r.Right)
		}
	}
	if len(names) != 1 || names[0] != "Road Trip|3" {
		t.Fatalf("playlist rows = %v", names)
	}
	rows := b.List().Rows
	if rows[len(rows)-2].Text != "Playlists" || !rows[len(rows)-2].Header {
		t.Fatalf("the Playlists group comes last: %+v", rows[len(rows)-3:])
	}
	_ = b.Search(context.Background(), "ROAD") // case does not matter
	found := false
	for _, r := range b.List().Rows {
		found = found || r.Playlist != nil
	}
	if !found {
		t.Fatal("matching is case-insensitive")
	}
}

func TestSearchGroupsPlaylistsByServer(t *testing.T) {
	b := NewBrowser(taggedLib{}) // A: Road Trip, B: Theirs
	b.SetSwitcher(twoServers())
	_ = b.Search(context.Background(), "t")
	var seq []string
	for _, r := range b.List().Rows {
		if r.Header && strings.HasPrefix(r.Text, "Playlists") {
			seq = append(seq, r.Text)
		}
		if r.Playlist != nil {
			seq = append(seq, r.Playlist.Name)
		}
	}
	want := "Playlists — Ressikan,Road Trip,Playlists — Friend (relay),Theirs"
	if strings.Join(seq, ",") != want {
		t.Fatalf("got %q, want %q", strings.Join(seq, ","), want)
	}
}

func TestHomeReturnsToTheRootMenu(t *testing.T) {
	b := NewBrowser(stubLib{})
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	_ = b.Search(ctx, "nsync")
	_ = b.OpenAlbum(ctx, *b.List().Selected().Album)
	_ = b.Search(ctx, "again")
	if !b.Home(ctx) || b.Title() != "Library" || len(b.stack) != 1 {
		t.Fatalf("home should drop every view: %q depth=%d", b.Title(), len(b.stack))
	}
	if b.Home(ctx) {
		t.Fatal("home at the root reports nothing to do")
	}
}

func TestPickerListsSameServerPlaylistsOnly(t *testing.T) {
	b := NewBrowser(taggedLib{}) // A: Road Trip (A:p1), B: Theirs (B:p7)
	b.SetSwitcher(twoServers())
	_ = b.LoadRoot(context.Background())
	err := b.OpenPicker(context.Background(), []library.Track{{ID: "B:9", Server: "B"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	rows := b.List().Rows
	if b.Title() != "Add to playlist" || len(rows) != 1 || rows[0].Playlist == nil || rows[0].Playlist.Name != "Theirs" {
		t.Fatalf("picker rows = %+v", rows)
	}
	p, ok := b.InPicker()
	if !ok || len(p.tracks) != 1 || p.server != "B" {
		t.Fatalf("picker state = %+v %v", p, ok)
	}
	b.CancelPicker()
	if _, ok := b.InPicker(); ok || b.Title() != "Library" {
		t.Fatal("cancel pops the picker")
	}
	// excluding the source playlist leaves nothing on B
	if err := b.OpenPicker(context.Background(), []library.Track{{ID: "B:9", Server: "B"}}, "B:p7"); err != errNoPlaylists {
		t.Fatalf("err = %v", err)
	}
	if _, ok := b.InPicker(); ok {
		t.Fatal("no candidates means no picker")
	}
}
