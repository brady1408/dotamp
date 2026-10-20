package plex

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brady1408/dotamp/internal/library"
)

func TestSimilarAsksForNearest(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"69463","title":"Touch of Grey","grandparentTitle":"Grateful Dead","parentTitle":"In the Dark","duration":350000}]}}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", "cid")
	ts, err := c.Similar(context.Background(), library.Track{ID: "48673"}, 5)
	if err != nil || len(ts) != 1 || ts[0].ID != "69463" || ts[0].Artist != "Grateful Dead" {
		t.Fatalf("%v %+v", err, ts)
	}
	if !strings.HasPrefix(path, "/library/metadata/48673/nearest?") || !strings.Contains(path, "limit=5") {
		t.Fatalf("path = %s", path)
	}
}

func TestTrackArtistComesFromOriginalTitle(t *testing.T) {
	c := New("http://x", "tok", "cid")
	feat := c.toTrack(metadata{RatingKey: "255289", Title: "Take Me to the Beach", GrandparentTitle: "Imagine Dragons", OriginalTitle: "Imagine Dragons feat. Ado"})
	plain := c.toTrack(metadata{RatingKey: "159561", Title: "Take Me to the Beach", GrandparentTitle: "Imagine Dragons"})
	if feat.Artist != "Imagine Dragons feat. Ado" || plain.Artist != "Imagine Dragons" {
		t.Fatalf("artists = %q, %q", feat.Artist, plain.Artist)
	}
}
