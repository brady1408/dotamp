package weblisten

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
)

type fakeRoom struct {
	live    atomic.Bool
	mu      sync.Mutex
	feeds   int
	dropped int
}

func (r *fakeRoom) Live() (bool, string) { return r.live.Load(), "A-Team" }
func (r *fakeRoom) Now() Now {
	return Now{Live: r.live.Load(), Channel: "A-Team", Track: &Track{Title: "Heavy", Artist: "Collective Soul", Album: "Dosage", Duration: 248},
		Position: 12, Upcoming: []Upcoming{{Title: "Shine", Artist: "Collective Soul", By: "Alice"}}}
}
func (r *fakeRoom) NewFeed() *audio.Feed {
	r.mu.Lock()
	r.feeds++
	r.mu.Unlock()
	return audio.NewFeed(48000)
}
func (r *fakeRoom) DropFeed(*audio.Feed) { r.mu.Lock(); r.dropped++; r.mu.Unlock() }
func (r *fakeRoom) Rate() int            { return 48000 }

var secret = []byte("0123456789abcdef0123456789abcdef")

func setup(t *testing.T, max int) (*httptest.Server, *fakeRoom, *Hub, string) {
	t.Helper()
	room := &fakeRoom{}
	room.live.Store(true)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(room, max, log)
	hub.checkEvery = 50 * time.Millisecond
	srv := httptest.NewServer(NewServer(hub, room, secret, time.Now, log))
	t.Cleanup(srv.Close)
	return srv, room, hub, Sign(secret, "u1", time.Now().Add(time.Hour))
}

func TestSignVerify(t *testing.T) {
	now := time.Now()
	tok := Sign(secret, "331650444860325888", now.Add(time.Hour))
	if user, ok := Verify(secret, tok, now); !ok || user != "331650444860325888" {
		t.Fatalf("valid token refused: %q %v", user, ok)
	}
	if _, ok := Verify(secret, tok, now.Add(2*time.Hour)); ok {
		t.Fatal("expired token accepted")
	}
	if _, ok := Verify([]byte("another secret, rotated........."), tok, now); ok {
		t.Fatal("token from an old secret accepted")
	}
	payload, sig, _ := strings.Cut(tok, ".")
	if _, ok := Verify(secret, payload+"x."+sig, now); ok {
		t.Fatal("tampered payload accepted")
	}
	if _, ok := Verify(secret, "garbage", now); ok {
		t.Fatal("garbage accepted")
	}
}

func get(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func TestPageNeedsAValidLink(t *testing.T) {
	srv, _, _, tok := setup(t, 25)
	if res, body := get(t, srv.URL+"/l/nope"); res.StatusCode != 403 || !strings.Contains(body, "This link has expired") {
		t.Fatalf("bad link: %d %q", res.StatusCode, body[:min(len(body), 120)])
	}
	if res, body := get(t, srv.URL+"/l/"+tok); res.StatusCode != 200 || !strings.Contains(body, "<audio") || !strings.Contains(body, tok) {
		t.Fatalf("page: %d", res.StatusCode)
	}
	if res, _ := get(t, srv.URL+"/healthz"); res.StatusCode != 200 {
		t.Fatalf("healthz %d", res.StatusCode)
	}
}

func TestNowJSON(t *testing.T) {
	srv, _, _, tok := setup(t, 25)
	res, body := get(t, srv.URL+"/l/"+tok+"/now.json")
	var n Now
	if res.StatusCode != 200 || json.Unmarshal([]byte(body), &n) != nil || !n.Live || n.Track.Title != "Heavy" || n.Upcoming[0].By != "Alice" {
		t.Fatalf("now.json %d %s", res.StatusCode, body)
	}
	if res, _ := get(t, srv.URL+"/l/nope/now.json"); res.StatusCode != 403 {
		t.Fatalf("now.json without a link: %d", res.StatusCode)
	}
}

func TestStreamIsRefusedWhenNotLive(t *testing.T) {
	srv, room, _, tok := setup(t, 25)
	room.live.Store(false)
	if res, _ := get(t, srv.URL+"/l/"+tok+"/stream.ogg"); res.StatusCode != 503 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestStreamSendsOggHeadersThenAudio(t *testing.T) {
	srv, _, _, tok := setup(t, 25)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/l/"+tok+"/stream.ogg", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "audio/ogg" {
		t.Fatalf("%d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	r := bufio.NewReader(res.Body)
	head := make([]byte, 64)
	if _, err := io.ReadFull(r, head); err != nil || string(head[:4]) != "OggS" || !strings.Contains(string(head), "OpusHead") {
		t.Fatalf("stream does not start with an OpusHead page: %q", head[:min(len(head), 40)])
	}
	got := 0
	buf := make([]byte, 4096)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		n, err := r.Read(buf)
		got += strings.Count(string(buf[:n]), "OggS")
		if err != nil {
			break
		}
	}
	if got < 20 {
		t.Fatalf("only %d pages in a second", got)
	}
}

func openStream(t *testing.T, url string) *http.Response {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestTheCapRefusesExtraListeners(t *testing.T) {
	srv, _, _, tok := setup(t, 1)
	first := openStream(t, srv.URL+"/l/"+tok+"/stream.ogg")
	if first.StatusCode != 200 {
		t.Fatalf("first %d", first.StatusCode)
	}
	if res, _ := get(t, srv.URL+"/l/"+tok+"/stream.ogg"); res.StatusCode != 429 {
		t.Fatalf("second listener got %d", res.StatusCode)
	}
}

func TestLeavingDiscordEndsStreams(t *testing.T) {
	srv, room, _, tok := setup(t, 25)
	res := openStream(t, srv.URL+"/l/"+tok+"/stream.ogg")
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, res.Body); close(done) }()
	time.Sleep(150 * time.Millisecond)
	room.live.Store(false)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream kept going after the bot left Discord")
	}
}

func TestTheEncoderStopsWithTheLastListener(t *testing.T) {
	srv, room, hub, tok := setup(t, 25)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/l/"+tok+"/stream.ogg", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if !hub.Running() {
		t.Fatal("encoder not running with a listener")
	}
	cancel()
	res.Body.Close()
	deadline := time.Now().Add(2 * time.Second)
	for hub.Running() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	room.mu.Lock()
	feeds, dropped := room.feeds, room.dropped
	room.mu.Unlock()
	if hub.Running() || feeds != 1 || dropped != 1 {
		t.Fatalf("running %v feeds %d dropped %d", hub.Running(), feeds, dropped)
	}
}

func TestNowSaysWhenThePlayerIsFull(t *testing.T) {
	srv, _, _, tok := setup(t, 1)
	_, body := get(t, srv.URL+"/l/"+tok+"/now.json")
	if strings.Contains(body, `"full":true`) {
		t.Fatal("full before anyone listens")
	}
	openStream(t, srv.URL+"/l/"+tok+"/stream.ogg")
	time.Sleep(50 * time.Millisecond)
	if _, body := get(t, srv.URL+"/l/"+tok+"/now.json"); !strings.Contains(body, `"full":true`) {
		t.Fatalf("now.json at the cap: %s", body)
	}
	if _, page := get(t, srv.URL+"/l/"+tok); !strings.Contains(page, "The web player is full right now") || !strings.Contains(page, "isn't in a voice channel") {
		t.Fatal("the page lacks the full and not-playing messages")
	}
}
