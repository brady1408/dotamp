// Package requests turns what someone typed or picked in Discord's /play
// into tracks from the library: autocomplete, typed-word ranking and the
// memory of what autocomplete offered. Shared by dotamp and the house bot.
package requests

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/brady1408/dotamp/internal/library"
)

// Core resolves /play values against a library.
type Core struct {
	lib     func() library.Library
	mu      sync.Mutex
	choices map[string]choice
}

func New(lib func() library.Library) *Core { return &Core{lib: lib} }

// Expired reports a t: value the core no longer remembers offering.
func (c *Core) Expired(what string) bool {
	if !strings.HasPrefix(what, "t:") {
		return false
	}
	ch, ok := c.recall(what)
	return !ok || ch.track == nil
}

// Resolve turns a /play value into tracks and the label a reply names. A
// non-empty refusal is the private reply, and nothing should be queued.
func (c *Core) Resolve(ctx context.Context, what string) (ts []library.Track, label, refusal string) {
	kind, id, hasPrefix := strings.Cut(what, ":")
	switch {
	case hasPrefix && kind == "t":
		ch, ok := c.recall(what)
		if !ok || ch.track == nil {
			return nil, "", "That choice expired; search again"
		}
		ts, label = []library.Track{*ch.track}, ch.label
	case hasPrefix && kind == "a":
		got, err := c.lib().AlbumTracks(ctx, id)
		if err != nil {
			return nil, "", "Couldn't fetch that: " + err.Error()
		}
		ts, label = got, c.labelOr(what, firstAlbum(got))
	case hasPrefix && kind == "p":
		got, err := c.lib().PlaylistTracks(ctx, id)
		if err != nil {
			return nil, "", "Couldn't fetch that: " + err.Error()
		}
		ts, label = got, c.labelOr(what, c.playlistName(ctx, id))
	default: // typed without picking from autocomplete: rank the hits against the words
		res, err := c.lib().Search(ctx, what)
		if err != nil {
			return nil, "", "Couldn't fetch that: " + err.Error()
		}
		if len(res.Tracks) == 0 {
			return nil, "", fmt.Sprintf("Nothing found for %q", what)
		}
		best, score := bestMatch(what, res.Tracks)
		if score < matchFloor {
			var opts []string
			for _, t := range res.Tracks[:min(3, len(res.Tracks))] {
				opts = append(opts, TrackLabel(t))
			}
			return nil, "", fmt.Sprintf("Nothing close to %q; pick from the list: %s", what, strings.Join(opts, "; "))
		}
		ts, label = []library.Track{best}, TrackLabel(best)
	}
	if len(ts) == 0 {
		return nil, "", fmt.Sprintf("Nothing found for %q", what)
	}
	return ts, label, ""
}

const (
	CompleteDeadline = 2500 * time.Millisecond // Discord allows 3 s for autocomplete
	choiceTTL        = 10 * time.Minute
	maxChoices       = 25
)

// choice is an autocomplete entry the bot offered, kept so Enter on it can
// be resolved: the library cannot fetch a track by ID.
type choice struct {
	label string
	track *library.Track
	at    time.Time
}

func (c *Core) remember(value, label string, t *library.Track) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.choices == nil {
		c.choices = map[string]choice{}
	}
	now := time.Now()
	for k, ch := range c.choices {
		if now.Sub(ch.at) > choiceTTL {
			delete(c.choices, k)
		}
	}
	c.choices[value] = choice{label: label, track: t, at: now}
}

func (c *Core) recall(value string) (choice, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.choices[value]
	return ch, ok && time.Since(ch.at) <= choiceTTL
}

func TrackLabel(t library.Track) string {
	if t.Artist == "" {
		return t.Title
	}
	return t.Title + " · " + t.Artist
}

// complete answers autocomplete for /play: tracks, then albums, then the
// playlists whose names contain the query, within Discord's deadline.
func (c *Core) Complete(ctx context.Context, query string) []discord.AutocompleteChoice {
	ctx, cancel := context.WithTimeout(ctx, CompleteDeadline)
	defer cancel()
	resCh := make(chan library.SearchResult, 1)
	plCh := make(chan []library.Playlist, 1)
	go func() {
		r, err := c.lib().Search(ctx, query)
		if err != nil {
			r = library.SearchResult{}
		}
		resCh <- r
	}()
	go func() {
		ps, _ := c.lib().Playlists(ctx)
		plCh <- ps
	}()
	var res library.SearchResult
	var pls []library.Playlist
	for i := 0; i < 2; i++ {
		select {
		case res = <-resCh:
		case pls = <-plCh:
		case <-ctx.Done():
			i = 2
		}
	}
	var out []discord.AutocompleteChoice
	// shown is the menu text; label is what the reply says was added
	add := func(shown, value, label string, t *library.Track) {
		if len(out) >= maxChoices {
			return
		}
		c.remember(value, label, t)
		out = append(out, discord.AutocompleteChoiceString{Name: cut(shown, 100), Value: value})
	}
	for i := range res.Tracks {
		t := res.Tracks[i]
		shown := "♪ " + TrackLabel(t)
		if t.Album != "" { // two versions of a song differ by album as often as by artist
			shown += " — " + t.Album
		}
		add(shown, "t:"+t.ID, TrackLabel(t), &t)
	}
	for _, a := range res.Albums {
		add("◎ "+a.Title+" · "+a.Artist, "a:"+a.ID, a.Title, nil)
	}
	q := strings.ToLower(query)
	for _, p := range pls {
		if strings.Contains(strings.ToLower(p.Name), q) {
			add("☰ "+p.Name, "p:"+p.ID, p.Name, nil)
		}
	}
	return out
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func Clock(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// matchFloor is the share of the typed words a hit must contain to be
// queued without a pick; below it the asker is shown the choices instead.
const matchFloor = 0.6

var stopwords = map[string]bool{"the": true, "and": true, "of": true, "in": true, "on": true, "to": true, "is": true, "it": true, "my": true, "me": true, "you": true, "your": true, "for": true, "with": true}

// words lowercases, strips punctuation and drops the little words that
// every title shares, so "god is a woman" is judged on god and woman.
func words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(w) > 2 && !stopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

// bestMatch is the hit whose artist and title cover most of the typed
// words, and that share; ties keep the server's order.
func bestMatch(query string, ts []library.Track) (library.Track, float64) {
	q := words(query)
	if len(q) == 0 {
		return ts[0], 1
	}
	best, bestScore := ts[0], -1.0
	for _, t := range ts {
		have := map[string]bool{}
		for _, w := range words(t.Artist + " " + t.Title) {
			have[w] = true
		}
		hit := 0
		for _, w := range q {
			if have[w] {
				hit++
			}
		}
		if score := float64(hit) / float64(len(q)); score > bestScore {
			best, bestScore = t, score
		}
	}
	return best, bestScore
}

func (c *Core) labelOr(value, fallback string) string {
	if ch, ok := c.recall(value); ok {
		return ch.label
	}
	return fallback
}

// playlistName finds a playlist's name by ID for the reply; a listing
// error leaves a generic label rather than failing the request.
func (c *Core) playlistName(ctx context.Context, id string) string {
	ps, err := c.lib().Playlists(ctx)
	if err == nil {
		for _, p := range ps {
			if p.ID == id {
				return p.Name
			}
		}
	}
	return "the playlist"
}

func firstAlbum(ts []library.Track) string {
	if len(ts) > 0 && ts[0].Album != "" {
		return ts[0].Album
	}
	return "the album"
}
