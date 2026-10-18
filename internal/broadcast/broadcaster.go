package broadcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	dave "github.com/thomas-vilte/dave-go/session"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/requests"
	"github.com/brady1408/dotamp/internal/stream"
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

	Commands   bool            // answer /play, /np, /queue from the room
	Library    library.Library // the DJ's library, for search and resolving requests
	Player     Player          // the queue, for requests and /np, /queue
	History    *history.Writer // what played while live, per server; nil = no log
	SaveNights bool            // save each broadcast's played songs as a playlist on leave
	SaveNight  func(ctx context.Context, name string, ts []library.Track) (notice string, err error)
}

var (
	joinTimeout = 15 * time.Second // a voice handshake that takes longer has failed
	retryDelay  = 2 * time.Second  // before the one reopen after a dropped connection
)

// deps is the Discord side, so tests can run the state machine on fakes.
type deps struct {
	connect     func(ctx context.Context) error // gateway up, cache warm
	find        func() (Location, bool)
	name        func(Location) string
	createConn  func(guild snowflake.ID) voice.Conn
	removeConn  func(guild snowflake.ID)
	awaitLeft   func(ctx context.Context, guild snowflake.ID) // Discord has seen us leave the guild's voice
	guilds      func() []snowflake.ID                         // servers the bot is in
	register    func(ctx context.Context, guild snowflake.ID) error
	voiceOf     func(guild, user snowflake.ID) (snowflake.ID, bool) // the channel a user is in
	closeClient func(ctx context.Context)
}

// session is one voice connection and its goroutines.
type session struct {
	conn  voice.Conn
	loc   Location
	name  string
	stop  chan struct{}
	retry bool            // this session is the one reopen after a drop; no second retry
	night []library.Track // what played while this session was live, in order
}

// Broadcaster joins the voice channel the user is in and streams the feed
// there. Every state change claims busy, so a join, a follow and a leave
// never overlap, and a teardown only applies to the session it was meant
// for.
type Broadcaster struct {
	cfg  Config
	deps deps

	mu   sync.Mutex
	cur  *session
	busy bool

	client  *bot.Client
	streams atomic.Int32 // live stream goroutines, for tests

	req        *requests.Core // /play resolution and autocomplete memory
	registered map[snowflake.ID]bool
}

func New(cfg Config) *Broadcaster {
	b := newWith(cfg, deps{})
	b.deps = deps{
		connect:    b.connectDiscord,
		find:       func() (Location, bool) { return Find(states{b.client}, cfg.UserID) },
		name:       b.channelName,
		createConn: func(g snowflake.ID) voice.Conn { return b.client.VoiceManager.CreateConn(g) },
		removeConn: func(g snowflake.ID) { b.client.VoiceManager.RemoveConn(g) },
		awaitLeft:  b.awaitLeft,
		guilds: func() (ids []snowflake.ID) {
			for g := range b.client.Caches.Guilds() {
				ids = append(ids, g.ID)
			}
			return
		},
		register: func(ctx context.Context, g snowflake.ID) error {
			_, err := b.client.Rest.SetGuildCommands(b.client.ApplicationID, g, commandDefs(), rest.WithCtx(ctx))
			return err
		},
		voiceOf: func(g, u snowflake.ID) (snowflake.ID, bool) {
			vs, ok := b.client.Caches.VoiceState(g, u)
			if !ok || vs.ChannelID == nil {
				return 0, false
			}
			return *vs.ChannelID, true
		},
		closeClient: func(ctx context.Context) {
			if b.client != nil {
				b.client.Close(ctx)
			}
		},
	}
	return b
}

func newWith(cfg Config, d deps) *Broadcaster {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Notify == nil {
		cfg.Notify = func(string) {}
	}
	if cfg.Mute == nil {
		cfg.Mute = func(bool) {}
	}
	b := &Broadcaster{cfg: cfg, deps: d}
	b.req = requests.New(func() library.Library { return b.cfg.Library })
	return b
}

// Channel names the channel being broadcast to, or "" when not live.
func (b *Broadcaster) Channel() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cur == nil {
		return ""
	}
	return b.cur.name
}

// claim takes the busy flag; false means another state change is running
// and the caller should give up rather than queue.
func (b *Broadcaster) claim() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.busy {
		return false
	}
	b.busy = true
	return true
}

func (b *Broadcaster) release() {
	b.mu.Lock()
	b.busy = false
	b.mu.Unlock()
}

// guard turns a panic in a broadcast goroutine into a dropped broadcast
// rather than a dead process in raw mode.
func (b *Broadcaster) guard(where string) {
	if r := recover(); r != nil {
		b.cfg.Logger.Error("broadcast panic", "where", where, "panic", r, "stack", string(debug.Stack()))
		b.cfg.Notify(fmt.Sprintf("Broadcast dropped: %v", r))
		go b.leaveCurrent(context.Background())
	}
}

// connectDiscord opens the gateway once.
func (b *Broadcaster) connectDiscord(ctx context.Context) error {
	if b.client != nil {
		return nil
	}
	client, err := disgo.New(b.cfg.Token,
		bot.WithLogger(slog.New(warnOnly{b.cfg.Logger.Handler()})),
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildVoiceStates)),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds, cache.FlagChannels, cache.FlagVoiceStates)),
		bot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(dave.CreateFunc())),
		bot.WithEventListenerFunc(b.onVoiceState),
		bot.WithEventListenerFunc(b.onCommand),
		bot.WithEventListenerFunc(b.onAutocomplete),
		bot.WithEventListenerFunc(b.onGuildJoin),
	)
	if err != nil {
		return fmt.Errorf("Discord login failed: %w", err)
	}
	if err := client.OpenGateway(ctx); err != nil {
		client.Close(ctx)
		return fmt.Errorf("Discord login failed: %w", err)
	}
	b.client = client
	time.Sleep(1500 * time.Millisecond) // voice states arrive with the guilds
	return nil
}

// states adapts disgo's cache to Find.
// awaitLeft waits until the cache no longer shows the bot in voice on the
// guild. A leave followed at once by a join races Discord's reply to the
// leave: that reply reaches the new connection, which disgo reads as "we
// were disconnected" and tears the new connection down mid-handshake.
func (b *Broadcaster) awaitLeft(ctx context.Context, guild snowflake.ID) {
	if b.client == nil {
		return
	}
	self := b.client.ID()
	if u, ok := b.client.Caches.SelfUser(); ok {
		self = u.ID
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		if vs, ok := b.client.Caches.VoiceState(guild, self); !ok || vs.ChannelID == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	time.Sleep(100 * time.Millisecond) // the voice manager handles the same event; let it pass
}

// warnOnly keeps disgo and dave-go, chatty at Info, down to warnings in a
// log that carries our own Info lines.
type warnOnly struct{ slog.Handler }

func (w warnOnly) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }
func (w warnOnly) WithAttrs(a []slog.Attr) slog.Handler         { return warnOnly{w.Handler.WithAttrs(a)} }
func (w warnOnly) WithGroup(g string) slog.Handler              { return warnOnly{w.Handler.WithGroup(g)} }

type states struct{ c *bot.Client }

func (s states) Each(fn func(guild, channel, user snowflake.ID) bool) {
	if s.c == nil {
		return
	}
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
	if b.client != nil {
		if ch, ok := b.client.Caches.Channel(loc.Channel); ok {
			return ch.Name()
		}
	}
	return loc.Channel.String()
}

// Toggle joins the channel the user is in, or leaves when live. A call
// while another state change is running does nothing.
func (b *Broadcaster) Toggle(ctx context.Context) error {
	if !b.claim() {
		return nil
	}
	defer b.release()
	ctx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()
	b.mu.Lock()
	cur := b.cur
	b.mu.Unlock()
	if cur != nil {
		b.cfg.Notify("Left " + cur.name) // before leave, so a night's save notice is what stays
		b.leave(ctx, cur)
		return nil
	}
	if err := b.deps.connect(ctx); err != nil {
		return err
	}
	if b.cfg.Commands {
		b.registerCommands(ctx)
	}
	loc, ok := b.deps.find()
	if !ok {
		return errors.New("You're not in a voice channel the bot can see")
	}
	return b.join(ctx, loc)
}

// join opens a voice connection and starts the session; the caller holds
// busy.
func (b *Broadcaster) join(ctx context.Context, loc Location) error {
	conn := b.deps.createConn(loc.Guild)
	if err := conn.Open(ctx, loc.Channel, false, true); err != nil {
		conn.Close(context.Background())
		b.deps.removeConn(loc.Guild)
		b.cfg.Logger.Warn("broadcast join", "channel", loc.Channel, "err", err)
		return fmt.Errorf("Broadcast failed: %w", err)
	}
	if err := conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		conn.Close(context.Background())
		b.deps.removeConn(loc.Guild)
		return fmt.Errorf("Broadcast failed: %w", err)
	}
	s := &session{conn: conn, loc: loc, name: b.deps.name(loc), stop: make(chan struct{})}
	b.mu.Lock()
	b.cur = s
	b.mu.Unlock()
	b.cfg.Mute(true)
	go b.drain(s)
	go b.stream(s)
	b.cfg.Notify("Broadcasting to " + s.name)
	return nil
}

// leave ends s if it is still the current session, and always unmutes.
func (b *Broadcaster) leave(ctx context.Context, s *session) {
	b.mu.Lock()
	if b.cur != s {
		b.mu.Unlock()
		return
	}
	// The song playing as we leave was heard by the room too.
	if b.cfg.Player != nil {
		if t, pos, name, ok := b.cfg.Player.Current(); ok {
			s.night = append(s.night, t)
			b.log(s, t, pos, "", name)
		}
	}
	b.cur = nil
	b.mu.Unlock()
	close(s.stop)
	s.conn.Close(ctx)
	b.deps.removeConn(s.loc.Guild)
	b.cfg.Mute(false)
	if b.cfg.SaveNights && b.cfg.SaveNight != nil && len(s.night) > 0 {
		// Synchronous, so a save on quit finishes before the process does;
		// every caller of leave is on its own goroutine, not the UI's.
		name := "Discord " + s.name + " " + time.Now().Format("2006-01-02")
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg, err := b.cfg.SaveNight(sctx, name, s.night)
		if err != nil && msg == "" {
			msg = "Save failed: " + err.Error()
		}
		b.cfg.Notify(msg)
	}
}

// log appends one history line for the session's server.
func (b *Broadcaster) log(s *session, t library.Track, played time.Duration, by, name string) {
	if b.cfg.History == nil {
		return
	}
	e := history.Entry{At: time.Now().UTC(), Channel: s.name, By: by, Name: name, Played: played.Seconds(), Track: t}
	if err := b.cfg.History.Append(s.loc.Guild.String(), e); err != nil {
		b.cfg.Logger.Warn("history", "err", err)
	}
}

// registerCommands puts the slash commands on every server the bot is in,
// once per server per run.
func (b *Broadcaster) registerCommands(ctx context.Context) {
	if b.registered == nil {
		b.registered = map[snowflake.ID]bool{}
	}
	failed := false
	for _, g := range b.deps.guilds() {
		if b.registered[g] {
			continue
		}
		if err := b.deps.register(ctx, g); err != nil {
			b.cfg.Logger.Warn("register commands", "guild", g, "err", err)
			failed = true
			continue
		}
		b.registered[g] = true
	}
	if failed {
		b.cfg.Notify("Discord commands failed to register: see the log")
	}
}

// TrackEnded records a track that stopped being current while live: into
// the history log and the night's list. Not live, it is nothing.
func (b *Broadcaster) TrackEnded(t library.Track, played time.Duration, by, name string) {
	b.mu.Lock()
	cur := b.cur
	if cur != nil {
		cur.night = append(cur.night, t)
	}
	b.mu.Unlock()
	if cur != nil {
		b.log(cur, t, played, by, name)
	}
}

// GuildName is the server's name when the bot is connected, else the ID.
func (b *Broadcaster) GuildName(id string) string {
	if b.client != nil {
		if g, err := snowflake.Parse(id); err == nil {
			if guild, ok := b.client.Caches.Guild(g); ok {
				return guild.Name
			}
		}
	}
	return id
}

func (b *Broadcaster) leaveCurrent(ctx context.Context) {
	b.mu.Lock()
	cur := b.cur
	b.mu.Unlock()
	if cur != nil {
		b.leave(ctx, cur)
	}
}

// drain keeps the UDP socket from backing up; incoming audio is discarded.
func (b *Broadcaster) drain(s *session) {
	defer b.guard("drain")
	stream.Drain(s.stop, s.conn.UDP())
}

func (b *Broadcaster) stream(s *session) {
	defer b.cfg.Mute(false)
	defer b.guard("stream")
	b.streams.Add(1)
	defer b.streams.Add(-1)
	err := stream.Run(s.stop, s.conn.UDP(), stream.Options{Rate: b.cfg.Rate, Feed: b.cfg.Feed, Logger: b.cfg.Logger, Label: s.name})
	switch {
	case err == nil:
		return
	case errors.Is(err, stream.ErrSetup):
		b.cfg.Notify("Broadcast failed: " + strings.TrimPrefix(err.Error(), stream.ErrSetup.Error()+": "))
		go b.leave(context.Background(), s)
		return
	}
	select {
	case <-s.stop: // we are being torn down; the error is ours
		return
	default:
	}
	b.cfg.Logger.Warn("broadcast write", "err", err)
	if s.retry {
		b.cfg.Notify("Broadcast dropped: " + err.Error())
		go b.leave(context.Background(), s)
		return
	}
	go b.reopen(s, err)
}

func (b *Broadcaster) reopen(s *session, cause error) {
	defer b.guard("reopen")
	ctx, cancel := context.WithTimeout(context.Background(), joinTimeout)
	defer cancel()
	b.leave(ctx, s)
	b.deps.awaitLeft(ctx, s.loc.Guild)
	time.Sleep(retryDelay)
	if !b.claim() {
		b.cfg.Notify("Broadcast dropped: " + cause.Error())
		return
	}
	defer b.release()
	if err := b.join(ctx, s.loc); err != nil {
		b.cfg.Notify("Broadcast dropped: " + cause.Error())
		return
	}
	b.mu.Lock()
	if b.cur != nil {
		b.cur.retry = true
	}
	b.mu.Unlock()
}

// onVoiceState follows the user between channels and leaves when they do.
// The work runs on its own goroutine, since a join waits for voice events
// that disgo delivers on the goroutine this handler is called from, and it
// claims busy like any other state change.
func (b *Broadcaster) onVoiceState(e *events.GuildVoiceStateUpdate) {
	if e.VoiceState.UserID != b.cfg.UserID {
		return
	}
	b.mu.Lock()
	cur := b.cur
	b.mu.Unlock()
	if cur == nil {
		return
	}
	channel, guild := e.VoiceState.ChannelID, e.VoiceState.GuildID
	go func() {
		defer b.guard("follow")
		if !b.claim() {
			return // a join or leave is already running; the user's next move or key catches up
		}
		defer b.release()
		ctx, cancel := context.WithTimeout(context.Background(), joinTimeout)
		defer cancel()
		switch {
		case channel == nil && guild != cur.loc.Guild:
			// leaving voice on another server says nothing about this one
		case channel == nil:
			b.cfg.Notify("You left " + cur.name + "; broadcast ended")
			b.leave(ctx, cur)
		case *channel != cur.loc.Channel || guild != cur.loc.Guild:
			b.leave(ctx, cur)
			b.deps.awaitLeft(ctx, cur.loc.Guild)
			if err := b.join(ctx, Location{guild, *channel}); err != nil {
				b.cfg.Logger.Warn("broadcast follow", "err", err)
				b.cfg.Notify(err.Error())
			}
		}
	}()
}

// Close leaves voice and the gateway; used on quit.
func (b *Broadcaster) Close(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b.leaveCurrent(ctx)
	b.deps.closeClient(ctx)
}

// Check logs in with the token over REST only and reports the bot's name
// and the servers it is in, for `dotamp discord`.
func Check(ctx context.Context, token string) (name string, guilds []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
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
