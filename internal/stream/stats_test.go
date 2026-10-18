package stream

import (
	"strings"
	"testing"
	"time"
)

func TestStatsLineSummarisesTheLastPeriod(t *testing.T) {
	t0 := time.Unix(0, 0)
	s := newStats(t0)
	s.slot(true, 500*time.Microsecond)
	s.slot(false, 0)
	s.gap(45 * time.Millisecond)
	s.depth(1200)
	if s.due(t0.Add(2 * time.Second)) {
		t.Fatal("reported before the period was up")
	}
	if !s.due(t0.Add(statsEvery + time.Millisecond)) {
		t.Fatal("did not report after the period")
	}
	line := s.line()
	for _, want := range []string{"slots=2", "empty=1", "maxgap=45ms", "maxwrite=500µs", "depth=1200"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line %q lacks %q", line, want)
		}
	}
	s.reset(t0.Add(statsEvery))
	if strings.Contains(s.line(), "empty=1") {
		t.Fatal("reset kept the old counts")
	}
}
