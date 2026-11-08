package room

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

const (
	stateVersion = 1
	staleAfter   = 30 * time.Minute
	rewind       = 3 * time.Second // a resume starts this far back so listeners re-hear the gap
	saveDebounce = time.Second
)

type savedRow struct {
	Track library.Track `json:"track"`
	By    string        `json:"by,omitempty"`
	Name  string        `json:"name,omitempty"`
}

type savedLane struct {
	By     string          `json:"by"`
	Name   string          `json:"name"`
	Tracks []library.Track `json:"tracks"`
}

// saved is the state file: what a restarted bot needs to carry on.
type saved struct {
	Version     int         `json:"version"`
	SavedAt     time.Time   `json:"saved_at"`
	Channel     string      `json:"channel"`
	ChannelName string      `json:"channel_name"`
	Autoplay    bool        `json:"autoplay"`
	Held        bool        `json:"held"`
	Index       int         `json:"index"`
	Position    float64     `json:"position"` // seconds
	Paused      bool        `json:"paused"`
	Queue       []savedRow  `json:"queue"`
	Lanes       []savedLane `json:"lanes"`
}

func (r *Room) statePath() string {
	if r.cfg.StateDir == "" {
		return ""
	}
	return filepath.Join(r.cfg.StateDir, r.cfg.Guild.String()+".json")
}

// Save writes the room's state now. A room that is not in a channel has
// nothing to resume and writes nothing.
func (r *Room) Save() error {
	path := r.statePath()
	r.mu.Lock()
	ch, name, autoplay, held := r.channel, r.name, r.autoplay, r.held
	r.mu.Unlock()
	if path == "" || ch == 0 {
		return nil
	}
	snap := r.ctrl.Snapshot()
	s := saved{Version: stateVersion, SavedAt: r.cfg.Deps.Now().UTC(), Channel: ch.String(), ChannelName: name,
		Autoplay: autoplay, Held: held, Index: snap.Index, Position: snap.Position.Seconds(), Paused: snap.Paused}
	for i, t := range snap.Tracks {
		s.Queue = append(s.Queue, savedRow{Track: t, By: snap.Tags[i][0], Name: snap.Tags[i][1]})
	}
	for _, l := range snap.Lanes {
		s.Lanes = append(s.Lanes, savedLane{By: l.By, Name: l.Name, Tracks: l.Tracks})
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(r.cfg.StateDir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // never a half-written file
}

// forget removes the state file: a deliberate leave is not resumed.
func (r *Room) forget() {
	if path := r.statePath(); path != "" {
		os.Remove(path)
	}
}

// markDirty asks the saver to write soon; bursts of changes coalesce.
func (r *Room) markDirty() {
	select {
	case r.dirty <- struct{}{}:
	default:
	}
}

func (r *Room) saver(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.dirty:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(saveDebounce):
		}
		if err := r.Save(); err != nil {
			r.cfg.Logger.Warn("save state", "err", err)
		}
	}
}

// Shutdown saves and leaves voice without clearing the queue, so the next
// start can resume. For process exit only.
func (r *Room) Shutdown(ctx context.Context) {
	r.joinMu.Lock()
	defer r.joinMu.Unlock()
	if err := r.Save(); err != nil {
		r.cfg.Logger.Warn("save state", "err", err)
	}
	r.mu.Lock()
	if r.channel == 0 {
		r.mu.Unlock()
		return
	}
	stop, conn := r.stop, r.conn
	r.channel, r.name, r.conn, r.stop = 0, "", nil, nil
	r.mu.Unlock()
	r.commit.Lock()
	r.gen++ // no radio pick lands after this
	r.commit.Unlock()
	close(stop)
	conn.Close(ctx)
}

// Resume reads the saved state. It rejoins the saved channel and plays on
// when someone is there; otherwise it loads the queue for the next Join.
// It reports whether it rejoined.
func (r *Room) Resume(ctx context.Context) (bool, error) {
	path := r.statePath()
	if path == "" {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var s saved
	if err := json.Unmarshal(b, &s); err != nil || s.Version != stateVersion {
		r.cfg.Logger.Warn("state file unreadable; set aside", "err", err)
		os.Rename(path, path+".bad")
		return false, nil
	}
	if r.cfg.Deps.Now().Sub(s.SavedAt) > staleAfter || len(s.Queue) == 0 && len(s.Lanes) == 0 && !s.Held {
		os.Remove(path)
		return false, nil
	}
	snap := audio.Snapshot{Index: s.Index, Paused: s.Paused,
		Position: max(time.Duration(s.Position*float64(time.Second))-rewind, 0)}
	for _, row := range s.Queue {
		snap.Tracks = append(snap.Tracks, row.Track)
		snap.Tags = append(snap.Tags, [2]string{row.By, row.Name})
	}
	for _, l := range s.Lanes {
		snap.Lanes = append(snap.Lanes, audio.Lane{By: l.By, Name: l.Name, Tracks: l.Tracks})
	}
	r.mu.Lock()
	r.autoplay, r.held = s.Autoplay, s.Held
	r.mu.Unlock()
	ch, _ := snowflake.Parse(s.Channel)
	if ch == 0 || r.cfg.Deps.Listeners(ch) == 0 {
		if err := r.ctrl.Restore(ctx, snap, false); err != nil {
			return false, err
		}
		r.mu.Lock()
		r.pending = len(snap.Tracks) > 0
		r.mu.Unlock()
		return false, nil
	}
	played := false
	if len(snap.Tracks) > 0 {
		if err := r.ctrl.Restore(context.WithoutCancel(ctx), snap, true); err != nil {
			// The song can't be opened right now: load the queue anyway and
			// let Join start it, which skips what won't play.
			r.cfg.Logger.Warn("resume: current song won't open; carrying on", "err", err)
		} else {
			played = true
		}
	}
	if !played {
		if err := r.ctrl.Restore(ctx, snap, false); err != nil {
			return false, err
		}
		r.mu.Lock()
		r.pending = len(snap.Tracks) > 0
		r.mu.Unlock()
	}
	if err := r.Join(ctx, ch, s.ChannelName); err != nil {
		return false, err
	}
	if cur, ok := r.ctrl.Current(); ok {
		r.cfg.Deps.Post(ch, "Back, picking up "+cur.Title)
	}
	return true, nil
}
