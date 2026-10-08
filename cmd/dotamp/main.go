package main

import (
	"bufio"
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
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/config"
	"github.com/brady1408/dotamp/internal/multi"
	"github.com/brady1408/dotamp/internal/netlog"
	"github.com/brady1408/dotamp/internal/plex"
	"github.com/brady1408/dotamp/internal/plextv"
	"github.com/brady1408/dotamp/internal/subsonic"
	"github.com/brady1408/dotamp/internal/ui"

	"golang.org/x/term"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	debugLog := flag.Bool("debug", false, "log every HTTP request and source locations")
	silent := flag.Bool("silent", false, "run without a sound device; the visualizers still move (for demos and headless machines)")
	flag.Parse()
	if *showVersion {
		fmt.Println("dotamp", version)
		return
	}
	if *debugLog {
		netlog.Enabled.Store(true) // the player redirects the log to a file; commands keep stderr
	}
	var err error
	switch flag.Arg(0) {
	case "login":
		err = login()
	case "servers":
		err = listServers()
	case "navidrome":
		err = addNavidrome(flag.Arg(1), flag.Arg(2))
	case "":
		err = run(*debugLog, *silent)
	default:
		err = fmt.Errorf("unknown command %q (try: dotamp login, dotamp servers, dotamp navidrome URL USER)", flag.Arg(0))
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

// login signs in with a Plex account and reports which server dotamp will use.
func login() error {
	cfg, err := loadOrEmpty()
	if err != nil {
		return err
	}
	if err := signIn(&cfg); err != nil {
		return err
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Println("Signed in.")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	server, _, cn, err := resolveServer(ctx, &cfg)
	if err != nil {
		fmt.Printf("Could not reach a server yet: %v\n", err)
		return nil
	}
	_ = config.Save(cfg)
	fmt.Printf("Server: %s via %s%s\n", server, cn.URI, where(cn))
	return nil
}

// loadOrEmpty returns the config, or an empty one when none exists yet or
// the existing one has no source configured.
func loadOrEmpty() (config.Config, error) {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) || (err != nil && strings.Contains(err.Error(), "dotamp login")) {
		cfg = config.Config{}
		err = nil
	}
	if cfg.ClientID == "" {
		cfg.ClientID = config.NewClientID()
	}
	if cfg.Volume <= 0 {
		cfg.Volume = 0.8
	}
	return cfg, err
}

// signIn runs the Plex PIN flow and stores the account token in cfg.
func signIn(cfg *config.Config) error {
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
	dots := time.NewTicker(2 * time.Second)
	defer dots.Stop()
	for {
		select {
		case tok := <-tokenCh:
			fmt.Println()
			cfg.AccountToken = tok
			return nil
		case err := <-errCh:
			fmt.Println()
			return err
		case <-dots.C:
			fmt.Print(".")
		}
	}
}

// prompter asks questions on the terminal, or reads answers from stdin when
// there is no terminal, so setup can be scripted and tested.
type prompter struct{ in *bufio.Reader }

func newPrompter() *prompter { return &prompter{in: bufio.NewReader(os.Stdin)} }

func (p *prompter) line(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := p.in.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (p *prompter) yes(prompt string, def bool) (bool, error) {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	a, err := p.line(prompt + " " + hint + " ")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(a) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

func (p *prompter) password(prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		return string(pw), err
	}
	return p.line("")
}

// checkNavidrome verifies a Subsonic server and returns its version.
func checkNavidrome(serverURL, user, pw string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return subsonic.New(serverURL, user, pw).Version(ctx)
}

// setup is the first run: one question about which server you have, then
// only the steps that apply.
func setup() (config.Config, error) {
	cfg, err := loadOrEmpty()
	if err != nil {
		return cfg, err
	}
	p := newPrompter()
	fmt.Println("Welcome to dotamp. Which music server do you have?")
	fmt.Println()
	fmt.Println("  1) Plex")
	fmt.Println("  2) Navidrome, or another Subsonic server")
	fmt.Println("  3) Both")
	fmt.Println()
	choice, err := p.line("Choice [1/2/3]: ")
	if err != nil {
		return cfg, fmt.Errorf("no config at %s; run `dotamp login` for Plex or `dotamp navidrome URL USER` for Navidrome", config.Path())
	}
	var wantPlex, wantNavidrome bool
	switch strings.ToLower(choice) {
	case "1", "plex":
		wantPlex = true
	case "2", "navidrome", "subsonic":
		wantNavidrome = true
	case "3", "both":
		wantPlex, wantNavidrome = true, true
	default:
		return cfg, fmt.Errorf("nothing configured. Later: `dotamp login` for Plex, `dotamp navidrome URL USER` for Navidrome, or edit %s", config.Path())
	}
	fmt.Println()
	if wantPlex {
		if err := signIn(&cfg); err != nil {
			return cfg, err
		}
		fmt.Println("Signed in.")
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if server, _, cn, err := resolveServer(ctx, &cfg); err == nil {
			fmt.Printf("Server: %s via %s%s\n", server, cn.URI, where(cn))
		} else {
			fmt.Printf("Signed in, but no server answered yet: %v\n", err)
		}
		cancel()
		fmt.Println()
	}
	if wantNavidrome {
		u, _ := p.line("Navidrome URL (e.g. http://192.168.1.20:4533): ")
		user, _ := p.line("Username: ")
		pw, _ := p.password("Password: ")
		if version, err := checkNavidrome(u, user, pw); err != nil {
			fmt.Printf("Could not connect: %v\n", err)
		} else {
			cfg.Navidrome = &config.Navidrome{URL: strings.TrimRight(u, "/"), User: user, Password: pw}
			fmt.Printf("Connected to Navidrome (%s).\n", version)
		}
		fmt.Println()
	}
	if cfg.AccountToken == "" && cfg.Navidrome == nil && !cfg.Manual() {
		return cfg, fmt.Errorf("nothing configured. Later: `dotamp login` for Plex, `dotamp navidrome URL USER` for Navidrome, or edit %s", config.Path())
	}
	if err := config.Save(cfg); err != nil {
		return cfg, err
	}
	fmt.Println("Saved. Starting dotamp; press ? for keys.")
	time.Sleep(time.Second)
	return cfg, nil
}

// addNavidrome checks a Subsonic server with a password read from the
// terminal and saves it to the config as a second library.
func addNavidrome(serverURL, user string) error {
	if serverURL == "" || user == "" {
		return errors.New("usage: dotamp navidrome URL USER")
	}
	pw, err := newPrompter().password(fmt.Sprintf("Password for %s at %s: ", user, serverURL))
	if err != nil {
		return err
	}
	version, err := checkNavidrome(serverURL, user, pw)
	if err != nil {
		return err
	}
	cfg, err := loadOrEmpty()
	if err != nil {
		return err
	}
	cfg.Navidrome = &config.Navidrome{URL: strings.TrimRight(serverURL, "/"), User: user, Password: pw}
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("Connected to %s (%s). Saved as the Navidrome library.\n", serverURL, version)
	return nil
}

// navidromeServer is the multi-server entry for the configured Navidrome.
func navidromeServer(cfg config.Config) (multi.Server, *subsonic.Client) {
	c := subsonic.New(cfg.Navidrome.URL, cfg.Navidrome.User, cfg.Navidrome.Password)
	c.SetServer("navidrome", "Navidrome")
	c.SetRemoteBitrate(cfg.RemoteBitrate)
	via := "remote"
	if c.Local() {
		via = "local"
	}
	return multi.Server{ID: "navidrome", Name: "Navidrome", Owned: true, Via: via}, c
}

// listServers prints every server on the account and how it can be reached.
func listServers() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if cfg.Navidrome != nil {
		srv, c := navidromeServer(cfg)
		if v, err := c.Version(ctx); err != nil {
			fmt.Printf("%-24s %-6s %s unreachable: %v\n", "Navidrome", "owned", cfg.Navidrome.URL, err)
		} else {
			fmt.Printf("%-24s %-6s %s (%s)  music library, %s\n", "Navidrome", "owned", cfg.Navidrome.URL, srv.Via, v)
		}
	}
	if cfg.AccountToken == "" {
		if cfg.Navidrome != nil {
			return nil
		}
		return errors.New("not signed in; run `dotamp login`")
	}
	tv := plexTV(cfg.ClientID)
	if u, err := tv.User(ctx, cfg.AccountToken); err == nil {
		pass := ""
		if u.Subscribed {
			pass = ", Plex Pass"
		}
		fmt.Printf("Signed in as %s%s\n", u.Username, pass)
	} else {
		return fmt.Errorf("account token rejected (%v); run `dotamp login`", err)
	}
	servers, err := tv.Servers(ctx, cfg.AccountToken)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		fmt.Println("plex.tv lists no servers for this account. Is this the account that owns your server, or has it been shared with you?")
		return nil
	}
	for _, srv := range servers {
		owner := "owned"
		if !srv.Owned {
			owner = "shared"
		}
		probe := plextv.Probe(srv.AccessToken, cfg.ClientID)
		cn, err := plextv.Connect(ctx, srv, probe)
		if err != nil {
			fmt.Printf("%-24s %-6s unreachable; every connection tried:\n", srv.Name, owner)
			for _, o := range plextv.Diagnose(ctx, srv, probe) {
				fmt.Printf("    %-72s %s  %v\n", o.URI, strings.Trim(where(o.Connection), " ()"), o.Err)
			}
			continue
		}
		c := plex.New(cn.URI, srv.AccessToken, cfg.ClientID)
		music := "music library"
		if _, err := c.MusicSection(ctx); err != nil {
			music = "no music library"
		}
		fmt.Printf("%-24s %-6s %s%s  %s\n", srv.Name, owner, cn.URI, where(cn), music)
	}
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

// connectOthers reaches every other server on the account in the background
// and adds the ones with a music library, so search can fan out to them.
func connectOthers(ctx context.Context, cfg config.Config, primaryID string, relayCap int, lib *multi.Library) {
	servers, err := plexTV(cfg.ClientID).Servers(ctx, cfg.AccountToken)
	if err != nil {
		log.Printf("other servers: %v", err)
		return
	}
	var wg sync.WaitGroup
	for _, srv := range servers {
		if srv.ID == primaryID {
			continue
		}
		wg.Add(1)
		go func(srv plextv.Server) {
			defer wg.Done()
			cn, err := plextv.Connect(ctx, srv, plextv.Probe(srv.AccessToken, cfg.ClientID))
			if err != nil {
				log.Printf("server %s: %v", srv.Name, err)
				return
			}
			c := plex.New(cn.URI, srv.AccessToken, cfg.ClientID)
			c.SetServer(srv.ID, srv.Name)
			c.SetConnection(cn.Local, cn.Relay, relayCap)
			c.SetRemoteBitrate(cfg.RemoteBitrate)
			if _, err := c.MusicSection(ctx); err != nil {
				log.Printf("server %s: %v", srv.Name, err)
				return
			}
			lib.Add(multi.Server{ID: srv.ID, Name: srv.Name, Owned: srv.Owned, Via: strings.Trim(where(cn), " ()")}, c)
			log.Printf("server: %s via %s%s", srv.Name, cn.URI, where(cn))
		}(srv)
	}
	wg.Wait()
}

// resolveServer returns the server URL and token to use: the manual pair when
// set, otherwise the account's server through the best connection, falling
// back to the last connection that worked when plex.tv cannot be reached.
func resolveServer(ctx context.Context, cfg *config.Config) (name, url string, cn plextv.Connection, err error) {
	if cfg.Manual() {
		return "configured server", cfg.Server, plextv.Connection{URI: cfg.Server, ServerID: "manual"}, nil
	}
	if cfg.AccountToken == "" {
		return "", "", plextv.Connection{}, errors.New("not signed in; run `dotamp login`")
	}
	tv := plexTV(cfg.ClientID)
	servers, err := tv.Servers(ctx, cfg.AccountToken)
	if err != nil {
		if cfg.LastServer != "" {
			log.Printf("plex.tv unreachable (%v); using the last server %s", err, cfg.LastServer)
			return "last known server", cfg.LastServer, plextv.Connection{URI: cfg.LastServer, ServerID: cfg.LastServerID}, nil
		}
		return "", "", plextv.Connection{}, err
	}
	if len(servers) == 0 {
		return "", "", plextv.Connection{}, errors.New("plex.tv lists no servers for this account; run `dotamp servers` to see which account is signed in")
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
			return "last known server", cfg.LastServer, plextv.Connection{URI: cfg.LastServer, ServerID: cfg.LastServerID}, nil
		}
		return "", "", plextv.Connection{}, err
	}
	cn.ServerID = srv.ID
	cfg.LastServer, cfg.LastToken, cfg.LastServerID = cn.URI, srv.AccessToken, srv.ID
	return srv.Name, cn.URI, cn, nil
}

func run(debugLog, silent bool) (err error) {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) || (err != nil && strings.Contains(err.Error(), "dotamp login")) {
		cfg, err = setup()
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

	lib := multi.New()
	hasPlex := cfg.Manual() || cfg.AccountToken != ""
	if hasPlex {
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

		relayCap := 1000 // kbps the relay carries for a free account
		if !cfg.Manual() {
			if sub, err := plexTV(cfg.ClientID).Subscribed(ctx, cfg.AccountToken); err == nil && sub {
				relayCap = 2000
			}
		}
		primary := plex.New(serverURL, token, cfg.ClientID)
		primary.SetServer(cn.ServerID, serverName)
		primary.SetConnection(cn.Local || !cn.Discovered, cn.Relay, relayCap)
		primary.SetRemoteBitrate(cfg.RemoteBitrate)
		if cfg.Section != "" {
			primary.SetSection(cfg.Section)
		} else {
			id, err := primary.MusicSection(ctx)
			if err != nil {
				return fmt.Errorf("plex at %s: %w", serverURL, err)
			}
			cfg.Section = id
			_ = config.Save(cfg)
		}
		lib.Add(multi.Server{ID: cn.ServerID, Name: serverName, Owned: true, Via: strings.Trim(where(cn), " ()")}, primary)
		if !cfg.Manual() {
			go connectOthers(ctx, cfg, cn.ServerID, relayCap, lib)
		}
	}
	if cfg.Navidrome != nil {
		srv, c := navidromeServer(cfg)
		add := func() {
			if err := c.Ping(ctx); err != nil {
				log.Printf("navidrome %s: %v", cfg.Navidrome.URL, err)
				return
			}
			lib.Add(srv, c)
			log.Printf("server: Navidrome via %s (%s)", cfg.Navidrome.URL, srv.Via)
		}
		if hasPlex {
			go add() // Plex is the primary; Navidrome joins when it answers
		} else {
			add()
			if len(lib.Servers()) == 0 {
				return fmt.Errorf("navidrome at %s did not answer; see the log", cfg.Navidrome.URL)
			}
		}
	}

	rate := cfg.OutputRate
	if rate <= 0 {
		rate = audio.OutRate
	}
	if rate < 8000 || rate > 384000 {
		return fmt.Errorf("output_rate %d is not a sample rate the device can use", rate)
	}
	var out audio.Output
	if silent {
		out = audio.NewSilentOutput(rate)
		log.Printf("output: silent at %d Hz", rate)
	} else {
		o, err := audio.NewOtoOutput(rate)
		if err != nil {
			return fmt.Errorf("audio device at %d Hz: %w", rate, err)
		}
		out = o
		log.Printf("output: %d Hz, 32-bit float", rate)
	}
	eng := audio.NewEngine(out, rate)
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
	app.SetSwitcher(lib)
	app.SetVisual(cfg.Visual)
	app.OnVisual = func(name string) {
		cfg.Visual = name
		_ = config.Save(cfg)
	}
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
