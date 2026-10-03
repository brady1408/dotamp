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
	Close() error
}

func NewSource(codec string, rs io.ReadSeekCloser) (Source, error) {
	switch codec {
	case "flac":
		return NewFLAC(rs)
	case "mp3":
		return NewMP3(rs)
	}
	return nil, fmt.Errorf("audio: unsupported codec %q", codec)
}
