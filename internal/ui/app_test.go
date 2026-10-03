package ui

import (
	"context"
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
	eng := audio.NewEngine(nullOutput{})
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
