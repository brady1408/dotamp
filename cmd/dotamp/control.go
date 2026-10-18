package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/config"
	"github.com/brady1408/dotamp/internal/control"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/multi"
	"github.com/brady1408/dotamp/internal/ui"
)

// ctlPlayer is the control API's view of the running player.
type ctlPlayer struct {
	ctrl *audio.Controller
	eng  *audio.Engine
	lib  *multi.Library
	app  *ui.App
	live func() string // the broadcast channel, "" when not live
}

func (p ctlPlayer) Status() control.Status {
	st := control.Status{Live: p.live(), QueueLen: len(p.ctrl.Tracks()), Index: p.ctrl.Index()}
	if t, ok := p.ctrl.Current(); ok {
		ti := control.TrackInfoOf(t)
		st.Track, st.Playing, st.Paused = &ti, true, !p.eng.Playing()
		st.Position = p.eng.Position().Seconds()
	}
	return st
}
func (p ctlPlayer) Queue() []library.Track { return p.ctrl.Tracks() }
func (p ctlPlayer) QueueNames() []string {
	ts := p.ctrl.Tracks()
	names := make([]string, len(ts))
	for i := range ts {
		_, names[i] = p.ctrl.RequesterOf(i)
	}
	return names
}
func (p ctlPlayer) Lanes() []audio.Lane { return p.ctrl.Pending() }
func (p ctlPlayer) Search(ctx context.Context, q string) (library.SearchResult, []library.Playlist, error) {
	res, err := p.lib.Search(ctx, q)
	if err != nil {
		return res, nil, err
	}
	pls, _ := p.lib.Playlists(ctx)
	var match []library.Playlist
	for _, pl := range pls {
		if strings.Contains(strings.ToLower(pl.Name), strings.ToLower(q)) {
			match = append(match, pl)
		}
	}
	return res, match, nil
}
func (p ctlPlayer) AlbumTracks(ctx context.Context, id string) ([]library.Track, error) {
	return p.lib.AlbumTracks(ctx, id)
}
func (p ctlPlayer) PlaylistTracks(ctx context.Context, id string) ([]library.Track, error) {
	return p.lib.PlaylistTracks(ctx, id)
}
func (p ctlPlayer) Play(ctx context.Context, ts []library.Track) error {
	return p.ctrl.PlayTracks(ctx, ts, 0)
}
func (p ctlPlayer) Add(ts []library.Track) { _ = p.ctrl.EnqueueAndPlay(context.Background(), ts) }
func (p ctlPlayer) Request(ctx context.Context, name string, ts []library.Track) (int, error) {
	return p.ctrl.Request(ctx, "ctl:"+name, name, ts) // its own id space, apart from Discord users
}
func (p ctlPlayer) Remove(ctx context.Context, i int) error { return p.ctrl.Remove(ctx, i) }
func (p ctlPlayer) Clear() int                              { return p.ctrl.Clear() }
func (p ctlPlayer) Next(ctx context.Context) error          { return p.ctrl.Next(ctx) }
func (p ctlPlayer) Prev(ctx context.Context) error          { return p.ctrl.Prev(ctx) }
func (p ctlPlayer) TogglePause()                            { p.ctrl.TogglePause() }
func (p ctlPlayer) Playlists(ctx context.Context) ([]library.Playlist, error) {
	return p.lib.Playlists(ctx)
}
func (p ctlPlayer) CreatePlaylist(ctx context.Context, name string, ts []library.Track) ([]library.Saved, error) {
	return p.lib.SaveQueue(ctx, name, ts)
}
func (p ctlPlayer) AddToPlaylist(ctx context.Context, id string, ts []library.Track) error {
	return p.lib.AddToPlaylist(ctx, id, ts)
}
func (p ctlPlayer) Notify(msg string) { p.app.Notify(msg) }

// parseCtl turns argv into a request.
func parseCtl(args []string) (control.Request, error) { return control.ParseArgs(args) }

// ctl sends one request to the running player and prints the JSON reply.
// Exit status is 1 unless the reply is ok.
func ctl(args []string) error {
	req, err := parseCtl(args)
	if err != nil {
		return err
	}
	r, err := control.NewClient(config.ControlPath()).Call(context.Background(), req)
	if err != nil {
		r = control.Reply{Error: err.Error()}
	}
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
	if !r.OK {
		os.Exit(1)
	}
	return nil
}
