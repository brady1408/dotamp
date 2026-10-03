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

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/config"
	"github.com/brady1408/dotamp/internal/plex"
	"github.com/brady1408/dotamp/internal/ui"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	debugLog := flag.Bool("debug", false, "log with source locations")
	flag.Parse()
	if *showVersion {
		fmt.Println("dotamp", version)
		return
	}
	if err := run(*debugLog); err != nil {
		fmt.Fprintln(os.Stderr, "dotamp:", err)
		os.Exit(1)
	}
}

func run(debugLog bool) (err error) {
	cfg, err := config.Load()
	if errors.Is(err, config.ErrNotFound) {
		return fmt.Errorf("no config at %s\n\nCreate it with:\n\n  {\"server\": \"http://192.168.23.23:32400\", \"token\": \"YOUR-PLEX-TOKEN\"}\n", config.Path())
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
	}
	log.SetFlags(flags)
	log.Printf("dotamp %s starting; config %s", version, config.Path())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	lib := plex.New(cfg.Server, cfg.Token, cfg.ClientID)
	if cfg.Section != "" {
		lib.SetSection(cfg.Section)
	} else {
		id, err := lib.MusicSection(ctx)
		if err != nil {
			return fmt.Errorf("plex at %s: %w", cfg.Server, err)
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
	go ctrl.Run(ctx)

	// A panic on the UI goroutine must restore the terminal before it reaches
	// the user; the message points at the log.
	defer func() {
		if r := recover(); r != nil {
			screen.Fini()
			log.Printf("panic: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("crashed: %v (see %s)", r, logPath)
		}
	}()
	return app.Run(ctx)
}
