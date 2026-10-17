package audio

import "sync"

// Feed hands a copy of the engine's output, before the volume knob, to one
// consumer such as a Discord broadcast. Writes never block: when the
// consumer falls behind, the oldest audio is dropped.
type Feed struct {
	mu   sync.Mutex
	buf  []float32 // interleaved stereo ring
	cap  int       // frames
	head int       // next frame to read
	n    int       // frames held
}

func NewFeed(capacityFrames int) *Feed {
	return &Feed{buf: make([]float32, capacityFrames*2), cap: capacityFrames}
}

func (f *Feed) Write(stereo []float32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	frames := len(stereo) / 2
	for i := 0; i < frames; i++ {
		pos := (f.head + f.n) % f.cap
		f.buf[2*pos], f.buf[2*pos+1] = stereo[2*i], stereo[2*i+1]
		if f.n < f.cap {
			f.n++
		} else {
			f.head = (f.head + 1) % f.cap // overwrote the oldest
		}
	}
}

// Read copies up to len(dst)/2 frames into dst and returns how many.
func (f *Feed) Read(dst []float32) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := min(len(dst)/2, f.n)
	for i := 0; i < want; i++ {
		pos := (f.head + i) % f.cap
		dst[2*i], dst[2*i+1] = f.buf[2*pos], f.buf[2*pos+1]
	}
	f.head = (f.head + want) % f.cap
	f.n -= want
	return want
}

// Available is how many frames a Read could return now.
func (f *Feed) Available() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}
