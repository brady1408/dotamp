package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/library"
)

// bigLib has 500 artists so the index must page; it records page requests.
type bigLib struct {
	stubLib
	pages []int
}

func (b *bigLib) Artists(_ context.Context, offset, limit int) ([]library.Artist, int, error) {
	b.pages = append(b.pages, offset)
	var out []library.Artist
	for i := offset; i < offset+limit && i < 500; i++ {
		out = append(out, library.Artist{ID: fmt.Sprint(i), Name: fmt.Sprintf("Artist %03d", i)})
	}
	return out, 500, nil
}

func (b *bigLib) ArtistIndex(context.Context) ([]library.Letter, error) {
	return []library.Letter{{Letter: "#", Count: 50}, {Letter: "A", Count: 200}, {Letter: "B", Count: 250}}, nil
}

func TestBrowserRootMenuOpensArtistsLazily(t *testing.T) {
	lib := &bigLib{}
	b := NewBrowser(lib)
	ctx := context.Background()
	if err := b.LoadRoot(ctx); err != nil {
		t.Fatal(err)
	}
	if b.Title() != "Library" || len(b.List().Rows) != 2 || b.List().Rows[0].Menu != MenuArtists {
		t.Fatalf("root = %q %+v", b.Title(), b.List().Rows)
	}
	if err := b.OpenArtists(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(b.List().Rows); n != 500 {
		t.Fatalf("artist rows = %d, want the full total so the scrollbar is honest", n)
	}
	if len(lib.pages) != 1 || lib.pages[0] != 0 {
		t.Fatalf("only the first page should load up front, got %v", lib.pages)
	}
	if r := b.List().Rows[499]; r.Artist != nil || r.Text == "" {
		t.Fatalf("unloaded row should be a placeholder: %+v", r)
	}
	b.List().Sel = 450
	b.Prepare(ctx, 20)
	if r := b.List().Rows[450]; r.Artist == nil || r.Artist.Name != "Artist 450" {
		t.Fatalf("row near the selection should load on Prepare: %+v", r)
	}
}

func TestBrowserJumpToLetter(t *testing.T) {
	lib := &bigLib{}
	b := NewBrowser(lib)
	ctx := context.Background()
	_ = b.LoadRoot(ctx)
	_ = b.OpenArtists(ctx)
	if !b.JumpLetter(ctx, 'B') {
		t.Fatal("jump should succeed in the artists view")
	}
	if b.List().Sel != 250 { // 50 under '#' + 200 under 'A'
		t.Fatalf("sel after jump = %d, want 250", b.List().Sel)
	}
	if r := b.List().Rows[250]; r.Artist == nil {
		t.Fatal("jump target must be loaded")
	}
	if b.JumpLetter(ctx, 'Q') {
		t.Fatal("a letter with no artists cannot be jumped to")
	}
	_ = b.LoadRoot(ctx)
	if b.JumpLetter(ctx, 'B') {
		t.Fatal("jump only applies in the artists view")
	}
}

func TestAppOpensAlbumAndPlaysFromTrack(t *testing.T) {
	app, s, lib := newApp(t)
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyRune, '/')
	for _, r := range "nsync" {
		key(app, tcell.KeyRune, r)
	}
	key(app, tcell.KeyEnter, 0)
	key(app, tcell.KeyEnter, 0) // Enter on the album opens it instead of playing
	if len(lib.played) != 0 {
		t.Fatalf("opening an album must not play it; played = %v", lib.played)
	}
	app.Draw()
	if r := strings.Join(rows(s), "\n"); !strings.Contains(r, "Bye Bye Bye") || !strings.Contains(r, "It's Gonna Be Me") {
		t.Fatalf("album view should list its tracks:\n%s", r)
	}
	key(app, tcell.KeyDown, 0)  // second track
	key(app, tcell.KeyEnter, 0) // play the album from here
	if len(lib.played) != 1 || lib.played[0] != "301" {
		t.Fatalf("played = %v, want the second track", lib.played)
	}
	if n, i := len(app.ctrl.Tracks()), app.ctrl.Index(); n != 2 || i != 1 {
		t.Fatalf("queue = %d tracks at index %d, want the album at track 2", n, i)
	}
	key(app, tcell.KeyBackspace2, 0)
	app.Draw()
	if r := rows(s); !strings.Contains(strings.Join(r, "\n"), "Search: nsync") {
		t.Fatal("backspace should return to the search results")
	}
}

func TestAppUppercaseJumpsInArtists(t *testing.T) {
	app, _, _ := newApp(t)
	app.browser = NewBrowser(&bigLib{})
	_ = app.browser.LoadRoot(context.Background())
	key(app, tcell.KeyTab, 0)
	key(app, tcell.KeyEnter, 0) // Artists is the first menu entry
	key(app, tcell.KeyRune, 'B')
	if sel := app.browser.List().Sel; sel != 250 {
		t.Fatalf("sel = %d after jumping to B", sel)
	}
	key(app, tcell.KeyRune, 'q') // lowercase actions still work: this would quit
}
