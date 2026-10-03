package audio

import (
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const OutRate = 44100

// Engine runs one Source at a time through decode → resample → ring → output,
// and taps the output for the analyzer.
type Engine struct {
	out    Output
	player Player
	ring   *Ring
	tap    *Tap
	done   chan struct{}

	mu        sync.Mutex
	src       Source
	srcClosed bool
	length    time.Duration
	playing   bool
	err       error
	stop      chan struct{} // closes to end the decode goroutine
	decoded   chan struct{} // closes when the decode goroutine exits
	srcDone   atomic.Bool   // decoder hit EOF; Done fires once the ring is empty
	doneSent  atomic.Bool

	volume   atomic.Int64 // volume × 1e6
	consumed atomic.Int64 // frames the output has read, ever
	seekAt   atomic.Int64 // consumed count at the last Play/Seek
	seekBase atomic.Int64 // position at the last Play/Seek, in output frames
}

func NewEngine(out Output) *Engine {
	e := &Engine{
		out:  out,
		ring: NewRing(OutRate),    // 44100 floats = half a second of stereo
		tap:  NewTap(OutRate * 2), // two seconds of history
		done: make(chan struct{}, 1),
	}
	e.volume.Store(1e6)
	e.player = out.NewPlayer(&reader{e: e})
	return e
}

func (e *Engine) Done() <-chan struct{} { return e.done }

func (e *Engine) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

func (e *Engine) Play(src Source) {
	e.stopDecode()
	e.mu.Lock()
	e.src, e.srcClosed, e.length, e.err = src, false, src.Length(), nil
	e.stop = make(chan struct{})
	e.decoded = make(chan struct{})
	e.srcDone.Store(false)
	e.doneSent.Store(false)
	e.ring.Drain()
	e.tap.Reset()
	e.seekAt.Store(e.consumed.Load())
	e.seekBase.Store(0)
	e.playing = true
	stop, decoded := e.stop, e.decoded
	e.mu.Unlock()
	go e.decode(src, stop, decoded)
	e.player.Play()
}

func (e *Engine) stopDecode() {
	e.mu.Lock()
	stop, decoded, src, closed := e.stop, e.decoded, e.src, e.srcClosed
	e.mu.Unlock()
	if stop != nil {
		close(stop)
		<-decoded
	}
	if src != nil && !closed {
		src.Close()
		e.mu.Lock()
		e.srcClosed = true
		e.mu.Unlock()
	}
	e.ring.Drain()
}

func (e *Engine) Stop() {
	e.stopDecode()
	e.mu.Lock()
	e.src, e.stop, e.decoded, e.playing = nil, nil, nil, false
	e.mu.Unlock()
	e.player.Pause()
}

func (e *Engine) Close() {
	e.Stop()
	e.player.Close()
}

func (e *Engine) decode(src Source, stop, decoded chan struct{}) {
	defer close(decoded)
	rs := NewResampler(src.SampleRate(), OutRate)
	buf := make([]float32, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		n, err := src.Read(buf)
		if n > 0 {
			out := rs.Process(buf[:n])
			for len(out) > 0 {
				w := e.ring.Write(out)
				out = out[w:]
				if len(out) > 0 {
					select {
					case <-stop:
						return
					case <-time.After(5 * time.Millisecond):
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				e.mu.Lock()
				e.err = err
				e.mu.Unlock()
			}
			e.srcDone.Store(true)
			return
		}
	}
}

func (e *Engine) Pause() {
	e.mu.Lock()
	e.playing = false
	e.mu.Unlock()
	e.player.Pause()
}

func (e *Engine) Resume() {
	e.mu.Lock()
	has := e.src != nil
	e.playing = has
	e.mu.Unlock()
	if has {
		e.player.Play()
	}
}

func (e *Engine) Playing() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.playing
}

func (e *Engine) Length() time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.length
}

func (e *Engine) Seek(d time.Duration) {
	e.mu.Lock()
	src, stop, decoded, length := e.src, e.stop, e.decoded, e.length
	e.mu.Unlock()
	if src == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	if length > 0 && d > length {
		d = length
	}
	// Stop the decoder, seek the source, restart the decoder on the same source.
	close(stop)
	<-decoded
	e.ring.Drain()
	if err := src.Seek(d); err != nil {
		e.mu.Lock()
		e.err = err
		e.mu.Unlock()
	}
	e.mu.Lock()
	e.stop = make(chan struct{})
	e.decoded = make(chan struct{})
	e.srcDone.Store(false)
	e.doneSent.Store(false)
	e.seekAt.Store(e.consumed.Load())
	e.seekBase.Store(int64(d.Seconds() * OutRate))
	stop, decoded = e.stop, e.decoded
	e.mu.Unlock()
	go e.decode(src, stop, decoded)
}

func (e *Engine) Position() time.Duration {
	frames := e.seekBase.Load() + e.consumed.Load() - e.seekAt.Load() - int64(e.player.BufferedSize()/4)
	if frames < 0 {
		frames = 0
	}
	return time.Duration(float64(frames) / OutRate * float64(time.Second))
}

func (e *Engine) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	e.volume.Store(int64(v * 1e6))
}

func (e *Engine) Volume() float64 { return float64(e.volume.Load()) / 1e6 }

func (e *Engine) Spectrum(n int, dst []float64) int {
	return e.tap.Latest(n, e.player.BufferedSize()/4, dst)
}

// reader is what the output pulls from: s16le stereo, never short, silence when
// the ring is empty. It applies volume and feeds the tap.
type reader struct {
	e       *Engine
	scratch []float32
}

func (r *reader) Read(p []byte) (int, error) {
	frames := len(p) / 4
	if cap(r.scratch) < frames*2 {
		r.scratch = make([]float32, frames*2)
	}
	s := r.scratch[:frames*2]
	n := r.e.ring.Read(s)
	for i := n; i < len(s); i++ {
		s[i] = 0
	}
	if n == 0 && r.e.srcDone.Load() && r.e.doneSent.CompareAndSwap(false, true) {
		r.e.mu.Lock()
		if r.e.src != nil && !r.e.srcClosed {
			r.e.src.Close()
			r.e.srcClosed = true
		}
		r.e.mu.Unlock()
		select {
		case r.e.done <- struct{}{}:
		default:
		}
	}
	vol := float32(r.e.Volume())
	for i := range s {
		s[i] *= vol
	}
	r.e.tap.Write(s)
	for i, v := range s {
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		binary.LittleEndian.PutUint16(p[2*i:], uint16(int16(v*32767)))
	}
	r.e.consumed.Add(int64(frames))
	return frames * 4, nil
}
