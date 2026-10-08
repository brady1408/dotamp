package mediakeys

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

// MPRIS is how Linux desktops talk to media players: a well-known name on
// the session bus and two interfaces on one object. The desktop sends the
// keyboard's media keys here and reads the properties for its widget.
const (
	mprisName   = "org.mpris.MediaPlayer2.dotamp"
	mprisPath   = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	mprisRoot   = "org.mpris.MediaPlayer2"
	mprisPlayer = "org.mpris.MediaPlayer2.Player"
	statePoll   = 500 * time.Millisecond
)

func playbackStatus(s State) string {
	switch {
	case !s.HasTrack:
		return "Stopped"
	case s.Playing:
		return "Playing"
	}
	return "Paused"
}

// metadata is the MPRIS a{sv} for the current track; trackid is required
// even when nothing plays.
func metadata(s State) map[string]dbus.Variant {
	m := map[string]dbus.Variant{"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/dotamp/track/current"))}
	if !s.HasTrack {
		return m
	}
	if s.Title != "" {
		m["xesam:title"] = dbus.MakeVariant(s.Title)
	}
	if s.Artist != "" {
		m["xesam:artist"] = dbus.MakeVariant([]string{s.Artist})
	}
	if s.Album != "" {
		m["xesam:album"] = dbus.MakeVariant(s.Album)
	}
	if s.Length > 0 {
		m["mpris:length"] = dbus.MakeVariant(s.Length.Microseconds())
	}
	return m
}

// player is the exported org.mpris.MediaPlayer2.Player object.
type player struct{ p Player }

func (o player) Next() *dbus.Error      { o.p.Handle(Next); return nil }
func (o player) Previous() *dbus.Error  { o.p.Handle(Prev); return nil }
func (o player) PlayPause() *dbus.Error { o.p.Handle(PlayPause); return nil }
func (o player) Play() *dbus.Error {
	if s := o.p.State(); !s.Playing {
		o.p.Handle(PlayPause)
	}
	return nil
}
func (o player) Pause() *dbus.Error {
	if s := o.p.State(); s.Playing {
		o.p.Handle(PlayPause)
	}
	return nil
}
func (o player) Stop() *dbus.Error { return o.Pause() }

// root is the exported org.mpris.MediaPlayer2 object.
type root struct{}

func (root) Raise() *dbus.Error { return nil }
func (root) Quit() *dbus.Error  { return nil }

var mprisNode = &introspect.Node{
	Name: string(mprisPath),
	Interfaces: []introspect.Interface{
		introspect.IntrospectData,
		prop.IntrospectData,
		{Name: mprisRoot, Methods: introspect.Methods(root{})},
		{Name: mprisPlayer, Methods: introspect.Methods(player{})},
	},
}

// listenMPRIS owns the bus name for as long as ctx lives, serving the
// desktop's calls and refreshing the properties from p twice a second.
func listenMPRIS(ctx context.Context, p Player, address string) error {
	if address == "" {
		return nil // no session bus, nothing to register with
	}
	conn, err := dbus.Connect(address)
	if err != nil {
		return fmt.Errorf("media keys: %w", err)
	}
	defer conn.Close()
	reply, err := conn.RequestName(mprisName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("media keys: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return errors.New("media keys: another dotamp already owns the MPRIS name")
	}
	st := p.State()
	ro := func(v any) *prop.Prop { return &prop.Prop{Value: v, Writable: false, Emit: prop.EmitTrue} }
	props, err := prop.Export(conn, mprisPath, prop.Map{
		mprisRoot: {
			"Identity": ro("dotamp"), "CanQuit": ro(false), "CanRaise": ro(false), "HasTrackList": ro(false),
			"SupportedUriSchemes": ro([]string{}), "SupportedMimeTypes": ro([]string{}),
		},
		mprisPlayer: {
			"PlaybackStatus": ro(playbackStatus(st)), "Metadata": ro(metadata(st)),
			"LoopStatus": ro("None"), "Rate": ro(1.0), "Shuffle": ro(false), "Volume": ro(1.0),
			"Position": ro(int64(0)), "MinimumRate": ro(1.0), "MaximumRate": ro(1.0),
			"CanGoNext": ro(true), "CanGoPrevious": ro(true), "CanPlay": ro(true), "CanPause": ro(true),
			"CanSeek": ro(false), "CanControl": ro(true),
		},
	})
	if err != nil {
		return fmt.Errorf("media keys: %w", err)
	}
	if err := conn.Export(root{}, mprisPath, mprisRoot); err != nil {
		return fmt.Errorf("media keys: %w", err)
	}
	if err := conn.Export(player{p}, mprisPath, mprisPlayer); err != nil {
		return fmt.Errorf("media keys: %w", err)
	}
	if err := conn.Export(introspect.NewIntrospectable(mprisNode), mprisPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("media keys: %w", err)
	}
	log.Printf("media keys: registered as %s", mprisName)
	tick := time.NewTicker(statePoll)
	defer tick.Stop()
	last := st
	for {
		select {
		case <-ctx.Done():
			_, _ = conn.ReleaseName(mprisName)
			return nil
		case <-tick.C:
			cur := p.State()
			if cur == last {
				continue
			}
			if playbackStatus(cur) != playbackStatus(last) {
				props.SetMust(mprisPlayer, "PlaybackStatus", playbackStatus(cur))
			}
			if cur.Title != last.Title || cur.Artist != last.Artist || cur.Album != last.Album || cur.Length != last.Length || cur.HasTrack != last.HasTrack {
				props.SetMust(mprisPlayer, "Metadata", metadata(cur))
			}
			last = cur
		}
	}
}
