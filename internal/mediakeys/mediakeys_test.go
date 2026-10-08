package mediakeys

import "testing"

func TestVirtualKeysMapToActions(t *testing.T) {
	cases := map[uint32]Action{
		vkMediaPlayPause: PlayPause,
		vkMediaNextTrack: Next,
		vkMediaPrevTrack: Prev,
		0x41:             None, // 'A'
	}
	for vk, want := range cases {
		if got := actionFor(vk); got != want {
			t.Errorf("vk %#x: got %v, want %v", vk, got, want)
		}
	}
	if actionFor(vkMediaPlayPause).String() != "play/pause" || Next.String() != "next" || Prev.String() != "previous" {
		t.Error("actions should name themselves for the log")
	}
}
