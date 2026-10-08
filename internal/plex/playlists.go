package plex

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/brady1408/dotamp/internal/library"
)

// playlistBatch is how many item keys go in one playlist request; Plex
// accepts long URIs but this keeps them well under proxy limits.
const playlistBatch = 500

func (c *Client) Playlists(ctx context.Context) ([]library.Playlist, error) {
	var out container
	if err := c.get(ctx, "/playlists", url.Values{"playlistType": {"audio"}}, &out); err != nil {
		return nil, err
	}
	ps := make([]library.Playlist, 0, len(out.MediaContainer.Metadata))
	for _, m := range out.MediaContainer.Metadata {
		ps = append(ps, c.toPlaylist(m))
	}
	return ps, nil
}

func (c *Client) PlaylistTracks(ctx context.Context, id string) ([]library.Track, error) {
	var out container
	if err := c.get(ctx, "/playlists/"+id+"/items", nil, &out); err != nil {
		return nil, err
	}
	ts := make([]library.Track, 0, len(out.MediaContainer.Metadata))
	for _, m := range out.MediaContainer.Metadata {
		ts = append(ts, c.toTrack(m))
	}
	return ts, nil
}

// CreatePlaylist makes a playlist of tracks, in order. The first batch of
// keys creates it; further batches are appended with PUT.
func (c *Client) CreatePlaylist(ctx context.Context, name string, tracks []library.Track) (library.Playlist, error) {
	if len(tracks) == 0 {
		return library.Playlist{}, errors.New("plex: a playlist needs at least one track")
	}
	mid, err := c.machineIdentifier(ctx)
	if err != nil {
		return library.Playlist{}, err
	}
	keys := make([]string, len(tracks))
	for i, t := range tracks {
		keys[i] = t.ID
	}
	uri := func(ks []string) string {
		return "server://" + mid + "/com.plexapp.plugins.library/library/metadata/" + strings.Join(ks, ",")
	}
	first := keys
	if len(first) > playlistBatch {
		first = keys[:playlistBatch]
	}
	var out container
	q := url.Values{"type": {"audio"}, "smart": {"0"}, "title": {name}, "uri": {uri(first)}}
	if err := c.post(ctx, "/playlists", q, &out); err != nil {
		return library.Playlist{}, err
	}
	if len(out.MediaContainer.Metadata) == 0 {
		return library.Playlist{}, errors.New("plex: playlist created but not returned")
	}
	p := c.toPlaylist(out.MediaContainer.Metadata[0])
	for i := playlistBatch; i < len(keys); i += playlistBatch {
		end := min(i+playlistBatch, len(keys))
		var more container
		if err := c.put(ctx, "/playlists/"+p.ID+"/items", url.Values{"uri": {uri(keys[i:end])}}, &more); err != nil {
			return p, err
		}
	}
	p.TrackCount = len(tracks)
	return p, nil
}

func (c *Client) toPlaylist(m metadata) library.Playlist {
	return library.Playlist{ID: m.RatingKey, Name: m.Title, TrackCount: m.LeafCount, Server: c.serverID}
}

// machineIdentifier is the server's own id, which playlist URIs must name.
// The plex.tv connection records it as the server id, but a manually
// configured server does not, so it is read from /identity once.
func (c *Client) machineIdentifier(ctx context.Context) (string, error) {
	if c.machineID != "" {
		return c.machineID, nil
	}
	var out container
	if err := c.get(ctx, "/identity", nil, &out); err != nil {
		return "", err
	}
	if out.MediaContainer.MachineIdentifier == "" {
		return "", errors.New("plex: /identity gave no machineIdentifier")
	}
	c.machineID = out.MediaContainer.MachineIdentifier
	return c.machineID, nil
}
