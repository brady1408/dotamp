// Package mediakeys delivers the keyboard's media keys (play/pause, next,
// previous) to the player even when the terminal does not have focus. The
// terminal never sees those keys: the operating system hands them to
// whatever registered for them, so each platform registers in its own way.
// Windows registers global hotkeys; Linux registers an MPRIS player on the
// session bus, which is where desktops send media keys; macOS is not wired
// yet and Listen returns at once there.
package mediakeys

import (
	"context"
	"time"
)

// Action is what a media key asks for.
type Action int

const (
	None Action = iota
	PlayPause
	Next
	Prev
)

func (a Action) String() string {
	switch a {
	case PlayPause:
		return "play/pause"
	case Next:
		return "next"
	case Prev:
		return "previous"
	}
	return "none"
}

// Windows virtual-key codes for the media keys; the same values are used
// by the mapping test on every platform.
const (
	vkMediaNextTrack = 0xB0
	vkMediaPrevTrack = 0xB1
	vkMediaPlayPause = 0xB3
)

func actionFor(vk uint32) Action {
	switch vk {
	case vkMediaPlayPause:
		return PlayPause
	case vkMediaNextTrack:
		return Next
	case vkMediaPrevTrack:
		return Prev
	}
	return None
}

// State is what the desktop's media controls show for the player.
type State struct {
	HasTrack, Playing    bool
	Title, Artist, Album string
	Length               time.Duration
}

// Player is what the listener drives: it handles each media key press and
// reports what is playing. Both are called from the listener's goroutine,
// so they must be safe to call from there.
type Player interface {
	Handle(Action)
	State() State
}

// Listen registers the media keys and drives p until ctx is done. It
// returns the registration error, if any, once the loop has stopped; a
// platform without support returns nil immediately.
func Listen(ctx context.Context, p Player) error { return listen(ctx, p) }
