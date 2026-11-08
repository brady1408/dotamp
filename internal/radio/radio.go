// Package radio picks the next song when nobody has asked for one: what
// sounds like tonight's songs, shaped by what the room plays through and
// skips, never what it just heard.
package radio

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
)

// RadioBy is the requester id and name on radio rows and history lines.
const RadioBy = "radio"

// RecentWindow is how long a played song stays off the radio.
const RecentWindow = 4 * time.Hour

// NightGap is the silence that ends a night: songs before it no longer
// seed the radio.
const NightGap = 2 * time.Hour

// Seeds is how many of tonight's songs the radio asks for sound-alikes,
// and SkipSeeds how many skips push theirs down.
const (
	Seeds     = 6
	SkipSeeds = 3
	halfLife  = 90 * time.Minute // a song's pull on the radio halves this long after it played
)

var ErrNothing = errors.New("radio: nothing to play")

type Library interface {
	library.Similar
	RecentAlbums(ctx context.Context, offset, limit int) ([]library.Album, error)
	AlbumTracks(ctx context.Context, albumID string) ([]library.Track, error)
}

type Input struct {
	Last    *library.Track  // the song that just played or is playing; nil when cold
	LastBy  string          // who asked for Last; RadioBy when the radio did
	History []history.Entry // this room's history, oldest first
	Queue   []library.Track // what is queued now
	Now     time.Time
	Rand    *rand.Rand
}

// Pick chooses one track: among the five that best sound like tonight, else
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
	cands := candidates(ctx, lib, in, ok)
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

// night is the tail of the history since the last NightGap of silence;
// empty when the room has been quiet longer than that.
func night(es []history.Entry, now time.Time) []history.Entry {
	if len(es) == 0 || now.Sub(es[len(es)-1].At) > NightGap {
		return nil
	}
	i := len(es) - 1
	for i > 0 && es[i].At.Sub(es[i-1].At) <= NightGap {
		i--
	}
	return es[i:]
}

type seed struct {
	t library.Track
	w float64
}

// seeds weighs tonight's songs: played through counts for, skipped counts
// against, and the pull fades with age. The radio's own picks never count
// for, so it can't wander off down its own rabbit hole; skipping one still
// counts against. Last, when someone asked for it, seeds at full weight;
// a radio Last seeds only when nobody has asked for anything tonight.
func seeds(in Input) (pos, neg []seed) {
	w := map[string]*seed{}
	var order []string
	add := func(t library.Track, v float64) {
		s, ok := w[t.ID]
		if !ok {
			s = &seed{t: t}
			w[t.ID] = s
			order = append(order, t.ID)
		}
		s.w += v
	}
	for _, e := range night(in.History, in.Now) {
		v := 1.0
		if dur := e.Track.Duration.Seconds(); dur > 0 && e.Played/dur < 0.5 {
			v = -1
		} else if e.By == RadioBy {
			continue
		}
		age := math.Max(in.Now.Sub(e.At).Minutes(), 0)
		add(e.Track, v*math.Pow(0.5, age/halfLife.Minutes()))
	}
	if in.Last != nil && in.LastBy != RadioBy {
		add(*in.Last, 0)
		w[in.Last.ID].w = math.Max(w[in.Last.ID].w, 1)
	}
	asked := false
	for _, s := range w {
		asked = asked || s.w > 0
	}
	if !asked && in.Last != nil { // nobody asked for anything tonight: follow what plays
		add(*in.Last, 0)
		w[in.Last.ID].w = math.Max(w[in.Last.ID].w, 1)
	}
	for _, id := range order {
		if s := w[id]; s.w > 0 {
			pos = append(pos, *s)
		} else if s.w < 0 {
			neg = append(neg, *s)
		}
	}
	sort.SliceStable(pos, func(i, j int) bool { return pos[i].w > pos[j].w })
	sort.SliceStable(neg, func(i, j int) bool { return neg[i].w < neg[j].w })
	return pos[:min(Seeds, len(pos))], neg[:min(SkipSeeds, len(neg))]
}

// candidates scores every sound-alike of tonight's seeds: each seed adds
// its weight, scaled from 1 for its closest match down to 0.5 for its
// twentieth, and skips subtract the same way. Best first; only songs a
// wanted seed suggested that pass ok and score at least half the best, so
// one stray seed's matches don't ride along with what the night points at.
func candidates(ctx context.Context, lib Library, in Input, ok func(library.Track) bool) []library.Track {
	pos, neg := seeds(in)
	all := append(append([]seed(nil), pos...), neg...)
	lists := make([][]library.Track, len(all))
	var wg sync.WaitGroup
	for i, s := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ts, err := lib.Similar(ctx, s.t, 20); err == nil {
				lists[i] = ts
			}
		}()
	}
	wg.Wait()
	score := map[string]float64{}
	liked := map[string]bool{} // suggested by at least one song the room wanted
	first := map[string]int{}  // tie-break: the order songs were first suggested in
	var tracks []library.Track
	for i, s := range all {
		for r, t := range lists[i] {
			if _, seen := first[t.ID]; !seen {
				first[t.ID] = len(tracks)
				tracks = append(tracks, t)
			}
			score[t.ID] += s.w * (1 - 0.5*float64(r)/20)
			liked[t.ID] = liked[t.ID] || s.w > 0
		}
	}
	var out []library.Track
	for _, t := range tracks {
		if liked[t.ID] && ok(t) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return score[out[i].ID] > score[out[j].ID] })
	if len(out) > 0 && score[out[0].ID] > 0 {
		for i, t := range out {
			if score[t.ID] < score[out[0].ID]/2 {
				return out[:i]
			}
		}
	}
	return out
}
