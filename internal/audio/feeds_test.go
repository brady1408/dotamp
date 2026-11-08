package audio

import "testing"

func TestExtraFeedsGetTheSameFrames(t *testing.T) {
	e := NewEngine(&fakeOutput{}, OutRate)
	t.Cleanup(e.Close)
	extra := e.NewFeed()
	r := &reader{e: e}
	e.ring.Write(make([]float32, 2*512))
	r.Read(make([]byte, 512*frameBytes))
	if e.Feed().Available() == 0 || extra.Available() != e.Feed().Available() {
		t.Fatalf("main %d, extra %d", e.Feed().Available(), extra.Available())
	}
}

func TestDroppedFeedStopsReceiving(t *testing.T) {
	e := NewEngine(&fakeOutput{}, OutRate)
	t.Cleanup(e.Close)
	extra := e.NewFeed()
	e.DropFeed(extra)
	r := &reader{e: e}
	e.ring.Write(make([]float32, 2*512))
	r.Read(make([]byte, 512*frameBytes))
	if extra.Available() != 0 {
		t.Fatalf("a dropped feed got %d frames", extra.Available())
	}
}
