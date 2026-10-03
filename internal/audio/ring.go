package audio

import "sync/atomic"

// Ring is a single-producer single-consumer ring of float32 samples.
type Ring struct {
	buf  []float32
	head atomic.Int64 // next read index (monotonic)
	tail atomic.Int64 // next write index (monotonic)
}

func NewRing(capacity int) *Ring { return &Ring{buf: make([]float32, capacity)} }

func (r *Ring) Len() int  { return int(r.tail.Load() - r.head.Load()) }
func (r *Ring) Free() int { return len(r.buf) - r.Len() }

func (r *Ring) Write(p []float32) int {
	n := min(len(p), r.Free())
	tail := r.tail.Load()
	for i := 0; i < n; i++ {
		r.buf[int((tail+int64(i))%int64(len(r.buf)))] = p[i]
	}
	r.tail.Store(tail + int64(n))
	return n
}

func (r *Ring) Read(p []float32) int {
	n := min(len(p), r.Len())
	head := r.head.Load()
	for i := 0; i < n; i++ {
		p[i] = r.buf[int((head+int64(i))%int64(len(r.buf)))]
	}
	r.head.Store(head + int64(n))
	return n
}

// Drain discards everything buffered. Producer side only.
func (r *Ring) Drain() { r.head.Store(r.tail.Load()) }
