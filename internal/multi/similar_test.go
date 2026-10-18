package multi

import (
	"context"
	"testing"

	"github.com/brady1408/dotamp/internal/library"
)

type similarFake struct{ *fake }

func (similarFake) Similar(_ context.Context, t library.Track, n int) ([]library.Track, error) {
	return []library.Track{{ID: "9", AlbumID: "al"}}, nil
}

func TestSimilarRoutesByServerAndTagsResults(t *testing.T) {
	m := New()
	m.Add(Server{ID: "a", Name: "A", Owned: true}, similarFake{&fake{id: "a"}})
	m.Add(Server{ID: "b", Name: "B"}, &fake{id: "b"})
	ts, err := m.Similar(context.Background(), library.Track{ID: "a:1"}, 5)
	if err != nil || len(ts) != 1 || ts[0].ID != "a:9" || ts[0].Server != "a" || ts[0].AlbumID != "a:al" {
		t.Fatalf("%v %+v", err, ts)
	}
	if ts, err := m.Similar(context.Background(), library.Track{ID: "b:1"}, 5); err != nil || ts != nil {
		t.Fatalf("a backend without Similar: %v %+v", err, ts)
	}
}
