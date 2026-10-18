package stream

import (
	"fmt"
	"time"
)

const statsEvery = 5 * time.Second

// stats summarises a stretch of the stream for the log: how many slots
// went out, how many had no audio, the worst tick gap and socket write,
// and the deepest the feed got.
type stats struct {
	start            time.Time
	slots, empty     int
	maxGap, maxWrite time.Duration
	maxDepth         int
	trimmed          int
}

func newStats(now time.Time) *stats { return &stats{start: now} }

func (s *stats) slot(sent bool, write time.Duration) {
	s.slots++
	if !sent {
		s.empty++
	}
	s.maxWrite = max(s.maxWrite, write)
}

func (s *stats) gap(d time.Duration) { s.maxGap = max(s.maxGap, d) }
func (s *stats) depth(frames int)    { s.maxDepth = max(s.maxDepth, frames) }
func (s *stats) due(now time.Time) bool {
	return now.Sub(s.start) >= statsEvery
}

func (s *stats) line() string {
	return fmt.Sprintf("slots=%d empty=%d maxgap=%v maxwrite=%v depth=%d trimmed=%d",
		s.slots, s.empty, s.maxGap.Round(time.Millisecond), s.maxWrite.Round(time.Microsecond), s.maxDepth, s.trimmed)
}

func (s *stats) reset(now time.Time) { *s = stats{start: now} }
