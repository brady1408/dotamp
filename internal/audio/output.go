package audio

import "io"

// Player is the slice of oto.Player the engine needs; tests supply a fake.
type Player interface {
	Play()
	Pause()
	BufferedSize() int // bytes of s16le stereo still queued in the device
	Close() error
}

// Output creates players that read s16le stereo at OutRate from r.
type Output interface {
	NewPlayer(r io.Reader) Player
}
