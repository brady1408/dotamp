package ui

import (
	"context"
	"fmt"

	"github.com/brady1408/dotamp/internal/library"
)

// Browser is the Library tab: a stack of views, each a title and a row set.
type Browser struct {
	lib   library.Library
	stack []view
}

type view struct {
	title string
	list  List
}

func NewBrowser(lib library.Library) *Browser { return &Browser{lib: lib} }

func (b *Browser) List() *List {
	if len(b.stack) == 0 {
		return &List{}
	}
	return &b.stack[len(b.stack)-1].list
}

func (b *Browser) Title() string {
	if len(b.stack) == 0 {
		return ""
	}
	return b.stack[len(b.stack)-1].title
}

func (b *Browser) push(title string, rows []Row) {
	var l List
	l.SetRows(rows)
	b.stack = append(b.stack, view{title: title, list: l})
}

func (b *Browser) Back(context.Context) bool {
	if len(b.stack) <= 1 {
		return false
	}
	b.stack = b.stack[:len(b.stack)-1]
	return true
}

func (b *Browser) LoadRecent(ctx context.Context) error {
	albums, err := b.lib.RecentAlbums(ctx, 0, 100)
	if err != nil {
		return err
	}
	b.stack = nil
	b.push("Recently added", albumRows("Albums", albums))
	return nil
}

func (b *Browser) Search(ctx context.Context, q string) error {
	res, err := b.lib.Search(ctx, q)
	if err != nil {
		return err
	}
	rows := []Row{{Text: "Artists", Header: true}}
	for i := range res.Artists {
		a := &res.Artists[i]
		rows = append(rows, Row{Text: a.Name, Artist: a})
	}
	rows = append(rows, albumRows("Albums", res.Albums)...)
	rows = append(rows, Row{Text: "Tracks", Header: true})
	for i := range res.Tracks {
		t := &res.Tracks[i]
		rows = append(rows, Row{Text: t.Artist + " — " + t.Title, Right: Clock(t.Duration), Track: t})
	}
	b.push("Search: "+q, rows)
	return nil
}

func (b *Browser) OpenAlbum(ctx context.Context, a library.Album) error {
	ts, err := b.lib.AlbumTracks(ctx, a.ID)
	if err != nil {
		return err
	}
	rows := []Row{{Text: fmt.Sprintf("%s — %s", a.Artist, a.Title), Header: true}}
	for i := range ts {
		t := &ts[i]
		rows = append(rows, Row{Text: fmt.Sprintf("%2d. %s", t.Index, t.Title), Right: Clock(t.Duration), Track: t})
	}
	b.push(a.Title, rows)
	return nil
}

func (b *Browser) OpenArtist(ctx context.Context, a library.Artist) error {
	albums, err := b.lib.ArtistAlbums(ctx, a.ID)
	if err != nil {
		return err
	}
	b.push(a.Name, albumRows(a.Name, albums))
	return nil
}

// AlbumTracks is what Enter on an album plays: the same call the view uses.
func (b *Browser) AlbumTracks(ctx context.Context, a library.Album) ([]library.Track, error) {
	return b.lib.AlbumTracks(ctx, a.ID)
}

func albumRows(header string, albums []library.Album) []Row {
	rows := []Row{{Text: header, Header: true}}
	for i := range albums {
		a := &albums[i]
		right := ""
		if a.Year > 0 {
			right = fmt.Sprint(a.Year)
		}
		text := a.Title
		if a.Artist != "" {
			text = a.Artist + " — " + a.Title
		}
		rows = append(rows, Row{Text: text, Right: right, Album: a})
	}
	return rows
}
