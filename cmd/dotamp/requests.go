package main

import (
	"context"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/broadcast"
	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
)

// player adapts the controller and engine to what the Discord commands need.
type player struct {
	ctrl *audio.Controller
	eng  *audio.Engine
}

func (p player) Request(ctx context.Context, by, name string, ts []library.Track) (int, error) {
	return p.ctrl.Request(ctx, by, name, ts)
}
func (p player) Enqueue(ts []library.Track) { p.ctrl.Enqueue(ts...) }
func (p player) Current() (library.Track, time.Duration, string, bool) {
	t, ok := p.ctrl.Current()
	_, name := p.ctrl.RequesterOf(p.ctrl.Index())
	return t, p.eng.Position(), name, ok
}
func (p player) Upcoming(n int) []broadcast.Upcoming {
	ts, i := p.ctrl.Tracks(), p.ctrl.Index()
	var out []broadcast.Upcoming
	for j := i + 1; j < len(ts) && len(out) < n; j++ {
		_, name := p.ctrl.RequesterOf(j)
		out = append(out, broadcast.Upcoming{Track: ts[j], Name: name})
	}
	return out
}
func (p player) Lanes() []audio.Lane { return p.ctrl.Pending() }

// nights adapts the history directory to the Library's Nights menu.
type nights struct {
	dir     string
	servers func() []string // reachable music server IDs
	name    func(id string) string
}

func (n nights) Nights() ([]history.Night, error) { return history.Nights(n.dir) }
func (n nights) GuildName(id string) string       { return n.name(id) }

// Night is the server's most-played tracks that can be streamed right now.
func (n nights) Night(_ context.Context, guild string) (string, []library.Track, int, error) {
	es, err := history.Read(n.dir, guild)
	if err != nil {
		return "", nil, 0, err
	}
	up := map[string]bool{}
	for _, s := range n.servers() {
		up[s] = true
	}
	var ts []library.Track
	skipped := 0
	for _, s := range history.Score(es, time.Now()) {
		if len(ts) == history.Top {
			break
		}
		if !up[s.Track.Server] {
			skipped++
			continue
		}
		ts = append(ts, s.Track)
	}
	return n.name(guild), ts, skipped, nil
}
