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
