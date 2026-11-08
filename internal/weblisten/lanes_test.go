package weblisten

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeLanes struct {
	mu    sync.Mutex
	songs map[string][]Song
	calls []string
}

func (l *fakeLanes) Mine(user string) []Song {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.songs[user]
}

func (l *fakeLanes) Search(_ context.Context, q string) []Choice {
	return []Choice{{Label: "♪ " + q, Value: "t:1"}}
}

func (l *fakeLanes) Add(_ context.Context, user, value string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "add "+user+" "+value)
	if value == "t:full" {
		return "", errors.New("You have 10 songs waiting; wait for some to play")
	}
	return "Added Shine", nil
}

func (l *fakeLanes) Remove(_ context.Context, user string, k int, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "remove "+user+" "+id)
	if id == "gone" {
		return errors.New("That song has moved or played")
	}
	return nil
}

func (l *fakeLanes) Top(_ context.Context, user string, k int, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, "top "+user+" "+id)
	return nil
}

func setupLanes(t *testing.T) (*httptest.Server, *fakeLanes, string) {
	t.Helper()
	room := &fakeRoom{}
	room.live.Store(true)
	lanes := &fakeLanes{songs: map[string][]Song{"u1": {{ID: "s1", Title: "Shine", Artist: "Collective Soul", Next: true}}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(NewServer(NewHub(room, 5, log), room, lanes, secret, time.Now, log))
	t.Cleanup(srv.Close)
	return srv, lanes, Sign(secret, "u1", time.Now().Add(time.Hour))
}

func post(t *testing.T, url string, body any) (int, map[string]string) {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]string
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestNowCarriesTheLinkOwnersSongs(t *testing.T) {
	srv, _, tok := setupLanes(t)
	res, body := get(t, srv.URL+"/l/"+tok+"/now.json")
	var n Now
	if res.StatusCode != 200 || json.Unmarshal([]byte(body), &n) != nil {
		t.Fatalf("status %d body %s", res.StatusCode, body)
	}
	if len(n.Mine) != 1 || n.Mine[0].ID != "s1" || !n.Mine[0].Next || !n.Lanes {
		t.Fatalf("mine = %+v lanes=%v", n.Mine, n.Lanes)
	}
}

func TestSearchAddRemoveTopActAsTheLinkOwner(t *testing.T) {
	srv, lanes, tok := setupLanes(t)
	base := srv.URL + "/l/" + tok
	res, body := get(t, base+"/search?q=shine")
	var cs []Choice
	if res.StatusCode != 200 || json.Unmarshal([]byte(body), &cs) != nil || len(cs) != 1 || cs[0].Value != "t:1" {
		t.Fatalf("search: %d %s", res.StatusCode, body)
	}
	if code, out := post(t, base+"/add", map[string]string{"value": "t:1"}); code != 200 || out["message"] != "Added Shine" {
		t.Fatalf("add: %d %v", code, out)
	}
	if code, out := post(t, base+"/add", map[string]string{"value": "t:full"}); code != 409 || !strings.Contains(out["error"], "10 songs") {
		t.Fatalf("refused add: %d %v", code, out)
	}
	if code, _ := post(t, base+"/remove", map[string]any{"k": 0, "id": "s1"}); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if code, out := post(t, base+"/remove", map[string]any{"k": 0, "id": "gone"}); code != 409 || out["error"] == "" {
		t.Fatalf("stale remove: %d %v", code, out)
	}
	if code, _ := post(t, base+"/top", map[string]any{"k": 1, "id": "s2"}); code != 200 {
		t.Fatalf("top: %d", code)
	}
	want := "add u1 t:1|add u1 t:full|remove u1 s1|remove u1 gone|top u1 s2"
	if got := strings.Join(lanes.calls, "|"); got != want {
		t.Fatalf("calls = %s", got)
	}
}

func TestLaneChangesNeedAValidLink(t *testing.T) {
	srv, lanes, _ := setupLanes(t)
	expired := Sign(secret, "u1", time.Now().Add(-time.Minute))
	for _, path := range []string{"/add", "/remove", "/top"} {
		if code, _ := post(t, srv.URL+"/l/"+expired+path, map[string]any{"value": "t:1", "k": 0, "id": "s1"}); code != 403 {
			t.Fatalf("%s with an expired link: %d", path, code)
		}
	}
	if res, _ := get(t, srv.URL+"/l/"+expired+"/search?q=x"); res.StatusCode != 403 {
		t.Fatalf("search with an expired link: %d", res.StatusCode)
	}
	if len(lanes.calls) != 0 {
		t.Fatalf("calls = %v", lanes.calls)
	}
}

func TestWithoutLanesThePageIsListenOnly(t *testing.T) {
	srv, _, _, tok := setup(t, 5)
	res, body := get(t, srv.URL+"/l/"+tok+"/now.json")
	var n Now
	json.Unmarshal([]byte(body), &n)
	if res.StatusCode != 200 || n.Lanes {
		t.Fatalf("lanes advertised without a lane backend: %s", body)
	}
	if code, _ := post(t, srv.URL+"/l/"+tok+"/add", map[string]string{"value": "t:1"}); code != 404 {
		t.Fatalf("add without lanes: %d", code)
	}
}
