package broadcast

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/requests"
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
	if c.Name == "play" && b.req.Expired(c.What) {
		return reply{Text: "That choice expired; search again"}, true
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
		s := fmt.Sprintf("%s (%s / %s)", requests.TrackLabel(t), requests.Clock(pos), requests.Clock(t.Duration))
		if name != "" {
			s += ", asked by " + name
		}
		return reply{Text: s}
	case "queue":
		var lines []string
		if t, _, name, ok := b.cfg.Player.Current(); ok {
			lines = append(lines, "Now: "+withName(requests.TrackLabel(t), name))
		}
		for i, u := range b.cfg.Player.Upcoming(10) {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, withName(requests.TrackLabel(u.Track), u.Name)))
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

func (b *Broadcaster) play(ctx context.Context, c command) reply {
	ts, label, refusal := b.req.Resolve(ctx, c.What)
	if refusal != "" {
		return reply{Text: refusal}
	}
	var n int
	var err error
	// The DJ's own /play goes straight onto the queue, no lane. DOTAMP_DJ_REQUESTS
	// sends it through a lane instead, for testing the lanes with one account.
	if c.UserID == b.cfg.UserID.String() && os.Getenv("DOTAMP_DJ_REQUESTS") == "" {
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

// complete answers /play autocomplete.
func (b *Broadcaster) complete(ctx context.Context, q string) []discord.AutocompleteChoice {
	return b.req.Complete(ctx, q)
}
