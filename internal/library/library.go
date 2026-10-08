// Package library is the seam between the player and a music server.
package library

import (
	"context"
	"time"
)

type Artist struct{ ID, Name, Server string }

type Album struct {
	ID, Title, Artist, ArtistID string
	Year, TrackCount            int
	AddedAt                     int64
	Server                      string // which server the album lives on
}

type Track struct {
	ID, Title, Artist, Album, AlbumID string
	Index                             int
	Duration                          time.Duration
	Codec                             string // "flac", "mp3", "aac"
	Container                         string
	PartKey                           string // server-relative path of the media file, e.g. /library/parts/1/1/file.flac
	Bitrate, SampleRate, BitDepth     int
	Server                            string // which server the track lives on
}

type Stream struct {
	URL     string
	Codec   string // codec of the bytes at URL: "flac" or "mp3"
	Headers map[string]string
}

// Letter is one entry of the artist index: how many artists sort under it.
type Letter struct {
	Letter string
	Count  int
}

type SearchResult struct {
	Artists []Artist
	Albums  []Album
	Tracks  []Track
}

// Playlist is one of the user's playlists on a server.
type Playlist struct {
	ID, Name   string
	TrackCount int
	Server     string // which server it lives on
}

// Saved is one server's share of a saved queue.
type Saved struct {
	Server   string // server ID
	Playlist Playlist
	Tracks   int
}

type Library interface {
	Search(ctx context.Context, query string) (SearchResult, error)
	RecentAlbums(ctx context.Context, offset, limit int) ([]Album, error)
	AlbumTracks(ctx context.Context, albumID string) ([]Track, error)
	ArtistAlbums(ctx context.Context, artistID string) ([]Album, error)
	// Artists returns a page of all artists in sort order and the total count.
	Artists(ctx context.Context, offset, limit int) ([]Artist, int, error)
	// ArtistIndex returns the first letters artists sort under, in sort order.
	ArtistIndex(ctx context.Context) ([]Letter, error)
	// Playlists lists the user's playlists in the server's order.
	Playlists(ctx context.Context) ([]Playlist, error)
	// PlaylistTracks returns a playlist's tracks in playlist order.
	PlaylistTracks(ctx context.Context, playlistID string) ([]Track, error)
	// CreatePlaylist makes a new playlist holding tracks, in order, and
	// returns it. Every track must belong to this library.
	CreatePlaylist(ctx context.Context, name string, tracks []Track) (Playlist, error)
	// AddToPlaylist appends tracks, in order, to the playlist.
	AddToPlaylist(ctx context.Context, playlistID string, tracks []Track) error
	// RemoveFromPlaylist drops the entry at position index (0-based).
	RemoveFromPlaylist(ctx context.Context, playlistID string, index int) error
	// MovePlaylistTrack moves the entry at from so it sits at position to.
	MovePlaylistTrack(ctx context.Context, playlistID string, from, to int) error
	Stream(ctx context.Context, t Track) (Stream, error)
}
