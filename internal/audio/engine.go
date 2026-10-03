package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const OutRate = 44100

// Engine runs Sources through decode → resample → ring → output, and taps the
// output for the analyzer. A second source can be queued with SetNext: the
// decoder continues straight into it when the first ends, so the ring never
// empties and the handover is gapless. The reader flips the audible source
// when it consumes past the boundary, which is when the deck changes.
type Engine struct {
	out      Output
	player   Player
	ring     *Ring
	tap      *Tap
	done     chan struct{} // the audible source ended with nothing queued
	switched chan struct{} // the audible source switched to the queued one

	ctl sync.Mutex // serialises Play/Seek/Stop/Close/TakeNext: the UI and the controller's Run both call them

	mu          sync.Mutex
	src         Source // what the listener hears
	srcClosed   bool
	decoding    Source // what the decode goroutine reads; equals src until it chains past the end
	next        Source // queued for after the current source
	nextStarted bool   // the decoder has read from next; it must be rewound before reuse
	length      time.Duration
	playing     bool
	err         error
	stop        chan struct{} // closes to end the decode goroutine
	decoded     chan struct{} // closes when the decode goroutine exits
	srcDone     atomic.Bool   // decoder hit EOF with nothing to chain to; Done fires once the ring is empty
	doneSent    atomic.Bool
	boundary    atomic.Int64 // ring write position, in frames, where decoding switched to next; -1 when none

	volume   atomic.Int64 // volume × 1e6
	consumed atomic.Int64 // real frames the output has read, ever (silence excluded)
	seekAt   atomic.Int64 // consumed count at the last Play/Seek/handover
	seekBase atomic.Int64 // position at the last Play/Seek/handover, in output frames
}

func NewEngine(out Output) *Engine {
	e := &Engine{
		out:      out,
		ring:     NewRing(OutRate),    // 44100 floats = half a second of stereo
		tap:      NewTap(OutRate * 2), // two seconds of history
		done:     make(chan struct{}, 1),
		switched: make(chan struct{}, 1),
	}
	e.volume.Store(1e6)
	e.boundary.Store(-1)
	e.player = out.NewPlayer(&reader{e: e})
	return e
}

func (e *Engine) Done() <-chan struct{}     { return e.done }
func (e *Engine) Handover() <-chan struct{} { return e.switched }

func (e *Engine) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

func (e *Engine) Play(src Source) { e.PlayAt(src, 0) }

// PlayAt starts src from position at, for resuming a stream that dropped.
// A source that cannot seek there plays from its start with the clock at 0.
// Any queued next source other than src is discarded.
func (e *Engine) PlayAt(src Source, at time.Duration) {
	e.ctl.Lock()
	defer e.ctl.Unlock()
	e.stopDecode()
	select { // signals left by the previous source must not act on this one
	case <-e.done:
	default:
	}
	select {
	case <-e.switched:
	default:
	}
	base := int64(0)
	if at > 0 {
		if err := src.Seek(at); err == nil {
			base = int64(at.Seconds() * OutRate)
		}
	}
	e.mu.Lock()
	if e.next != nil && e.next != src {
		e.next.Close()
	}
	e.next, e.nextStarted = nil, false
	e.src, e.srcClosed, e.decoding, e.length, e.err = src, false, src, src.Length(), nil
	e.stop = make(chan struct{})
	e.decoded = make(chan struct{})
	e.srcDone.Store(false)
	e.doneSent.Store(false)
	e.boundary.Store(-1)
	e.ring.Drain()
	e.tap.Reset()
	e.seekAt.Store(e.consumed.Load())
	e.seekBase.Store(base)
	e.playing = true
	stop, decoded := e.stop, e.decoded
	e.mu.Unlock()
	go e.decode(src, stop, decoded)
	e.player.Play()
}

// SetNext queues src to follow the current source without a gap. It replaces
// any source queued earlier.
func (e *Engine) SetNext(src Source) {
	e.mu.Lock()
	old := e.next
	e.next, e.nextStarted = src, false
	e.mu.Unlock()
	if old != nil && old != src {
		old.Close()
	}
}

// PeekNext reports the source queued to play next, if any.
func (e *Engine) PeekNext() Source {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.decoding != nil && e.decoding != e.src {
		return e.decoding
	}
	return e.next
}

// TakeNext removes and returns the queued source, rewound if the decoder had
// already started on it. The current source stops decoding; the caller is
// expected to Play something next.
func (e *Engine) TakeNext() Source {
	e.ctl.Lock()
	defer e.ctl.Unlock()
	e.haltDecoder()
	e.mu.Lock()
	next, started := e.next, e.nextStarted
	e.next, e.nextStarted = nil, false
	e.mu.Unlock()
	if next != nil && started {
		if err := next.Seek(0); err != nil {
			next.Close()
			return nil
		}
	}
	return next
}

// haltDecoder stops the decode goroutine and, if it had chained into the
// queued source, parks that source back as next. The audible source and the
// ring are left alone.
func (e *Engine) haltDecoder() {
	e.mu.Lock()
	stop, decoded, decoding := e.stop, e.decoded, e.decoding
	e.mu.Unlock()
	if stop != nil {
		close(stop)
		if decoding != nil {
			decoding.Interrupt()
		}
		<-decoded
	}
	e.mu.Lock()
	e.stop, e.decoded = nil, nil
	e.boundary.Store(-1)
	if e.decoding != nil && e.decoding != e.src {
		if e.next != nil && e.next != e.decoding {
			e.next.Close()
		}
		e.next, e.nextStarted = e.decoding, true
	}
	e.decoding = e.src
	e.mu.Unlock()
}

func (e *Engine) stopDecode() {
	e.haltDecoder()
	if src := e.claimClose(); src != nil {
		src.Close()
	}
	e.ring.Drain()
}

// claimClose returns the audible source if this caller is the one to close
// it. The reader (at a natural end or a handover) and the control path (on
// Stop/Play) both reach here; the flag flips under the lock so exactly one
// of them closes.
func (e *Engine) claimClose() Source {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.src == nil || e.srcClosed {
		return nil
	}
	e.srcClosed = true
	return e.src
}

func (e *Engine) Stop() {
	e.ctl.Lock()
	defer e.ctl.Unlock()
	e.stopDecode()
	e.mu.Lock()
	next := e.next
	e.next, e.nextStarted = nil, false
	e.src, e.decoding, e.stop, e.decoded, e.playing = nil, nil, nil, nil, false
	e.mu.Unlock()
	if next != nil {
		next.Close()
	}
	e.player.Pause()
}

func (e *Engine) Close() {
	e.Stop()
	e.player.Close()
}

// chainNext hands the decoder the queued source when the current one ends and
// records where in the ring the boundary falls. It reports whether the source
// had been read before and so needs rewinding.
func (e *Engine) chainNext() (Source, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.next == nil {
		return nil, false
	}
	next, started := e.next, e.nextStarted
	e.next, e.nextStarted = nil, true
	e.decoding = next
	e.boundary.Store(e.ring.Tail() / 2)
	return next, started
}

func (e *Engine) decode(src Source, stop, decoded chan struct{}) {
	defer close(decoded)
	defer func() { // a decoder bug ends the track as an error, not the process
		if r := recover(); r != nil {
			e.mu.Lock()
			e.err = fmt.Errorf("decoder panic: %v", r)
			e.mu.Unlock()
			e.srcDone.Store(true)
		}
	}()
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
		if err == nil {
			continue
		}
		if err != io.EOF {
			e.mu.Lock()
			e.err = err
			e.mu.Unlock()
			e.srcDone.Store(true)
			return
		}
		next, started := e.chainNext()
		if next == nil {
			e.srcDone.Store(true)
			return
		}
		if started {
			if err := next.Seek(0); err != nil {
				e.mu.Lock()
				e.err = err
				e.mu.Unlock()
				e.srcDone.Store(true)
				return
			}
		}
		src = next
		rs = NewResampler(src.SampleRate(), OutRate)
	}
}

// handover makes the queued source the audible one once the reader has
// consumed past the boundary. b is the boundary it saw, hf the ring position
// after its read, c the real frames consumed after its read.
func (e *Engine) handover(b, hf, c int64) {
	e.mu.Lock()
	if e.boundary.Load() != b { // a Stop or Seek got here first
		e.mu.Unlock()
		return
	}
	old, oldClosed := e.src, e.srcClosed
	e.src, e.srcClosed = e.decoding, false
	e.length = e.src.Length()
	e.seekBase.Store(0)
	e.seekAt.Store(c - (hf - b)) // frames of the new source already consumed in this read
	e.boundary.Store(-1)
	e.mu.Unlock()
	if old != nil && !oldClosed && old != e.src {
		old.Close()
	}
	select {
	case e.switched <- struct{}{}:
	default:
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
	e.ctl.Lock()
	defer e.ctl.Unlock()
	e.mu.Lock()
	src, length := e.src, e.length
	e.mu.Unlock()
	if src == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	e.haltDecoder() // parks a chained next back in the queue
	e.ring.Drain()
	if length > 0 && d >= length {
		// Decoders reject a seek to the very end; treat it as the track ending.
		e.mu.Lock()
		e.seekAt.Store(e.consumed.Load())
		e.seekBase.Store(int64(length.Seconds() * OutRate))
		e.doneSent.Store(false)
		e.srcDone.Store(true)
		e.mu.Unlock()
		return
	}
	base := int64(d.Seconds() * OutRate)
	if err := src.Seek(d); err != nil {
		e.mu.Lock()
		e.err = err
		e.mu.Unlock()
		base = e.positionFrames() // the source stayed put; so does the clock
	}
	e.mu.Lock()
	e.stop = make(chan struct{})
	e.decoded = make(chan struct{})
	e.srcDone.Store(false)
	e.doneSent.Store(false)
	e.seekAt.Store(e.consumed.Load())
	e.seekBase.Store(base)
	stop, decoded := e.stop, e.decoded
	e.mu.Unlock()
	go e.decode(src, stop, decoded)
}

func (e *Engine) positionFrames() int64 {
	frames := e.seekBase.Load() + e.consumed.Load() - e.seekAt.Load() - int64(e.player.BufferedSize()/4)
	return max(frames, 0)
}

func (e *Engine) Position() time.Duration {
	return time.Duration(float64(e.positionFrames()) / OutRate * float64(time.Second))
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
// the ring is empty. It applies volume, feeds the tap, and performs handovers.
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
		if src := r.e.claimClose(); src != nil {
			src.Close()
		}
		select {
		case r.e.done <- struct{}{}:
		default:
		}
	}
	c := r.e.consumed.Add(int64(n / 2)) // inserted silence does not move the clock
	if b := r.e.boundary.Load(); b >= 0 {
		if hf := r.e.ring.Head() / 2; hf >= b {
			r.e.handover(b, hf, c)
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
	return frames * 4, nil
}
