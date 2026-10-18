package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/control"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/multi"
	"github.com/brady1408/dotamp/internal/room"
)

// ctlPlayer is the control API's view of one room.
type ctlPlayer struct {
	r   *room.Room
	lib *multi.Library
	log *slog.Logger
}

func (p ctlPlayer) Status() control.Status {
	ctrl := p.r.Controller()
	_, live := p.r.Where()
	st := control.Status{Live: live, QueueLen: len(ctrl.Tracks()), Index: ctrl.Index()}
	if t, ok := ctrl.Current(); ok {
		ti := control.TrackInfoOf(t)
		st.Track, st.Playing, st.Paused = &ti, true, !p.r.Engine().Playing()
		st.Position = p.r.Engine().Position().Seconds()
	}
	return st
}
func (p ctlPlayer) Queue() []library.Track { return p.r.Controller().Tracks() }
func (p ctlPlayer) QueueNames() []string {
	ts := p.r.Controller().Tracks()
	names := make([]string, len(ts))
	for i := range ts {
		_, names[i] = p.r.Controller().RequesterOf(i)
	}
	return names
}
func (p ctlPlayer) Lanes() []audio.Lane { return p.r.Controller().Pending() }
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
	return p.r.Controller().PlayTracks(ctx, ts, 0)
}
func (p ctlPlayer) Add(ts []library.Track) {
	_ = p.r.Controller().EnqueueAndPlay(context.Background(), ts)
}
func (p ctlPlayer) Request(ctx context.Context, name string, ts []library.Track) (int, error) {
	return p.r.Request(ctx, "ctl:"+name, name, ts)
}
func (p ctlPlayer) Remove(ctx context.Context, i int) error { return p.r.Controller().Remove(ctx, i) }
func (p ctlPlayer) Clear() int                              { return p.r.Controller().Clear() }
func (p ctlPlayer) Next(ctx context.Context) error          { return p.r.Controller().Next(ctx) }
func (p ctlPlayer) Prev(ctx context.Context) error          { return p.r.Controller().Prev(ctx) }
func (p ctlPlayer) TogglePause()                            { p.r.Controller().TogglePause() }
func (p ctlPlayer) Playlists(ctx context.Context) ([]library.Playlist, error) {
	return p.lib.Playlists(ctx)
}
func (p ctlPlayer) CreatePlaylist(ctx context.Context, name string, ts []library.Track) ([]library.Saved, error) {
	return p.lib.SaveQueue(ctx, name, ts)
}
func (p ctlPlayer) AddToPlaylist(ctx context.Context, id string, ts []library.Track) error {
	return p.lib.AddToPlaylist(ctx, id, ts)
}
func (p ctlPlayer) Notify(msg string) { p.log.Info("ctl", "notice", msg) }
