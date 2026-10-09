package mediakeys

import (
	"testing"
	"time"
)

func TestNowPlayingStateAndFields(t *testing.T) {
	if playbackState(State{}) != nowPlayingStopped || playbackState(State{HasTrack: true}) != nowPlayingPaused || playbackState(State{HasTrack: true, Playing: true}) != nowPlayingPlaying {
		t.Fatal("playback state mapping")
	}
	f := nowPlayingFields(State{HasTrack: true, Playing: true, Title: "Billie Jean", Artist: "Michael Jackson", Album: "Thriller", Length: 293 * time.Second})
	if f["title"] != "Billie Jean" || f["artist"] != "Michael Jackson" || f["albumTitle"] != "Thriller" {
		t.Fatalf("fields = %v", f)
	}
	if f["playbackDuration"] != 293.0 || f["MPNowPlayingInfoPropertyPlaybackRate"] != 1.0 {
		t.Fatalf("duration/rate = %v %v", f["playbackDuration"], f["MPNowPlayingInfoPropertyPlaybackRate"])
	}
	if p := nowPlayingFields(State{HasTrack: true}); p["MPNowPlayingInfoPropertyPlaybackRate"] != 0.0 {
		t.Fatal("paused rate must be 0")
	}
	if len(nowPlayingFields(State{})) != 0 {
		t.Fatal("no track means no fields")
	}
}
