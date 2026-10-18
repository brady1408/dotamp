// Package radio picks the next song when nobody has asked for one: what
// sounds like the last song, shaped by what the room plays through and
// skips, never what it just heard.
package radio

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
)

// RadioBy is the requester id and name on radio rows and history lines.
const RadioBy = "radio"

// RecentWindow is how long a played song stays off the radio.
const RecentWindow = 4 * time.Hour

var ErrNothing = errors.New("radio: nothing to play")

type Library interface {
	library.Similar
	RecentAlbums(ctx context.Context, offset, limit int) ([]library.Album, error)
	AlbumTracks(ctx context.Context, albumID string) ([]library.Track, error)
}

type Input struct {
	Last    *library.Track  // the song that just played or is playing; nil when cold
	History []history.Entry // this room's history, oldest first
	Queue   []library.Track // what is queued now
	Now     time.Time
	Rand    *rand.Rand
}

// Pick chooses one track: among the five best that sound like Last, else
// the room's favourites, else a track from a recently added album.
func Pick(ctx context.Context, lib Library, in Input) (library.Track, error) {
	recent := map[string]bool{}
	for _, e := range in.History {
		if in.Now.Sub(e.At) <= RecentWindow {
			recent[e.Track.ID] = true
		}
	}
	queued := map[string]bool{}
	for _, t := range in.Queue {
		queued[t.ID] = true
	}
	ranked := history.Score(in.History, in.Now)
	score := map[string]float64{}
	for _, s := range ranked {
		score[s.Track.ID] = s.Score
	}
	streak := runArtist(in)
	ok := func(t library.Track) bool {
		if recent[t.ID] || queued[t.ID] || (in.Last != nil && t.ID == in.Last.ID) {
			return false
		}
		if s, seen := score[t.ID]; seen && s < 0 {
			return false
		}
		return streak == "" || !strings.EqualFold(t.Artist, streak)
	}
	var cands []library.Track
	if in.Last != nil {
		if sim, err := lib.Similar(ctx, *in.Last, 20); err == nil {
			for _, t := range sim {
				if ok(t) {
					cands = append(cands, t)
				}
			}
		}
	}
	if len(cands) == 0 {
		for _, s := range ranked {
			if len(cands) == 50 {
				break
			}
			if ok(s.Track) {
				cands = append(cands, s.Track)
			}
		}
	}
	if len(cands) == 0 {
		return coldStart(ctx, lib, in, ok)
	}
	top := cands[:min(5, len(cands))]
	return top[in.Rand.IntN(len(top))], nil
}

// runArtist is the artist of the last two plays when they share one.
func runArtist(in Input) string {
	n := len(in.History)
	if n < 2 {
		return ""
	}
	a, b := in.History[n-2].Track.Artist, in.History[n-1].Track.Artist
	if a != "" && strings.EqualFold(a, b) {
		return a
	}
	return ""
}

func coldStart(ctx context.Context, lib Library, in Input, ok func(library.Track) bool) (library.Track, error) {
	albums, err := lib.RecentAlbums(ctx, 0, 20)
	if err != nil || len(albums) == 0 {
		return library.Track{}, ErrNothing
	}
	for _, i := range in.Rand.Perm(len(albums)) {
		ts, err := lib.AlbumTracks(ctx, albums[i].ID)
		if err != nil {
			continue
		}
		for _, j := range in.Rand.Perm(len(ts)) {
			if ok(ts[j]) {
				return ts[j], nil
			}
		}
	}
	return library.Track{}, ErrNothing
}
