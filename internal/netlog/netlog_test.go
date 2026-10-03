package netlog

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTransportLogsRedactedRequestsWhenEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(nil)
	client := &http.Client{Transport: New()}

	Enabled.Store(false)
	if _, err := client.Get(srv.URL + "/quiet?X-Plex-Token=sekrit"); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("logged while disabled: %q", buf.String())
	}

	Enabled.Store(true)
	if _, err := client.Get(srv.URL + "/library/parts/1?X-Plex-Token=sekrit"); err != nil {
		t.Fatal(err)
	}
	line := buf.String()
	if !strings.Contains(line, "GET") || !strings.Contains(line, "/library/parts/1") || !strings.Contains(line, "204") {
		t.Fatalf("log line = %q", line)
	}
	if strings.Contains(line, "sekrit") {
		t.Fatalf("log line leaks the token: %q", line)
	}
}
