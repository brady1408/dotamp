// Package stream sends an engine's output to a Discord voice socket as
// Opus, paced by the wall clock: shared by dotamp's broadcast and the
// house bot.
package stream

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
)

// UDP is where packets go; disgo's voice.UDPConn satisfies it.
type UDP interface {
	Write(p []byte) (int, error)
	Read(p []byte) (int, error)
}

type Options struct {
	Rate   int         // engine sample rate
	Feed   *audio.Feed // engine output before the volume knob
	Logger *slog.Logger
	Label  string // channel name for the stats line
}

// ErrSetup wraps a failure to build the Opus encoder.
var ErrSetup = errors.New("stream setup")

// Run sends the feed as Opus every 20 ms until stop closes (nil) or a
// write fails (that error).
func Run(stop <-chan struct{}, udp UDP, o Options) error {
	pipe, err := NewPipeline(o.Rate)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSetup, err)
	}
	slice := o.Rate / 50 // frames per 20 ms
	src := newFeeder(o.Feed, slice)
	// The feed holds the last two seconds whether or not anyone listens;
	// start live, not with a backlog that would play out as a rush.
	for o.Feed.Read(src.buf) > 0 {
	}
	var sp Speaker
	var pending [][]byte
	now := time.Now()
	pacer := NewPacer(now)
	st := newStats(now)
	last := now
	// A fine tick with a wall-clock pacer: a tick that lands late sends the
	// slots it owes, so timer jitter never opens a hole in the stream.
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-tick.C:
		}
		now = time.Now()
		st.gap(now.Sub(last))
		last = now
		for range pacer.Due(now) {
			if frames := src.take(); frames != nil {
				pipe.Push(frames)
			}
			pending = append(pending, pipe.Packets()...)
			var pkt []byte
			sent := false
			switch sp.Tick(len(pending) > 0) {
			case SendAudio:
				pkt, pending = pending[0], pending[1:]
				sent = true
			case SendSilence:
				pkt = Silence
			default:
				st.slot(false, 0)
				continue
			}
			t0 := time.Now()
			_, err := udp.Write(pkt)
			st.slot(sent, time.Since(t0))
			if err != nil {
				return err
			}
			if len(pending) > 10 { // the resampler yields one packet per slice; this is a bug guard
				st.trimmed += len(pending) - 10
				pending = pending[len(pending)-10:]
			}
		}
		st.depth(o.Feed.Available())
		if st.due(now) {
			o.Logger.Info("broadcast", "channel", o.Label, "stats", st.line(), "skipped", src.dropped)
			st.reset(now)
		}
	}
}

// Drain reads and discards incoming packets until stop closes or Read fails,
// so the socket never backs up.
func Drain(stop <-chan struct{}, udp UDP) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
		}
		if _, err := udp.Read(buf); err != nil {
			return
		}
	}
}
