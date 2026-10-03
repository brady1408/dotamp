package plex

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

var _ library.Library = (*Client)(nil)

func (c *Client) MusicSection(ctx context.Context) (string, error) {
	var out container
	if err := c.get(ctx, "/library/sections", nil, &out); err != nil {
		return "", err
	}
	for _, d := range out.MediaContainer.Directory {
		if d.Type == "artist" {
			c.section = d.Key
			return d.Key, nil
		}
	}
	return "", errors.New("plex: no music library found")
}

func (c *Client) Search(ctx context.Context, query string) (library.SearchResult, error) {
	q := url.Values{"query": {query}, "sectionId": {c.section}, "limit": {"30"}}
	var out container
	if err := c.get(ctx, "/hubs/search", q, &out); err != nil {
		return library.SearchResult{}, err
	}
	var res library.SearchResult
	for _, h := range out.MediaContainer.Hub {
		switch h.Type {
		case "artist":
			for _, m := range h.Metadata {
				res.Artists = append(res.Artists, library.Artist{ID: m.RatingKey, Name: m.Title})
			}
		case "album":
			for _, m := range h.Metadata {
				res.Albums = append(res.Albums, toAlbum(m))
			}
		case "track":
			for _, m := range h.Metadata {
				res.Tracks = append(res.Tracks, toTrack(m))
			}
		}
	}
	return res, nil
}

func (c *Client) RecentAlbums(ctx context.Context, offset, limit int) ([]library.Album, error) {
	q := url.Values{
		"type": {"9"}, "sort": {"addedAt:desc"},
		"X-Plex-Container-Start": {strconv.Itoa(offset)},
		"X-Plex-Container-Size":  {strconv.Itoa(limit)},
	}
	var out container
	if err := c.get(ctx, "/library/sections/"+c.section+"/all", q, &out); err != nil {
		return nil, err
	}
	albums := make([]library.Album, 0, len(out.MediaContainer.Metadata))
	for _, m := range out.MediaContainer.Metadata {
		albums = append(albums, toAlbum(m))
	}
	return albums, nil
}

func (c *Client) AlbumTracks(ctx context.Context, albumID string) ([]library.Track, error) {
	var out container
	if err := c.get(ctx, "/library/metadata/"+albumID+"/children", nil, &out); err != nil {
		return nil, err
	}
	tracks := make([]library.Track, 0, len(out.MediaContainer.Metadata))
	for _, m := range out.MediaContainer.Metadata {
		tracks = append(tracks, toTrack(m))
	}
	return tracks, nil
}

func (c *Client) ArtistAlbums(ctx context.Context, artistID string) ([]library.Album, error) {
	var out container
	if err := c.get(ctx, "/library/metadata/"+artistID+"/children", nil, &out); err != nil {
		return nil, err
	}
	albums := make([]library.Album, 0, len(out.MediaContainer.Metadata))
	for _, m := range out.MediaContainer.Metadata {
		albums = append(albums, toAlbum(m))
	}
	return albums, nil
}

// Stream never puts the token in the URL: it rides in StreamHeaders, so a
// logged or displayed URL cannot leak it.
func (c *Client) Stream(ctx context.Context, t library.Track) (library.Stream, error) {
	switch t.Codec {
	case "flac", "mp3":
		if t.PartKey == "" {
			return library.Stream{}, errors.New("plex: track has no part key")
		}
		return library.Stream{URL: c.server + t.PartKey, Codec: t.Codec, Headers: c.StreamHeaders()}, nil
	}
	q := url.Values{
		"path": {"/library/metadata/" + t.ID}, "mediaIndex": {"0"}, "partIndex": {"0"},
		"protocol": {"http"},
	}
	return library.Stream{URL: c.server + "/music/:/transcode/universal/start.mp3?" + q.Encode(), Codec: "mp3", Headers: c.StreamHeaders()}, nil
}

func toAlbum(m metadata) library.Album {
	return library.Album{ID: m.RatingKey, Title: m.Title, Artist: m.ParentTitle, ArtistID: m.ParentRatingKey,
		Year: m.Year, TrackCount: m.LeafCount, AddedAt: m.AddedAt}
}

func toTrack(m metadata) library.Track {
	t := library.Track{ID: m.RatingKey, Title: m.Title, Index: m.Index, Album: m.ParentTitle,
		AlbumID: m.ParentRatingKey, Artist: m.GrandparentTitle, Duration: time.Duration(m.Duration) * time.Millisecond}
	if len(m.Media) > 0 {
		md := m.Media[0]
		t.Codec, t.Container, t.Bitrate = md.AudioCodec, md.Container, md.Bitrate
		if len(md.Part) > 0 {
			t.PartKey = md.Part[0].Key
			for _, s := range md.Part[0].Stream {
				if s.StreamType == 2 {
					t.SampleRate, t.BitDepth = s.SamplingRate, s.BitDepth
				}
			}
		}
	}
	return t
}
