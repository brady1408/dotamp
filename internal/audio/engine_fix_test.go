package audio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

// Concurrent control from two goroutines (the UI and the controller's Run)
// must never panic on a double channel close.
func TestEngineConcurrentControlDoesNotPanic(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out, OutRate)
	defer e.Close()
	e.Play(&fakeSource{rate: 44100, frames: 44100 * 10})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				switch (i + g) % 5 {
				case 0:
					e.Seek(time.Duration(i%9) * time.Second)
				case 1:
					e.Play(&fakeSource{rate: 48000, frames: 48000 * 5})
				case 2:
					e.Pause()
				case 3:
					e.Resume()
				case 4:
					e.Stop()
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				out.p.pull(512)
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// A seek at or past the end of a real file ends the track instead of playing
// on with a clock stuck at the length.
func TestEngineSeekPastEndOfRealFileEndsTrack(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out, OutRate)
	defer e.Close()
	src, err := NewFLAC(openFixture(t, "sine440-44k.flac"))
	if err != nil {
		t.Fatal(err)
	}
	e.Play(src)
	e.Seek(time.Hour)
	deadline := time.After(3 * time.Second)
	for {
		out.p.pull(1024)
		select {
		case <-e.Done():
			if p, l := e.Position(), e.Length(); p > l {
				t.Fatalf("position %v ran past length %v", p, l)
			}
			return
		case <-deadline:
			t.Fatal("track did not end after seeking past the end")
		default:
		}
	}
}

// A Done left over from the previous source must not advance the queue past
// the first track of whatever Play comes next.
func TestEnginePlayDrainsStaleDone(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out, OutRate)
	defer e.Close()
	e.Play(&fakeSource{rate: 44100, frames: 441})
	deadline := time.After(3 * time.Second)
	for len(e.done) == 0 { // wait for the stale Done to be queued, without receiving it
		out.p.pull(512)
		select {
		case <-deadline:
			t.Fatal("first source never finished")
		default:
		}
	}
	e.Play(&fakeSource{rate: 44100, frames: 44100 * 10})
	select {
	case <-e.Done():
		t.Fatal("stale Done from the previous source leaked into the new one")
	default:
	}
}

// blockedSource never returns from Read until released: a stalled network.
type blockedSource struct {
	fakeSource
	release chan struct{}
	once    sync.Once
}

func (b *blockedSource) Read(dst []float32) (int, error) {
	<-b.release
	return b.fakeSource.Read(dst)
}
func (b *blockedSource) Interrupt() { b.once.Do(func() { close(b.release) }) }

// Silence inserted on underrun must not move the clock.
func TestEnginePositionIgnoresUnderrunSilence(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out, OutRate)
	defer e.Close()
	src := &blockedSource{fakeSource: fakeSource{rate: 44100, frames: 44100}, release: make(chan struct{})}
	e.Play(src)
	out.p.pull(44100) // one second of pure silence
	if p := e.Position(); p != 0 {
		t.Fatalf("position advanced to %v on silence", p)
	}
	src.Interrupt()
}

// Stop must return promptly even when the decoder is blocked in a network read.
func TestEngineStopUnblocksStalledRead(t *testing.T) {
	data, _ := os.ReadFile("testdata/sine440-44k.flac")
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "99999999")
		w.WriteHeader(200)
		_, _ = w.Write(data[:16384])
		w.(http.Flusher).Flush()
		<-hold
	}))
	defer srv.Close()
	defer close(hold)
	hf, err := OpenHTTP(context.Background(), srv.URL+"/a.flac", nil)
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewFLAC(hf)
	if err != nil {
		t.Fatal(err)
	}
	out := &fakeOutput{}
	e := NewEngine(out, OutRate)
	e.Play(src)
	time.Sleep(100 * time.Millisecond) // decoder consumes the 16 KB and blocks
	done := make(chan struct{})
	go func() { e.Stop(); e.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung on a stalled read")
	}
}

// panicSource panics in Read: a decoder bug must not take the process down.
type panicSource struct{ fakeSource }

func (p *panicSource) Read([]float32) (int, error) { panic("decoder bug") }

func TestEngineDecodePanicBecomesError(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out, OutRate)
	defer e.Close()
	e.Play(&panicSource{fakeSource{rate: 44100, frames: 44100}})
	deadline := time.After(3 * time.Second)
	for {
		out.p.pull(512)
		select {
		case <-e.Done():
			if e.Err() == nil {
				t.Fatal("a recovered decode panic must surface as Err()")
			}
			return
		case <-deadline:
			t.Fatal("Done never fired after a decode panic")
		default:
		}
	}
}

var _ io.Reader = (*reader)(nil)

func TestEngineFormatFollowsTheAudibleSource(t *testing.T) {
	out := &fakeOutput{}
	e := NewEngine(out, OutRate)
	defer e.Close()
	if f := e.Format(); f != (Format{}) {
		t.Fatalf("no source: %+v", f)
	}
	src, _ := NewFLAC(openFixture(t, "sine440-44k.flac"))
	e.Play(src)
	if f := e.Format(); f.SampleRate != 44100 || f.BitDepth != 16 || f.Codec != "flac" {
		t.Fatalf("format = %+v", f)
	}
}
