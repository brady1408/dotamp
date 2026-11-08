package stream

import (
	"time"

	"github.com/brady1408/dotamp/internal/audio"
)

// EncodeLoop encodes the feed to Opus, one 20 ms packet per slot, paced by
// the wall clock, and hands each packet to emit. When the feed has no audio
// (paused, between songs) it encodes silence, so a listener's stream never
// goes idle. It returns when stop closes.
func EncodeLoop(stop <-chan struct{}, feed *audio.Feed, rate int, emit func([]byte)) error {
	pipe, err := NewPipeline(rate)
	if err != nil {
		return err
	}
	slice := rate / 50
	src := newFeeder(feed, slice)
	for feed.Read(src.buf) > 0 { // start live
	}
	quiet := make([]float32, 2*slice)
	pacer := NewPacer(time.Now())
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return nil
		case now := <-tick.C:
			for range pacer.Due(now) {
				frames := src.take()
				if frames == nil {
					frames = quiet
				}
				pipe.Push(frames)
				for _, p := range pipe.Packets() {
					emit(p)
				}
			}
		}
	}
}
