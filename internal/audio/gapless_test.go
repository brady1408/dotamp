package audio

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

// constSource emits a constant value, so its frames are distinguishable from
// the sine of fakeSource and from inserted silence.
type constSource struct {
	fakeSource
	value float32
}

func (c *constSource) Read(dst []float32) (int, error) {
	n := 0
	for n+1 < len(dst) && c.pos < c.frames {
		dst[n], dst[n+1] = c.value, c.value
		n += 2
		c.pos++
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// pullSlowly reads frames at a pace the decoder easily keeps ahead of, so any
// silence in the output is a real gap, not an underrun. It returns the left
// channel as int16 values.
func pullSlowly(p *fakePlayer, frames int) []int16 {
	out := make([]int16, 0, frames)
	for len(out) < frames {
		b := p.pull(256)
		for i := 0; i+1 < len(b); i += 4 {
			out = append(out, int16(uint16(b[i])|uint16(b[i+1])<<8))
		}
		time.Sleep(200 * time.Microsecond)
	}
	return out
}

func TestEngineHandoverLeavesNoGap(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	a := &fakeSource{rate: 44100, frames: 4410}                                       // 0.1 s sine
	b := &constSource{fakeSource: fakeSource{rate: 48000, frames: 4800}, value: 0.25} // 0.1 s at 48 kHz
	e.Play(a)
	e.SetNext(b)
	time.Sleep(50 * time.Millisecond) // let the decoder run across the boundary
	s := pullSlowly(out.p, 4410+2000) // into B, but not past its end
	const bVal = int16(8191)          // 0.25 × 32767, truncated as the reader does
	firstB := -1
	for i, v := range s {
		if v == bVal {
			firstB = i
			break
		}
	}
	if firstB < 0 {
		t.Fatal("the next source never played")
	}
	// Walk back from the first B frame: the frames before it must be A's sine,
	// not a run of zeros.
	zeros := 0
	for i := firstB - 1; i >= 0 && s[i] == 0; i-- {
		zeros++
	}
	if zeros > 2 { // a sine crosses zero; a gap is dozens of zeros
		t.Fatalf("%d silent frames before the handover", zeros)
	}
	if firstB < 4300 || firstB > 4500 {
		t.Fatalf("B started at frame %d, want ~4410 (A's length)", firstB)
	}
	select {
	case <-e.Handover():
	default:
		t.Fatal("Handover should have fired")
	}
	if l := e.Length(); l != b.Length() {
		t.Fatalf("length after handover = %v, want %v", l, b.Length())
	}
	if !a.closed {
		t.Fatal("the finished source must be closed after the handover")
	}
	select {
	case <-e.Done():
		t.Fatal("Done must not fire for a handover")
	default:
	}
}

func TestEngineHandoverPositionRestarts(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	a := &fakeSource{rate: 44100, frames: 4410}
	b := &fakeSource{rate: 44100, frames: 44100}
	e.Play(a)
	e.SetNext(b)
	time.Sleep(50 * time.Millisecond)
	pullSlowly(out.p, 4410+2205) // 0.05 s into B
	if p := e.Position(); p < 40*time.Millisecond || p > 60*time.Millisecond {
		t.Fatalf("position after handover = %v, want ~50 ms", p)
	}
}

func TestEngineSeekKeepsQueuedNext(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	a := &fakeSource{rate: 44100, frames: 4410}
	b := &constSource{fakeSource: fakeSource{rate: 44100, frames: 4410}, value: 0.25}
	e.Play(a)
	e.SetNext(b)
	time.Sleep(50 * time.Millisecond) // decoder has already moved on to B
	e.Seek(50 * time.Millisecond)     // back into A: B must be rewound and kept
	time.Sleep(50 * time.Millisecond)
	s := pullSlowly(out.p, 2205+4000) // into B, but not past its end
	const bVal = int16(8191)          // 0.25 × 32767, truncated as the reader does
	firstB, lastB := -1, -1
	for i, v := range s {
		if v == bVal {
			if firstB < 0 {
				firstB = i
			}
			lastB = i
		}
	}
	if firstB < 2100 || firstB > 2300 {
		t.Fatalf("B started at %d, want ~2205 (A's remaining half)", firstB)
	}
	if lastB-firstB < 3900 {
		t.Fatalf("B played %d contiguous frames, want ~4000: it was not rewound", lastB-firstB)
	}
	if b.closed {
		t.Fatal("B must not be closed by a seek on A")
	}
}

func TestEnginePlayDiscardsPendingNext(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	a := &fakeSource{rate: 44100, frames: 44100}
	b := &fakeSource{rate: 44100, frames: 44100}
	c := &fakeSource{rate: 44100, frames: 44100}
	e.Play(a)
	e.SetNext(b)
	e.Play(c)
	if !b.closed || !a.closed {
		t.Fatal("Play must close the previous source and the pending next")
	}
	if got := e.TakeNext(); got != nil {
		t.Fatal("no next should remain after Play")
	}
}

func TestEngineTakeNextRewindsAStartedNext(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	a := &fakeSource{rate: 44100, frames: 441}
	b := &fakeSource{rate: 44100, frames: 44100}
	e.Play(a)
	e.SetNext(b)
	time.Sleep(50 * time.Millisecond) // decoder is well into B
	got := e.TakeNext()
	if got != b {
		t.Fatalf("TakeNext = %v, want b", got)
	}
	if b.pos != 0 {
		t.Fatalf("a started next must be rewound; pos = %d", b.pos)
	}
}

// The controller hands a prefetched track to the engine and advances the
// queue on the handover instead of opening the stream again.
func TestControllerGaplessHandover(t *testing.T) {
	srv := fixtureServer(t)
	lib := &fakeLib{url: srv.URL}
	out := &fakeOutput{}
	eng := NewEngine(out)
	defer eng.Close()
	c := NewController(lib, eng, func(string) {})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	ts := []library.Track{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}
	if err := c.PlayTracks(ctx, ts, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(700 * time.Millisecond) // one prefetch tick: the 2 s fixture is within 10 s of its end
	if eng.PeekNext() == nil {
		t.Fatal("prefetch should have queued the next source in the engine")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out.p.pull(1024)
		time.Sleep(time.Millisecond)
		if cur, _ := c.Current(); cur.ID == "b" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never handed over to b")
		}
	}
	if got := lib.streamOpens(); len(got) != 2 {
		t.Fatalf("stream opens = %v, want exactly one open per track", got)
	}
	if c.Index() != 1 {
		t.Fatalf("queue index = %d, want 1", c.Index())
	}
}
