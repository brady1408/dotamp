package plex

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
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
	keys := trackKeys(tracks)
	first := keys
	if len(first) > playlistBatch {
		first = keys[:playlistBatch]
	}
	var out container
	q := url.Values{"type": {"audio"}, "smart": {"0"}, "title": {name}, "uri": {itemURI(mid, first)}}
	if err := c.post(ctx, "/playlists", q, &out); err != nil {
		return library.Playlist{}, err
	}
	if len(out.MediaContainer.Metadata) == 0 {
		return library.Playlist{}, errors.New("plex: playlist created but not returned")
	}
	p := c.toPlaylist(out.MediaContainer.Metadata[0])
	if len(keys) > playlistBatch {
		if err := c.putItems(ctx, mid, p.ID, keys[playlistBatch:]); err != nil {
			return p, err
		}
	}
	p.TrackCount = len(tracks)
	return p, nil
}

// itemURI names tracks for the playlist endpoints.
func itemURI(machineID string, keys []string) string {
	return "server://" + machineID + "/com.plexapp.plugins.library/library/metadata/" + strings.Join(keys, ",")
}

func trackKeys(tracks []library.Track) []string {
	keys := make([]string, len(tracks))
	for i, t := range tracks {
		keys[i] = t.ID
	}
	return keys
}

// putItems appends keys to a playlist in batches.
func (c *Client) putItems(ctx context.Context, mid, id string, keys []string) error {
	for i := 0; i < len(keys); i += playlistBatch {
		end := min(i+playlistBatch, len(keys))
		var out container
		if err := c.put(ctx, "/playlists/"+id+"/items", url.Values{"uri": {itemURI(mid, keys[i:end])}}, &out); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) AddToPlaylist(ctx context.Context, id string, tracks []library.Track) error {
	if len(tracks) == 0 {
		return nil
	}
	mid, err := c.machineIdentifier(ctx)
	if err != nil {
		return err
	}
	return c.putItems(ctx, mid, id, trackKeys(tracks))
}

// playlistItems returns the entries' playlistItemIDs in playlist order.
func (c *Client) playlistItems(ctx context.Context, id string) ([]int64, error) {
	var out container
	if err := c.get(ctx, "/playlists/"+id+"/items", nil, &out); err != nil {
		return nil, err
	}
	ids := make([]int64, len(out.MediaContainer.Metadata))
	for i, m := range out.MediaContainer.Metadata {
		ids[i] = m.PlaylistItemID
	}
	return ids, nil
}

func (c *Client) RemoveFromPlaylist(ctx context.Context, id string, index int) error {
	items, err := c.playlistItems(ctx, id)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(items) {
		return fmt.Errorf("plex: playlist has %d entries", len(items))
	}
	var out container
	return c.delete(ctx, fmt.Sprintf("/playlists/%s/items/%d", id, items[index]), nil, &out)
}

// MovePlaylistTrack moves the entry at from to position to. Plex places an
// item after another, so the predecessor at the destination is named;
// moving to the top names none.
func (c *Client) MovePlaylistTrack(ctx context.Context, id string, from, to int) error {
	items, err := c.playlistItems(ctx, id)
	if err != nil {
		return err
	}
	n := len(items)
	if from < 0 || from >= n || to < 0 || to >= n {
		return fmt.Errorf("plex: playlist has %d entries", n)
	}
	if from == to {
		return nil
	}
	q := url.Values{}
	// The entry that will precede the moved one, in the list without it.
	rest := append(append([]int64(nil), items[:from]...), items[from+1:]...)
	if to > 0 {
		q.Set("after", strconv.FormatInt(rest[to-1], 10))
	}
	var out container
	return c.put(ctx, fmt.Sprintf("/playlists/%s/items/%d/move", id, items[from]), q, &out)
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
