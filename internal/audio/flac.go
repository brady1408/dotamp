package audio

import (
	"io"
	"time"

	"github.com/mewkiz/flac"
)

type flacSource struct {
	rs     io.ReadSeekCloser
	stream *flac.Stream
	rate   int
	nch    int
	scale  float32
	pend   []float32 // decoded but not yet handed out
	skip   int       // frames to drop after a seek landed before the target
	total  uint64
}

func NewFLAC(rs io.ReadSeekCloser) (Source, error) {
	st, err := flac.NewSeek(rs)
	if err != nil {
		return nil, err
	}
	return &flacSource{
		rs: rs, stream: st,
		rate:  int(st.Info.SampleRate),
		nch:   int(st.Info.NChannels),
		scale: 1 / float32(int64(1)<<(st.Info.BitsPerSample-1)),
		total: st.Info.NSamples,
	}, nil
}

func (s *flacSource) SampleRate() int { return s.rate }

func (s *flacSource) Length() time.Duration {
	if s.total == 0 {
		return 0
	}
	return time.Duration(float64(s.total) / float64(s.rate) * float64(time.Second))
}

func (s *flacSource) Read(dst []float32) (int, error) {
	n := 0
	for n < len(dst) {
		if len(s.pend) == 0 {
			fr, err := s.stream.ParseNext()
			if err != nil {
				if n > 0 {
					return n, nil
				}
				return 0, err // io.EOF at end of stream
			}
			blk := int(fr.BlockSize)
			s.pend = s.pend[:0]
			first := 0
			if s.skip > 0 {
				first = min(s.skip, blk)
				s.skip -= first
			}
			for i := first; i < blk; i++ {
				l := float32(fr.Subframes[0].Samples[i]) * s.scale
				r := l
				if s.nch > 1 {
					r = float32(fr.Subframes[1].Samples[i]) * s.scale
				}
				s.pend = append(s.pend, l, r)
			}
		}
		c := copy(dst[n:], s.pend)
		c -= c % 2
		s.pend = s.pend[c:]
		n += c
		if c == 0 {
			break
		}
	}
	return n, nil
}

func (s *flacSource) Seek(d time.Duration) error {
	sample := uint64(d.Seconds() * float64(s.rate))
	got, err := s.stream.Seek(sample)
	if err != nil {
		return err
	}
	s.pend = s.pend[:0]
	s.skip = 0
	if got < sample { // the seek table lands on a frame boundary; drop up to the exact sample
		s.skip = int(sample - got)
	}
	return nil
}

func (s *flacSource) Interrupt() {
	if i, ok := s.rs.(interrupter); ok {
		i.Interrupt()
	}
}

func (s *flacSource) Close() error { return s.rs.Close() }
