// Package control lets another process on the same machine read and drive a
// running dotamp: one JSON request per connection on a localhost port, with
// a token published in control.json beside the config.
package control

import (
	"context"
	"strings"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

// Request is one call. Ids are typed: t:, a: or p: followed by the
// library's qualified id, as every listing returns them.
type Request struct {
	Token string   `json:"token"`
	Op    string   `json:"op"`
	Q     string   `json:"q,omitempty"`
	ID    string   `json:"id,omitempty"`
	IDs   []string `json:"ids,omitempty"`
	As    string   `json:"as,omitempty"`
	Name  string   `json:"name,omitempty"`
	Index *int     `json:"index,omitempty"`
}

type Reply struct {
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type TrackInfo struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Duration float64 `json:"duration"` // seconds
}

type Row struct {
	Index int `json:"index"`
	TrackInfo
	By string `json:"by"`
}

type Status struct {
	Playing  bool       `json:"playing"`
	Paused   bool       `json:"paused"`
	Track    *TrackInfo `json:"track,omitempty"`
	Position float64    `json:"position"` // seconds
	Index    int        `json:"index"`
	QueueLen int        `json:"queue_len"`
	Live     string     `json:"live"`
}

type AlbumInfo struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Year       int    `json:"year"`
	TrackCount int    `json:"track_count"`
}

type PlaylistInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
	Server     string `json:"server"`
}

type LaneInfo struct {
	Name    string `json:"name"`
	Waiting int    `json:"waiting"`
	Next    string `json:"next"`
}

type SearchInfo struct {
	Tracks    []TrackInfo    `json:"tracks"`
	Albums    []AlbumInfo    `json:"albums"`
	Playlists []PlaylistInfo `json:"playlists"`
}

// Player is what the server drives; cmd/dotamp supplies it over the
// controller, engine and library.
type Player interface {
	Status() Status
	Queue() []library.Track
	QueueNames() []string // requester per queue row, "" for the DJ's own
	Lanes() []audio.Lane
	Search(ctx context.Context, q string) (library.SearchResult, []library.Playlist, error)
	AlbumTracks(ctx context.Context, id string) ([]library.Track, error)
	PlaylistTracks(ctx context.Context, id string) ([]library.Track, error)
	Play(ctx context.Context, ts []library.Track) error
	Add(ts []library.Track)
	Request(ctx context.Context, name string, ts []library.Track) (pending int, err error)
	Remove(ctx context.Context, i int) error
	Clear() int
	Next(ctx context.Context) error
	Prev(ctx context.Context) error
	TogglePause()
	Playlists(ctx context.Context) ([]library.Playlist, error)
	CreatePlaylist(ctx context.Context, name string, ts []library.Track) ([]library.Saved, error)
	AddToPlaylist(ctx context.Context, id string, ts []library.Track) error
	Notify(msg string)
}

func TrackID(t library.Track) string       { return "t:" + t.ID }
func AlbumID(a library.Album) string       { return "a:" + a.ID }
func PlaylistID(p library.Playlist) string { return "p:" + p.ID }

// kindOf splits a typed id into its kind letter and the library's id.
func kindOf(id string) (kind, rest string, ok bool) {
	kind, rest, ok = strings.Cut(id, ":")
	if !ok || rest == "" || (kind != "t" && kind != "a" && kind != "p") {
		return "", "", false
	}
	return kind, rest, true
}

func TrackInfoOf(t library.Track) TrackInfo {
	return TrackInfo{ID: TrackID(t), Title: t.Title, Artist: t.Artist, Album: t.Album, Duration: t.Duration.Seconds()}
}

func albumInfo(a library.Album) AlbumInfo {
	return AlbumInfo{ID: AlbumID(a), Title: a.Title, Artist: a.Artist, Year: a.Year, TrackCount: a.TrackCount}
}

func playlistInfo(p library.Playlist) PlaylistInfo {
	return PlaylistInfo{ID: PlaylistID(p), Name: p.Name, TrackCount: p.TrackCount, Server: p.Server}
}
