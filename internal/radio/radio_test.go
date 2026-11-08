package radio

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
)

type lib struct {
	similar []library.Track            // what every seed sounds like, unless bySeed says otherwise
	bySeed  map[string][]library.Track // per seed ID
	err     error
	albums  []library.Album
	tracks  map[string][]library.Track
}

func (l lib) Similar(_ context.Context, t library.Track, _ int) ([]library.Track, error) {
	if ts, ok := l.bySeed[t.ID]; ok {
		return ts, l.err
	}
	return l.similar, l.err
}
func (l lib) RecentAlbums(context.Context, int, int) ([]library.Album, error) { return l.albums, nil }
func (l lib) AlbumTracks(_ context.Context, id string) ([]library.Track, error) {
	return l.tracks[id], nil
}

func tr(id, artist string) library.Track {
	return library.Track{ID: id, Title: id, Artist: artist, Duration: 200 * time.Second}
}
func played(t library.Track, ago time.Duration, now time.Time, secs float64) history.Entry {
	return history.Entry{At: now.Add(-ago), Played: secs, Track: t}
}
func fixed() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestPickFollowsSoundAndSkipsWhatJustPlayed(t *testing.T) {
	now := time.Now()
	last := tr("x", "A")
	l := lib{similar: []library.Track{tr("s1", "B"), tr("s2", "C"), tr("s3", "D")}}
	in := Input{Last: &last, Now: now, Rand: fixed(),
		History: []history.Entry{played(tr("s1", "B"), time.Hour, now, 200)},
		Queue:   []library.Track{tr("s2", "C")}}
	got, err := Pick(context.Background(), l, in)
	if err != nil || got.ID != "s3" {
		t.Fatalf("got %v %v", got.ID, err)
	}
}

func TestPickAvoidsAThirdSongInARowByOneArtist(t *testing.T) {
	now := time.Now()
	last := tr("x2", "A")
	l := lib{similar: []library.Track{tr("a3", "A"), tr("b1", "B")}}
	in := Input{Last: &last, Now: now, Rand: fixed(), History: []history.Entry{
		played(tr("x1", "A"), 10*time.Minute, now, 200), played(last, 5*time.Minute, now, 200)}}
	if got, _ := Pick(context.Background(), l, in); got.ID != "b1" {
		t.Fatalf("got %s", got.ID)
	}
}

func TestPickDropsTracksTheRoomSkips(t *testing.T) {
	now := time.Now()
	last := tr("x", "A")
	bad := tr("bad", "B")
	l := lib{similar: []library.Track{bad, tr("ok", "C")}}
	in := Input{Last: &last, Now: now, Rand: fixed(), History: []history.Entry{
		played(bad, 30*24*time.Hour, now, 10), played(bad, 20*24*time.Hour, now, 10)}}
	if got, _ := Pick(context.Background(), l, in); got.ID != "ok" {
		t.Fatalf("got %s", got.ID)
	}
}

func TestPickFallsBackToRoomFavouritesThenRecentAlbums(t *testing.T) {
	now := time.Now()
	fav := tr("fav", "F")
	in := Input{Now: now, Rand: fixed(), History: []history.Entry{played(fav, 30*24*time.Hour, now, 200)}}
	if got, _ := Pick(context.Background(), lib{}, in); got.ID != "fav" {
		t.Fatalf("history fallback got %s", got.ID)
	}
	cold := lib{albums: []library.Album{{ID: "al"}}, tracks: map[string][]library.Track{"al": {tr("t1", "Z")}}}
	if got, err := Pick(context.Background(), cold, Input{Now: now, Rand: fixed()}); err != nil || got.ID != "t1" {
		t.Fatalf("cold start got %s %v", got.ID, err)
	}
	if _, err := Pick(context.Background(), lib{}, Input{Now: now, Rand: fixed()}); !errors.Is(err, ErrNothing) {
		t.Fatalf("empty library: %v", err)
	}
}

func TestPickChoosesAmongTheTopFive(t *testing.T) {
	now := time.Now()
	last := tr("x", "A")
	var sim []library.Track
	for i := 0; i < 12; i++ {
		sim = append(sim, tr(fmt.Sprintf("s%d", i), fmt.Sprintf("art%d", i)))
	}
	seen := map[string]bool{}
	for seed := uint64(0); seed < 50; seed++ {
		got, _ := Pick(context.Background(), lib{similar: sim}, Input{Last: &last, Now: now, Rand: rand.New(rand.NewPCG(seed, seed))})
		seen[got.ID] = true
	}
	for id := range seen {
		var n int
		fmt.Sscanf(id, "s%d", &n)
		if n >= 5 {
			t.Fatalf("picked %s, outside the top five", id)
		}
	}
	if len(seen) < 2 {
		t.Fatal("no variety among the top five")
	}
}

func TestPickFollowsTheNightNotJustTheLastSong(t *testing.T) {
	now := time.Now()
	n1, n2, n3, last := tr("n1", "N1"), tr("n2", "N2"), tr("n3", "N3"), tr("odd", "O")
	l := lib{bySeed: map[string][]library.Track{
		"n1":  {tr("vibe", "V"), tr("o1", "X1")},
		"n2":  {tr("vibe", "V"), tr("o2", "X2")},
		"n3":  {tr("vibe", "V"), tr("o3", "X3")},
		"odd": {tr("polka", "P")},
	}}
	in := Input{Last: &last, Now: now, Rand: fixed(), History: []history.Entry{
		played(n1, 40*time.Minute, now, 200), played(n2, 30*time.Minute, now, 200),
		played(n3, 20*time.Minute, now, 200)}}
	for seed := uint64(0); seed < 20; seed++ {
		in.Rand = rand.New(rand.NewPCG(seed, seed))
		got, err := Pick(context.Background(), l, in)
		if err != nil {
			t.Fatal(err)
		}
		if got.ID == "polka" {
			t.Fatalf("seed %d: the odd last song outvoted the night", seed)
		}
	}
	in.Rand = fixed()
	if got := topCandidate(t, l, in); got != "vibe" {
		t.Fatalf("top candidate %s, want the song the whole night points at", got)
	}
}

func TestPickPushesDownWhatSoundsLikeTonightsSkips(t *testing.T) {
	now := time.Now()
	good, bad, last := tr("good", "G"), tr("bad", "B"), tr("x", "A")
	l := lib{bySeed: map[string][]library.Track{
		"x":    {tr("dud", "D"), tr("fine", "F")},
		"good": {tr("fine", "F")},
		"bad":  {tr("dud", "D")},
	}}
	in := Input{Last: &last, Now: now, Rand: fixed(), History: []history.Entry{
		played(good, 30*time.Minute, now, 200), played(bad, 10*time.Minute, now, 15)}}
	if got := topCandidate(t, l, in); got != "fine" {
		t.Fatalf("top candidate %s, want fine", got)
	}
}

func TestPickForgetsLastNightAfterAGap(t *testing.T) {
	now := time.Now()
	old, last := tr("old", "Q"), tr("x", "A")
	l := lib{bySeed: map[string][]library.Track{
		"old": {tr("stale", "S"), tr("stale2", "S2"), tr("stale3", "S3")},
		"x":   {tr("fresh", "F")},
	}}
	in := Input{Last: &last, Now: now, Rand: fixed(), History: []history.Entry{
		played(old, 5*time.Hour, now, 200), played(last, 2*time.Minute, now, 200)}}
	if got, _ := Pick(context.Background(), l, in); got.ID != "fresh" {
		t.Fatalf("got %s; a song from before the gap still seeded the radio", got.ID)
	}
}

func TestNightStartsAfterTheLastLongGap(t *testing.T) {
	now := time.Now()
	es := []history.Entry{played(tr("a", "A"), 6*time.Hour, now, 200), played(tr("b", "B"), 3*time.Hour, now, 200),
		played(tr("c", "C"), 50*time.Minute, now, 200), played(tr("d", "D"), 5*time.Minute, now, 200)}
	got := night(es, now)
	if len(got) != 2 || got[0].Track.ID != "c" {
		t.Fatalf("night = %v", got)
	}
	if got := night(es[:2], now); len(got) != 0 {
		t.Fatalf("a night that ended hours ago is not tonight: %v", got)
	}
}

// topCandidate is the best-scored pick, read off a run with one candidate slot.
func topCandidate(t *testing.T, l lib, in Input) string {
	t.Helper()
	cs := candidates(context.Background(), l, in, func(library.Track) bool { return true })
	if len(cs) == 0 {
		t.Fatal("no candidates")
	}
	return cs[0].ID
}

func TestRadioPicksNeverSeedTheRadio(t *testing.T) {
	now := time.Now()
	asked, wander, last := tr("asked", "Q"), tr("wander", "W"), tr("cur", "C")
	l := lib{bySeed: map[string][]library.Track{
		"asked":  {tr("home", "H")},
		"wander": {tr("deeper", "D"), tr("deeper2", "D2"), tr("deeper3", "D3")},
		"cur":    {tr("deeper", "D")},
	}}
	radioPlay := played(wander, 10*time.Minute, now, 200)
	radioPlay.By = RadioBy
	in := Input{Last: &last, LastBy: RadioBy, Now: now, Rand: fixed(), History: []history.Entry{
		played(asked, 30*time.Minute, now, 200), radioPlay}}
	if got := topCandidate(t, l, in); got != "home" {
		t.Fatalf("top candidate %s; the radio followed its own picks", got)
	}
}
