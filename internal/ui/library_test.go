package ui

import (
	"context"
	"testing"

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
	return []library.Track{{ID: "300", Title: "Bye Bye Bye", AlbumID: id}}, nil
}
func (stubLib) ArtistAlbums(context.Context, string) ([]library.Album, error) { return nil, nil }
func (stubLib) Stream(context.Context, library.Track) (library.Stream, error) {
	return library.Stream{}, nil
}

func TestBrowserRecentSearchOpenBack(t *testing.T) {
	b := NewBrowser(stubLib{})
	ctx := context.Background()
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
