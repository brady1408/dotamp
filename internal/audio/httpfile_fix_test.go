package audio

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenHTTPConnectionErrorRedactsQuery(t *testing.T) {
	srv := httptest.NewServer(nil)
	url := srv.URL + "/a.flac?X-Plex-Token=sekrit"
	srv.Close() // refused from here on
	_, err := OpenHTTP(context.Background(), url, nil)
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if strings.Contains(err.Error(), "sekrit") {
		t.Fatalf("error leaks the query string: %v", err)
	}
}
