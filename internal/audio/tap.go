package audio

import "sync"

// Tap keeps the most recent frames handed to the output so the analyzer can
// read what the speaker is playing now. Left and right are kept apart; the
// mono view mixes them on the way out.
type Tap struct {
	mu      sync.Mutex
	left    []float32 // rings
	right   []float32
	cap     int
	written int64
}

func NewTap(capacityFrames int) *Tap {
	return &Tap{left: make([]float32, capacityFrames), right: make([]float32, capacityFrames), cap: capacityFrames}
}

func (t *Tap) Write(p []float32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := 0; i+1 < len(p); i += 2 {
		slot := int(t.written % int64(t.cap))
		t.left[slot], t.right[slot] = p[i], p[i+1]
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

// start returns the first frame of a window of n frames ending lagFrames
// before the newest, or -1 when the window reaches before the kept history.
func (t *Tap) start(n, lagFrames int) int64 {
	end := t.written - int64(lagFrames)
	start := end - int64(n)
	if start < 0 || start < t.written-int64(t.cap) {
		return -1
	}
	return start
}

// Latest fills dst with n mono-mixed frames ending lagFrames before the
// newest. It returns 0 when there is not enough history yet.
func (t *Tap) Latest(n, lagFrames int, dst []float64) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	start := t.start(n, lagFrames)
	if start < 0 {
		return 0
	}
	for i := 0; i < n; i++ {
		slot := int((start + int64(i)) % int64(t.cap))
		dst[i] = float64(t.left[slot]+t.right[slot]) * 0.5
	}
	return n
}

// LatestStereo is Latest with the channels kept apart.
func (t *Tap) LatestStereo(n, lagFrames int, l, r []float64) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	start := t.start(n, lagFrames)
	if start < 0 {
		return 0
	}
	for i := 0; i < n; i++ {
		slot := int((start + int64(i)) % int64(t.cap))
		l[i], r[i] = float64(t.left[slot]), float64(t.right[slot])
	}
	return n
}
