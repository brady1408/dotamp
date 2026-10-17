package broadcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	"github.com/thomas-vilte/dave-go/session"

	"github.com/brady1408/dotamp/internal/audio"
)

// Config is everything a Broadcaster needs from the player.
type Config struct {
	Token  string       // bot token; never logged
	UserID snowflake.ID // the account to follow into voice
	Rate   int          // engine sample rate
	Feed   *audio.Feed  // the output before the volume knob
	Mute   func(bool)   // silences the local device while live
	Notify func(string) // notice line; safe from any goroutine
	Logger *slog.Logger
}

// Broadcaster joins the voice channel the user is in and streams the feed
// there. The gateway opens on the first Toggle and stays open; the voice
// connection comes and goes.
type Broadcaster struct {
	cfg    Config
	mu     sync.Mutex
	client *bot.Client
	conn   voice.Conn
	loc    Location
	name   string
	busy   bool
	stop   chan struct{}
}

func New(cfg Config) *Broadcaster {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Notify == nil {
		cfg.Notify = func(string) {}
	}
	if cfg.Mute == nil {
		cfg.Mute = func(bool) {}
	}
	return &Broadcaster{cfg: cfg}
}

// Channel names the channel being broadcast to, or "" when not live.
func (b *Broadcaster) Channel() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.name
}

// connect opens the gateway once.
func (b *Broadcaster) connect(ctx context.Context) error {
	b.mu.Lock()
	have := b.client != nil
	b.mu.Unlock()
	if have {
		return nil
	}
	client, err := disgo.New(b.cfg.Token,
		bot.WithLogger(b.cfg.Logger),
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildVoiceStates)),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds, cache.FlagChannels, cache.FlagVoiceStates)),
		bot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(session.CreateFunc())),
		bot.WithEventListenerFunc(b.onVoiceState),
	)
	if err != nil {
		return fmt.Errorf("Discord login failed: %w", err)
	}
	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("Discord login failed: %w", err)
	}
	b.mu.Lock()
	b.client = client
	b.mu.Unlock()
	time.Sleep(1500 * time.Millisecond) // voice states arrive with the guilds
	return nil
}

// states adapts disgo's cache to Find.
type states struct{ c *bot.Client }

func (s states) Each(fn func(guild, channel, user snowflake.ID) bool) {
	for g := range s.c.Caches.Guilds() {
		for vs := range s.c.Caches.VoiceStates(g.ID) {
			ch := snowflake.ID(0)
			if vs.ChannelID != nil {
				ch = *vs.ChannelID
			}
			if !fn(g.ID, ch, vs.UserID) {
				return
			}
		}
	}
}

func (b *Broadcaster) channelName(loc Location) string {
	if ch, ok := b.client.Caches.Channel(loc.Channel); ok {
		return ch.Name()
	}
	return loc.Channel.String()
}

// Toggle joins the channel the user is in, or leaves when live. A second
// call while a join is in flight does nothing.
func (b *Broadcaster) Toggle(ctx context.Context) error {
	b.mu.Lock()
	if b.busy {
		b.mu.Unlock()
		return nil
	}
	if b.conn != nil {
		name := b.name
		b.mu.Unlock()
		b.leave(ctx)
		b.cfg.Notify("Left " + name)
		return nil
	}
	b.busy = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.busy = false; b.mu.Unlock() }()

	if err := b.connect(ctx); err != nil {
		return err
	}
	loc, ok := Find(states{b.client}, b.cfg.UserID)
	if !ok {
		return errors.New("You're not in a voice channel the bot can see")
	}
	return b.join(ctx, loc)
}

func (b *Broadcaster) join(ctx context.Context, loc Location) error {
	conn := b.client.VoiceManager.CreateConn(loc.Guild)
	if err := conn.Open(ctx, loc.Channel, false, true); err != nil {
		b.client.VoiceManager.RemoveConn(loc.Guild)
		return fmt.Errorf("Broadcast failed: %w", err)
	}
	if err := conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		conn.Close(ctx)
		b.client.VoiceManager.RemoveConn(loc.Guild)
		return fmt.Errorf("Broadcast failed: %w", err)
	}
	stop := make(chan struct{})
	b.mu.Lock()
	b.conn, b.loc, b.stop = conn, loc, stop
	b.name = b.channelName(loc)
	name := b.name
	b.mu.Unlock()
	b.cfg.Mute(true)
	go b.drain(conn, stop)
	go b.stream(conn, stop)
	b.cfg.Notify("Broadcasting to " + name)
	return nil
}

// leave ends the broadcast if one is live and always unmutes.
func (b *Broadcaster) leave(ctx context.Context) {
	b.mu.Lock()
	conn, stop := b.conn, b.stop
	b.conn, b.name, b.stop = nil, "", nil
	client := b.client
	b.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if conn != nil {
		conn.Close(ctx)
		if client != nil {
			client.VoiceManager.RemoveConn(conn.GuildID())
		}
	}
	b.cfg.Mute(false)
}

// drain keeps the UDP socket from backing up; incoming audio is discarded.
func (b *Broadcaster) drain(conn voice.Conn, stop chan struct{}) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		if _, err := conn.UDP().Read(buf); err != nil {
			return
		}
	}
}

// stream pulls from the feed every 20 ms, encodes, and sends audio or
// silence as the Speaker decides. The unmute is deferred before anything
// that can fail, so no exit path leaves the speakers off.
func (b *Broadcaster) stream(conn voice.Conn, stop chan struct{}) {
	defer b.cfg.Mute(false)
	pipe, err := NewPipeline(b.cfg.Rate)
	if err != nil {
		b.cfg.Notify("Broadcast failed: " + err.Error())
		return
	}
	var sp Speaker
	chunk := make([]float32, 2*(b.cfg.Rate/25)) // up to 40 ms of source per tick, to catch up
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	ctx := context.Background()
	var pending [][]byte
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if n := b.cfg.Feed.Read(chunk); n > 0 {
			pipe.Push(chunk[:2*n])
		}
		pending = append(pending, pipe.Packets()...)
		send, speaking, changed := sp.Tick(len(pending) > 0)
		if changed {
			flag := voice.SpeakingFlagNone
			if speaking {
				flag = voice.SpeakingFlagMicrophone
			}
			_ = conn.SetSpeaking(ctx, flag)
		}
		var pkt []byte
		switch send {
		case SendAudio:
			pkt, pending = pending[0], pending[1:]
		case SendSilence:
			pkt = Silence
		default:
			continue
		}
		if _, err := conn.UDP().Write(pkt); err != nil {
			b.cfg.Logger.Warn("broadcast write", "err", err)
			b.cfg.Notify("Broadcast dropped: " + err.Error())
			go b.leave(ctx)
			return
		}
		if len(pending) > 25 { // half a second behind: drop to stay live
			pending = pending[len(pending)-25:]
		}
	}
}

// onVoiceState follows the user between channels and leaves when they do.
func (b *Broadcaster) onVoiceState(e *events.GuildVoiceStateUpdate) {
	if e.VoiceState.UserID != b.cfg.UserID {
		return
	}
	b.mu.Lock()
	live := b.conn != nil
	loc, name := b.loc, b.name
	b.mu.Unlock()
	if !live {
		return
	}
	ctx := context.Background()
	switch {
	case e.VoiceState.ChannelID == nil:
		b.leave(ctx)
		b.cfg.Notify("You left " + name + "; broadcast ended")
	case *e.VoiceState.ChannelID != loc.Channel:
		b.leave(ctx)
		if err := b.join(ctx, Location{e.VoiceState.GuildID, *e.VoiceState.ChannelID}); err != nil {
			b.cfg.Notify(err.Error())
		}
	}
}

// Close leaves voice and the gateway; used on quit.
func (b *Broadcaster) Close(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b.leave(ctx)
	b.mu.Lock()
	client := b.client
	b.client = nil
	b.mu.Unlock()
	if client != nil {
		client.Close(ctx)
	}
}

// Check logs in with the token over REST only and reports the bot's name
// and the servers it is in, for `dotamp discord`.
func Check(ctx context.Context, token string) (name string, guilds []string, err error) {
	get := func(path string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/v10"+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d from %s", resp.StatusCode, path)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
	var me struct {
		Username string `json:"username"`
	}
	if err := get("/users/@me", &me); err != nil {
		return "", nil, err
	}
	var gs []struct {
		Name string `json:"name"`
	}
	if err := get("/users/@me/guilds", &gs); err != nil {
		return "", nil, err
	}
	for _, g := range gs {
		guilds = append(guilds, g.Name)
	}
	return me.Username, guilds, nil
}
