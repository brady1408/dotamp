package audio

import (
	"sync"
	"testing"
)

func TestRingWriteReadWrap(t *testing.T) {
	r := NewRing(8)
	if n := r.Write([]float32{1, 2, 3, 4, 5, 6}); n != 6 {
		t.Fatalf("wrote %d", n)
	}
	out := make([]float32, 4)
	if n := r.Read(out); n != 4 || out[0] != 1 || out[3] != 4 {
		t.Fatalf("read %d %v", n, out)
	}
	if n := r.Write([]float32{7, 8, 9, 10, 11, 12}); n != 6 { // wraps around
		t.Fatalf("wrote %d after wrap, free was %d", n, r.Free())
	}
	if r.Len() != 8 {
		t.Fatalf("len = %d", r.Len())
	}
	out = make([]float32, 8)
	r.Read(out)
	if out[0] != 5 || out[7] != 12 {
		t.Fatalf("wrapped read = %v", out)
	}
}

func TestRingPartialWriteAndDrain(t *testing.T) {
	r := NewRing(4)
	if n := r.Write(make([]float32, 10)); n != 4 {
		t.Fatalf("partial write = %d", n)
	}
	r.Drain()
	if r.Len() != 0 || r.Free() != 4 {
		t.Fatal("drain failed")
	}
}

func TestRingConcurrent(t *testing.T) {
	r := NewRing(1024)
	const total = 200000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		buf := make([]float32, 100)
		sent := 0
		for sent < total {
			for i := range buf {
				buf[i] = float32(sent + i)
			}
			n := r.Write(buf)
			sent += n
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]float32, 64)
		got := 0
		for got < total {
			n := r.Read(buf)
			for i := 0; i < n; i++ {
				if int(buf[i]) != got+i {
					t.Errorf("sample %d = %v", got+i, buf[i])
					return
				}
			}
			got += n
		}
	}()
	wg.Wait()
}
