package broadcast

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

type fakePlayer struct {
	mu       sync.Mutex
	requests []string // "name:id,id"
	cur      *library.Track
	lanes    []audio.Lane
	tooMany  bool
}

func joinIDs(ts []library.Track) string {
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	return strings.Join(ids, ",")
}

func (p *fakePlayer) Request(_ context.Context, by, name string, ts []library.Track) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tooMany {
		return 7, audio.ErrTooManyPending
	}
	p.requests = append(p.requests, name+":"+joinIDs(ts))
	return len(ts), nil
}
func (p *fakePlayer) Enqueue(ts []library.Track) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, "DJ:"+joinIDs(ts))
}
func (p *fakePlayer) Current() (library.Track, time.Duration, string, bool) {
	if p.cur == nil {
		return library.Track{}, 0, "", false
	}
	return *p.cur, 72 * time.Second, "Alice", true
}
func (p *fakePlayer) Upcoming(n int) []Upcoming {
	return []Upcoming{{Track: library.Track{Title: "Shine", Artist: "Collective Soul"}, Name: "Bob"}}
}
func (p *fakePlayer) Lanes() []audio.Lane { return p.lanes }

type fakeLib struct {
	library.Library // nil: only the methods below are called
	delay           time.Duration
}

func (l fakeLib) Search(ctx context.Context, q string) (library.SearchResult, error) {
	select {
	case <-time.After(l.delay):
	case <-ctx.Done():
		return library.SearchResult{}, ctx.Err()
	}
	return library.SearchResult{
		Tracks: []library.Track{{ID: "s:1", Title: "Heavy", Artist: "Collective Soul"}},
		Albums: []library.Album{{ID: "s:9", Title: "Dosage", Artist: "Collective Soul"}},
	}, nil
}
func (l fakeLib) Playlists(context.Context) ([]library.Playlist, error) {
	return []library.Playlist{{ID: "s:p1", Name: "Road Trip", TrackCount: 3}}, nil
}
func (l fakeLib) AlbumTracks(_ context.Context, id string) ([]library.Track, error) {
	return []library.Track{{ID: "s:2", Album: "Dosage"}, {ID: "s:3", Album: "Dosage"}}, nil
}
func (l fakeLib) PlaylistTracks(_ context.Context, id string) ([]library.Track, error) {
	return []library.Track{{ID: "s:4"}, {ID: "s:5"}, {ID: "s:6"}}, nil
}

// live puts the harness broadcaster in chan-10 (guild 1) with a fake player.
func live(t *testing.T, h *harness, p *fakePlayer, lib library.Library) {
	t.Helper()
	h.b.cfg.Player, h.b.cfg.Library, h.b.cfg.Commands = p, lib, true
	if err := h.b.Toggle(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stream", func() bool { return h.b.streams.Load() == 1 })
}

func choiceStrings(cs []discord.AutocompleteChoice) []string {
	var out []string
	for _, c := range cs {
		s := c.(discord.AutocompleteChoiceString)
		out = append(out, s.Name+"="+s.Value)
	}
	return out
}

func TestCompleteOffersTracksAlbumsAndPlaylists(t *testing.T) {
	h := newHarness(t)
	live(t, h, &fakePlayer{}, fakeLib{})
	got := strings.Join(choiceStrings(h.b.complete(context.Background(), "ro")), " ")
	want := "♪ Heavy · Collective Soul=t:s:1 ◎ Dosage · Collective Soul=a:s:9 ☰ Road Trip=p:s:p1"
	if got != want {
		t.Fatalf("choices = %v", got)
	}
}

func TestCompleteGivesUpAtTheDeadline(t *testing.T) {
	h := newHarness(t)
	live(t, h, &fakePlayer{}, fakeLib{delay: 10 * time.Second})
	start := time.Now()
	cs := h.b.complete(context.Background(), "ro")
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("took %v", took)
	}
	if got := choiceStrings(cs); len(got) != 1 || got[0] != "☰ Road Trip=p:s:p1" {
		t.Fatalf("choices = %v", got)
	}
}

func TestPlayQueuesAChosenTrackPublicly(t *testing.T) {
	h := newHarness(t)
	p := &fakePlayer{}
	live(t, h, p, fakeLib{})
	h.b.complete(context.Background(), "heavy") // offers t:s:1
	r := h.b.command(context.Background(), command{Name: "play", UserID: "u1", UserName: "Alice", What: "t:s:1", Guild: 1, Voice: 10})
	if !r.Public || r.Text != "Alice added Heavy · Collective Soul" {
		t.Fatalf("reply = %+v", r)
	}
	if strings.Join(p.requests, " ") != "Alice:s:1" {
		t.Fatalf("requests = %v", p.requests)
	}
}

func TestPlayQueuesAnAlbumAndAPlaylistWithCounts(t *testing.T) {
	h := newHarness(t)
	p := &fakePlayer{}
	live(t, h, p, fakeLib{})
	h.b.complete(context.Background(), "") // everything is offered
	r := h.b.command(context.Background(), command{Name: "play", UserID: "u1", UserName: "Alice", What: "a:s:9", Guild: 1, Voice: 10})
	if r.Text != "Alice added Dosage (2 songs)" {
		t.Fatalf("album reply = %q", r.Text)
	}
	r = h.b.command(context.Background(), command{Name: "play", UserID: "u1", UserName: "Alice", What: "p:s:p1", Guild: 1, Voice: 10})
	if r.Text != "Alice added Road Trip (3 songs)" {
		t.Fatalf("playlist reply = %q", r.Text)
	}
}

func TestPlayWithFreeTextTakesTheFirstTrackHit(t *testing.T) {
	h := newHarness(t)
	p := &fakePlayer{}
	live(t, h, p, fakeLib{})
	r := h.b.command(context.Background(), command{Name: "play", UserID: "u1", UserName: "Alice", What: "heavy", Guild: 1, Voice: 10})
	if r.Text != "Alice added Heavy · Collective Soul" || len(p.requests) != 1 {
		t.Fatalf("reply = %+v requests %v", r, p.requests)
	}
}

func TestPlayWithAnExpiredChoiceSaysSo(t *testing.T) {
	h := newHarness(t)
	p := &fakePlayer{}
	live(t, h, p, fakeLib{})
	r := h.b.command(context.Background(), command{Name: "play", UserID: "u1", UserName: "Alice", What: "t:s:404", Guild: 1, Voice: 10})
	if r.Public || r.Text != "That choice expired; search again" || len(p.requests) != 0 {
		t.Fatalf("reply = %+v requests %v", r, p.requests)
	}
}

func TestCommandsRefuseWhenNotLiveOrInAnotherChannel(t *testing.T) {
	h := newHarness(t)
	h.b.cfg.Player, h.b.cfg.Library, h.b.cfg.Commands = &fakePlayer{}, fakeLib{}, true
	r := h.b.command(context.Background(), command{Name: "np", Guild: 1, Voice: 10})
	if r.Public || r.Text != "dotamp isn't broadcasting right now" {
		t.Fatalf("reply = %+v", r)
	}
	live(t, h, &fakePlayer{}, fakeLib{})
	r = h.b.command(context.Background(), command{Name: "np", Guild: 1, Voice: 11})
	if r.Text != "Join chan-10 to request songs" {
		t.Fatalf("reply = %+v", r)
	}
}

func TestTheDJsOwnPlayAppendsWithoutALane(t *testing.T) {
	h := newHarness(t) // harness UserID is 100
	p := &fakePlayer{}
	live(t, h, p, fakeLib{})
	r := h.b.command(context.Background(), command{Name: "play", UserID: "100", UserName: "Brady", What: "heavy", Guild: 1, Voice: 10})
	if !r.Public || r.Text != "Brady added Heavy · Collective Soul" || strings.Join(p.requests, " ") != "DJ:s:1" {
		t.Fatalf("reply = %+v requests %v", r, p.requests)
	}
}

func TestCapReplyUsesTheCount(t *testing.T) {
	h := newHarness(t)
	p := &fakePlayer{tooMany: true}
	live(t, h, p, fakeLib{})
	r := h.b.command(context.Background(), command{Name: "play", UserID: "u1", UserName: "Alice", What: "heavy", Guild: 1, Voice: 10})
	if r.Public || r.Text != "You have 7 songs waiting; wait for some to play" {
		t.Fatalf("reply = %+v", r)
	}
}

func TestNpAndQueueDescribeTheRoom(t *testing.T) {
	h := newHarness(t)
	p := &fakePlayer{cur: &library.Track{Title: "Heavy", Artist: "Collective Soul", Duration: 248 * time.Second},
		lanes: []audio.Lane{{Name: "Alice", Tracks: make([]library.Track, 12)}, {Name: "Bob", Tracks: make([]library.Track, 3)}}}
	live(t, h, p, fakeLib{})
	r := h.b.command(context.Background(), command{Name: "np", Guild: 1, Voice: 10})
	if r.Public || r.Text != "Heavy · Collective Soul (1:12 / 4:08), asked by Alice" {
		t.Fatalf("np = %+v", r)
	}
	r = h.b.command(context.Background(), command{Name: "queue", Guild: 1, Voice: 10})
	want := "Now: Heavy · Collective Soul — Alice\n1. Shine · Collective Soul — Bob\nWaiting: Alice 12, Bob 3"
	if r.Public || r.Text != want {
		t.Fatalf("queue = %q", r.Text)
	}
}

func TestPlaylistLabelIsLookedUpWhenNotRecalled(t *testing.T) {
	h := newHarness(t)
	live(t, h, &fakePlayer{}, fakeLib{})
	r := h.b.command(context.Background(), command{Name: "play", UserID: "u1", UserName: "Alice", What: "p:s:p1", Guild: 1, Voice: 10})
	if r.Text != "Alice added Road Trip (3 songs)" {
		t.Fatalf("playlist reply = %q", r.Text)
	}
}
