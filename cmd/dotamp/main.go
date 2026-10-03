package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/config"
	"github.com/brady1408/dotamp/internal/netlog"
	"github.com/brady1408/dotamp/internal/plex"
	"github.com/brady1408/dotamp/internal/plextv"
	"github.com/brady1408/dotamp/internal/ui"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	debugLog := flag.Bool("debug", false, "log every HTTP request and source locations")
	flag.Parse()
	if *showVersion {
		fmt.Println("dotamp", version)
		return
	}
	var err error
	switch flag.Arg(0) {
	case "login":
		err = login()
	case "":
		err = run(*debugLog)
	default:
		err = fmt.Errorf("unknown command %q (try: dotamp login)", flag.Arg(0))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dotamp:", err)
		os.Exit(1)
	}
}

func plexTV(clientID string) *plextv.Client {
	c := plextv.New(clientID)
	if u := os.Getenv("DOTAMP_PLEXTV"); u != "" { // tests point this at a fake plex.tv
		c.BaseURL = u
	}
	return c
}

// login signs in with a Plex account through the PIN flow, saves the account
// token, and reports which server dotamp will use.
func login() error {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		cfg = config.Config{}
	} else if err != nil && !strings.Contains(err.Error(), "dotamp login") {
		return err
	}
	if cfg.ClientID == "" {
		cfg.ClientID = config.NewClientID()
	}
	tv := plexTV(cfg.ClientID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pin, err := tv.RequestPin(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Open this link and approve dotamp:\n\n  %s\n\nCode: %s\n\nWaiting", tv.AuthURL(pin), pin.Code)
	tokenCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		tok, err := tv.PollPin(ctx, pin, 2*time.Second)
		if err != nil {
			errCh <- err
			return
		}
		tokenCh <- tok
	}()
	var token string
	dots := time.NewTicker(2 * time.Second)
	defer dots.Stop()
wait:
	for {
		select {
		case token = <-tokenCh:
			break wait
		case err := <-errCh:
			fmt.Println()
			return err
		case <-dots.C:
			fmt.Print(".")
		}
	}
	fmt.Println()
	cfg.AccountToken = token
	if cfg.Volume <= 0 {
		cfg.Volume = 0.8
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Println("Signed in.")
	server, _, cn, err := resolveServer(ctx, &cfg)
	if err != nil {
		fmt.Printf("Could not reach a server yet: %v\n", err)
		return nil
	}
	_ = config.Save(cfg)
	fmt.Printf("Server: %s via %s%s\n", server, cn.URI, where(cn))
	return nil
}

func where(cn plextv.Connection) string {
	switch {
	case cn.URI == "" || !cn.Discovered:
		return ""
	case cn.Relay:
		return " (relay)"
	case cn.Local:
		return " (local)"
	}
	return " (remote)"
}

// resolveServer returns the server URL and token to use: the manual pair when
// set, otherwise the account's server through the best connection, falling
// back to the last connection that worked when plex.tv cannot be reached.
func resolveServer(ctx context.Context, cfg *config.Config) (name, url string, cn plextv.Connection, err error) {
	if cfg.Manual() {
		return "configured server", cfg.Server, plextv.Connection{URI: cfg.Server}, nil
	}
	if cfg.AccountToken == "" {
		return "", "", plextv.Connection{}, errors.New("not signed in; run `dotamp login`")
	}
	tv := plexTV(cfg.ClientID)
	servers, err := tv.Servers(ctx, cfg.AccountToken)
	if err != nil {
		if cfg.LastServer != "" {
			log.Printf("plex.tv unreachable (%v); using the last server %s", err, cfg.LastServer)
			return "last known server", cfg.LastServer, plextv.Connection{URI: cfg.LastServer}, nil
		}
		return "", "", plextv.Connection{}, err
	}
	srv := plextv.ChooseServer(servers, cfg.ServerName)
	if srv == nil {
		var names []string
		for _, s := range servers {
			names = append(names, s.Name)
		}
		return "", "", plextv.Connection{}, fmt.Errorf("no server named %q; the account has: %s", cfg.ServerName, strings.Join(names, ", "))
	}
	cn, err = plextv.Connect(ctx, *srv, plextv.Probe(srv.AccessToken, cfg.ClientID))
	if err != nil {
		if cfg.LastServer != "" {
			log.Printf("%v; using the last server %s", err, cfg.LastServer)
			return "last known server", cfg.LastServer, plextv.Connection{URI: cfg.LastServer}, nil
		}
		return "", "", plextv.Connection{}, err
	}
	cfg.LastServer, cfg.LastToken = cn.URI, srv.AccessToken
	return srv.Name, cn.URI, cn, nil
}

func run(debugLog bool) (err error) {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("not signed in. Run `dotamp login`, or write a server and token to %s", config.Path())
	}
	if err != nil {
		return err
	}

	if err := os.MkdirAll(config.CacheDir(), 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(config.CacheDir(), "dotamp.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	log.SetOutput(logf)
	flags := log.LstdFlags
	if debugLog {
		flags |= log.Lshortfile
		netlog.Enabled.Store(true)
	}
	log.SetFlags(flags)
	log.Printf("dotamp %s starting; config %s", version, config.Path())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	serverName, serverURL, cn, err := resolveServer(ctx, &cfg)
	if err != nil {
		return err
	}
	token := cfg.Token
	if !cfg.Manual() {
		token = cfg.LastToken
		_ = config.Save(cfg) // remembers the connection that worked
	}
	log.Printf("server: %s via %s%s", serverName, serverURL, where(cn))

	lib := plex.New(serverURL, token, cfg.ClientID)
	if cfg.Section != "" {
		lib.SetSection(cfg.Section)
	} else {
		id, err := lib.MusicSection(ctx)
		if err != nil {
			return fmt.Errorf("plex at %s: %w", serverURL, err)
		}
		cfg.Section = id
		_ = config.Save(cfg)
	}

	out, err := audio.NewOtoOutput(audio.OutRate)
	if err != nil {
		return fmt.Errorf("audio device: %w", err)
	}
	eng := audio.NewEngine(out)
	defer eng.Close()
	eng.SetVolume(cfg.Volume)

	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	var app *ui.App
	ctrl := audio.NewController(lib, eng, func(msg string) {
		if app != nil {
			app.Notify(msg)
		}
	})
	app = ui.New(screen, ctrl, eng, lib, func(v float64) {
		cfg.Volume = v
		_ = config.Save(cfg)
	})
	// A panic on either goroutine must restore the terminal before it reaches
	// the user; the message points at the log. The decode goroutine recovers
	// its own panics inside the engine.
	crashed := make(chan any, 1)
	guard := func(where string) {
		if r := recover(); r != nil {
			log.Printf("panic in %s: %v\n%s", where, r, debug.Stack())
			select {
			case crashed <- r:
			default:
			}
			cancel()
		}
	}
	go func() {
		defer guard("controller")
		ctrl.Run(ctx)
	}()
	func() {
		defer guard("ui")
		err = app.Run(ctx)
	}()
	select {
	case r := <-crashed:
		screen.Fini()
		return fmt.Errorf("crashed: %v (see %s)", r, logPath)
	default:
	}
	return err
}
