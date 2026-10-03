package audio

import "sync"

// Tap keeps the most recent frames handed to the output so the analyzer can
// read what the speaker is playing now.
type Tap struct {
	mu      sync.Mutex
	buf     []float32 // mono, ring
	cap     int
	written int64
}

func NewTap(capacityFrames int) *Tap {
	return &Tap{buf: make([]float32, capacityFrames), cap: capacityFrames}
}

func (t *Tap) Write(p []float32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i+1 < len(p); i += 2 {
		t.buf[int(t.written%int64(t.cap))] = (p[i] + p[i+1]) * 0.5
		t.written++
	}
}

func (t *Tap) Written() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.written
}

func (t *Tap) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.written = 0
}

func (t *Tap) Latest(n, lagFrames int, dst []float64) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	end := t.written - int64(lagFrames)
	start := end - int64(n)
	oldest := t.written - int64(t.cap)
	if start < 0 || start < oldest {
		return 0
	}
	for i := 0; i < n; i++ {
		dst[i] = float64(t.buf[int((start+int64(i))%int64(t.cap))])
	}
	return n
}
