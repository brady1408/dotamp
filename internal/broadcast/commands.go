package broadcast

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

const (
	completeDeadline = 2500 * time.Millisecond // Discord allows 3 s for autocomplete
	choiceTTL        = 10 * time.Minute
	maxChoices       = 25
)

// Player is what the room's commands drive; the app supplies it.
type Player interface {
	Request(ctx context.Context, by, name string, ts []library.Track) (pending int, err error)
	Enqueue(ts []library.Track) // the DJ's own /play: appended like the a key, no lane
	Current() (t library.Track, pos time.Duration, name string, ok bool)
	Upcoming(n int) []Upcoming
	Lanes() []audio.Lane
}

// Upcoming is a queue row after the cursor and who asked for it.
type Upcoming struct {
	Track library.Track
	Name  string
}

// command is a slash command as the pure core sees it.
type command struct {
	Name, UserID, UserName, What string
	Guild, Voice                 snowflake.ID // Voice is the channel the asker is in; 0 = none
}

type reply struct {
	Text   string
	Public bool
}

// choice is an autocomplete entry the bot offered, kept so Enter on it can
// be resolved: the library cannot fetch a track by ID.
type choice struct {
	label string
	track *library.Track
	at    time.Time
}

func (b *Broadcaster) remember(value, label string, t *library.Track) {
	b.cmu.Lock()
	defer b.cmu.Unlock()
	if b.choices == nil {
		b.choices = map[string]choice{}
	}
	now := time.Now()
	for k, c := range b.choices {
		if now.Sub(c.at) > choiceTTL {
			delete(b.choices, k)
		}
	}
	b.choices[value] = choice{label: label, track: t, at: now}
}

func (b *Broadcaster) recall(value string) (choice, bool) {
	b.cmu.Lock()
	defer b.cmu.Unlock()
	c, ok := b.choices[value]
	return c, ok && time.Since(c.at) <= choiceTTL
}

func trackLabel(t library.Track) string {
	if t.Artist == "" {
		return t.Title
	}
	return t.Title + " · " + t.Artist
}

// complete answers autocomplete for /play: tracks, then albums, then the
// playlists whose names contain the query, within Discord's deadline.
func (b *Broadcaster) complete(ctx context.Context, query string) []discord.AutocompleteChoice {
	ctx, cancel := context.WithTimeout(ctx, completeDeadline)
	defer cancel()
	resCh := make(chan library.SearchResult, 1)
	plCh := make(chan []library.Playlist, 1)
	go func() {
		r, err := b.cfg.Library.Search(ctx, query)
		if err != nil {
			r = library.SearchResult{}
		}
		resCh <- r
	}()
	go func() {
		ps, _ := b.cfg.Library.Playlists(ctx)
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
		b.remember(value, label, t)
		out = append(out, discord.AutocompleteChoiceString{Name: cut(shown, 100), Value: value})
	}
	for i := range res.Tracks {
		t := res.Tracks[i]
		add("♪ "+trackLabel(t), "t:"+t.ID, trackLabel(t), &t)
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

// precheck answers what can be refused from cached state alone, before
// any library round trip: not live, the asker elsewhere, an expired choice.
// The adapter uses it to keep those refusals ephemeral.
func (b *Broadcaster) precheck(c command) (reply, bool) {
	b.mu.Lock()
	cur := b.cur
	b.mu.Unlock()
	if cur == nil {
		return reply{Text: "dotamp isn't broadcasting right now"}, true
	}
	if c.Guild != cur.loc.Guild || c.Voice != cur.loc.Channel {
		return reply{Text: "Join " + cur.name + " to request songs"}, true
	}
	if c.Name == "play" && strings.HasPrefix(c.What, "t:") {
		if ch, ok := b.recall(c.What); !ok || ch.track == nil {
			return reply{Text: "That choice expired; search again"}, true
		}
	}
	return reply{}, false
}

func (b *Broadcaster) command(ctx context.Context, c command) reply {
	if r, refused := b.precheck(c); refused {
		return r
	}
	switch c.Name {
	case "play":
		return b.play(ctx, c)
	case "np":
		t, pos, name, ok := b.cfg.Player.Current()
		if !ok {
			return reply{Text: "Nothing is playing"}
		}
		s := fmt.Sprintf("%s (%s / %s)", trackLabel(t), clock(pos), clock(t.Duration))
		if name != "" {
			s += ", asked by " + name
		}
		return reply{Text: s}
	case "queue":
		var lines []string
		if t, _, name, ok := b.cfg.Player.Current(); ok {
			lines = append(lines, "Now: "+withName(trackLabel(t), name))
		}
		for i, u := range b.cfg.Player.Upcoming(10) {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, withName(trackLabel(u.Track), u.Name)))
		}
		var waiting []string
		for _, l := range b.cfg.Player.Lanes() {
			if len(l.Tracks) > 0 {
				waiting = append(waiting, fmt.Sprintf("%s %d", l.Name, len(l.Tracks)))
			}
		}
		if len(waiting) > 0 {
			lines = append(lines, "Waiting: "+strings.Join(waiting, ", "))
		}
		if len(lines) == 0 {
			return reply{Text: "The queue is empty"}
		}
		return reply{Text: strings.Join(lines, "\n")}
	}
	return reply{Text: "Unknown command"}
}

func withName(label, name string) string {
	if name == "" {
		return label
	}
	return label + " — " + name
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (b *Broadcaster) play(ctx context.Context, c command) reply {
	var ts []library.Track
	var label string
	kind, id, hasPrefix := strings.Cut(c.What, ":")
	switch {
	case hasPrefix && kind == "t":
		ch, ok := b.recall(c.What)
		if !ok || ch.track == nil {
			return reply{Text: "That choice expired; search again"}
		}
		ts, label = []library.Track{*ch.track}, ch.label
	case hasPrefix && kind == "a":
		got, err := b.cfg.Library.AlbumTracks(ctx, id)
		if err != nil {
			return reply{Text: "Couldn't fetch that: " + err.Error()}
		}
		ts, label = got, b.labelOr(c.What, firstAlbum(got))
	case hasPrefix && kind == "p":
		got, err := b.cfg.Library.PlaylistTracks(ctx, id)
		if err != nil {
			return reply{Text: "Couldn't fetch that: " + err.Error()}
		}
		ts, label = got, b.labelOr(c.What, b.playlistName(ctx, id))
	default:
		res, err := b.cfg.Library.Search(ctx, c.What)
		if err != nil {
			return reply{Text: "Couldn't fetch that: " + err.Error()}
		}
		if len(res.Tracks) == 0 {
			return reply{Text: fmt.Sprintf("Nothing found for %q", c.What)}
		}
		ts, label = res.Tracks[:1], trackLabel(res.Tracks[0])
	}
	if len(ts) == 0 {
		return reply{Text: fmt.Sprintf("Nothing found for %q", c.What)}
	}
	var n int
	var err error
	if c.UserID == b.cfg.UserID.String() { // the DJ: straight onto the queue, no lane
		b.cfg.Player.Enqueue(ts)
	} else {
		n, err = b.cfg.Player.Request(ctx, c.UserID, c.UserName, ts)
	}
	if errors.Is(err, audio.ErrTooManyPending) {
		return reply{Text: fmt.Sprintf("You have %d songs waiting; wait for some to play", n)}
	}
	if err != nil {
		return reply{Text: "Couldn't fetch that: " + err.Error()}
	}
	if len(ts) == 1 {
		return reply{Text: c.UserName + " added " + label, Public: true}
	}
	return reply{Text: fmt.Sprintf("%s added %s (%d songs)", c.UserName, label, len(ts)), Public: true}
}

func (b *Broadcaster) labelOr(value, fallback string) string {
	if ch, ok := b.recall(value); ok {
		return ch.label
	}
	return fallback
}

// playlistName finds a playlist's name by ID for the reply; a listing
// error leaves a generic label rather than failing the request.
func (b *Broadcaster) playlistName(ctx context.Context, id string) string {
	ps, err := b.cfg.Library.Playlists(ctx)
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
