package stream

import (
	"testing"

	"github.com/brady1408/dotamp/internal/audio"
)

func slices(feed *audio.Feed, slice, n int) {
	feed.Write(make([]float32, 2*slice*n))
}

func TestFeederWaitsForThePrimeDepthBeforeStarting(t *testing.T) {
	feed := audio.NewFeed(48000 * 2)
	f := newFeeder(feed, 960)
	slices(feed, 960, primeSlices-1)
	if f.take() != nil {
		t.Fatal("started one slice short of the prime depth")
	}
	slices(feed, 960, 1)
	if got := f.take(); len(got) != 2*960 {
		t.Fatalf("took %d samples, want one slice", len(got))
	}
	for i := 1; i < primeSlices; i++ {
		if got := f.take(); len(got) != 2*960 {
			t.Fatalf("take %d = %d samples; a primed feeder runs the feed down to empty", i, len(got))
		}
	}
	if f.take() != nil {
		t.Fatal("took from an empty feed")
	}
}

func TestFeederStopsAndReprimesWhenTheFeedRunsDry(t *testing.T) {
	feed := audio.NewFeed(48000 * 2)
	f := newFeeder(feed, 960)
	slices(feed, 960, primeSlices)
	for range primeSlices {
		f.take()
	}
	if f.take() != nil {
		t.Fatal("took from an empty feed")
	}
	slices(feed, 960, primeSlices-1)
	if f.take() != nil {
		t.Fatal("restarted short of the prime depth after running dry")
	}
	slices(feed, 960, 1)
	if f.take() == nil {
		t.Fatal("did not restart once the prime depth was back")
	}
}

func TestFeederNeverTakesAPartialSlice(t *testing.T) {
	feed := audio.NewFeed(48000 * 2)
	f := newFeeder(feed, 960)
	slices(feed, 960, primeSlices)
	for range primeSlices - 1 {
		f.take()
	}
	feed.Write(make([]float32, 2*480)) // the device delivered half a slice
	// 1.5 slices available: one full take, then the half stays put
	if f.take() == nil {
		t.Fatal("refused a full slice")
	}
	if f.take() != nil {
		t.Fatal("took a partial slice")
	}
	if feed.Available() != 480 {
		t.Fatalf("partial slice consumed: %d frames left", feed.Available())
	}
}

func TestFeederSkipsAheadWhenTheFeedHasDrifted(t *testing.T) {
	feed := audio.NewFeed(48000 * 2)
	f := newFeeder(feed, 960)
	slices(feed, 960, driftSlices+5)
	if f.take() == nil {
		t.Fatal("no slice after a skip")
	}
	if got := feed.Available(); got != 960*(primeSlices-1) {
		t.Fatalf("%d frames left after the skip, want %d", got, 960*(primeSlices-1))
	}
	if want := 960 * (driftSlices + 5 - primeSlices); f.dropped != want {
		t.Fatalf("dropped = %d, want %d", f.dropped, want)
	}
}
