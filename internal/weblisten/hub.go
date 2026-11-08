package weblisten

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/stream"
)

// Room is what the web listener needs from the house bot's room.
type Room interface {
	Live() (bool, string) // in a Discord channel, and its name
	Now() Now
	NewFeed() *audio.Feed
	DropFeed(*audio.Feed)
	Rate() int
}

var (
	ErrNotLive = errors.New("not playing in Discord")
	ErrFull    = errors.New("too many listeners")
)

// Sub is one listener: Opus packets arrive on C, which closes when the
// stream ends (the bot left Discord, or the server stopped).
type Sub struct{ C chan []byte }

// Hub runs one encoder for all listeners, only while someone listens and
// the bot is in Discord, and fans its packets out. A slow listener drops
// packets rather than holding the others back.
type Hub struct {
	room       Room
	max        int
	log        *slog.Logger
	checkEvery time.Duration

	mu   sync.Mutex
	subs map[*Sub]struct{}
	stop chan struct{}
}

func NewHub(room Room, max int, log *slog.Logger) *Hub {
	return &Hub{room: room, max: max, log: log, checkEvery: time.Second, subs: map[*Sub]struct{}{}}
}

// Full reports whether every listener slot is taken.
func (h *Hub) Full() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) >= h.max
}

func (h *Hub) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stop != nil
}

func (h *Hub) Subscribe() (*Sub, error) {
	if live, _ := h.room.Live(); !live {
		return nil, ErrNotLive
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= h.max {
		return nil, ErrFull
	}
	s := &Sub{C: make(chan []byte, 100)} // two seconds of slack
	h.subs[s] = struct{}{}
	if h.stop == nil {
		h.stop = make(chan struct{})
		go h.run(h.stop)
	}
	return s, nil
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; !ok {
		return
	}
	delete(h.subs, s)
	close(s.C)
	if len(h.subs) == 0 && h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
}

// endAll closes every listener's stream and stops the encoder.
func (h *Hub) endAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		delete(h.subs, s)
		close(s.C)
	}
	if h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
}

func (h *Hub) run(stop chan struct{}) {
	feed := h.room.NewFeed()
	defer h.room.DropFeed(feed)
	go func() { // the bot leaving Discord ends every stream
		t := time.NewTicker(h.checkEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if live, _ := h.room.Live(); !live {
					h.endAll()
					return
				}
			}
		}
	}()
	err := stream.EncodeLoop(stop, feed, h.room.Rate(), func(p []byte) {
		h.mu.Lock()
		defer h.mu.Unlock()
		for s := range h.subs {
			select {
			case s.C <- p:
			default: // this listener is behind; it skips a packet
			}
		}
	})
	if err != nil {
		h.log.Warn("web encoder", "err", err)
		h.endAll()
	}
}
