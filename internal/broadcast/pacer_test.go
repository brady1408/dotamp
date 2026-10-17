package broadcast

import (
	"testing"
	"time"
)

func TestPacerSendsOneSlotEveryTwentyMilliseconds(t *testing.T) {
	t0 := time.Unix(0, 0)
	p := NewPacer(t0)
	got := []int{p.Due(t0), p.Due(t0.Add(10 * time.Millisecond)), p.Due(t0.Add(20 * time.Millisecond)), p.Due(t0.Add(65 * time.Millisecond)), p.Due(t0.Add(80 * time.Millisecond))}
	want := []int{1, 0, 1, 2, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("due = %v, want %v", got, want)
		}
	}
}

func TestPacerCatchesUpInBurstsAfterALateTick(t *testing.T) {
	t0 := time.Unix(0, 0)
	p := NewPacer(t0)
	p.Due(t0)
	late := t0.Add(200 * time.Millisecond) // slots 20..200 are owed: ten of them
	if n := p.Due(late); n != burst {
		t.Fatalf("first catch-up = %d, want %d", n, burst)
	}
	if n := p.Due(late); n != 10-burst {
		t.Fatalf("second catch-up = %d, want %d", n, 10-burst)
	}
	if n := p.Due(late.Add(20 * time.Millisecond)); n != 1 {
		t.Fatalf("back in step = %d, want 1", n)
	}
}

func TestPacerStartsOverAfterAStall(t *testing.T) {
	t0 := time.Unix(0, 0)
	p := NewPacer(t0)
	p.Due(t0)
	if n := p.Due(t0.Add(3 * time.Second)); n != 1 {
		t.Fatalf("after a stall = %d, want 1", n)
	}
	if n := p.Due(t0.Add(3*time.Second + 20*time.Millisecond)); n != 1 {
		t.Fatalf("slot after the restart = %d, want 1", n)
	}
}
