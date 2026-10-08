package audio

import (
	"sync/atomic"
	"testing"
	"time"
)

type countingReader struct{ n atomic.Int64 }

func (c *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	c.n.Add(int64(len(p)))
	return len(p), nil
}

func TestSilentPlayerDrainsAtTheSampleRate(t *testing.T) {
	const rate = 44100
	r := &countingReader{}
	p := NewSilentOutput(rate).NewPlayer(r)
	defer p.Close()
	p.Play()
	time.Sleep(300 * time.Millisecond)
	p.Pause()
	got := r.n.Load()
	want := int64(rate*frameBytes) * 3 / 10 // 300 ms of float32 stereo
	if got < want*6/10 || got > want*15/10 {
		t.Fatalf("300 ms of playback should read about %d bytes, read %d", want, got)
	}
	if p.BufferedSize() <= 0 || p.BufferedSize() > rate*frameBytes/10 {
		t.Fatalf("BufferedSize should be a small positive lag, got %d", p.BufferedSize())
	}
	time.Sleep(100 * time.Millisecond)
	if after := r.n.Load(); after != got {
		t.Fatalf("a paused player must not read: %d -> %d", got, after)
	}
}
