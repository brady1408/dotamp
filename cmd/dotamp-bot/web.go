package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/radio"
	"github.com/brady1408/dotamp/internal/requests"
	"github.com/brady1408/dotamp/internal/room"
	"github.com/brady1408/dotamp/internal/weblisten"
)

// maxListeners caps the web player: 25 × 128 kbps is about 3.2 Mbps of upload.
const maxListeners = 25

// webRoom shows one room to the web listener.
type webRoom struct{ r *room.Room }

func (w webRoom) Live() (bool, string)   { ch, name := w.r.Where(); return ch != 0, name }
func (w webRoom) NewFeed() *audio.Feed   { return w.r.Engine().NewFeed() }
func (w webRoom) DropFeed(f *audio.Feed) { w.r.Engine().DropFeed(f) }
func (w webRoom) Rate() int              { return 48000 }

func (w webRoom) Now() weblisten.Now {
	live, name := w.Live()
	n := weblisten.Now{Live: live, Channel: name, Upcoming: []weblisten.Upcoming{}}
	ctrl := w.r.Controller()
	if t, ok := ctrl.Current(); ok && live {
		n.Track = &weblisten.Track{Title: t.Title, Artist: t.Artist, Album: t.Album, Duration: t.Duration.Seconds()}
		n.Position = w.r.Engine().Position().Seconds()
	}
	if !live {
		return n
	}
	ts, i := ctrl.Tracks(), ctrl.Index()
	for j := i + 1; j < len(ts) && len(n.Upcoming) < 10; j++ {
		_, by := ctrl.RequesterOf(j)
		if by == radio.RadioBy {
			by = "radio"
		}
		n.Upcoming = append(n.Upcoming, weblisten.Upcoming{Title: ts[j].Title, Artist: ts[j].Artist, By: by})
	}
	return n
}

// serveWeb runs the web listener until ctx ends; serving is set while it
// is actually listening, so /listen never hands out a link that can't work.
func serveWeb(ctx context.Context, addr string, r *room.Room, lanes weblisten.Lanes, secret []byte, serving *atomic.Bool, log *slog.Logger) {
	wr := webRoom{r}
	hub := weblisten.NewHub(wr, maxListeners, log)
	srv := &http.Server{Addr: addr, Handler: weblisten.NewServer(hub, wr, lanes, secret, time.Now, log), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("web listener", "err", err)
		return
	}
	serving.Store(true)
	defer serving.Store(false)
	log.Info("web listener", "addr", addr)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("web listener", "err", err)
	}
}

// webLanes lets a /listen link manage its owner's request lane, by the
// same rules and limits as /play and /remove.
type webLanes struct {
	r    *room.Room
	req  *requests.Core
	name func(user string) string
}

func (l webLanes) Mine(user string) []weblisten.Song {
	return webSongs(l.r.Controller().Mine(user))
}

func (l webLanes) Search(ctx context.Context, q string) []weblisten.Choice {
	return webChoices(l.req.Complete(ctx, q))
}

func (l webLanes) Add(ctx context.Context, user, value string) (string, error) {
	if ch, _ := l.r.Where(); ch == 0 {
		return "", errors.New("dotamp isn't in a voice channel; ask someone to /join it")
	}
	if l.req.Expired(value) {
		return "", errors.New("That choice expired; search again")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ts, label, refusal := l.req.Resolve(ctx, value)
	if refusal != "" {
		return "", errors.New(refusal)
	}
	n, err := l.r.Request(ctx, user, l.name(user), ts)
	return addResult(label, len(ts), n, err)
}

func (l webLanes) Remove(ctx context.Context, user string, k int, id string) error {
	return laneErr(l.r.Controller().RemoveMine(context.WithoutCancel(ctx), user, k, id))
}

func (l webLanes) Top(ctx context.Context, user string, k int, id string) error {
	return laneErr(l.r.Controller().TopMine(context.WithoutCancel(ctx), user, k, id))
}

func webSongs(ts []library.Track, fed bool) []weblisten.Song {
	out := make([]weblisten.Song, 0, len(ts))
	for i, t := range ts {
		out = append(out, weblisten.Song{ID: t.ID, Title: t.Title, Artist: t.Artist, Next: fed && i == 0})
	}
	return out
}

func webChoices(cs []discord.AutocompleteChoice) []weblisten.Choice {
	out := make([]weblisten.Choice, 0, len(cs))
	for _, c := range cs {
		if s, ok := c.(discord.AutocompleteChoiceString); ok {
			out = append(out, weblisten.Choice{Label: s.Name, Value: s.Value})
		}
	}
	return out
}

// addResult words a request's outcome like /play does.
func addResult(label string, songs, pending int, err error) (string, error) {
	switch {
	case errors.Is(err, audio.ErrTooManyPending):
		return "", fmt.Errorf("You have %d songs waiting; wait for some to play", pending)
	case err != nil:
		return "", errors.New("Couldn't fetch that: " + err.Error())
	case songs == 1:
		return "Added " + label, nil
	}
	return fmt.Sprintf("Added %s (%d songs)", label, songs), nil
}

func laneErr(err error) error {
	if errors.Is(err, audio.ErrMoved) {
		return errors.New("That song has moved or played")
	}
	return err
}

// memberName is a user's name in the guild, for songs added from the web.
func (b *houseBot) memberName(g snowflake.ID) func(string) string {
	return func(user string) string {
		id, err := snowflake.Parse(user)
		if err != nil {
			return "web listener"
		}
		if m, ok := b.client.Caches.Member(g, id); ok {
			return m.EffectiveName()
		}
		if m, err := b.client.Rest.GetMember(g, id); err == nil {
			return m.EffectiveName()
		}
		return "web listener"
	}
}
