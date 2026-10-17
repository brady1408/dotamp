package audio

import (
	"testing"
	"time"
)

func TestFeedDropsOldestWhenFull(t *testing.T) {
	f := NewFeed(4) // 4 stereo frames
	f.Write([]float32{1, 1, 2, 2, 3, 3})
	f.Write([]float32{4, 4, 5, 5}) // 5 frames into 4: frame 1 is dropped
	if f.Available() != 4 {
		t.Fatalf("available = %d", f.Available())
	}
	dst := make([]float32, 10)
	n := f.Read(dst)
	if n != 4 || dst[0] != 2 || dst[6] != 5 || dst[7] != 5 {
		t.Fatalf("read %d frames: %v", n, dst[:2*n])
	}
	if f.Read(dst) != 0 {
		t.Fatal("empty after a full read")
	}
}

func TestFeedPartialRead(t *testing.T) {
	f := NewFeed(8)
	f.Write([]float32{1, 1, 2, 2, 3, 3})
	dst := make([]float32, 4) // room for 2 frames
	if n := f.Read(dst); n != 2 || dst[2] != 2 {
		t.Fatalf("n=%d dst=%v", n, dst)
	}
	if n := f.Read(dst); n != 1 || dst[0] != 3 {
		t.Fatalf("n=%d dst=%v", n, dst)
	}
}

// The feed sits before the volume knob and the mute sits after the
// visualizer tap: a muted engine at volume 0 still feeds full level, sends
// zeros to the device, and leaves the volume setting alone.
func TestEngineFeedIsBeforeVolumeAndMuteIsAfterTheTap(t *testing.T) {
	out := &fakeOutput{}
	eng := NewEngine(out, OutRate)
	defer eng.Close()
	eng.SetVolume(0)
	eng.SetLocalMute(true)
	eng.Play(&constSource{fakeSource: fakeSource{rate: OutRate, frames: OutRate}, value: 0.5})
	for _, v := range pullSlowly(out.p, 4410) {
		if v != 0 {
			t.Fatalf("device must get zeros while muted, got %d", v)
		}
	}
	dst := make([]float32, 2*4410)
	n := eng.Feed().Read(dst)
	if n == 0 {
		t.Fatal("feed is empty")
	}
	peak := float32(0)
	for _, v := range dst[:2*n] {
		if v > peak {
			peak = v
		}
	}
	if peak < 0.4 {
		t.Fatalf("feed must carry full level before the volume knob, peak %v", peak)
	}
	if eng.Volume() != 0 {
		t.Fatal("the mute must not touch the volume setting")
	}

	// With volume up and the mute on, the visualizer tap still sees audio.
	out2 := &fakeOutput{}
	eng2 := NewEngine(out2, OutRate)
	defer eng2.Close()
	eng2.SetVolume(1)
	eng2.SetLocalMute(true)
	eng2.Play(&constSource{fakeSource: fakeSource{rate: OutRate, frames: OutRate}, value: 0.5})
	_ = pullSlowly(out2.p, 4410)
	time.Sleep(10 * time.Millisecond)
	l, r := make([]float64, 1024), make([]float64, 1024)
	if eng2.Stereo(1024, l, r) == 0 || l[512] == 0 {
		t.Fatalf("the visualizer tap should still see audio while muted: n=%d l[512]=%v", eng2.Stereo(1024, l, r), l[512])
	}
	eng2.SetLocalMute(false)
	for _, v := range pullSlowly(out2.p, 2048) {
		if v != 0 {
			return // audio reaches the device again once unmuted
		}
	}
	t.Fatal("unmuting must restore the device output")
}
