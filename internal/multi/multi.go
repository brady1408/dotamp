// Package multi joins several servers' libraries into one. Items are tagged
// with their server and their IDs are qualified as "server:id", so a later
// call routes back to the server that produced them. Search asks every
// server; browsing uses the current one.
package multi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/brady1408/dotamp/internal/library"
)

type Server struct {
	ID, Name string
	Owned    bool
	Via      string // "local", "remote", "relay"
}

type Library struct {
	mu      sync.RWMutex
	order   []string
	libs    map[string]library.Library
	servers map[string]Server
	current string
}

var _ library.Library = (*Library)(nil)

func New() *Library {
	return &Library{libs: map[string]library.Library{}, servers: map[string]Server{}}
}

// Add registers a server. The first one added is current.
func (m *Library) Add(s Server, lib library.Library) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.libs[s.ID]; !dup {
		m.order = append(m.order, s.ID)
	}
	m.libs[s.ID], m.servers[s.ID] = lib, s
	if m.current == "" {
		m.current = s.ID
	}
}

func (m *Library) Servers() []Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Server, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.servers[id])
	}
	return out
}

func (m *Library) Current() Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[m.current]
}

func (m *Library) SetCurrent(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.libs[id]; !ok {
		return false
	}
	m.current = id
	return true
}

func (m *Library) lib(id string) (library.Library, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.libs[id]
	if !ok {
		return nil, fmt.Errorf("multi: unknown server %q", id)
	}
	return l, nil
}

func (m *Library) currentLib() (string, library.Library, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == "" {
		return "", nil, errors.New("multi: no servers")
	}
	return m.current, m.libs[m.current], nil
}

func qualify(sid, id string) string { return sid + ":" + id }

// split returns the server and raw id of a qualified id. An unqualified id
// belongs to the given fallback server.
func split(id, fallback string) (string, string) {
	if i := strings.IndexByte(id, ':'); i > 0 {
		return id[:i], id[i+1:]
	}
	return fallback, id
}

func tagArtist(sid string, a library.Artist) library.Artist {
	a.ID, a.Server = qualify(sid, a.ID), sid
	return a
}

func tagAlbum(sid string, a library.Album) library.Album {
	a.ID, a.Server = qualify(sid, a.ID), sid
	if a.ArtistID != "" {
		a.ArtistID = qualify(sid, a.ArtistID)
	}
	return a
}

func tagTrack(sid string, t library.Track) library.Track {
	t.ID, t.Server = qualify(sid, t.ID), sid
	if t.AlbumID != "" {
		t.AlbumID = qualify(sid, t.AlbumID)
	}
	return t
}

func (m *Library) Search(ctx context.Context, q string) (library.SearchResult, error) {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	libs := make([]library.Library, len(ids))
	for i, id := range ids {
		libs[i] = m.libs[id]
	}
	m.mu.RUnlock()
	if len(ids) == 0 {
		return library.SearchResult{}, errors.New("multi: no servers")
	}
	results := make([]library.SearchResult, len(ids))
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = libs[i].Search(ctx, q)
		}(i)
	}
	wg.Wait()
	var out library.SearchResult
	failed := 0
	for i, sid := range ids {
		if errs[i] != nil {
			failed++
			continue
		}
		for _, a := range results[i].Artists {
			out.Artists = append(out.Artists, tagArtist(sid, a))
		}
		for _, a := range results[i].Albums {
			out.Albums = append(out.Albums, tagAlbum(sid, a))
		}
		for _, t := range results[i].Tracks {
			out.Tracks = append(out.Tracks, tagTrack(sid, t))
		}
	}
	if failed == len(ids) {
		return out, fmt.Errorf("multi: every server failed: %v", errs[0])
	}
	return out, nil
}

func (m *Library) RecentAlbums(ctx context.Context, offset, limit int) ([]library.Album, error) {
	sid, l, err := m.currentLib()
	if err != nil {
		return nil, err
	}
	albums, err := l.RecentAlbums(ctx, offset, limit)
	for i := range albums {
		albums[i] = tagAlbum(sid, albums[i])
	}
	return albums, err
}

func (m *Library) Artists(ctx context.Context, offset, limit int) ([]library.Artist, int, error) {
	sid, l, err := m.currentLib()
	if err != nil {
		return nil, 0, err
	}
	artists, total, err := l.Artists(ctx, offset, limit)
	for i := range artists {
		artists[i] = tagArtist(sid, artists[i])
	}
	return artists, total, err
}

func (m *Library) ArtistIndex(ctx context.Context) ([]library.Letter, error) {
	_, l, err := m.currentLib()
	if err != nil {
		return nil, err
	}
	return l.ArtistIndex(ctx)
}

func (m *Library) AlbumTracks(ctx context.Context, albumID string) ([]library.Track, error) {
	sid, raw := split(albumID, m.Current().ID)
	l, err := m.lib(sid)
	if err != nil {
		return nil, err
	}
	tracks, err := l.AlbumTracks(ctx, raw)
	for i := range tracks {
		tracks[i] = tagTrack(sid, tracks[i])
	}
	return tracks, err
}

func (m *Library) ArtistAlbums(ctx context.Context, artistID string) ([]library.Album, error) {
	sid, raw := split(artistID, m.Current().ID)
	l, err := m.lib(sid)
	if err != nil {
		return nil, err
	}
	albums, err := l.ArtistAlbums(ctx, raw)
	for i := range albums {
		albums[i] = tagAlbum(sid, albums[i])
	}
	return albums, err
}

func (m *Library) Stream(ctx context.Context, t library.Track) (library.Stream, error) {
	sid := t.Server
	if sid == "" {
		sid, _ = split(t.ID, m.Current().ID)
	}
	l, err := m.lib(sid)
	if err != nil {
		return library.Stream{}, err
	}
	_, t.ID = split(t.ID, sid)
	if t.AlbumID != "" {
		_, t.AlbumID = split(t.AlbumID, sid)
	}
	return l.Stream(ctx, t)
}

func tagPlaylist(sid string, p library.Playlist) library.Playlist {
	p.ID, p.Server = qualify(sid, p.ID), sid
	return p
}

// Playlists asks every server and lists what answers, in registered order.
func (m *Library) Playlists(ctx context.Context) ([]library.Playlist, error) {
	m.mu.RLock()
	ids := append([]string(nil), m.order...)
	libs := make([]library.Library, len(ids))
	for i, id := range ids {
		libs[i] = m.libs[id]
	}
	m.mu.RUnlock()
	if len(ids) == 0 {
		return nil, errors.New("multi: no servers")
	}
	results := make([][]library.Playlist, len(ids))
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = libs[i].Playlists(ctx)
		}(i)
	}
	wg.Wait()
	var out []library.Playlist
	failed := 0
	for i, sid := range ids {
		if errs[i] != nil {
			failed++
			continue
		}
		for _, p := range results[i] {
			out = append(out, tagPlaylist(sid, p))
		}
	}
	if failed == len(ids) {
		return nil, fmt.Errorf("multi: every server failed: %v", errs[0])
	}
	return out, nil
}

func (m *Library) PlaylistTracks(ctx context.Context, id string) ([]library.Track, error) {
	sid, raw := split(id, m.Current().ID)
	l, err := m.lib(sid)
	if err != nil {
		return nil, err
	}
	tracks, err := l.PlaylistTracks(ctx, raw)
	for i := range tracks {
		tracks[i] = tagTrack(sid, tracks[i])
	}
	return tracks, err
}

// CreatePlaylist routes to the one server every track belongs to.
func (m *Library) CreatePlaylist(ctx context.Context, name string, tracks []library.Track) (library.Playlist, error) {
	if len(tracks) == 0 {
		return library.Playlist{}, errors.New("multi: a playlist needs at least one track")
	}
	sid := serverOf(tracks[0], m.Current().ID)
	raw := make([]library.Track, len(tracks))
	for i, t := range tracks {
		if serverOf(t, m.Current().ID) != sid {
			return library.Playlist{}, errors.New("multi: tracks from several servers; use SaveQueue")
		}
		_, t.ID = split(t.ID, sid)
		raw[i] = t
	}
	l, err := m.lib(sid)
	if err != nil {
		return library.Playlist{}, err
	}
	p, err := l.CreatePlaylist(ctx, name, raw)
	if err != nil {
		return library.Playlist{}, err
	}
	return tagPlaylist(sid, p), nil
}

// SaveQueue makes one playlist per server, named name, each holding that
// server's tracks in queue order. Servers go in registered order; a server
// that fails is reported in the error as "<name> failed: <reason>" and the
// rest still save. Only the first error is returned.
func (m *Library) SaveQueue(ctx context.Context, name string, tracks []library.Track) ([]library.Saved, error) {
	if len(tracks) == 0 {
		return nil, errors.New("multi: the queue is empty")
	}
	cur := m.Current().ID
	groups := map[string][]library.Track{}
	for _, t := range tracks {
		sid := serverOf(t, cur)
		groups[sid] = append(groups[sid], t)
	}
	m.mu.RLock()
	order := append([]string(nil), m.order...)
	names := map[string]string{}
	for id, s := range m.servers {
		names[id] = s.Name
	}
	m.mu.RUnlock()
	// unknown servers come last so their failure does not hide real saves
	for sid := range groups {
		if _, known := names[sid]; !known {
			order = append(order, sid)
			names[sid] = sid
		}
	}
	var saved []library.Saved
	var first error
	for _, sid := range order {
		ts, ok := groups[sid]
		if !ok {
			continue
		}
		p, err := m.CreatePlaylist(ctx, name, ts)
		if err != nil {
			if first == nil {
				first = fmt.Errorf("%s failed: %w", names[sid], err)
			}
			continue
		}
		saved = append(saved, library.Saved{Server: sid, Playlist: p, Tracks: len(ts)})
	}
	return saved, first
}

func serverOf(t library.Track, fallback string) string {
	if t.Server != "" {
		return t.Server
	}
	sid, _ := split(t.ID, fallback)
	return sid
}

func (m *Library) AddToPlaylist(ctx context.Context, id string, tracks []library.Track) error {
	sid, raw := split(id, m.Current().ID)
	l, err := m.lib(sid)
	if err != nil {
		return err
	}
	rawTracks := make([]library.Track, len(tracks))
	for i, t := range tracks {
		if serverOf(t, sid) != sid {
			return errors.New("multi: tracks from another server")
		}
		_, t.ID = split(t.ID, sid)
		rawTracks[i] = t
	}
	return l.AddToPlaylist(ctx, raw, rawTracks)
}

func (m *Library) RemoveFromPlaylist(ctx context.Context, id string, index int) error {
	sid, raw := split(id, m.Current().ID)
	l, err := m.lib(sid)
	if err != nil {
		return err
	}
	return l.RemoveFromPlaylist(ctx, raw, index)
}

func (m *Library) MovePlaylistTrack(ctx context.Context, id string, from, to int) error {
	sid, raw := split(id, m.Current().ID)
	l, err := m.lib(sid)
	if err != nil {
		return err
	}
	return l.MovePlaylistTrack(ctx, raw, from, to)
}
