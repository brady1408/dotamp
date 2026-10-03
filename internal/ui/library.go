package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/brady1408/dotamp/internal/library"
)

const (
	MenuArtists = "artists"
	MenuRecent  = "recent"

	artistPage = 200 // artists fetched per request while scrolling the index
)

// Browser is the Library tab: a stack of views, each a title and a row set.
// The root is a small menu; Artists is an index of every artist that loads a
// page at a time as the selection moves.
type Browser struct {
	lib   library.Library
	stack []view
}

type view struct {
	title   string
	list    List
	tracks  []library.Track // set for an album view: Enter on a track plays these from there
	artists *artistIndex    // set for the Artists view
}

type artistIndex struct {
	total   int
	letters []library.Letter
	loaded  map[int]bool // page number -> fetched
}

func NewBrowser(lib library.Library) *Browser { return &Browser{lib: lib} }

func (b *Browser) top() *view {
	if len(b.stack) == 0 {
		return nil
	}
	return &b.stack[len(b.stack)-1]
}

func (b *Browser) List() *List {
	if v := b.top(); v != nil {
		return &v.list
	}
	return &List{}
}

func (b *Browser) Title() string {
	if v := b.top(); v != nil {
		return v.title
	}
	return ""
}

// AlbumContext returns the album's tracks when the current view is an album.
func (b *Browser) AlbumContext() ([]library.Track, bool) {
	if v := b.top(); v != nil && v.tracks != nil {
		return v.tracks, true
	}
	return nil, false
}

func (b *Browser) push(v view) {
	b.stack = append(b.stack, v)
}

func (b *Browser) Back(context.Context) bool {
	if len(b.stack) <= 1 {
		return false
	}
	b.stack = b.stack[:len(b.stack)-1]
	return true
}

// LoadRoot resets the stack to the root menu.
func (b *Browser) LoadRoot(context.Context) error {
	var l List
	l.SetRows([]Row{
		{Text: "Artists", Menu: MenuArtists},
		{Text: "Recently added", Menu: MenuRecent},
	})
	b.stack = []view{{title: "Library", list: l}}
	return nil
}

func (b *Browser) LoadRecent(ctx context.Context) error {
	albums, err := b.lib.RecentAlbums(ctx, 0, 100)
	if err != nil {
		return err
	}
	var l List
	l.SetRows(albumRows("Albums", albums))
	b.push(view{title: "Recently added", list: l})
	return nil
}

// OpenArtists opens the index: one row per artist, with only the first page
// fetched. Rows past it are placeholders until Prepare reaches them.
func (b *Browser) OpenArtists(ctx context.Context) error {
	artists, total, err := b.lib.Artists(ctx, 0, artistPage)
	if err != nil {
		return err
	}
	letters, err := b.lib.ArtistIndex(ctx)
	if err != nil {
		return err
	}
	rows := make([]Row, total)
	for i := range rows {
		rows[i] = Row{Text: "…"}
	}
	idx := &artistIndex{total: total, letters: letters, loaded: map[int]bool{0: true}}
	fill(rows, 0, artists)
	var l List
	l.SetRows(rows)
	b.push(view{title: fmt.Sprintf("Artists (%d)", total), list: l, artists: idx})
	return nil
}

func fill(rows []Row, offset int, artists []library.Artist) {
	for i := range artists {
		if offset+i < len(rows) {
			a := artists[i]
			rows[offset+i] = Row{Text: a.Name, Artist: &a}
		}
	}
}

// ensure fetches the page holding row i of the Artists view if it is missing.
func (b *Browser) ensure(ctx context.Context, v *view, i int) error {
	if i < 0 || i >= v.artists.total {
		return nil
	}
	page := i / artistPage
	if v.artists.loaded[page] {
		return nil
	}
	artists, _, err := b.lib.Artists(ctx, page*artistPage, artistPage)
	if err != nil {
		return err
	}
	v.artists.loaded[page] = true
	fill(v.list.Rows, page*artistPage, artists)
	return nil
}

// Prepare loads the rows around the selection before a draw of height h.
func (b *Browser) Prepare(ctx context.Context, h int) {
	v := b.top()
	if v == nil || v.artists == nil {
		return
	}
	for i := v.list.Sel - h; i <= v.list.Sel+h; i += artistPage {
		_ = b.ensure(ctx, v, i)
	}
	_ = b.ensure(ctx, v, v.list.Sel+h)
}

// JumpLetter moves the selection to the first artist under r in the Artists
// view. It reports whether a jump happened.
func (b *Browser) JumpLetter(ctx context.Context, r rune) bool {
	v := b.top()
	if v == nil || v.artists == nil {
		return false
	}
	want := strings.ToUpper(string(r))
	offset := 0
	for _, l := range v.artists.letters {
		if strings.ToUpper(l.Letter) == want {
			if l.Count == 0 || offset >= v.artists.total {
				return false
			}
			v.list.Sel = offset
			_ = b.ensure(ctx, v, offset)
			return true
		}
		offset += l.Count
	}
	return false
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
	var l List
	l.SetRows(rows)
	b.push(view{title: "Search: " + q, list: l})
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
	var l List
	l.SetRows(rows)
	if ts == nil {
		ts = []library.Track{}
	}
	b.push(view{title: a.Title, list: l, tracks: ts})
	return nil
}

func (b *Browser) OpenArtist(ctx context.Context, a library.Artist) error {
	albums, err := b.lib.ArtistAlbums(ctx, a.ID)
	if err != nil {
		return err
	}
	var l List
	l.SetRows(albumRows(a.Name, albums))
	b.push(view{title: a.Name, list: l})
	return nil
}

// AlbumTracks is what `a` on an album appends: the same call the view uses.
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
