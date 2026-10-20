package requests

import (
	"context"
	"strings"
	"testing"

	"github.com/disgoorg/disgo/discord"

	"github.com/brady1408/dotamp/internal/library"
)

type lib struct{ library.Library }

func (lib) Search(_ context.Context, q string) (library.SearchResult, error) {
	if strings.Contains(strings.ToLower(q), "god") {
		return library.SearchResult{Tracks: []library.Track{
			{ID: "s:7", Title: "God Is a Weapon", Artist: "Falling in Reverse"},
			{ID: "s:8", Title: "God is a woman", Artist: "Ariana Grande"},
		}}, nil
	}
	return library.SearchResult{
		Tracks: []library.Track{{ID: "s:1", Title: "Heavy", Artist: "Collective Soul"}},
		Albums: []library.Album{{ID: "s:9", Title: "Dosage", Artist: "Collective Soul"}},
	}, nil
}
func (lib) Playlists(context.Context) ([]library.Playlist, error) {
	return []library.Playlist{{ID: "s:p1", Name: "Road Trip"}}, nil
}
func (lib) AlbumTracks(context.Context, string) ([]library.Track, error) {
	return []library.Track{{ID: "s:2", Album: "Dosage"}, {ID: "s:3", Album: "Dosage"}}, nil
}
func (lib) PlaylistTracks(context.Context, string) ([]library.Track, error) {
	return []library.Track{{ID: "s:4"}, {ID: "s:5"}, {ID: "s:6"}}, nil
}

func core() *Core { return New(func() library.Library { return lib{} }) }

func TestResolveKinds(t *testing.T) {
	c := core()
	c.Complete(context.Background(), "")
	cases := []struct {
		what, label, refusal string
		n                    int
	}{
		{"t:s:1", "Heavy · Collective Soul", "", 1},
		{"a:s:9", "Dosage", "", 2},
		{"p:s:p1", "Road Trip", "", 3},
		{"heavy", "Heavy · Collective Soul", "", 1},
		{"ariana grande god is a woman", "God is a woman · Ariana Grande", "", 1},
		{"t:s:404", "", "That choice expired; search again", 0},
	}
	for _, k := range cases {
		ts, label, refusal := c.Resolve(context.Background(), k.what)
		if len(ts) != k.n || label != k.label || refusal != k.refusal {
			t.Fatalf("%q → %d %q %q", k.what, len(ts), label, refusal)
		}
	}
	if !c.Expired("t:s:404") || c.Expired("t:s:1") || c.Expired("heavy") {
		t.Fatal("Expired is wrong")
	}
}

func TestResolveRefusesAPoorTypedMatch(t *testing.T) {
	_, _, refusal := core().Resolve(context.Background(), "god bless the broken road")
	if !strings.HasPrefix(refusal, `Nothing close to "god bless the broken road"`) {
		t.Fatalf("refusal = %q", refusal)
	}
}

func TestCompleteLabelsTellVersionsApart(t *testing.T) {
	c := New(func() library.Library { return versions{} })
	var names []string
	for _, ch := range c.Complete(context.Background(), "beach") {
		names = append(names, ch.(discord.AutocompleteChoiceString).Name)
	}
	want := []string{
		"♪ Take Me to the Beach · Imagine Dragons — LOOM",
		"♪ Take Me to the Beach · Imagine Dragons feat. Ado — Take Me to the Beach",
	}
	if strings.Join(names, " | ") != strings.Join(want, " | ") {
		t.Fatalf("names = %q", names)
	}
}

type versions struct{ library.Library }

func (versions) Search(context.Context, string) (library.SearchResult, error) {
	return library.SearchResult{Tracks: []library.Track{
		{ID: "s:1", Title: "Take Me to the Beach", Artist: "Imagine Dragons", Album: "LOOM"},
		{ID: "s:2", Title: "Take Me to the Beach", Artist: "Imagine Dragons feat. Ado", Album: "Take Me to the Beach"},
	}}, nil
}
func (versions) Playlists(context.Context) ([]library.Playlist, error) { return nil, nil }
