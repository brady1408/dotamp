package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/multi"
)

const (
	MenuArtists   = "artists"
	MenuRecent    = "recent"
	MenuPlaylists = "playlists"
	MenuServers   = "servers"

	artistPage = 200 // artists fetched per request while scrolling the index
)

// Switcher is the multi-server view the Library tab needs: which servers
// exist, which is current, and a way to change it.
type Switcher interface {
	Servers() []multi.Server
	Current() multi.Server
	SetCurrent(id string) bool
}

// Browser is the Library tab: a stack of views, each a title and a row set.
// The root is a small menu; Artists is an index of every artist that loads a
// page at a time as the selection moves.
type Browser struct {
	lib   library.Library
	sw    Switcher // nil when there is a single, unnamed server
	stack []view

	// Album rows carry a quality tag taken from the album's first track.
	// Servers only report format per track, so tags are fetched in the
	// background for the albums in view and cached for the session.
	qmu     sync.Mutex
	quality map[string]string   // album ID -> tag ("" once known to be empty)
	pending map[string]struct{} // fetches in flight
	qlimit  chan struct{}       // caps concurrent fetches
}

// SetSwitcher enables the Servers menu and names servers in search results.
func (b *Browser) SetSwitcher(sw Switcher) { b.sw = sw }

// CurrentServerName is shown in the tab row; empty with a single server.
func (b *Browser) CurrentServerName() string {
	if b.sw == nil || len(b.sw.Servers()) < 2 {
		return ""
	}
	return b.sw.Current().Name
}

// serverName labels a server for a header: its name, and how it is reached
// when that is not local, so a relay-backed copy reads as such.
func (b *Browser) serverName(id string) string {
	if b.sw == nil {
		return ""
	}
	for _, s := range b.sw.Servers() {
		if s.ID == id {
			if s.Via != "" && s.Via != "local" {
				return s.Name + " (" + s.Via + ")"
			}
			return s.Name
		}
	}
	return id
}

// serverLabel names a server for a notice: its name when known, else its id.
func (b *Browser) serverLabel(id string) string {
	if b.sw == nil {
		return id
	}
	for _, s := range b.sw.Servers() {
		if s.ID == id {
			return s.Name
		}
	}
	return id
}

type view struct {
	title     string
	list      List
	tracks    []library.Track // set for an album or playlist view: Enter on a track plays these from there
	artists   *artistIndex    // set for the Artists view
	playlists bool            // the Playlists list, rebuilt by ReloadPlaylists
	picker    *picker         // the "Add to playlist" view
}

var errNoPlaylists = errors.New("no playlists")

// picker is the "Add to playlist" view's payload: what to add, which server
// the playlists must be on, and a playlist to leave out (the source).
type picker struct {
	tracks          []library.Track
	server, exclude string
}

type artistIndex struct {
	total   int
	letters []library.Letter
	loaded  map[int]bool // page number -> fetched
}

func NewBrowser(lib library.Library) *Browser {
	return &Browser{lib: lib, quality: map[string]string{}, pending: map[string]struct{}{}, qlimit: make(chan struct{}, 4)}
}

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

// OpenPicker pushes a list of the playlists on the tracks' server. It
// returns errNoPlaylists, pushing nothing, when there are none to offer.
func (b *Browser) OpenPicker(ctx context.Context, tracks []library.Track, exclude string) error {
	if len(tracks) == 0 {
		return errNoPlaylists
	}
	server := tracks[0].Server
	ps, err := b.lib.Playlists(ctx)
	if err != nil {
		return err
	}
	var rows []Row
	for i := range ps {
		p := &ps[i]
		if p.Server == server && p.ID != exclude {
			rows = append(rows, Row{Text: p.Name, Right: fmt.Sprint(p.TrackCount), Playlist: p})
		}
	}
	if len(rows) == 0 {
		return errNoPlaylists
	}
	var l List
	l.SetRows(rows)
	b.push(view{title: "Add to playlist", list: l, picker: &picker{tracks: tracks, server: server, exclude: exclude}})
	return nil
}

// InPicker reports whether the open view is the picker and returns its payload.
func (b *Browser) InPicker() (*picker, bool) {
	if v := b.top(); v != nil && v.picker != nil {
		return v.picker, true
	}
	return nil, false
}

// CancelPicker pops the picker if it is open.
func (b *Browser) CancelPicker() {
	if _, ok := b.InPicker(); ok {
		b.stack = b.stack[:len(b.stack)-1]
	}
}

// Home drops every view above the root menu. It reports whether anything
// was open.
func (b *Browser) Home(context.Context) bool {
	if len(b.stack) <= 1 {
		return false
	}
	b.stack = b.stack[:1]
	return true
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
	rows := []Row{
		{Text: "Artists", Menu: MenuArtists},
		{Text: "Recently added", Menu: MenuRecent},
		{Text: "Playlists", Menu: MenuPlaylists},
	}
	if b.sw != nil && len(b.sw.Servers()) > 1 {
		rows = append(rows, Row{Text: "Servers", Menu: MenuServers})
	}
	var l List
	l.SetRows(rows)
	b.stack = []view{{title: "Library", list: l}}
	return nil
}

// RefreshRoot rebuilds the root menu when the number of servers has changed
// since it was built, which happens as background connections finish. It
// does nothing when a deeper view is open.
func (b *Browser) RefreshRoot(ctx context.Context) {
	if len(b.stack) != 1 || b.sw == nil {
		return
	}
	want := 3
	if len(b.sw.Servers()) > 1 {
		want = 4
	}
	if len(b.stack[0].list.Rows) == want {
		return
	}
	sel := b.stack[0].list.Sel
	_ = b.LoadRoot(ctx)
	if sel >= 0 && sel < len(b.stack[0].list.Rows) {
		b.stack[0].list.Sel = sel
	}
}

// OpenServers lists the account's servers with the current one marked.
func (b *Browser) OpenServers(context.Context) error {
	if b.sw == nil {
		return errors.New("one server only")
	}
	cur := b.sw.Current().ID
	rows := []Row{{Text: "Servers", Header: true}}
	for _, s := range b.sw.Servers() {
		mark := "  "
		if s.ID == cur {
			mark = "▸ "
		}
		text := mark + s.Name
		if s.Via != "" {
			text += "  (" + s.Via + ")"
		}
		if !s.Owned {
			text += "  shared"
		}
		rows = append(rows, Row{Text: text, ServerID: s.ID})
	}
	var l List
	l.SetRows(rows)
	b.push(view{title: "Servers", list: l})
	return nil
}

// SwitchServer makes id the current server for browsing and returns to the
// root menu. It reports whether the switch happened.
func (b *Browser) SwitchServer(ctx context.Context, id string) bool {
	if b.sw == nil || !b.sw.SetCurrent(id) {
		return false
	}
	_ = b.LoadRoot(ctx)
	return true
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
	if v == nil {
		return
	}
	b.tagAlbums(ctx, v, h)
	if v.artists == nil {
		return
	}
	for i := v.list.Sel - h; i <= v.list.Sel+h; i += artistPage {
		_ = b.ensure(ctx, v, i)
	}
	_ = b.ensure(ctx, v, v.list.Sel+h)
}

// tagAlbums applies cached quality tags to the album rows in view and
// starts a fetch for the ones not known yet. Tags land on a later draw.
func (b *Browser) tagAlbums(ctx context.Context, v *view, h int) {
	lo, hi := max(v.list.Top-h, 0), min(v.list.Top+2*h, len(v.list.Rows))
	for i := lo; i < hi; i++ {
		r := &v.list.Rows[i]
		if r.Album == nil || r.tagged {
			continue
		}
		b.qmu.Lock()
		tag, known := b.quality[r.Album.ID]
		_, inflight := b.pending[r.Album.ID]
		if !known && !inflight {
			b.pending[r.Album.ID] = struct{}{}
		}
		b.qmu.Unlock()
		switch {
		case known:
			r.tagged = true
			if tag != "" {
				if r.Right != "" {
					r.Right += "  "
				}
				r.Right += tag
			}
		case !inflight:
			go b.fetchTag(ctx, r.Album.ID)
		}
	}
}

func (b *Browser) fetchTag(ctx context.Context, albumID string) {
	b.qlimit <- struct{}{}
	defer func() { <-b.qlimit }()
	ts, err := b.lib.AlbumTracks(ctx, albumID)
	tag := ""
	if err == nil && len(ts) > 0 {
		tag = ts[0].Quality()
	}
	b.qmu.Lock()
	if err == nil {
		b.quality[albumID] = tag
	}
	delete(b.pending, albumID)
	b.qmu.Unlock()
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
	// Servers do not search playlists, so match their names here.
	var playlists []library.Playlist
	if ps, err := b.lib.Playlists(ctx); err == nil {
		needle := strings.ToLower(q)
		for _, p := range ps {
			if strings.Contains(strings.ToLower(p.Name), needle) {
				playlists = append(playlists, p)
			}
		}
	}
	// With several servers, each group is split per server so a copy on a
	// friend's server reads as such.
	servers := []string{""}
	if b.sw != nil && len(b.sw.Servers()) > 1 {
		servers = servers[:0]
		for _, s := range b.sw.Servers() {
			servers = append(servers, s.ID)
		}
	}
	header := func(kind, sid string) string {
		if sid == "" {
			return kind
		}
		return kind + " — " + b.serverName(sid)
	}
	var rows []Row
	for _, sid := range servers {
		rows = append(rows, Row{Text: header("Artists", sid), Header: true})
		for i := range res.Artists {
			a := &res.Artists[i]
			if sid == "" || a.Server == sid {
				rows = append(rows, Row{Text: a.Name, Artist: a})
			}
		}
		var albums []library.Album
		for _, a := range res.Albums {
			if sid == "" || a.Server == sid {
				albums = append(albums, a)
			}
		}
		rows = append(rows, albumRows(header("Albums", sid), albums)...)
		rows = append(rows, Row{Text: header("Tracks", sid), Header: true})
		for i := range res.Tracks {
			t := &res.Tracks[i]
			if sid == "" || t.Server == sid {
				text := t.Artist + " — " + t.Title
				if t.Album != "" {
					text += "  ·  " + t.Album
				}
				rows = append(rows, Row{Text: text, Right: Clock(t.Duration), Track: t})
			}
		}
		rows = append(rows, Row{Text: header("Playlists", sid), Header: true})
		for i := range playlists {
			p := &playlists[i]
			if sid == "" || p.Server == sid {
				rows = append(rows, Row{Text: p.Name, Right: fmt.Sprint(p.TrackCount), Playlist: p})
			}
		}
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

// playlistRows lists playlists, grouped under a header per server when
// there are several, as search results are.
func (b *Browser) playlistRows(ps []library.Playlist) []Row {
	if len(ps) == 0 {
		return []Row{{Text: "No playlists", Header: true}}
	}
	servers := []string{""}
	if b.sw != nil && len(b.sw.Servers()) > 1 {
		servers = servers[:0]
		for _, s := range b.sw.Servers() {
			servers = append(servers, s.ID)
		}
	}
	var rows []Row
	for _, sid := range servers {
		if sid != "" {
			rows = append(rows, Row{Text: b.serverName(sid), Header: true})
		}
		for i := range ps {
			p := &ps[i]
			if sid == "" || p.Server == sid {
				rows = append(rows, Row{Text: p.Name, Right: fmt.Sprint(p.TrackCount), Playlist: p})
			}
		}
	}
	return rows
}

// OpenPlaylists pushes the list of every server's playlists.
func (b *Browser) OpenPlaylists(ctx context.Context) error {
	ps, err := b.lib.Playlists(ctx)
	if err != nil {
		return err
	}
	var l List
	l.SetRows(b.playlistRows(ps))
	b.push(view{title: "Playlists", list: l, playlists: true})
	return nil
}

// ReloadPlaylists refetches the list when it is the open view, keeping the
// selection, so a playlist saved a moment ago appears. Deeper views are
// left alone.
func (b *Browser) ReloadPlaylists(ctx context.Context) {
	v := b.top()
	if v == nil || !v.playlists {
		return
	}
	ps, err := b.lib.Playlists(ctx)
	if err != nil {
		return
	}
	sel := v.list.Sel
	v.list.SetRows(b.playlistRows(ps))
	if sel < len(v.list.Rows) {
		v.list.Sel = sel
	}
}

// PlaylistTracks is what `a` on a playlist row appends: the same call the
// view uses.
func (b *Browser) PlaylistTracks(ctx context.Context, p library.Playlist) ([]library.Track, error) {
	return b.lib.PlaylistTracks(ctx, p.ID)
}

// OpenPlaylist pushes a playlist's tracks. It carries them as an album
// view does, so Enter on a track plays the playlist from there.
func (b *Browser) OpenPlaylist(ctx context.Context, p library.Playlist) error {
	ts, err := b.lib.PlaylistTracks(ctx, p.ID)
	if err != nil {
		return err
	}
	rows := []Row{{Text: fmt.Sprintf("%s — %d tracks", p.Name, len(ts)), Header: true}}
	for i := range ts {
		t := &ts[i]
		text := fmt.Sprintf("%2d. %s", i+1, t.Title)
		if t.Artist != "" {
			text = fmt.Sprintf("%2d. %s — %s", i+1, t.Artist, t.Title)
		}
		rows = append(rows, Row{Text: text, Right: Clock(t.Duration), Track: t})
	}
	var l List
	l.SetRows(rows)
	if ts == nil {
		ts = []library.Track{}
	}
	b.push(view{title: p.Name, list: l, tracks: ts})
	return nil
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
