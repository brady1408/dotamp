package audio

import "sync/atomic"

// Ring is a single-producer single-consumer ring of float32 samples. Only the
// consumer moves head, so a writer never touches a slot the reader may be
// copying; a producer-side Drain is a request the consumer applies on its
// next Read.
type Ring struct {
	buf     []float32
	head    atomic.Int64 // next read index (monotonic); consumer-owned
	tail    atomic.Int64 // next write index (monotonic); producer-owned
	drainTo atomic.Int64 // samples before this index are stale; consumer skips them
}

func NewRing(capacity int) *Ring { return &Ring{buf: make([]float32, capacity)} }

func (r *Ring) Len() int { return max(int(r.tail.Load()-r.head.Load()), 0) }

// Head and Tail are the monotonic sample positions the consumer has read or
// skipped up to, and the producer has written up to.
func (r *Ring) Head() int64 { return r.head.Load() }
func (r *Ring) Tail() int64 { return r.tail.Load() }
func (r *Ring) Free() int   { return len(r.buf) - r.Len() }

func (r *Ring) Write(p []float32) int {
	n := max(min(len(p), r.Free()), 0)
	tail := r.tail.Load()
	for i := 0; i < n; i++ {
		r.buf[int((tail+int64(i))%int64(len(r.buf)))] = p[i]
	}
	r.tail.Store(tail + int64(n))
	return n
}

func (r *Ring) Read(p []float32) int {
	head := r.head.Load()
	if d := r.drainTo.Load(); d > head {
		head = d
		r.head.Store(head)
	}
	n := max(min(len(p), int(r.tail.Load()-head)), 0)
	for i := 0; i < n; i++ {
		p[i] = r.buf[int((head+int64(i))%int64(len(r.buf)))]
	}
	r.head.Store(head + int64(n))
	return n
}

// Drain marks everything written so far as stale. The consumer skips it on
// its next Read; until then Len still counts it, so a full ring stays full.
func (r *Ring) Drain() { r.drainTo.Store(r.tail.Load()) }
