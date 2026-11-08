package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/radio"
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
func serveWeb(ctx context.Context, addr string, r *room.Room, secret []byte, serving *atomic.Bool, log *slog.Logger) {
	wr := webRoom{r}
	hub := weblisten.NewHub(wr, maxListeners, log)
	srv := &http.Server{Addr: addr, Handler: weblisten.NewServer(hub, wr, secret, time.Now, log), ReadHeaderTimeout: 10 * time.Second}
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
