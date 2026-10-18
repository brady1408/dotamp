package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

type fakePlayer struct {
	mu      sync.Mutex
	playCtx context.Context // the ctx Play was given
	block   bool            // Search blocks until its ctx ends
	partial bool            // CreatePlaylist saves one server and fails another
	calls   []string
	queue   []library.Track
	names   []string
	lanes   []audio.Lane
	tooMany bool
	notices []string
}

func joinIDs(ts []library.Track) string {
	var ids []string
	for _, t := range ts {
		ids = append(ids, t.ID)
	}
	return strings.Join(ids, ",")
}

func (p *fakePlayer) record(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, s)
}

func (p *fakePlayer) last() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) == 0 {
		return ""
	}
	return p.calls[len(p.calls)-1]
}

func (p *fakePlayer) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{QueueLen: len(p.queue), Index: 0}
	if len(p.queue) > 0 {
		ti := TrackInfoOf(p.queue[0])
		st.Track, st.Playing, st.Position = &ti, true, 72
	}
	return st
}
func (p *fakePlayer) Queue() []library.Track { p.mu.Lock(); defer p.mu.Unlock(); return p.queue }
func (p *fakePlayer) QueueNames() []string   { p.mu.Lock(); defer p.mu.Unlock(); return p.names }
func (p *fakePlayer) Lanes() []audio.Lane    { return p.lanes }
func (p *fakePlayer) Search(ctx context.Context, _ string) (library.SearchResult, []library.Playlist, error) {
	if p.block {
		<-ctx.Done()
		return library.SearchResult{}, nil, ctx.Err()
	}
	return library.SearchResult{
		Tracks: []library.Track{{ID: "s:1", Title: "Heavy", Artist: "Collective Soul", Album: "Dosage", Duration: 248 * time.Second, Server: "s"}},
		Albums: []library.Album{{ID: "s:9", Title: "Dosage", Artist: "Collective Soul", Year: 1999, TrackCount: 11, Server: "s"}},
	}, []library.Playlist{{ID: "s:p1", Name: "Road Trip", TrackCount: 3, Server: "s"}}, nil
}
func (p *fakePlayer) AlbumTracks(_ context.Context, id string) ([]library.Track, error) {
	return []library.Track{{ID: "s:2", Title: "Tremble", Album: "Dosage", Server: "s"}, {ID: "s:3", Title: "Heavy", Album: "Dosage", Server: "s"}}, nil
}
func (p *fakePlayer) PlaylistTracks(_ context.Context, id string) ([]library.Track, error) {
	return []library.Track{{ID: "s:4", Server: "s"}, {ID: "s:5", Server: "s"}, {ID: "s:6", Server: "s"}}, nil
}
func (p *fakePlayer) Play(ctx context.Context, ts []library.Track) error {
	p.record("play:" + joinIDs(ts))
	p.mu.Lock()
	p.queue = ts
	p.playCtx = ctx
	p.mu.Unlock()
	return nil
}
func (p *fakePlayer) Add(ts []library.Track) {
	p.record("add:" + joinIDs(ts))
	p.mu.Lock()
	p.queue = append(p.queue, ts...)
	p.mu.Unlock()
}
func (p *fakePlayer) Request(_ context.Context, name string, ts []library.Track) (int, error) {
	if p.tooMany {
		return 7, audio.ErrTooManyPending
	}
	p.record("request:" + name + ":" + joinIDs(ts))
	return len(ts), nil
}
func (p *fakePlayer) Remove(_ context.Context, i int) error {
	p.record(fmt.Sprintf("remove:%d", i))
	return nil
}
func (p *fakePlayer) Clear() int                 { p.record("clear"); return 3 }
func (p *fakePlayer) Next(context.Context) error { p.record("next"); return nil }
func (p *fakePlayer) Prev(context.Context) error { p.record("prev"); return nil }
func (p *fakePlayer) TogglePause()               { p.record("pause") }
func (p *fakePlayer) Playlists(context.Context) ([]library.Playlist, error) {
	return []library.Playlist{{ID: "s:p1", Name: "Road Trip", TrackCount: 3, Server: "s"}}, nil
}
func (p *fakePlayer) CreatePlaylist(_ context.Context, name string, ts []library.Track) ([]library.Saved, error) {
	p.record("create:" + name + ":" + joinIDs(ts))
	saved := []library.Saved{{Server: "s", Playlist: library.Playlist{ID: "s:new", Name: name, TrackCount: len(ts), Server: "s"}, Tracks: len(ts)}}
	if p.partial {
		return saved, errors.New("Navidrome failed: HTTP 500")
	}
	return saved, nil
}
func (p *fakePlayer) AddToPlaylist(_ context.Context, id string, ts []library.Track) error {
	for _, t := range ts {
		if t.Server != "s" {
			return errors.New("multi: tracks from another server")
		}
	}
	p.record("addto:" + id + ":" + joinIDs(ts))
	return nil
}
func (p *fakePlayer) Notify(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.notices = append(p.notices, msg)
}

func start(t *testing.T) (*Client, *fakePlayer, string) {
	t.Helper()
	p := &fakePlayer{}
	path := filepath.Join(t.TempDir(), "control.json")
	s, err := Listen(path, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return NewClient(path), p, path
}

func call(t *testing.T, c *Client, req Request) Reply {
	t.Helper()
	r, err := c.Call(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func port(t *testing.T, path string) int {
	t.Helper()
	var f struct{ Port int }
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &f) != nil || f.Port == 0 {
		t.Fatalf("control.json = %s", b)
	}
	return f.Port
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("result %T is not an object: %v", v, v)
	}
	return m
}

func TestControlFileIsPrivateAndRemovedOnClose(t *testing.T) {
	p := &fakePlayer{}
	path := filepath.Join(t.TempDir(), "control.json")
	s, err := Listen(path, p)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Fatalf("control.json: %v %v", err, st)
	}
	var f struct {
		Port  int
		Token string
	}
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &f) != nil || f.Port == 0 || len(f.Token) < 32 {
		t.Fatalf("control.json = %s", b)
	}
	s.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("control.json survived Close")
	}
}

func TestBadTokenAndUnknownOpAreRefused(t *testing.T) {
	c, _, path := start(t)
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(conn, `{"token":"wrong","op":"status"}`)
	var r Reply
	if json.NewDecoder(conn).Decode(&r) != nil || r.OK || r.Error != "bad token" {
		t.Fatalf("reply = %+v", r)
	}
	conn.Close()
	if r := call(t, c, Request{Op: "dance"}); r.OK || r.Error != `unknown op "dance"` {
		t.Fatalf("reply = %+v", r)
	}
}

func TestStatusQueueAndRequestsReportThePlayer(t *testing.T) {
	c, p, _ := start(t)
	p.queue = []library.Track{{ID: "s:1", Title: "Heavy", Artist: "Collective Soul", Duration: 248 * time.Second}}
	p.names = []string{"Alice"}
	p.lanes = []audio.Lane{{Name: "Bob", Tracks: []library.Track{{Title: "Shine"}}}}
	r := call(t, c, Request{Op: "status"})
	st := asMap(t, r.Result)
	if !r.OK || st["queue_len"].(float64) != 1 || asMap(t, st["track"])["title"] != "Heavy" || st["position"].(float64) != 72 {
		t.Fatalf("status = %+v", r)
	}
	q := call(t, c, Request{Op: "queue"}).Result.([]any)
	if row := asMap(t, q[0]); row["id"] != "t:s:1" || row["by"] != "Alice" || row["index"].(float64) != 0 || row["duration"].(float64) != 248 {
		t.Fatalf("queue = %+v", q)
	}
	l := call(t, c, Request{Op: "requests"}).Result.([]any)
	if lane := asMap(t, l[0]); lane["name"] != "Bob" || lane["waiting"].(float64) != 1 || lane["next"] != "Shine" {
		t.Fatalf("requests = %+v", l)
	}
}

func TestSearchReturnsTypedIDs(t *testing.T) {
	c, _, _ := start(t)
	r := asMap(t, call(t, c, Request{Op: "search", Q: "co"}).Result)
	tr := asMap(t, r["tracks"].([]any)[0])
	al := asMap(t, r["albums"].([]any)[0])
	pl := asMap(t, r["playlists"].([]any)[0])
	if tr["id"] != "t:s:1" || al["id"] != "a:s:9" || pl["id"] != "p:s:p1" || al["track_count"].(float64) != 11 {
		t.Fatalf("ids = %v %v %v", tr, al, pl)
	}
}

func TestAddExpandsAlbumAndPlaylistIDsAndRemembersTracks(t *testing.T) {
	c, p, _ := start(t)
	r := call(t, c, Request{Op: "add", IDs: []string{"a:s:9", "p:s:p1"}})
	if !r.OK || asMap(t, r.Result)["queued"].(float64) != 5 {
		t.Fatalf("reply = %+v", r)
	}
	if p.last() != "add:s:2,s:3,s:4,s:5,s:6" {
		t.Fatalf("calls = %v", p.calls)
	}
	if r := call(t, c, Request{Op: "play", IDs: []string{"t:s:3"}}); !r.OK || p.last() != "play:s:3" {
		t.Fatalf("play = %+v calls %v", r, p.calls)
	}
	p.mu.Lock()
	n := append([]string(nil), p.notices...)
	p.mu.Unlock()
	if len(n) == 0 || !strings.HasPrefix(n[0], "Added") {
		t.Fatalf("notices = %v", n)
	}
}

func TestUnlistedTrackIdIsRefused(t *testing.T) {
	c, p, _ := start(t)
	r := call(t, c, Request{Op: "play", IDs: []string{"t:s:404"}})
	if r.OK || r.Error != "unknown track t:s:404; search first" || len(p.calls) != 0 {
		t.Fatalf("reply = %+v calls %v", r, p.calls)
	}
	if r := call(t, c, Request{Op: "add", IDs: []string{"s:1"}}); r.OK || !strings.Contains(r.Error, "bad id") {
		t.Fatalf("untyped id = %+v", r)
	}
}

func TestAddAsGoesThroughALane(t *testing.T) {
	c, p, _ := start(t)
	call(t, c, Request{Op: "search", Q: "heavy"})
	r := call(t, c, Request{Op: "add", IDs: []string{"t:s:1"}, As: "Claude"})
	if !r.OK || p.last() != "request:Claude:s:1" || asMap(t, r.Result)["pending"].(float64) != 1 {
		t.Fatalf("reply = %+v calls %v", r, p.calls)
	}
	p.tooMany = true
	if r := call(t, c, Request{Op: "add", IDs: []string{"t:s:1"}, As: "Claude"}); r.OK || r.Error != "You have 7 songs waiting; wait for some to play" {
		t.Fatalf("cap reply = %+v", r)
	}
}

func TestTransportVerbsAndRemoveClear(t *testing.T) {
	c, p, _ := start(t)
	for _, op := range []string{"next", "prev", "pause"} {
		if r := call(t, c, Request{Op: op}); !r.OK || asMap(t, r.Result)["queue_len"] == nil {
			t.Fatalf("%s = %+v", op, r)
		}
	}
	if r := call(t, c, Request{Op: "remove"}); r.OK || r.Error != "missing index" {
		t.Fatalf("remove without index = %+v", r)
	}
	p.mu.Lock()
	p.queue = []library.Track{{ID: "s:1"}}
	p.mu.Unlock()
	i := 0
	call(t, c, Request{Op: "remove", Index: &i})
	r := call(t, c, Request{Op: "clear"})
	if strings.Join(p.calls, " ") != "next prev pause remove:0 clear" || asMap(t, r.Result)["dropped_requests"].(float64) != 3 {
		t.Fatalf("calls = %v reply %+v", p.calls, r)
	}
	if !strings.Contains(p.last(), "clear") || !strings.Contains(strings.Join(p.notices, " "), "Queue cleared (and 3 requests)") {
		t.Fatalf("notices = %v", p.notices)
	}
}

func TestPlaylistVerbs(t *testing.T) {
	c, p, _ := start(t)
	pl := call(t, c, Request{Op: "playlists"}).Result.([]any)
	if asMap(t, pl[0])["id"] != "p:s:p1" {
		t.Fatalf("playlists = %+v", pl)
	}
	r := call(t, c, Request{Op: "playlist_create", Name: "Night", IDs: []string{"a:s:9"}})
	if !r.OK || p.last() != "create:Night:s:2,s:3" {
		t.Fatalf("create = %+v calls %v", r, p.calls)
	}
	if got := asMap(t, asMap(t, r.Result)["playlists"].([]any)[0]); got["name"] != "Night" || got["id"] != "p:s:new" {
		t.Fatalf("created = %+v", got)
	}
	if r := call(t, c, Request{Op: "playlist_create", IDs: []string{"a:s:9"}}); r.OK || r.Error != "missing name" {
		t.Fatalf("create without name = %+v", r)
	}
	call(t, c, Request{Op: "search", Q: "heavy"})
	if r := call(t, c, Request{Op: "playlist_add", ID: "p:s:p1", IDs: []string{"t:s:1"}}); !r.OK || p.last() != "addto:s:p1:s:1" || asMap(t, r.Result)["added"].(float64) != 1 {
		t.Fatalf("playlist_add = %+v calls %v", r, p.calls)
	}
	if r := call(t, c, Request{Op: "playlist_add", ID: "s:p1", IDs: []string{"t:s:1"}}); r.OK {
		t.Fatalf("untyped playlist id accepted: %+v", r)
	}
}

func TestConcurrentCallsAreAllAnswered(t *testing.T) {
	c, _, _ := start(t)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := c.Call(context.Background(), Request{Op: "status"})
			if err != nil || !r.OK {
				errs <- fmt.Errorf("%v %+v", err, r)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestSilentClientIsDropped(t *testing.T) {
	old := readTimeout
	readTimeout = 100 * time.Millisecond
	t.Cleanup(func() { readTimeout = old })
	c, _, path := start(t)
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port(t, path)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(300 * time.Millisecond)
	if r := call(t, c, Request{Op: "status"}); !r.OK {
		t.Fatalf("status after a silent client = %+v", r)
	}
	buf := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("silent connection was not closed")
	}
}

func TestStreamingVerbsOutliveTheCallAndTheServer(t *testing.T) {
	p := &fakePlayer{}
	path := filepath.Join(t.TempDir(), "control.json")
	s, err := Listen(path, p)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(path)
	call(t, c, Request{Op: "search", Q: "heavy"})
	call(t, c, Request{Op: "play", IDs: []string{"t:s:1"}})
	p.mu.Lock()
	ctx := p.playCtx
	p.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		t.Fatalf("the stream's context ended with the reply: %v", ctx)
	}
	s.Close()
	if ctx.Err() != nil {
		t.Fatal("the stream's context ended with the server; the track must play on")
	}
}

func TestCloseDoesNotWaitOnASlowHandler(t *testing.T) {
	p := &fakePlayer{block: true}
	path := filepath.Join(t.TempDir(), "control.json")
	s, err := Listen(path, p)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(path)
	go c.Call(context.Background(), Request{Op: "search", Q: "slow"})
	time.Sleep(100 * time.Millisecond) // the handler is inside Search now
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited on a handler stuck in the library")
	}
}

func TestCloseLeavesAnotherServersFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.json")
	a, err := Listen(path, &fakePlayer{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Listen(path, &fakePlayer{}) // a second dotamp took the file over
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	if r := call(t, NewClient(path), Request{Op: "status"}); !r.OK {
		t.Fatalf("the first server's Close broke the second: %+v", r)
	}
	b.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the owner's Close left the file")
	}
}

func TestPlaylistCreateReportsAPartialFailure(t *testing.T) {
	c, p, _ := start(t)
	p.partial = true
	r := call(t, c, Request{Op: "playlist_create", Name: "Night", IDs: []string{"a:s:9"}})
	if r.OK || !strings.Contains(r.Error, "Navidrome failed") {
		t.Fatalf("reply = %+v", r)
	}
	if got := asMap(t, r.Result)["playlists"].([]any); len(got) != 1 {
		t.Fatalf("the saved half is missing: %+v", r)
	}
}

func TestRemoveOutOfRangeIsAnError(t *testing.T) {
	c, p, _ := start(t)
	p.queue = []library.Track{{ID: "s:1"}}
	i := 5
	if r := call(t, c, Request{Op: "remove", Index: &i}); r.OK || r.Error != "no queue row 5" || len(p.calls) != 0 {
		t.Fatalf("reply = %+v calls %v", r, p.calls)
	}
}
