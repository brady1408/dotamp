package audio

import (
	"fmt"
	"io"
	"time"
)

// Source is a decoded audio stream: interleaved stereo float32 in [-1, 1].
type Source interface {
	SampleRate() int
	Read(dst []float32) (int, error)
	Seek(d time.Duration) error
	Length() time.Duration
	// Interrupt makes a Read blocked on I/O return promptly with an error.
	// The source is then only good for Seek or Close.
	Interrupt()
	Close() error
}

// interrupter is implemented by readers whose blocked Read can be cut short.
type interrupter interface{ Interrupt() }

func NewSource(codec string, rs io.ReadSeekCloser) (Source, error) {
	switch codec {
	case "flac":
		return NewFLAC(rs)
	case "mp3":
		return NewMP3(rs)
	}
	return nil, fmt.Errorf("audio: unsupported codec %q", codec)
}
