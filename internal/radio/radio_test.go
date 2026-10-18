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
	similar []library.Track
	err     error
	albums  []library.Album
	tracks  map[string][]library.Track
}

func (l lib) Similar(context.Context, library.Track, int) ([]library.Track, error) {
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
