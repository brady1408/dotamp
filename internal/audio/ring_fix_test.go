package audio

import (
	"sync"
	"testing"
	"time"
)

// Drain from the producer side must never leave Len() above capacity or make
// Write return a negative count, whatever the reader is doing at the time.
func TestRingDrainDuringReadKeepsInvariants(t *testing.T) {
	r := NewRing(256)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]float32, 64)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if n := r.Read(buf); n < 0 || n > 64 {
				t.Errorf("Read returned %d", n)
				return
			}
			if l := r.Len(); l < 0 || l > 256 {
				t.Errorf("Len = %d after Read", l)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]float32, 100)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if n := r.Write(buf); n < 0 || n > 100 {
				t.Errorf("Write returned %d", n)
				return
			}
			if i%7 == 0 {
				r.Drain()
			}
			if l := r.Len(); l < 0 || l > 256 {
				t.Errorf("Len = %d after Write/Drain", l)
				return
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
