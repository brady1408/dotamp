// Package mediakeys delivers the keyboard's media keys (play/pause, next,
// previous) to the player even when the terminal does not have focus. The
// terminal never sees those keys: the operating system hands them to
// whatever registered for them, so each platform registers in its own way.
// Only Windows is wired so far; elsewhere Listen returns at once.
package mediakeys

import "context"

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

// Handler receives each media key press. It is called from the listener's
// own goroutine, so it must be safe to call from there.
type Handler func(Action)

// Listen registers the media keys and calls h for each press until ctx is
// done. It returns the registration error, if any, once the loop has
// stopped; a platform without support returns nil immediately.
func Listen(ctx context.Context, h Handler) error { return listen(ctx, h) }
