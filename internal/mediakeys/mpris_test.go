package mediakeys

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestPlaybackStatusAndMetadata(t *testing.T) {
	if s := playbackStatus(State{}); s != "Stopped" {
		t.Fatalf("no track = %q", s)
	}
	if s := playbackStatus(State{HasTrack: true}); s != "Paused" {
		t.Fatalf("track, not playing = %q", s)
	}
	if s := playbackStatus(State{HasTrack: true, Playing: true}); s != "Playing" {
		t.Fatalf("playing = %q", s)
	}
	m := metadata(State{HasTrack: true, Title: "Billie Jean", Artist: "Michael Jackson", Album: "Thriller", Length: 4*time.Minute + 53*time.Second})
	if m["xesam:title"].Value() != "Billie Jean" || m["xesam:album"].Value() != "Thriller" {
		t.Fatalf("metadata = %v", m)
	}
	if a, ok := m["xesam:artist"].Value().([]string); !ok || len(a) != 1 || a[0] != "Michael Jackson" {
		t.Fatalf("artist should be a string list: %v", m["xesam:artist"])
	}
	if l, ok := m["mpris:length"].Value().(int64); !ok || l != 293_000_000 {
		t.Fatalf("length is microseconds: %v", m["mpris:length"])
	}
	if _, ok := m["mpris:trackid"].Value().(dbus.ObjectPath); !ok {
		t.Fatal("trackid must be an object path")
	}
	if len(metadata(State{})) != 1 { // only the trackid when nothing plays
		t.Fatalf("empty state metadata = %v", metadata(State{}))
	}
}

// fakePlayer records actions and serves a settable state.
type fakePlayer struct {
	mu      sync.Mutex
	actions []Action
	state   State
}

func (f *fakePlayer) Handle(a Action) { f.mu.Lock(); f.actions = append(f.actions, a); f.mu.Unlock() }
func (f *fakePlayer) State() State    { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (f *fakePlayer) got() []Action {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Action(nil), f.actions...)
}

// privateBus starts a session daemon for this test and returns its address.
func privateBus(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon not installed")
	}
	cmd := exec.Command("dbus-daemon", "--session", "--print-address=1", "--nofork")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	buf := make([]byte, 512)
	n, err := out.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(buf[:n]))
}

func TestMPRISServesKeysAndProperties(t *testing.T) {
	addr := privateBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	p := &fakePlayer{state: State{HasTrack: true, Playing: true, Title: "Your Love", Artist: "The Outfield"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- listenMPRIS(ctx, p, addr) }()

	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	obj := client.Object("org.mpris.MediaPlayer2.dotamp", "/org/mpris/MediaPlayer2")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if call := obj.Call("org.mpris.MediaPlayer2.Player.PlayPause", 0); call.Err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the player never appeared on the bus")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := obj.Call("org.mpris.MediaPlayer2.Player.Next", 0).Err; err != nil {
		t.Fatal(err)
	}
	if err := obj.Call("org.mpris.MediaPlayer2.Player.Previous", 0).Err; err != nil {
		t.Fatal(err)
	}
	if err := obj.Call("org.mpris.MediaPlayer2.Player.Pause", 0).Err; err != nil { // playing: toggles
		t.Fatal(err)
	}
	if got := p.got(); len(got) != 4 || got[0] != PlayPause || got[1] != Next || got[2] != Prev || got[3] != PlayPause {
		t.Fatalf("actions = %v", got)
	}
	var status string
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "PlaybackStatus").Store(&status); err != nil || status != "Playing" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	var meta map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "Metadata").Store(&meta); err != nil || meta["xesam:title"].Value() != "Your Love" {
		t.Fatalf("meta=%v err=%v", meta, err)
	}
	var identity string
	if err := obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2", "Identity").Store(&identity); err != nil || identity != "dotamp" {
		t.Fatalf("identity=%q err=%v", identity, err)
	}
	// a state change reaches the bus within a poll
	p.mu.Lock()
	p.state.Playing = false
	p.mu.Unlock()
	deadline = time.Now().Add(3 * time.Second)
	for status != "Paused" {
		_ = obj.Call("org.freedesktop.DBus.Properties.Get", 0, "org.mpris.MediaPlayer2.Player", "PlaybackStatus").Store(&status)
		if time.Now().After(deadline) {
			t.Fatalf("status never became Paused: %q", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("listen returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listen did not stop on cancel")
	}
	_ = os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
}
