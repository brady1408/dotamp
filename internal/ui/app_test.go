package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

type nullPlayer struct{}

func (nullPlayer) Play()             {}
func (nullPlayer) Pause()            {}
func (nullPlayer) BufferedSize() int { return 0 }
func (nullPlayer) Close() error      { return nil }

type nullOutput struct{}

func (nullOutput) NewPlayer(io.Reader) audio.Player { return nullPlayer{} }

type appLib struct {
	stubLib
	url    string
	played []string
}

func (l *appLib) Stream(_ context.Context, t library.Track) (library.Stream, error) {
	l.played = append(l.played, t.ID)
	return library.Stream{URL: l.url + "/a.flac", Codec: "flac"}, nil
}

func newApp(t *testing.T) (*App, tcell.SimulationScreen, *appLib) {
	t.Helper()
	data, err := os.ReadFile("../audio/testdata/sine440-44k.flac")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "a.flac", time.Time{}, strings.NewReader(string(data)))
	}))
	t.Cleanup(srv.Close)
	lib := &appLib{url: srv.URL}
	eng := audio.NewEngine(nullOutput{}, audio.OutRate)
	t.Cleanup(eng.Close)
	ctrl := audio.NewController(lib, eng, func(string) {})
	s := sim(t, 80, 24)
	app := New(s, ctrl, eng, lib, func(float64) {})
	return app, s, lib
}

func key(a *App, k tcell.Key, r rune) bool { return a.Handle(tcell.NewEventKey(k, r, 0)) }

func TestAppDrawsAllRegionsAndQuits(t *testing.T) {
	app, s, _ := newApp(t)
	app.Draw()
	r := rows(s)
	if !strings.Contains(r[0], "0:00 / 0:00") {
		t.Fatalf("deck row = %q", r[0])
	}
	if !strings.Contains(r[10], "Queue") || !strings.Contains(r[10], "Library") {
		t.Fatalf("tab row = %q", r[10])
	}
	if !key(app, tcell.KeyRune, 'q') {
		t.Fatal("q must quit")
	}
}

func TestAppSearchAndPlayAlbum(t *testing.T) {
	app, s, lib := newApp(t)
	key(app, tcell.KeyTab, 0) // Library tab
	key(app, tcell.KeyRune, '/')
	for _, r := range "nsync" {
		key(app, tcell.KeyRune, r)
	}
	key(app, tcell.KeyEnter, 0)
	app.Draw()
	if r := rows(s); !strings.Contains(strings.Join(r, "\n"), "Search: nsync") {
		t.Fatalf("search title missing:\n%s", strings.Join(r, "\n"))
	}
	key(app, tcell.KeyEnter, 0) // selected = the album -> open it
	key(app, tcell.KeyEnter, 0) // first track -> play the album from the top
	if len(lib.played) != 1 || lib.played[0] != "300" {
		t.Fatalf("played = %v", lib.played)
	}
	cur, ok := app.ctrl.Current()
	if !ok || cur.ID != "300" {
		t.Fatalf("current = %+v", cur)
	}
	app.Draw()
	if r := rows(s); !strings.Contains(r[0], "Bye Bye Bye") {
		t.Fatalf("deck should show the track: %q", r[0])
	}
}

func TestAppHelpOverlayAndEscape(t *testing.T) {
	app, s, _ := newApp(t)
	key(app, tcell.KeyRune, '?')
	app.Draw()
	if r := rows(s); !strings.Contains(strings.Join(r, "\n"), "space") {
		t.Fatal("help overlay should list keys")
	}
	key(app, tcell.KeyEscape, 0)
	app.Draw()
	if r := rows(s); strings.Contains(strings.Join(r, "\n"), "play / pause") {
		t.Fatal("help should close on escape")
	}
}

func TestAppNoticeShowsThenClears(t *testing.T) {
	app, s, _ := newApp(t)
	app.Notify("Skipped B: boom")
	app.Handle(app.s.PollEvent()) // the posted notice
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Skipped B") {
		t.Fatalf("row1 = %q", r[1])
	}
	app.noticeUntil = time.Now().Add(-time.Second)
	app.Draw()
	if r := rows(s); strings.Contains(r[1], "Skipped B") {
		t.Fatal("notice should expire")
	}
}

func TestAppMouseSeekAndListClick(t *testing.T) {
	app, _, _ := newApp(t)
	_ = app.ctrl.PlayTracks(context.Background(), []library.Track{{ID: "300", Title: "x", Duration: 100 * time.Second}}, 0)
	time.Sleep(30 * time.Millisecond)
	app.Draw()                                               // computes the layout the mouse handler uses
	app.Handle(tcell.NewEventMouse(40, 2, tcell.Button1, 0)) // halfway along the seek bar
	// The fixture is 2 s long, so halfway along the bar is ~1 s.
	if p := app.eng.Position(); p < 900*time.Millisecond || p > 1200*time.Millisecond {
		t.Fatalf("position after click = %v", p)
	}
}

func TestAppPlaysAPlaylistFromItsSecondTrackAndAppendsOne(t *testing.T) {
	app, s, lib := newApp(t)
	key(app, tcell.KeyTab, 0) // Library
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyDown, 0) // Playlists is the third root row
	key(app, tcell.KeyEnter, 0)
	app.Draw()
	if r := strings.Join(rows(s), "\n"); !strings.Contains(r, "Road Trip") {
		t.Fatalf("playlist list missing:\n%s", r)
	}
	key(app, tcell.KeyRune, 'a') // append the whole playlist
	if got := app.ctrl.Tracks(); len(got) != 2 || got[0].ID != "301" {
		t.Fatalf("queue after a = %+v", got)
	}
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Added Road Trip (2)") {
		t.Fatalf("notice row = %q", r[1])
	}
	key(app, tcell.KeyEnter, 0) // open it
	key(app, tcell.KeyDown, 0)  // second track
	key(app, tcell.KeyEnter, 0) // play the playlist from there
	cur, ok := app.ctrl.Current()
	if !ok || cur.ID != "300" || len(lib.played) != 1 {
		t.Fatalf("current=%+v played=%v", cur, lib.played)
	}
	if got := app.ctrl.Tracks(); len(got) != 2 || got[0].ID != "301" || got[1].ID != "300" {
		t.Fatalf("playing a playlist replaces the queue with it in order: %+v", got)
	}
}

type fakeSaver struct {
	name  string
	got   []library.Track
	saved []library.Saved
	err   error
}

func (f *fakeSaver) SaveQueue(_ context.Context, name string, ts []library.Track) ([]library.Saved, error) {
	f.name, f.got = name, ts
	return f.saved, f.err
}

func typeKeys(a *App, s string) {
	for _, r := range s {
		key(a, tcell.KeyRune, r)
	}
}

func TestSaveQueuePromptsAndReportsEachServer(t *testing.T) {
	app, s, _ := newApp(t)
	sv := &fakeSaver{saved: []library.Saved{{Server: "A", Tracks: 2}, {Server: "B", Tracks: 1}}}
	app.SetSaver(sv)
	app.ctrl.Enqueue(library.Track{ID: "A:1", Server: "A"}, library.Track{ID: "B:1", Server: "B"}, library.Track{ID: "A:2", Server: "A"})
	key(app, tcell.KeyRune, 'w')
	app.Draw()
	if r := strings.Join(rows(s), "\n"); !strings.Contains(r, "Save queue as:") {
		t.Fatalf("prompt missing:\n%s", r)
	}
	typeKeys(app, "  Road Trip ")
	key(app, tcell.KeyEnter, 0)
	if sv.name != "Road Trip" || len(sv.got) != 3 || sv.got[1].ID != "B:1" {
		t.Fatalf("saver got name=%q tracks=%+v", sv.name, sv.got)
	}
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Saved Road Trip to A (2) and B (1)") {
		t.Fatalf("notice = %q", r[1])
	}
	if app.search.Open {
		t.Fatal("prompt must close after saving")
	}
}

func TestSaveQueueEmptyBlankAndEscape(t *testing.T) {
	app, s, _ := newApp(t)
	sv := &fakeSaver{}
	app.SetSaver(sv)
	key(app, tcell.KeyRune, 'w')
	app.Draw()
	if app.search.Open || !strings.Contains(rows(s)[1], "Queue is empty") {
		t.Fatalf("empty queue: open=%v row1=%q", app.search.Open, rows(s)[1])
	}
	app.ctrl.Enqueue(library.Track{ID: "A:1", Server: "A"})
	key(app, tcell.KeyRune, 'w')
	key(app, tcell.KeyEnter, 0) // blank name
	app.Draw()
	if sv.got != nil || !strings.Contains(rows(s)[1], "No name, not saved") {
		t.Fatalf("blank name: got=%v row1=%q", sv.got, rows(s)[1])
	}
	key(app, tcell.KeyRune, 'w')
	typeKeys(app, "x")
	key(app, tcell.KeyEscape, 0)
	if sv.got != nil || app.search.Open || app.search.Text != "" {
		t.Fatal("escape cancels without saving and clears the field")
	}
}

func TestSaveQueuePartialAndTotalFailure(t *testing.T) {
	app, s, _ := newApp(t)
	sv := &fakeSaver{saved: []library.Saved{{Server: "B", Tracks: 1}}, err: errors.New("Mine failed: down")}
	app.SetSaver(sv)
	app.ctrl.Enqueue(library.Track{ID: "A:1", Server: "A"}, library.Track{ID: "B:1", Server: "B"})
	key(app, tcell.KeyRune, 'w')
	typeKeys(app, "Mix")
	key(app, tcell.KeyEnter, 0)
	app.Draw()
	if r := rows(s)[1]; !strings.Contains(r, "Saved Mix to B (1); Mine failed: down") {
		t.Fatalf("partial notice = %q", r)
	}
	sv.saved, sv.err = nil, errors.New("Mine failed: down")
	key(app, tcell.KeyRune, 'w')
	typeKeys(app, "Mix")
	key(app, tcell.KeyEnter, 0)
	app.Draw()
	if r := rows(s)[1]; !strings.Contains(r, "Save failed: Mine failed: down") {
		t.Fatalf("total notice = %q", r)
	}
}

func TestSaveUsesServerNamesAndReloadsThePlaylistsView(t *testing.T) {
	app, s, _ := newApp(t)
	app.SetSwitcher(twoServers())
	sv := &fakeSaver{saved: []library.Saved{{Server: "A", Tracks: 1}}}
	app.SetSaver(sv)
	app.ctrl.Enqueue(library.Track{ID: "A:1", Server: "A"})
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyEnter, 0) // Playlists view open
	key(app, tcell.KeyRune, 'w')
	typeKeys(app, "Mix")
	key(app, tcell.KeyEnter, 0)
	app.Draw()
	if r := rows(s)[1]; !strings.Contains(r, "Saved Mix to Ressikan (1)") {
		t.Fatalf("notice = %q", r)
	}
	if app.browser.Title() != "Playlists" {
		t.Fatalf("view = %q", app.browser.Title())
	}
}

func TestSaveKeyWhileSearchingTypesIntoTheSearch(t *testing.T) {
	app, _, _ := newApp(t)
	app.SetSaver(&fakeSaver{})
	app.ctrl.Enqueue(library.Track{ID: "A:1", Server: "A"})
	key(app, tcell.KeyRune, '/')
	key(app, tcell.KeyRune, 'w')
	if app.search.Text != "w" || app.saving {
		t.Fatalf("w inside the search box is text: %q saving=%v", app.search.Text, app.saving)
	}
}

func TestSaveNoticeWording(t *testing.T) {
	name := func(id string) string { return map[string]string{"A": "Mine", "B": "Friend"}[id] }
	cases := []struct {
		saved []library.Saved
		err   error
		want  string
	}{
		{[]library.Saved{{Server: "A", Tracks: 14}}, nil, "Saved Road Trip to Mine (14)"},
		{[]library.Saved{{Server: "A", Tracks: 14}, {Server: "B", Tracks: 3}}, nil, "Saved Road Trip to Mine (14) and Friend (3)"},
		{[]library.Saved{{Server: "B", Tracks: 3}}, errors.New("Mine failed: down"), "Saved Road Trip to Friend (3); Mine failed: down"},
		{nil, errors.New("Mine failed: down"), "Save failed: Mine failed: down"},
	}
	for _, c := range cases {
		if got := saveNotice("Road Trip", c.saved, c.err, name); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

type repeatLib struct{ stubLib }

func (repeatLib) PlaylistTracks(context.Context, string) ([]library.Track, error) {
	return []library.Track{{ID: "300", Title: "Bye Bye Bye"}, {ID: "301", Title: "It's Gonna Be Me"}, {ID: "300", Title: "Bye Bye Bye"}}, nil
}

func TestEnterOnARepeatedPlaylistTrackStartsAtThatCopy(t *testing.T) {
	app, _, _ := newApp(t)
	app.lib = repeatLib{}
	app.browser = NewBrowser(repeatLib{})
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyEnter, 0) // Playlists
	key(app, tcell.KeyEnter, 0) // open the first playlist
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyDown, 0) // the second copy of 300, at position 3
	key(app, tcell.KeyEnter, 0)
	if i := app.ctrl.Index(); i != 2 {
		t.Fatalf("playback should start at the row pressed, index 2, got %d", i)
	}
}

func TestHelpLinesFitTheOverlay(t *testing.T) {
	for _, l := range helpLines {
		if n := len([]rune("  " + l)); n > helpWidth {
			t.Errorf("help line %q is %d columns, the overlay is %d", l, n, helpWidth)
		}
	}
}

func TestClearKeyEmptiesTheQueueAndSaysSo(t *testing.T) {
	app, s, _ := newApp(t)
	_ = app.ctrl.PlayTracks(context.Background(), []library.Track{{ID: "300", Title: "x"}, {ID: "301", Title: "y"}}, 0)
	key(app, tcell.KeyRune, 'c')
	app.Draw()
	if n := len(app.ctrl.Tracks()); n != 0 {
		t.Fatalf("queue should be empty, has %d", n)
	}
	if r := rows(s); !strings.Contains(r[1], "Queue cleared") || !strings.Contains(r[0], "0:00 / 0:00") {
		t.Fatalf("deck rows = %q / %q", r[0], r[1])
	}
}

func TestRemoveKeyDropsTheSelectedQueueRowOnly(t *testing.T) {
	app, _, _ := newApp(t)
	_ = app.ctrl.PlayTracks(context.Background(), []library.Track{{ID: "300", Title: "x"}, {ID: "301", Title: "y"}, {ID: "302", Title: "z"}}, 0)
	app.Draw()                 // the queue list is built on draw
	key(app, tcell.KeyDown, 0) // select y
	key(app, tcell.KeyRune, 'x')
	if got := app.ctrl.Tracks(); len(got) != 2 || got[1].ID != "302" {
		t.Fatalf("after x: %+v", got)
	}
	if cur, _ := app.ctrl.Current(); cur.ID != "300" {
		t.Fatalf("playback must stay on x: %+v", cur)
	}
	key(app, tcell.KeyTab, 0) // Library
	key(app, tcell.KeyRune, 'x')
	if got := app.ctrl.Tracks(); len(got) != 2 {
		t.Fatalf("x in the Library must not touch the queue: %+v", got)
	}
}

func TestEscapeInTheLibraryGoesHome(t *testing.T) {
	app, _, _ := newApp(t)
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyRune, '/')
	typeKeys(app, "nsync")
	key(app, tcell.KeyEnter, 0)
	key(app, tcell.KeyEnter, 0) // open the album
	if app.browser.Title() == "Library" {
		t.Fatal("precondition: a deeper view is open")
	}
	key(app, tcell.KeyEscape, 0)
	if app.browser.Title() != "Library" {
		t.Fatalf("escape should return to the root menu, got %q", app.browser.Title())
	}
}

type editLib struct {
	stubLib
	mu    sync.Mutex
	added []string
}

func (l *editLib) AddToPlaylist(_ context.Context, id string, ts []library.Track) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, t := range ts {
		l.added = append(l.added, id+"<-"+t.ID)
	}
	return nil
}

func newEditApp(t *testing.T) (*App, tcell.SimulationScreen, *editLib) {
	t.Helper()
	app, s, _ := newApp(t)
	lib := &editLib{}
	app.lib = lib
	app.browser = NewBrowser(lib)
	_ = app.browser.LoadRoot(context.Background())
	return app, s, lib
}

func TestToPlaylistFromASearchTrackAndAnAlbum(t *testing.T) {
	app, s, lib := newEditApp(t)
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyRune, '/')
	typeKeys(app, "nsync")
	key(app, tcell.KeyEnter, 0) // results: album 200 selected
	key(app, tcell.KeyRune, 't')
	app.Draw()
	if r := strings.Join(rows(s), "\n"); !strings.Contains(r, "Add to playlist") || !strings.Contains(r, "Road Trip") {
		t.Fatalf("picker missing:\n%s", r)
	}
	key(app, tcell.KeyEnter, 0) // Road Trip (p1)
	if got := strings.Join(lib.added, " "); got != "p1<-300 p1<-301" {
		t.Fatalf("added = %q", got)
	}
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Added 2 to Road Trip") || app.browser.Title() != "Search: nsync" {
		t.Fatalf("notice=%q title=%q", r[1], app.browser.Title())
	}
	key(app, tcell.KeyEscape, 0) // home
	key(app, tcell.KeyRune, 't') // on the root menu: nothing
	if app.browser.Title() != "Library" {
		t.Fatal("t on a menu row must do nothing")
	}
}

func TestToPlaylistFromTheQueueUsesTheTrackServer(t *testing.T) {
	app, s, lib := newEditApp(t)
	app.ctrl.Enqueue(library.Track{ID: "300", Title: "x"})
	app.Draw()
	key(app, tcell.KeyRune, 't')
	if app.tab != tabLibrary || app.browser.Title() != "Add to playlist" {
		t.Fatalf("t in the queue opens the picker in the Library: tab=%d title=%q", app.tab, app.browser.Title())
	}
	key(app, tcell.KeyBackspace, 0)
	if app.browser.Title() != "Library" || len(lib.added) != 0 {
		t.Fatal("backspace cancels without adding")
	}
	app.tab = tabQueue
	key(app, tcell.KeyRune, 't')
	key(app, tcell.KeyDown, 0) // Empty (p2)
	key(app, tcell.KeyEnter, 0)
	app.Draw()
	if got := strings.Join(lib.added, " "); got != "p2<-300" {
		t.Fatalf("added = %q", got)
	}
	if r := rows(s); !strings.Contains(r[1], "Added 1 to Empty") {
		t.Fatalf("notice = %q", r[1])
	}
}

type emptyAlbumLib struct{ stubLib }

func (emptyAlbumLib) AlbumTracks(context.Context, string) ([]library.Track, error) { return nil, nil }

func TestToPlaylistWithNothingToAdd(t *testing.T) {
	app, s, _ := newApp(t)
	app.browser = NewBrowser(emptyAlbumLib{})
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyRune, '/')
	typeKeys(app, "nsync")
	key(app, tcell.KeyEnter, 0)
	key(app, tcell.KeyRune, 't')
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Nothing to add") || app.browser.Title() != "Search: nsync" {
		t.Fatalf("notice=%q title=%q", r[1], app.browser.Title())
	}
}

func TestToPlaylistWithNoPlaylistsNamesTheServer(t *testing.T) {
	app, s, _ := newApp(t)
	app.browser = NewBrowser(noPlaylists{})
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyRune, '/')
	typeKeys(app, "nsync")
	key(app, tcell.KeyEnter, 0)
	key(app, tcell.KeyRune, 't')
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "No playlists on") || app.browser.Title() != "Search: nsync" {
		t.Fatalf("notice=%q title=%q", r[1], app.browser.Title())
	}
}

type orderLib struct {
	stubLib
	mu      sync.Mutex
	order   []string // the playlist's entries, mutated by remove and move
	failRem bool
	calls   []string
}

func (l *orderLib) PlaylistTracks(context.Context, string) ([]library.Track, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := make([]library.Track, len(l.order))
	for i, id := range l.order {
		ts[i] = library.Track{ID: id, Title: "T" + id}
	}
	return ts, nil
}
func (l *orderLib) RemoveFromPlaylist(_ context.Context, id string, i int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("remove:%s:%d", id, i))
	if l.failRem {
		return errors.New("gone")
	}
	l.order = append(l.order[:i], l.order[i+1:]...)
	return nil
}
func (l *orderLib) MovePlaylistTrack(_ context.Context, id string, from, to int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("move:%s:%d:%d", id, from, to))
	x := l.order[from]
	rest := append(append([]string(nil), l.order[:from]...), l.order[from+1:]...)
	l.order = append(append(append([]string(nil), rest[:to]...), x), rest[to:]...)
	return nil
}

func openPlaylistApp(t *testing.T, lib *orderLib) (*App, tcell.SimulationScreen) {
	t.Helper()
	app, s, _ := newApp(t)
	app.lib = lib
	app.browser = NewBrowser(lib)
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyEnter, 0) // Playlists
	key(app, tcell.KeyEnter, 0) // Road Trip
	return app, s
}

func TestPlaylistViewRemoveAndMoveFollowTheCursor(t *testing.T) {
	lib := &orderLib{order: []string{"a", "b", "c"}}
	app, s := openPlaylistApp(t, lib)
	key(app, tcell.KeyDown, 0) // b
	key(app, tcell.KeyRune, ']')
	if strings.Join(lib.order, "") != "acb" || app.browser.List().Selected().Track.ID != "b" {
		t.Fatalf("down: order=%v sel=%+v", lib.order, app.browser.List().Selected())
	}
	key(app, tcell.KeyRune, ']') // b is last: nothing
	key(app, tcell.KeyRune, '[')
	key(app, tcell.KeyRune, '[')
	key(app, tcell.KeyRune, '[') // b is first: nothing
	if strings.Join(lib.order, "") != "bac" || app.browser.List().Selected().Track.ID != "b" {
		t.Fatalf("up: order=%v sel=%+v", lib.order, app.browser.List().Selected())
	}
	if n := len(lib.calls); n != 3 {
		t.Fatalf("moves at the ends must send nothing: %v", lib.calls)
	}
	key(app, tcell.KeyRune, 'x') // remove b
	app.Draw()
	if strings.Join(lib.order, "") != "ac" || app.browser.List().Selected().Track.ID != "a" {
		t.Fatalf("remove: order=%v sel=%+v", lib.order, app.browser.List().Selected())
	}
	if r := rows(s); !strings.Contains(r[1], "Removed Tb from Road Trip") {
		t.Fatalf("notice = %q", r[1])
	}
	if app.ctrl.Tracks() != nil {
		t.Fatal("the queue is never touched")
	}
	if !strings.Contains(strings.Join(rows(s), "\n"), "2 tracks") {
		t.Fatal("header count should refresh")
	}
}

func TestPlaylistViewRemoveFailureRefreshes(t *testing.T) {
	lib := &orderLib{order: []string{"a", "b"}, failRem: true}
	app, s := openPlaylistApp(t, lib)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyRune, 'x') // the server refuses
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Remove failed: gone") {
		t.Fatalf("notice = %q", r[1])
	}
	if n := len(app.browser.List().Rows); n != 3 { // header + a + b, re-fetched
		t.Fatalf("the view should re-fetch after a failure: %d rows", n)
	}
	lib.mu.Lock()
	lib.order = []string{"a"} // edited elsewhere: b is gone
	lib.mu.Unlock()
	key(app, tcell.KeyRune, 'x')
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Playlist changed on the server") {
		t.Fatalf("a stale position refreshes instead of writing: %q", r[1])
	}
	if n := len(app.browser.List().Rows); n != 2 || len(lib.calls) != 1 {
		t.Fatalf("rows=%d calls=%v", n, lib.calls)
	}
}

func TestEditKeysAreInertOutsideAPlaylist(t *testing.T) {
	lib := &orderLib{order: []string{"a"}}
	app, _, _ := newApp(t)
	app.lib = lib
	app.browser = NewBrowser(lib)
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyRune, '/')
	typeKeys(app, "nsync")
	key(app, tcell.KeyEnter, 0)
	key(app, tcell.KeyEnter, 0) // album view
	key(app, tcell.KeyRune, 'x')
	key(app, tcell.KeyRune, '[')
	key(app, tcell.KeyRune, ']')
	if len(lib.calls) != 0 {
		t.Fatalf("no server calls from an album view: %v", lib.calls)
	}
}

func TestPlaylistViewRefusesToEditAShiftedPlaylist(t *testing.T) {
	lib := &orderLib{order: []string{"a", "b", "c"}}
	app, s := openPlaylistApp(t, lib)
	key(app, tcell.KeyDown, 0) // b at position 1
	lib.mu.Lock()
	lib.order = []string{"z", "a", "b", "c"} // someone inserted above it elsewhere
	lib.mu.Unlock()
	key(app, tcell.KeyRune, 'x')
	app.Draw()
	if len(lib.calls) != 0 {
		t.Fatalf("no write may happen when position 1 is no longer b: %v", lib.calls)
	}
	if r := rows(s); !strings.Contains(r[1], "Playlist changed on the server") {
		t.Fatalf("notice = %q", r[1])
	}
	if n := len(app.browser.List().Rows); n != 5 { // header + z a b c
		t.Fatalf("the view should show the server's list now: %d rows", n)
	}
	key(app, tcell.KeyRune, ']') // the same guard protects moves
	if len(lib.calls) != 1 || !strings.HasPrefix(lib.calls[0], "move:") {
		t.Fatalf("a move after the refresh is on the fresh list: %v", lib.calls)
	}
}

type flakyRefreshLib struct {
	orderLib
	failAfter int // PlaylistTracks calls allowed before it starts failing
	n         int
}

func (l *flakyRefreshLib) PlaylistTracks(ctx context.Context, id string) ([]library.Track, error) {
	l.n++
	if l.n > l.failAfter {
		return nil, errors.New("server went away")
	}
	return l.orderLib.PlaylistTracks(ctx, id)
}

func TestFailedRefreshKeepsTheViewAndTheNotice(t *testing.T) {
	lib := &flakyRefreshLib{orderLib: orderLib{order: []string{"a", "b"}}}
	lib.failAfter = 2 // open (1) and the pre-write check (2) succeed; the refresh fails
	app, s, _ := newApp(t)
	app.lib = lib
	app.browser = NewBrowser(lib)
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyEnter, 0) // Playlists
	key(app, tcell.KeyEnter, 0) // Road Trip
	key(app, tcell.KeyRune, 'x')
	app.Draw()
	if app.browser.Title() != "Road Trip" {
		t.Fatalf("a failed refresh must keep the view, got %q", app.browser.Title())
	}
	if r := rows(s); !strings.Contains(r[1], "Removed Ta from Road Trip") {
		t.Fatalf("the operation's notice must survive the refresh failure: %q", r[1])
	}
}

type nameLib struct {
	stubLib
	mu    sync.Mutex
	calls []string
}

func (l *nameLib) RenamePlaylist(_ context.Context, id, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "rename:"+id+":"+name)
	return nil
}
func (l *nameLib) DeletePlaylist(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "delete:"+id)
	return nil
}

func playlistsApp(t *testing.T) (*App, tcell.SimulationScreen, *nameLib) {
	t.Helper()
	app, s, _ := newApp(t)
	lib := &nameLib{}
	app.lib = lib
	app.browser = NewBrowser(lib)
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyDown, 0)
	key(app, tcell.KeyEnter, 0) // Playlists: Road Trip selected
	return app, s, lib
}

func TestRenamePlaylistFromARowPrefillsAndApplies(t *testing.T) {
	app, s, lib := playlistsApp(t)
	key(app, tcell.KeyRune, 'e')
	app.Draw()
	if r := strings.Join(rows(s), "\n"); !strings.Contains(r, "Rename:") || !strings.Contains(r, "Road Trip▏") {
		t.Fatalf("prompt should be prefilled:\n%s", r)
	}
	typeKeys(app, " 2")
	key(app, tcell.KeyEnter, 0)
	app.Draw()
	if len(lib.calls) != 1 || lib.calls[0] != "rename:p1:Road Trip 2" {
		t.Fatalf("calls = %v", lib.calls)
	}
	if r := rows(s); !strings.Contains(r[1], "Renamed to Road Trip 2") {
		t.Fatalf("notice = %q", r[1])
	}
	// blank and escape both cancel without a call
	key(app, tcell.KeyRune, 'e')
	for app.search.Text != "" {
		key(app, tcell.KeyBackspace, 0)
	}
	key(app, tcell.KeyEnter, 0)
	key(app, tcell.KeyRune, 'e')
	key(app, tcell.KeyEscape, 0)
	if len(lib.calls) != 1 || app.search.Open {
		t.Fatalf("blank/escape must not rename: %v open=%v", lib.calls, app.search.Open)
	}
}

func TestRenameInsideAPlaylistUpdatesTheTitle(t *testing.T) {
	app, _, lib := playlistsApp(t)
	key(app, tcell.KeyEnter, 0) // open Road Trip
	key(app, tcell.KeyRune, 'e')
	typeKeys(app, "!")
	key(app, tcell.KeyEnter, 0)
	if app.browser.Title() != "Road Trip!" || len(lib.calls) != 1 {
		t.Fatalf("title=%q calls=%v", app.browser.Title(), lib.calls)
	}
}

func TestDeletePlaylistAsksFirst(t *testing.T) {
	app, s, lib := playlistsApp(t)
	key(app, tcell.KeyRune, 'd')
	app.Draw()
	if r := rows(s); !strings.Contains(r[1], "Delete Road Trip? y/n") {
		t.Fatalf("notice = %q", r[1])
	}
	key(app, tcell.KeyRune, 'n')
	app.Draw()
	if len(lib.calls) != 0 || !strings.Contains(rows(s)[1], "Not deleted") {
		t.Fatalf("n must cancel: %v %q", lib.calls, rows(s)[1])
	}
	key(app, tcell.KeyRune, 'd')
	key(app, tcell.KeyRune, 'y')
	app.Draw()
	if len(lib.calls) != 1 || lib.calls[0] != "delete:p1" || !strings.Contains(rows(s)[1], "Deleted Road Trip") {
		t.Fatalf("y must delete: %v %q", lib.calls, rows(s)[1])
	}
	if app.browser.Title() != "Playlists" {
		t.Fatalf("still on the list: %q", app.browser.Title())
	}
}

func TestDeleteInsideAPlaylistPopsBack(t *testing.T) {
	app, _, lib := playlistsApp(t)
	key(app, tcell.KeyEnter, 0) // open Road Trip
	key(app, tcell.KeyRune, 'd')
	key(app, tcell.KeyRune, 'y')
	if app.browser.Title() != "Playlists" || len(lib.calls) != 1 {
		t.Fatalf("title=%q calls=%v", app.browser.Title(), lib.calls)
	}
}

func TestRenameAndDeleteAreInertOffPlaylists(t *testing.T) {
	app, _, lib := playlistsApp(t)
	key(app, tcell.KeyEscape, 0) // root menu
	key(app, tcell.KeyRune, 'e')
	key(app, tcell.KeyRune, 'd')
	key(app, tcell.KeyRune, 'y')
	if len(lib.calls) != 0 || app.search.Open {
		t.Fatalf("nothing to act on: %v", lib.calls)
	}
}
