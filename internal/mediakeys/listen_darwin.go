//go:build darwin

package mediakeys

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// macOS sends media keys to whichever process claimed "Now Playing" through
// MediaPlayer.framework. dotamp registers a target for the remote commands
// and publishes the track, calling the Objective-C runtime through purego
// so the build stays cgo-free. Commands arrive on the main thread's run
// loop, which RunMain spins.

var (
	darwinOnce  sync.Once
	darwinErr   error
	darwinClass objc.Class
	remoteMu    sync.Mutex
	remote      Player // the Player the remote commands drive
)

const (
	frameworkMediaPlayer = "/System/Library/Frameworks/MediaPlayer.framework/MediaPlayer"
	frameworkFoundation  = "/System/Library/Frameworks/Foundation.framework/Foundation"
)

func sel(name string) objc.SEL { return objc.RegisterName(name) }

func handle(a Action) {
	remoteMu.Lock()
	p := remote
	remoteMu.Unlock()
	if p != nil {
		p.Handle(a)
	}
}

func state() State {
	remoteMu.Lock()
	p := remote
	remoteMu.Unlock()
	if p == nil {
		return State{}
	}
	return p.State()
}

// setup loads the frameworks and defines the target class once.
func setup() error {
	darwinOnce.Do(func() {
		for _, fw := range []string{frameworkFoundation, frameworkMediaPlayer} {
			if _, err := purego.Dlopen(fw, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
				darwinErr = fmt.Errorf("media keys: %w", err)
				return
			}
		}
		// MPRemoteCommandHandlerStatusSuccess is 0.
		cls, err := objc.RegisterClass("DotampRemote", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
			{Cmd: sel("playPause:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) int { handle(PlayPause); return 0 }},
			{Cmd: sel("play:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) int {
				if !state().Playing {
					handle(PlayPause)
				}
				return 0
			}},
			{Cmd: sel("pause:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) int {
				if state().Playing {
					handle(PlayPause)
				}
				return 0
			}},
			{Cmd: sel("next:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) int { handle(Next); return 0 }},
			{Cmd: sel("previous:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) int { handle(Prev); return 0 }},
		})
		if err != nil {
			darwinErr = fmt.Errorf("media keys: %w", err)
			return
		}
		darwinClass = cls
	})
	return darwinErr
}

func nsString(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), s)
}

// publish pushes the state to MPNowPlayingInfoCenter.
func publish(center objc.ID, s State) {
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
	defer pool.Send(sel("drain"))
	dict := objc.ID(objc.GetClass("NSMutableDictionary")).Send(sel("dictionary"))
	for k, v := range nowPlayingFields(s) {
		var val objc.ID
		switch x := v.(type) {
		case string:
			val = nsString(x)
		case float64:
			val = objc.ID(objc.GetClass("NSNumber")).Send(sel("numberWithDouble:"), x)
		}
		dict.Send(sel("setObject:forKey:"), val, nsString(k))
	}
	center.Send(sel("setNowPlayingInfo:"), dict)
	center.Send(sel("setPlaybackState:"), playbackState(s))
}

func listen(ctx context.Context, p Player) error {
	if err := setup(); err != nil {
		return err
	}
	remoteMu.Lock()
	remote = p
	remoteMu.Unlock()
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(sel("new"))
	target := objc.ID(darwinClass).Send(sel("new"))
	cc := objc.ID(objc.GetClass("MPRemoteCommandCenter")).Send(sel("sharedCommandCenter"))
	commands := map[string]string{
		"togglePlayPauseCommand": "playPause:",
		"playCommand":            "play:",
		"pauseCommand":           "pause:",
		"nextTrackCommand":       "next:",
		"previousTrackCommand":   "previous:",
	}
	for cmd, action := range commands {
		c := cc.Send(sel(cmd))
		c.Send(sel("setEnabled:"), true)
		c.Send(sel("addTarget:action:"), target, sel(action))
	}
	center := objc.ID(objc.GetClass("MPNowPlayingInfoCenter")).Send(sel("defaultCenter"))
	last := p.State()
	publish(center, last)
	pool.Send(sel("drain"))
	log.Printf("media keys: registered with Now Playing")
	tick := time.NewTicker(statePoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			for cmd := range commands {
				cc.Send(sel(cmd)).Send(sel("removeTarget:"), target)
			}
			publish(center, State{})
			remoteMu.Lock()
			remote = nil
			remoteMu.Unlock()
			return nil
		case <-tick.C:
			cur := p.State()
			if cur != last {
				publish(center, cur)
				last = cur
			}
		}
	}
}
