package audio

import "testing"

func TestTapLatestWithLag(t *testing.T) {
	tap := NewTap(16)
	in := make([]float32, 0, 20*2)
	for i := 0; i < 20; i++ {
		in = append(in, float32(i), float32(i)) // mono mix == i
	}
	tap.Write(in)
	if tap.Written() != 20 {
		t.Fatalf("written = %d", tap.Written())
	}
	dst := make([]float64, 4)
	if n := tap.Latest(4, 0, dst); n != 4 || dst[0] != 16 || dst[3] != 19 {
		t.Fatalf("latest lag0 = %d %v", n, dst)
	}
	if n := tap.Latest(4, 3, dst); n != 4 || dst[0] != 13 || dst[3] != 16 {
		t.Fatalf("latest lag3 = %d %v", n, dst)
	}
	if n := tap.Latest(4, 14, dst); n != 0 { // would reach before the kept window
		t.Fatalf("expected 0 for lag beyond history, got %d", n)
	}
	tap.Reset()
	if n := tap.Latest(1, 0, dst); n != 0 || tap.Written() != 0 {
		t.Fatal("reset failed")
	}
}

func TestTapMonoMix(t *testing.T) {
	tap := NewTap(4)
	tap.Write([]float32{1, 0, 0, 1})
	dst := make([]float64, 2)
	tap.Latest(2, 0, dst)
	if dst[0] != 0.5 || dst[1] != 0.5 {
		t.Fatalf("mono mix = %v", dst)
	}
}
