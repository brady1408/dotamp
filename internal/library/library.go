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

type Library interface {
	Search(ctx context.Context, query string) (SearchResult, error)
	RecentAlbums(ctx context.Context, offset, limit int) ([]Album, error)
	AlbumTracks(ctx context.Context, albumID string) ([]Track, error)
	ArtistAlbums(ctx context.Context, artistID string) ([]Album, error)
	// Artists returns a page of all artists in sort order and the total count.
	Artists(ctx context.Context, offset, limit int) ([]Artist, int, error)
	// ArtistIndex returns the first letters artists sort under, in sort order.
	ArtistIndex(ctx context.Context) ([]Letter, error)
	Stream(ctx context.Context, t Track) (Stream, error)
}
