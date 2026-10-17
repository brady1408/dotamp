package broadcast

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/audio"
)

// fakeUDP stands in for the voice socket: Write can fail or panic on demand.
type fakeUDP struct {
	voice.UDPConn
	mu      sync.Mutex
	writes  int
	err     error
	panicAt int // write number that panics; 0 = never
}

func (u *fakeUDP) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.writes++
	if u.panicAt > 0 && u.writes == u.panicAt {
		panic("codec exploded")
	}
	if u.err != nil {
		return 0, u.err
	}
	return len(p), nil
}

func (u *fakeUDP) Read(p []byte) (int, error) {
	time.Sleep(5 * time.Millisecond)
	return 0, nil
}

type fakeConn struct {
	voice.Conn
	guild    snowflake.ID
	udp      *fakeUDP
	openWait bool // Open blocks until ctx is done
	closed   atomic.Int32
}

func (c *fakeConn) Open(ctx context.Context, ch snowflake.ID, _, _ bool) error {
	if c.openWait {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (c *fakeConn) SetSpeaking(context.Context, voice.SpeakingFlags) error { return nil }
func (c *fakeConn) UDP() voice.UDPConn                                     { return c.udp }
func (c *fakeConn) GuildID() snowflake.ID                                  { return c.guild }
func (c *fakeConn) Close(context.Context)                                  { c.closed.Add(1) }

// harness wires a Broadcaster to fakes and records what it did.
type harness struct {
	b        *Broadcaster
	mu       sync.Mutex
	conns    []*fakeConn
	removed  int
	notices  []string
	mutes    []bool
	loc      Location
	found    bool
	openWait bool
	udpErr   error
	panicAt  int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{loc: Location{1, 10}, found: true}
	cfg := Config{
		UserID: 100, Rate: 48000, Feed: audio.NewFeed(48000),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Mute:   func(on bool) { h.mu.Lock(); h.mutes = append(h.mutes, on); h.mu.Unlock() },
		Notify: func(m string) { h.mu.Lock(); h.notices = append(h.notices, m); h.mu.Unlock() },
	}
	h.b = newWith(cfg, deps{
		connect: func(context.Context) error { return nil },
		find: func() (Location, bool) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.loc, h.found
		},
		name: func(l Location) string { return "chan-" + l.Channel.String() },
		createConn: func(g snowflake.ID) voice.Conn {
			h.mu.Lock()
			defer h.mu.Unlock()
			c := &fakeConn{guild: g, udp: &fakeUDP{err: h.udpErr, panicAt: h.panicAt}, openWait: h.openWait}
			h.conns = append(h.conns, c)
			return c
		},
		removeConn:  func(snowflake.ID) { h.mu.Lock(); h.removed++; h.mu.Unlock() },
		closeClient: func(context.Context) {},
	})
	// a steady feed so the stream has audio to send
	go func() {
		pcm := make([]float32, 2*960)
		for i := 0; i < 400; i++ {
			cfg.Feed.Write(pcm)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return h
}

func (h *harness) lastNotice() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.notices) == 0 {
		return ""
	}
	return h.notices[len(h.notices)-1]
}

func (h *harness) lastMute() (bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.mutes) == 0 {
		return false, false
	}
	return h.mutes[len(h.mutes)-1], true
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestToggleTimesOutWhenTheJoinNeverCompletes(t *testing.T) {
	h := newHarness(t)
	h.openWait = true
	old := joinTimeout
	joinTimeout = 60 * time.Millisecond
	defer func() { joinTimeout = old }()
	start := time.Now()
	err := h.b.Toggle(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Broadcast failed") || time.Since(start) > time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
	h.openWait = false
	if err := h.b.Toggle(context.Background()); err != nil { // busy must be released
		t.Fatalf("second toggle: %v", err)
	}
	if h.b.Channel() == "" {
		t.Fatal("should be live now")
	}
}

func TestFollowNeverStartsASecondStream(t *testing.T) {
	h := newHarness(t)
	if err := h.b.Toggle(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stream", func() bool { return h.b.streams.Load() == 1 })
	// the user moves channels; a b press lands during the follow
	ch := snowflake.ID(20)
	h.b.onVoiceState(&events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: voiceState(1, &ch, 100)}})
	_ = h.b.Toggle(context.Background())
	h.b.onVoiceState(&events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: voiceState(1, &ch, 100)}})
	time.Sleep(150 * time.Millisecond)
	if n := h.b.streams.Load(); n > 1 {
		t.Fatalf("%d streams running at once", n)
	}
	if h.b.Channel() != "" && h.b.Channel() != "chan-20" {
		t.Fatalf("channel = %q", h.b.Channel())
	}
}

func TestLeaveEventFromAnotherGuildIsIgnored(t *testing.T) {
	h := newHarness(t)
	_ = h.b.Toggle(context.Background())
	h.b.onVoiceState(&events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: voiceState(2, nil, 100)}})
	time.Sleep(50 * time.Millisecond)
	if h.b.Channel() != "chan-10" {
		t.Fatalf("a nil event from guild 2 must not end a broadcast in guild 1: %q", h.b.Channel())
	}
	h.b.onVoiceState(&events.GuildVoiceStateUpdate{GenericGuildVoiceState: &events.GenericGuildVoiceState{VoiceState: voiceState(1, nil, 100)}})
	waitFor(t, "leave", func() bool { return h.b.Channel() == "" })
	if !strings.Contains(h.lastNotice(), "You left chan-10; broadcast ended") {
		t.Fatalf("notice = %q", h.lastNotice())
	}
	if on, ok := h.lastMute(); !ok || on {
		t.Fatal("must be unmuted after leaving")
	}
}

func TestPanicInTheStreamEndsTheBroadcastCleanly(t *testing.T) {
	h := newHarness(t)
	h.panicAt = 3
	_ = h.b.Toggle(context.Background())
	waitFor(t, "drop", func() bool { return h.b.Channel() == "" })
	if !strings.Contains(h.lastNotice(), "Broadcast dropped") {
		t.Fatalf("notice = %q", h.lastNotice())
	}
	if on, _ := h.lastMute(); on {
		t.Fatal("must be unmuted after a panic")
	}
}

func TestWriteErrorRetriesOnceThenDrops(t *testing.T) {
	h := newHarness(t)
	old := retryDelay
	retryDelay = 10 * time.Millisecond
	defer func() { retryDelay = old }()
	h.udpErr = errors.New("socket closed")
	_ = h.b.Toggle(context.Background())
	waitFor(t, "retry", func() bool { h.mu.Lock(); defer h.mu.Unlock(); return len(h.conns) == 2 })
	waitFor(t, "drop", func() bool { return h.b.Channel() == "" && strings.Contains(h.lastNotice(), "Broadcast dropped") })
	h.mu.Lock()
	n := len(h.conns)
	h.mu.Unlock()
	if n != 2 {
		t.Fatalf("exactly one retry: %d conns", n)
	}
}

func voiceState(guild snowflake.ID, ch *snowflake.ID, user snowflake.ID) discord.VoiceState {
	return discord.VoiceState{GuildID: guild, ChannelID: ch, UserID: user}
}
