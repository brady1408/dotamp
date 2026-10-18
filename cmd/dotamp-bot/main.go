// dotamp-bot is the house bot: a Discord music bot that plays Brady's
// library in allowed servers on /join, with request lanes, skip votes, a
// DJ role and autoplay radio.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/disgoorg/disgo"
	disgobot "github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
	dave "github.com/thomas-vilte/dave-go/session"

	"github.com/brady1408/dotamp/internal/control"
	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/multi"
	"github.com/brady1408/dotamp/internal/plex"
	"github.com/brady1408/dotamp/internal/requests"
	"github.com/brady1408/dotamp/internal/room"
	"github.com/brady1408/dotamp/internal/stream"
	"github.com/brady1408/dotamp/internal/subsonic"
)

var version = "dev"

type houseBot struct {
	cfg    Config
	log    *slog.Logger
	client *disgobot.Client
	lib    *multi.Library
	req    *requests.Core
	rooms  map[snowflake.ID]*room.Room
}

func main() {
	cfgPath := flag.String("config", "/config/config.json", "config file")
	flag.Parse()
	if flag.Arg(0) == "ctl" {
		os.Exit(ctl(filepath.Join(filepath.Dir(*cfgPath), "control.json"), flag.Args()[1:]))
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(*cfgPath, log); err != nil {
		log.Error("dotamp-bot", "err", err)
		os.Exit(1)
	}
}

// ctl is `dotamp-bot ctl VERB …`: one call to the running bot.
func ctl(path string, args []string) int {
	req, err := control.ParseArgs(args)
	r := control.Reply{}
	if err == nil {
		r, err = control.NewClient(path).Call(context.Background(), req)
	}
	if err != nil {
		r = control.Reply{Error: err.Error()}
	}
	b, _ := json.Marshal(r)
	fmt.Println(string(b))
	if !r.OK {
		return 1
	}
	return 0
}

func run(cfgPath string, log *slog.Logger) error {
	cfg, sec, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	log.Info("dotamp-bot starting", "version", version, "guilds", cfg.AllowGuilds)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	lib := multi.New()
	if cfg.Plex != nil {
		c := plex.New(cfg.Plex.URL, sec.plex, "dotamp-bot")
		mid, err := c.MachineID(ctx)
		if err != nil {
			return fmt.Errorf("plex at %s: %w", cfg.Plex.URL, err)
		}
		c.SetServer(mid, "Ressikan Files")
		lib.Add(multi.Server{ID: mid, Name: "Ressikan Files", Owned: true, Via: "local"}, c)
	}
	if cfg.Navidrome != nil {
		c := subsonic.New(cfg.Navidrome.URL, cfg.Navidrome.User, sec.navidrome)
		c.SetServer("navidrome", "Navidrome")
		lib.Add(multi.Server{ID: "navidrome", Name: "Navidrome", Owned: true, Via: "local"}, c)
	}
	if len(lib.Servers()) == 0 {
		return errors.New("no music library configured")
	}

	b := &houseBot{cfg: cfg, log: log, lib: lib, rooms: map[snowflake.ID]*room.Room{}}
	b.req = requests.New(func() library.Library { return lib })
	client, err := disgo.New(sec.discord,
		disgobot.WithLogger(slog.New(warnOnly{log.Handler()})),
		disgobot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuilds, gateway.IntentGuildVoiceStates)),
		disgobot.WithCacheConfigOpts(cache.WithCaches(cache.FlagGuilds, cache.FlagChannels, cache.FlagVoiceStates, cache.FlagMembers, cache.FlagRoles)),
		disgobot.WithVoiceManagerConfigOpts(voice.WithDaveSessionCreateFunc(dave.CreateFunc())),
		disgobot.WithEventListenerFunc(b.onCommand),
		disgobot.WithEventListenerFunc(b.onAutocomplete),
		disgobot.WithEventListenerFunc(b.onGuildReady),
		disgobot.WithEventListenerFunc(b.onGuildJoin),
	)
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	b.client = client

	hist := history.NewWriter(cfg.HistoryDir)
	for _, gs := range cfg.AllowGuilds {
		g, err := snowflake.Parse(gs)
		if err != nil {
			return fmt.Errorf("allow_guilds: %q: %w", gs, err)
		}
		guild := g
		b.rooms[g] = room.New(ctx, room.Config{
			Guild: g, Library: lib, History: hist, HistoryDir: cfg.HistoryDir,
			Autoplay: cfg.autoplay(), IdleLeave: cfg.idleLeave(), Logger: log.With("guild", gs),
			Deps: room.Deps{
				Open:      func(ctx context.Context, ch snowflake.ID) (room.Conn, error) { return b.open(ctx, guild, ch) },
				Listeners: func(ch snowflake.ID) int { return b.listeners(guild, ch) },
				Post:      b.post,
				Now:       time.Now,
			},
		})
	}
	if len(b.rooms) == 1 {
		for _, r := range b.rooms {
			srv, err := control.Listen(filepath.Join(filepath.Dir(cfgPath), "control.json"), ctlPlayer{r: r, lib: lib, log: log})
			if err != nil {
				log.Warn("control", "err", err)
			} else {
				defer srv.Close()
			}
		}
	}

	if err := client.OpenGateway(ctx); err != nil {
		return fmt.Errorf("discord login: %w", err)
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, r := range b.rooms {
			r.Leave(cctx)
		}
		client.Close(cctx)
	}()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("dotamp-bot stopping")
			return nil
		case <-tick.C:
			for _, r := range b.rooms {
				r.Tick(ctx)
			}
		}
	}
}

func (b *houseBot) guard(where string) {
	if r := recover(); r != nil {
		b.log.Error("panic", "where", where, "panic", r, "stack", string(debug.Stack()))
	}
}

// onGuildReady and onGuildJoin keep the bot to its allowlist and put the
// commands on each allowed server.
func (b *houseBot) onGuildReady(e *events.GuildReady) { b.admit(e.GuildID, e.Guild.Name) }
func (b *houseBot) onGuildJoin(e *events.GuildJoin)   { b.admit(e.GuildID, e.Guild.Name) }

func (b *houseBot) admit(g snowflake.ID, name string) {
	go func() {
		defer b.guard("admit")
		if !allowedGuild(b.cfg, g) {
			b.log.Warn("left unallowed guild", "id", g, "name", name)
			if err := b.client.Rest.LeaveGuild(g); err != nil {
				b.log.Warn("leave guild", "err", err)
			}
			return
		}
		if _, err := b.client.Rest.SetGuildCommands(b.client.ApplicationID, g, commandDefs()); err != nil {
			b.log.Warn("register commands", "guild", g, "err", err)
			return
		}
		b.log.Info("commands registered", "guild", g, "name", name)
	}()
}

// voiceConn adapts disgo's voice connection to the room.
type voiceConn struct {
	b     *houseBot
	conn  voice.Conn
	guild snowflake.ID
}

func (v voiceConn) UDP() stream.UDP { return v.conn.UDP() }

// Close leaves voice and waits for Discord to register it: a join right
// after a leave otherwise races Discord's reply to the leave.
func (v voiceConn) Close(ctx context.Context) {
	v.conn.Close(ctx)
	v.b.client.VoiceManager.RemoveConn(v.guild)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		if vs, ok := v.b.client.Caches.VoiceState(v.guild, v.b.client.ID()); !ok || vs.ChannelID == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	time.Sleep(100 * time.Millisecond)
}

func (b *houseBot) open(ctx context.Context, g, ch snowflake.ID) (room.Conn, error) {
	conn := b.client.VoiceManager.CreateConn(g)
	if err := conn.Open(ctx, ch, false, true); err != nil {
		conn.Close(context.Background())
		b.client.VoiceManager.RemoveConn(g)
		return nil, err
	}
	if err := conn.SetSpeaking(ctx, voice.SpeakingFlagMicrophone); err != nil {
		conn.Close(context.Background())
		b.client.VoiceManager.RemoveConn(g)
		return nil, err
	}
	return voiceConn{b: b, conn: conn, guild: g}, nil
}

// listeners counts the people in a voice channel: not this bot, and not
// other bots when the member cache knows them.
func (b *houseBot) listeners(g, ch snowflake.ID) int {
	if ch == 0 || b.client == nil {
		return 0
	}
	self := b.client.ID()
	n := 0
	for vs := range b.client.Caches.VoiceStates(g) {
		if vs.ChannelID == nil || *vs.ChannelID != ch || vs.UserID == self {
			continue
		}
		if m, ok := b.client.Caches.Member(g, vs.UserID); ok && m.User.Bot {
			continue
		}
		n++
	}
	return n
}

func (b *houseBot) voiceOf(g, user snowflake.ID) snowflake.ID {
	if vs, ok := b.client.Caches.VoiceState(g, user); ok && vs.ChannelID != nil {
		return *vs.ChannelID
	}
	return 0
}

func (b *houseBot) channelName(ch snowflake.ID) string {
	if c, ok := b.client.Caches.Channel(ch); ok {
		return c.Name()
	}
	return ch.String()
}

func (b *houseBot) roleName(g snowflake.ID) func(snowflake.ID) string {
	return func(id snowflake.ID) string {
		if r, ok := b.client.Caches.Role(g, id); ok {
			return r.Name
		}
		return ""
	}
}

func (b *houseBot) post(ch snowflake.ID, msg string) {
	if _, err := b.client.Rest.CreateMessage(ch, discord.MessageCreate{Content: msg}); err != nil {
		b.log.Warn("post", "err", err)
	}
}

// warnOnly keeps disgo and dave-go, chatty at Info, down to warnings.
type warnOnly struct{ slog.Handler }

func (w warnOnly) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }
func (w warnOnly) WithAttrs(a []slog.Attr) slog.Handler         { return warnOnly{w.Handler.WithAttrs(a)} }
func (w warnOnly) WithGroup(g string) slog.Handler              { return warnOnly{w.Handler.WithGroup(g)} }
