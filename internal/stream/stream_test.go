package stream

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
)

type udp struct {
	mu     sync.Mutex
	writes int
	err    error
}

func (u *udp) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.writes++
	if u.err != nil {
		return 0, u.err
	}
	return len(p), nil
}
func (u *udp) Read(p []byte) (int, error) { time.Sleep(5 * time.Millisecond); return 0, nil }
func (u *udp) count() int                 { u.mu.Lock(); defer u.mu.Unlock(); return u.writes }

func opts(feed *audio.Feed) Options {
	return Options{Rate: 48000, Feed: feed, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Label: "t"}
}

func feedFor(feed *audio.Feed) {
	for i := 0; i < 50; i++ {
		feed.Write(make([]float32, 2*960))
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunSendsUntilStopped(t *testing.T) {
	feed := audio.NewFeed(48000)
	stop := make(chan struct{})
	u := &udp{}
	done := make(chan error, 1)
	go func() { done <- Run(stop, u, opts(feed)) }()
	go feedFor(feed)
	time.Sleep(400 * time.Millisecond)
	close(stop)
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if u.count() < 5 {
		t.Fatalf("only %d packets", u.count())
	}
}

func TestRunReturnsTheWriteError(t *testing.T) {
	feed := audio.NewFeed(48000)
	go feedFor(feed)
	boom := errors.New("socket gone")
	if err := Run(make(chan struct{}), &udp{err: boom}, opts(feed)); !errors.Is(err, boom) {
		t.Fatalf("Run = %v", err)
	}
}

func TestDrainStopsOnStop(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { Drain(stop, &udp{}); close(done) }()
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Drain did not stop")
	}
}
