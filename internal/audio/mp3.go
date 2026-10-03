package audio

import (
	"encoding/binary"
	"io"
	"time"

	mp3 "github.com/hajimehoshi/go-mp3"
)

// go-mp3 always emits 16-bit little-endian stereo at the file's sample rate.
type mp3Source struct {
	rs  io.ReadSeekCloser
	dec *mp3.Decoder
	raw []byte
}

func NewMP3(rs io.ReadSeekCloser) (Source, error) {
	d, err := mp3.NewDecoder(rs)
	if err != nil {
		return nil, err
	}
	return &mp3Source{rs: rs, dec: d}, nil
}

func (s *mp3Source) SampleRate() int { return s.dec.SampleRate() }

func (s *mp3Source) Length() time.Duration {
	n := s.dec.Length()
	if n <= 0 {
		return 0
	}
	return time.Duration(float64(n/4) / float64(s.dec.SampleRate()) * float64(time.Second))
}

func (s *mp3Source) Read(dst []float32) (int, error) {
	want := (len(dst) / 2) * 4
	if cap(s.raw) < want {
		s.raw = make([]byte, want)
	}
	n, err := io.ReadFull(s.dec, s.raw[:want])
	if err == io.ErrUnexpectedEOF {
		err = nil
		if n == 0 {
			err = io.EOF
		}
	}
	if err != nil && n == 0 {
		return 0, err
	}
	n -= n % 4
	for i := 0; i < n; i += 2 {
		dst[i/2] = float32(int16(binary.LittleEndian.Uint16(s.raw[i:]))) / 32768
	}
	return n / 2, nil
}

func (s *mp3Source) Seek(d time.Duration) error {
	frame := int64(d.Seconds() * float64(s.dec.SampleRate()))
	_, err := s.dec.Seek(frame*4, io.SeekStart)
	return err
}

func (s *mp3Source) Interrupt() {
	if i, ok := s.rs.(interrupter); ok {
		i.Interrupt()
	}
}

func (s *mp3Source) Close() error { return s.rs.Close() }
