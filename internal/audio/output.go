package audio

import "io"

// Player is the slice of oto.Player the engine needs; tests supply a fake.
type Player interface {
	Play()
	Pause()
	BufferedSize() int // bytes of float32 stereo still queued in the device
	Close() error
}

// Output creates players that read 32-bit float stereo at the engine's rate from r.
type Output interface {
	NewPlayer(r io.Reader) Player
}
