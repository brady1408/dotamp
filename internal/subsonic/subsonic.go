// Package subsonic is a library backend for servers speaking the Subsonic
// API, Navidrome first among them.
package subsonic

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/netlog"
)

const (
	apiVersion = "1.16.1"
	clientName = "dotamp"
)

type Client struct {
	base     string
	user     string
	password string
	http     *http.Client

	serverID, serverName string
	local                bool // reached over the LAN: never transcode
	remoteBitrate        int  // kbps to transcode to when not local; 0 = original

	mu      sync.Mutex
	artists []library.Artist // the whole index, fetched once
	letters []library.Letter
}

var _ library.Library = (*Client)(nil)

// New returns a client for the server at base. local is guessed from the
// address: a private IP or a bare hostname counts as the LAN.
func New(base, user, password string) *Client {
	c := &Client{
		base:     strings.TrimRight(base, "/"),
		user:     user,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second, Transport: netlog.New()},
	}
	if u, err := url.Parse(c.base); err == nil {
		h := u.Hostname()
		ip := net.ParseIP(h)
		c.local = (ip != nil && (ip.IsPrivate() || ip.IsLoopback())) || (ip == nil && !strings.Contains(h, "."))
	}
	return c
}

func (c *Client) SetServer(id, name string) { c.serverID, c.serverName = id, name }
func (c *Client) SetLocal(local bool)       { c.local = local }
func (c *Client) SetRemoteBitrate(kbps int) { c.remoteBitrate = kbps }

// auth signs a request the Subsonic way: a random salt and md5(password+salt).
// The password itself never leaves the process.
func (c *Client) auth(q url.Values) url.Values {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	salt := hex.EncodeToString(raw[:])
	sum := md5.Sum([]byte(c.password + salt))
	q.Set("u", c.user)
	q.Set("t", hex.EncodeToString(sum[:]))
	q.Set("s", salt)
	q.Set("v", apiVersion)
	q.Set("c", clientName)
	q.Set("f", "json")
	return q
}

// StreamHeaders is empty: Subsonic streams carry their auth in the URL.
func (c *Client) streamURL(q url.Values) string {
	return c.base + "/rest/stream?" + c.auth(q).Encode()
}

type envelope struct {
	Response struct {
		Status        string `json:"status"`
		Version       string `json:"version"`
		Type          string `json:"type"`
		ServerVersion string `json:"serverVersion"`
		Error         *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		SearchResult3 *struct {
			Artist []artist `json:"artist"`
			Album  []album  `json:"album"`
			Song   []song   `json:"song"`
		} `json:"searchResult3"`
		Artists *struct {
			Index []struct {
				Name   string   `json:"name"`
				Artist []artist `json:"artist"`
			} `json:"index"`
		} `json:"artists"`
		AlbumList2 *struct {
			Album []album `json:"album"`
		} `json:"albumList2"`
		Album     *album  `json:"album"`
		Artist    *artist `json:"artist"`
		Playlists *struct {
			Playlist []playlist `json:"playlist"`
		} `json:"playlists"`
		Playlist *playlist `json:"playlist"`
	} `json:"subsonic-response"`
}

type artist struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	AlbumCount int     `json:"albumCount"`
	Album      []album `json:"album"`
}

type album struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Artist    string `json:"artist"`
	ArtistID  string `json:"artistId"`
	Year      int    `json:"year"`
	SongCount int    `json:"songCount"`
	Created   string `json:"created"`
	Song      []song `json:"song"`
}

type song struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Album        string `json:"album"`
	AlbumID      string `json:"albumId"`
	Artist       string `json:"artist"`
	ArtistID     string `json:"artistId"`
	Track        int    `json:"track"`
	Duration     int    `json:"duration"`
	BitRate      int    `json:"bitRate"`
	Suffix       string `json:"suffix"`
	SamplingRate int    `json:"samplingRate"`
	BitDepth     int    `json:"bitDepth"`
}

type playlist struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SongCount int    `json:"songCount"`
	Entry     []song `json:"entry"`
}

func (c *Client) call(ctx context.Context, method string, q url.Values) (*envelope, error) {
	if q == nil {
		q = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/rest/"+method+"?"+c.auth(q).Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subsonic: %s: HTTP %d", method, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("subsonic: %s: %w", method, err)
	}
	if env.Response.Status != "ok" {
		if env.Response.Error != nil {
			return nil, fmt.Errorf("subsonic: %s: %s", method, env.Response.Error.Message)
		}
		return nil, fmt.Errorf("subsonic: %s: status %q", method, env.Response.Status)
	}
	return &env, nil
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "ping", nil)
	return err
}

// Version pings and returns the server's own version string.
func (c *Client) Version(ctx context.Context) (string, error) {
	env, err := c.call(ctx, "ping", nil)
	if err != nil {
		return "", err
	}
	if env.Response.ServerVersion != "" {
		return env.Response.Type + " " + env.Response.ServerVersion, nil
	}
	return "subsonic " + env.Response.Version, nil
}

// Local reports whether the server address looks like the LAN.
func (c *Client) Local() bool { return c.local }

func (c *Client) toArtist(a artist) library.Artist {
	return library.Artist{ID: a.ID, Name: a.Name, Server: c.serverID}
}

func (c *Client) toAlbum(a album) library.Album {
	out := library.Album{ID: a.ID, Title: a.Name, Artist: a.Artist, ArtistID: a.ArtistID, Year: a.Year, TrackCount: a.SongCount, Server: c.serverID}
	if t, err := time.Parse(time.RFC3339, a.Created); err == nil {
		out.AddedAt = t.Unix()
	}
	return out
}

func (c *Client) toTrack(s song, fallback *album) library.Track {
	t := library.Track{ID: s.ID, Title: s.Title, Artist: s.Artist, Album: s.Album, AlbumID: s.AlbumID, Index: s.Track,
		Duration: time.Duration(s.Duration) * time.Second, Codec: s.Suffix, Container: s.Suffix, PartKey: s.ID,
		Bitrate: s.BitRate, SampleRate: s.SamplingRate, BitDepth: s.BitDepth, Server: c.serverID}
	if fallback != nil {
		if t.Album == "" {
			t.Album = fallback.Name
		}
		if t.AlbumID == "" {
			t.AlbumID = fallback.ID
		}
		if t.Artist == "" {
			t.Artist = fallback.Artist
		}
	}
	return t
}

func (c *Client) Search(ctx context.Context, query string) (library.SearchResult, error) {
	env, err := c.call(ctx, "search3", url.Values{"query": {query}, "artistCount": {"10"}, "albumCount": {"20"}, "songCount": {"30"}})
	if err != nil {
		return library.SearchResult{}, err
	}
	var res library.SearchResult
	if r := env.Response.SearchResult3; r != nil {
		for _, a := range r.Artist {
			res.Artists = append(res.Artists, c.toArtist(a))
		}
		for _, a := range r.Album {
			res.Albums = append(res.Albums, c.toAlbum(a))
		}
		for _, s := range r.Song {
			res.Tracks = append(res.Tracks, c.toTrack(s, nil))
		}
	}
	return res, nil
}

// loadArtists fetches the whole artist index once; Subsonic hands it over in
// one response, letters included.
func (c *Client) loadArtists(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.artists != nil {
		return nil
	}
	env, err := c.call(ctx, "getArtists", nil)
	if err != nil {
		return err
	}
	var artists []library.Artist
	var letters []library.Letter
	if env.Response.Artists != nil {
		for _, ix := range env.Response.Artists.Index {
			letters = append(letters, library.Letter{Letter: ix.Name, Count: len(ix.Artist)})
			for _, a := range ix.Artist {
				artists = append(artists, c.toArtist(a))
			}
		}
	}
	if artists == nil {
		artists = []library.Artist{}
	}
	c.artists, c.letters = artists, letters
	return nil
}

func (c *Client) Artists(ctx context.Context, offset, limit int) ([]library.Artist, int, error) {
	if err := c.loadArtists(ctx); err != nil {
		return nil, 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	total := len(c.artists)
	if offset >= total {
		return nil, total, nil
	}
	end := min(offset+limit, total)
	return append([]library.Artist(nil), c.artists[offset:end]...), total, nil
}

func (c *Client) ArtistIndex(ctx context.Context) ([]library.Letter, error) {
	if err := c.loadArtists(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]library.Letter(nil), c.letters...), nil
}

func (c *Client) RecentAlbums(ctx context.Context, offset, limit int) ([]library.Album, error) {
	env, err := c.call(ctx, "getAlbumList2", url.Values{"type": {"newest"}, "size": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}})
	if err != nil {
		return nil, err
	}
	var out []library.Album
	if env.Response.AlbumList2 != nil {
		for _, a := range env.Response.AlbumList2.Album {
			out = append(out, c.toAlbum(a))
		}
	}
	return out, nil
}

func (c *Client) AlbumTracks(ctx context.Context, albumID string) ([]library.Track, error) {
	env, err := c.call(ctx, "getAlbum", url.Values{"id": {albumID}})
	if err != nil {
		return nil, err
	}
	var out []library.Track
	if a := env.Response.Album; a != nil {
		for _, s := range a.Song {
			out = append(out, c.toTrack(s, a))
		}
	}
	return out, nil
}

func (c *Client) ArtistAlbums(ctx context.Context, artistID string) ([]library.Album, error) {
	env, err := c.call(ctx, "getArtist", url.Values{"id": {artistID}})
	if err != nil {
		return nil, err
	}
	var out []library.Album
	if a := env.Response.Artist; a != nil {
		for _, al := range a.Album {
			out = append(out, c.toAlbum(al))
		}
	}
	return out, nil
}

// Stream serves the original file when dotamp can decode it and the
// connection allows, and otherwise asks the server for an MP3.
func (c *Client) toPlaylist(p playlist) library.Playlist {
	return library.Playlist{ID: p.ID, Name: p.Name, TrackCount: p.SongCount, Server: c.serverID}
}

func (c *Client) Playlists(ctx context.Context) ([]library.Playlist, error) {
	env, err := c.call(ctx, "getPlaylists", nil)
	if err != nil {
		return nil, err
	}
	var out []library.Playlist
	if env.Response.Playlists != nil {
		for _, p := range env.Response.Playlists.Playlist {
			out = append(out, c.toPlaylist(p))
		}
	}
	return out, nil
}

func (c *Client) PlaylistTracks(ctx context.Context, id string) ([]library.Track, error) {
	env, err := c.call(ctx, "getPlaylist", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if env.Response.Playlist == nil {
		return nil, fmt.Errorf("subsonic: playlist %s not found", id)
	}
	ts := make([]library.Track, 0, len(env.Response.Playlist.Entry))
	for _, s := range env.Response.Playlist.Entry {
		ts = append(ts, c.toTrack(s, nil))
	}
	return ts, nil
}

// CreatePlaylist makes a playlist of tracks in order. Servers answer with
// the playlist; one that does not is asked for its listing and the newest
// playlist of that name is taken.
func (c *Client) CreatePlaylist(ctx context.Context, name string, tracks []library.Track) (library.Playlist, error) {
	if len(tracks) == 0 {
		return library.Playlist{}, errors.New("subsonic: a playlist needs at least one track")
	}
	q := url.Values{"name": {name}}
	for _, t := range tracks {
		q.Add("songId", t.ID)
	}
	env, err := c.call(ctx, "createPlaylist", q)
	if err != nil {
		return library.Playlist{}, err
	}
	if env.Response.Playlist != nil {
		p := c.toPlaylist(*env.Response.Playlist)
		p.TrackCount = len(tracks)
		return p, nil
	}
	ps, err := c.Playlists(ctx)
	if err != nil {
		return library.Playlist{}, err
	}
	for i := len(ps) - 1; i >= 0; i-- {
		if ps[i].Name == name {
			return ps[i], nil
		}
	}
	return library.Playlist{}, fmt.Errorf("subsonic: created %q but the server does not list it", name)
}

func (c *Client) AddToPlaylist(ctx context.Context, id string, tracks []library.Track) error {
	if len(tracks) == 0 {
		return nil
	}
	q := url.Values{"playlistId": {id}}
	for _, t := range tracks {
		q.Add("songIdToAdd", t.ID)
	}
	_, err := c.call(ctx, "updatePlaylist", q)
	return err
}

func (c *Client) RemoveFromPlaylist(ctx context.Context, id string, index int) error {
	_, err := c.call(ctx, "updatePlaylist", url.Values{"playlistId": {id}, "songIndexToRemove": {strconv.Itoa(index)}})
	return err
}

// MovePlaylistTrack rewrites the playlist's contents in the new order;
// the Subsonic API has no move call, but createPlaylist with a playlistId
// replaces the entry list.
func (c *Client) MovePlaylistTrack(ctx context.Context, id string, from, to int) error {
	ts, err := c.PlaylistTracks(ctx, id)
	if err != nil {
		return err
	}
	n := len(ts)
	if from < 0 || from >= n || to < 0 || to >= n {
		return fmt.Errorf("subsonic: playlist has %d entries", n)
	}
	if from == to {
		return nil
	}
	moved := ts[from]
	rest := append(append([]library.Track(nil), ts[:from]...), ts[from+1:]...)
	ordered := append(append(append([]library.Track(nil), rest[:to]...), moved), rest[to:]...)
	q := url.Values{"playlistId": {id}}
	for _, t := range ordered {
		q.Add("songId", t.ID)
	}
	_, err = c.call(ctx, "createPlaylist", q)
	return err
}

func (c *Client) Stream(ctx context.Context, t library.Track) (library.Stream, error) {
	bitrate := 0
	if !c.local && c.remoteBitrate > 0 {
		bitrate = c.remoteBitrate
	}
	codec := strings.ToLower(t.Codec)
	if bitrate == 0 && (codec == "flac" || codec == "mp3") {
		return library.Stream{URL: c.streamURL(url.Values{"id": {t.ID}, "format": {"raw"}}), Codec: codec}, nil
	}
	if bitrate == 0 {
		bitrate = 320
	}
	return library.Stream{URL: c.streamURL(url.Values{"id": {t.ID}, "format": {"mp3"}, "maxBitRate": {strconv.Itoa(bitrate)}}), Codec: "mp3"}, nil
}
