package audio

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

const prefetchAhead = 10 * time.Second

// Controller joins the queue, the library, and the engine: it opens streams,
// advances on track end, prefetches the next track, and reports skips.
type Controller struct {
	lib    library.Library
	eng    *Engine
	notify func(string)

	qmu sync.Mutex // guards q; the ui goroutine and Run both touch it
	q   Queue

	mu      sync.Mutex // guards current, has, pre
	current library.Track
	has     bool
	pre     *prefetched
}

type prefetched struct {
	id  string
	src Source
}

func NewController(lib library.Library, eng *Engine, notify func(string)) *Controller {
	return &Controller{lib: lib, eng: eng, notify: notify}
}

func (c *Controller) Current() (library.Track, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current, c.has
}

func (c *Controller) withQ(f func(q *Queue)) {
	c.qmu.Lock()
	defer c.qmu.Unlock()
	f(&c.q)
}

func (c *Controller) Tracks() (ts []library.Track) {
	c.withQ(func(q *Queue) { ts = append([]library.Track(nil), q.Tracks()...) })
	return
}

func (c *Controller) Index() (i int) {
	c.withQ(func(q *Queue) { i = q.Index() })
	return
}

func (c *Controller) Shuffle() (v bool) { c.withQ(func(q *Queue) { v = q.Shuffle }); return }
func (c *Controller) Repeat() (v bool)  { c.withQ(func(q *Queue) { v = q.Repeat }); return }
func (c *Controller) ToggleShuffle()    { c.withQ(func(q *Queue) { q.Shuffle = !q.Shuffle }) }
func (c *Controller) ToggleRepeat()     { c.withQ(func(q *Queue) { q.Repeat = !q.Repeat }) }

func (c *Controller) queueNext() (ok bool) { c.withQ(func(q *Queue) { ok = q.Next() }); return }
func (c *Controller) queuePrev() (ok bool) { c.withQ(func(q *Queue) { ok = q.Prev() }); return }

func (c *Controller) open(ctx context.Context, t library.Track) (Source, error) {
	st, err := c.lib.Stream(ctx, t)
	if err != nil {
		return nil, err
	}
	hf, err := OpenHTTP(ctx, st.URL, st.Headers)
	if err != nil {
		return nil, err
	}
	src, err := NewSource(st.Codec, hf)
	if err != nil {
		hf.Close()
		return nil, err
	}
	return src, nil
}

func (c *Controller) stopped() {
	c.eng.Stop()
	c.mu.Lock()
	c.has = false
	c.mu.Unlock()
}

// start plays the queue's current track, trying twice before skipping forward.
func (c *Controller) start(ctx context.Context) error {
	for {
		var t library.Track
		var ok bool
		c.withQ(func(q *Queue) { t, ok = q.Current() })
		if !ok {
			c.stopped()
			return nil
		}
		src := c.takePrefetched(t.ID)
		var err error
		for attempt := 0; src == nil && attempt < 2; attempt++ {
			src, err = c.open(ctx, t)
		}
		if src != nil {
			c.mu.Lock()
			c.current, c.has = t, true
			c.mu.Unlock()
			c.eng.Play(src)
			return nil
		}
		log.Printf("skip %s (%s): %v", t.Title, t.ID, err)
		c.notify(fmt.Sprintf("Skipped %s: %v", t.Title, err))
		if !c.queueNext() {
			c.stopped()
			return err
		}
	}
}

func (c *Controller) takePrefetched(id string) Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pre != nil && c.pre.id == id {
		src := c.pre.src
		c.pre = nil
		return src
	}
	if c.pre != nil {
		c.pre.src.Close()
		c.pre = nil
	}
	return nil
}

func (c *Controller) PlayTracks(ctx context.Context, ts []library.Track, start int) error {
	c.withQ(func(q *Queue) { q.Replace(ts, start) })
	return c.start(ctx)
}

func (c *Controller) Enqueue(ts ...library.Track) { c.withQ(func(q *Queue) { q.Append(ts...) }) }

func (c *Controller) Next(ctx context.Context) error {
	if !c.queueNext() {
		c.eng.Stop()
		return nil
	}
	return c.start(ctx)
}

func (c *Controller) Prev(ctx context.Context) error {
	if c.eng.Position() > 3*time.Second || !c.queuePrev() {
		c.eng.Seek(0)
		return nil
	}
	return c.start(ctx)
}

func (c *Controller) Jump(ctx context.Context, i int) error {
	var ok bool
	c.withQ(func(q *Queue) { ok = q.Jump(i) })
	if !ok {
		return nil
	}
	return c.start(ctx)
}

func (c *Controller) TogglePause() {
	if c.eng.Playing() {
		c.eng.Pause()
	} else {
		c.eng.Resume()
	}
}

func (c *Controller) SeekBy(delta time.Duration) {
	c.eng.Seek(c.eng.Position() + delta)
}

// Run advances the queue when a track ends and prefetches the next track
// shortly before. It returns when ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.eng.Done():
			if !c.queueNext() {
				c.stopped()
				continue
			}
			if err := c.start(ctx); err != nil {
				log.Printf("advance: %v", err)
			}
		case <-tick.C:
			c.maybePrefetch(ctx)
		}
	}
}

func (c *Controller) maybePrefetch(ctx context.Context) {
	l := c.eng.Length()
	if l == 0 || l-c.eng.Position() > prefetchAhead {
		return
	}
	next := c.peekNext()
	if next == nil {
		return
	}
	c.mu.Lock()
	already := c.pre != nil && c.pre.id == next.ID
	c.mu.Unlock()
	if already {
		return
	}
	src, err := c.open(ctx, *next)
	if err != nil {
		log.Printf("prefetch %s: %v", next.ID, err)
		return
	}
	c.mu.Lock()
	if c.pre != nil {
		c.pre.src.Close()
	}
	c.pre = &prefetched{id: next.ID, src: src}
	c.mu.Unlock()
}

// peekNext returns the track Next() would move to, without moving. The copy's
// step() may build a shuffle order of its own; the real cursor is untouched.
func (c *Controller) peekNext() *library.Track {
	var q Queue
	c.withQ(func(real *Queue) { q = *real })
	if !q.Next() {
		return nil
	}
	t, _ := q.Current()
	return &t
}
