package audio

import (
	"io"
	"math"
	"sync"
	"testing"
	"time"
)

// fakeSource is a stereo sine of the given rate and length with seek support.
type fakeSource struct {
	rate, frames, pos int
	closed            bool
}

func (f *fakeSource) SampleRate() int { return f.rate }
func (f *fakeSource) Length() time.Duration {
	return time.Duration(float64(f.frames) / float64(f.rate) * float64(time.Second))
}
func (f *fakeSource) Read(dst []float32) (int, error) {
	n := 0
	for n+1 < len(dst) && f.pos < f.frames {
		v := float32(math.Sin(2 * math.Pi * 440 * float64(f.pos) / float64(f.rate)))
		dst[n], dst[n+1] = v, v
		n += 2
		f.pos++
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
func (f *fakeSource) Seek(d time.Duration) error {
	f.pos = int(d.Seconds() * float64(f.rate))
	return nil
}
func (f *fakeSource) Close() error { f.closed = true; return nil }

// fakeOutput lets the test pull bytes from the engine's reader on demand.
// fakePlayer is goroutine-safe like oto's player: the controller's Run
// goroutine and the test both drive it.
type fakeOutput struct{ p *fakePlayer }
type fakePlayer struct {
	mu       sync.Mutex
	r        io.Reader
	playing  bool
	buffered int
	pauses   int
}

func (o *fakeOutput) NewPlayer(r io.Reader) Player { o.p = &fakePlayer{r: r}; return o.p }
func (p *fakePlayer) Play()                        { p.mu.Lock(); p.playing = true; p.mu.Unlock() }
func (p *fakePlayer) Pause()                       { p.mu.Lock(); p.playing = false; p.pauses++; p.mu.Unlock() }
func (p *fakePlayer) BufferedSize() int            { p.mu.Lock(); defer p.mu.Unlock(); return p.buffered }
func (p *fakePlayer) Close() error                 { return nil }
func (p *fakePlayer) isPlaying() bool              { p.mu.Lock(); defer p.mu.Unlock(); return p.playing }
func (p *fakePlayer) setBuffered(n int)            { p.mu.Lock(); p.buffered = n; p.mu.Unlock() }
func (p *fakePlayer) pull(frames int) []byte {
	b := make([]byte, frames*4)
	_, _ = io.ReadFull(p.r, b)
	return b
}

// pullUntilDone drains the engine's reader until it signals Done. The test
// pulls far faster than real time, so most pulls return inserted silence while
// the decoder catches up; a short sleep on those keeps the loop from spinning.
func pullUntilDone(t *testing.T, e *Engine, p *fakePlayer) (frames int, nonSilent int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		b := p.pull(1024)
		frames += 1024
		silent := true
		for i := 0; i+1 < len(b); i += 4 {
			if b[i] != 0 || b[i+1] != 0 {
				nonSilent++
				silent = false
			}
		}
		select {
		case <-e.Done():
			return
		case <-deadline:
			t.Fatal("engine never signalled Done")
		default:
		}
		if silent {
			time.Sleep(time.Millisecond)
		}
	}
}

func TestEnginePlaysAndResamples(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	src := &fakeSource{rate: 48000, frames: 48000} // 1 s at 48 kHz
	e.Play(src)
	if !out.p.isPlaying() {
		t.Fatal("Play must start the player")
	}
	_, nonSilent := pullUntilDone(t, e, out.p)
	if nonSilent < 43000 || nonSilent > 45000 {
		t.Fatalf("non-silent frames = %d, want ~44100 (resampled 1 s)", nonSilent)
	}
	if !src.closed {
		t.Fatal("finished source must be closed")
	}
}

func TestEngineVolumeAndSilenceWhenEmpty(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	e.SetVolume(2)
	if e.Volume() != 1 {
		t.Fatal("volume must clamp to 1")
	}
	e.SetVolume(0)
	e.Play(&fakeSource{rate: 44100, frames: 4410})
	time.Sleep(50 * time.Millisecond) // let the decoder fill the ring
	b := out.p.pull(1024)
	for _, x := range b {
		if x != 0 {
			t.Fatal("volume 0 must produce silence")
		}
	}
	e.Stop()
	b = out.p.pull(16) // nothing queued: reader must still return bytes, all zero
	if len(b) != 64 {
		t.Fatal("reader must never short-read")
	}
}

func TestEngineSeekClampsAndPosition(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	src := &fakeSource{rate: 44100, frames: 44100 * 10}
	e.Play(src)
	e.Seek(-5 * time.Second)
	if p := e.Position(); p < 0 || p > 100*time.Millisecond {
		t.Fatalf("position after negative seek = %v", p)
	}
	e.Seek(time.Hour)
	if p := e.Position(); p < 9900*time.Millisecond || p > 10*time.Second {
		t.Fatalf("position after seek past end = %v (length %v)", p, e.Length())
	}
	e.Seek(3 * time.Second)
	time.Sleep(50 * time.Millisecond)
	out.p.pull(44100)           // 1 s of output consumed
	out.p.setBuffered(4410 * 4) // 0.1 s still in the device buffer
	if p := e.Position(); p < 3800*time.Millisecond || p > 4000*time.Millisecond {
		t.Fatalf("position = %v, want ~3.9 s", p)
	}
}

func TestEnginePauseResume(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	e.Play(&fakeSource{rate: 44100, frames: 44100})
	e.Pause()
	if e.Playing() || out.p.isPlaying() {
		t.Fatal("pause must stop the player")
	}
	e.Resume()
	if !e.Playing() || !out.p.isPlaying() {
		t.Fatal("resume must start the player")
	}
}

func TestEngineSpectrumUsesTap(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out)
	defer e.Close()
	e.Play(&fakeSource{rate: 44100, frames: 44100})
	time.Sleep(50 * time.Millisecond)
	out.p.pull(8192)
	dst := make([]float64, 2048)
	if n := e.Spectrum(2048, dst); n != 2048 {
		t.Fatalf("spectrum frames = %d", n)
	}
	peak := 0.0
	for _, v := range dst {
		peak = math.Max(peak, math.Abs(v))
	}
	if peak < 0.5 {
		t.Fatalf("tap peak = %f", peak)
	}
}
