package audio

import (
	"fmt"
	"io"
	"os"
	"time"
)

// Format is what a decoder knows about the file it is playing. Bitrate is
// derived from size and length when the container does not state one.
type Format struct {
	Codec      string
	SampleRate int
	BitDepth   int // 0 for lossy codecs
	Bitrate    int // kbps
}

// sizer is implemented by readers that know their total length in bytes.
type sizer interface{ Size() int64 }

func sizeOf(rs io.ReadSeeker) int64 {
	if sz, ok := rs.(sizer); ok {
		return sz.Size()
	}
	if f, ok := rs.(interface{ Stat() (os.FileInfo, error) }); ok {
		if st, err := f.Stat(); err == nil {
			return st.Size()
		}
	}
	return 0
}

func kbps(size int64, d time.Duration) int {
	if size <= 0 || d <= 0 {
		return 0
	}
	return int(float64(size)*8/d.Seconds()/1000 + 0.5)
}

// Source is a decoded audio stream: interleaved stereo float32 in [-1, 1].
type Source interface {
	SampleRate() int
	Read(dst []float32) (int, error)
	Seek(d time.Duration) error
	Length() time.Duration
	Format() Format
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
