package subsonic

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brady1408/dotamp/internal/library"
)

func TestSimilarAsksForSimilarSongs2(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path + " id=" + r.URL.Query().Get("id") + " count=" + r.URL.Query().Get("count")
		io.WriteString(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","similarSongs2":{"song":[{"id":"b6","title":"So Far Away","artist":"Dire Straits","album":"Brothers in Arms","duration":312}]}}}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "brady", "secret")
	ts, err := c.Similar(context.Background(), library.Track{ID: "GU"}, 5)
	if err != nil || len(ts) != 1 || ts[0].ID != "b6" || ts[0].Album != "Brothers in Arms" {
		t.Fatalf("%v %+v", err, ts)
	}
	if got != "/rest/getSimilarSongs2 id=GU count=5" {
		t.Fatalf("request = %s", got)
	}
}

func TestTransportErrorsDoNotCarryTheAuthToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close() // the server drops the connection
	}))
	defer srv.Close()
	_, err := New(srv.URL, "brady", "secret").Similar(context.Background(), library.Track{ID: "x"}, 1)
	if err == nil {
		t.Fatal("expected an error")
	}
	if s := err.Error(); strings.Contains(s, "t=") || strings.Contains(s, "s=") || strings.Contains(s, "u=brady") {
		t.Fatalf("error leaks credentials: %s", s)
	}
}
