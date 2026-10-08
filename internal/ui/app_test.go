package ui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
