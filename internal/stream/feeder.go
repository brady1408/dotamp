package stream

import "github.com/brady1408/dotamp/internal/audio"

const (
	primeSlices = 8  // 160 ms in hand before the stream starts or resumes
	driftSlices = 50 // a second behind the device: skip ahead once
)

// feeder takes whole 20 ms slices from the engine feed. The device fills
// the feed in blocks of its own size and timing, so the feeder starts, and
// restarts after running dry, only with primeSlices in hand: a slot then
// always finds a full slice, and a device block that lands a little late
// never leaves a hole in the stream. A feed that has drifted far ahead,
// which only clock drift between the device and the pacer can cause, is
// skipped back to the prime depth in one step.
type feeder struct {
	feed    *audio.Feed
	slice   int // frames per slot
	primed  bool
	buf     []float32
	dropped int // frames skipped for drift
}

func newFeeder(feed *audio.Feed, slice int) *feeder {
	return &feeder{feed: feed, slice: slice, buf: make([]float32, 2*slice)}
}

// take returns the next slice, or nil when the feed cannot supply a whole
// one; it never consumes a partial slice.
func (f *feeder) take() []float32 {
	avail := f.feed.Available()
	if avail > f.slice*driftSlices {
		for avail > f.slice*primeSlices {
			n := min(avail-f.slice*primeSlices, f.slice)
			f.feed.Read(f.buf[:2*n])
			f.dropped += n
			avail -= n
		}
	}
	need := f.slice
	if !f.primed {
		need = f.slice * primeSlices
	}
	if avail < need {
		f.primed = false
		return nil
	}
	f.primed = true
	f.feed.Read(f.buf)
	return f.buf
}
