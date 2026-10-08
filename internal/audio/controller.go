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

	mu        sync.Mutex // guards current, has, nextID, nextTrack, retried
	current   library.Track
	has       bool
	nextID    string        // track whose source is queued in the engine, "" if none
	nextTrack library.Track // that track, for the handover
	retried   string        // track ID whose stream was already reopened once
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

// start plays the queue's current track, trying twice before skipping
// forward. It gives up after one pass over the queue, so Repeat cannot turn
// a dead server into an endless loop.
func (c *Controller) start(ctx context.Context) error {
	var tries, size int
	c.withQ(func(q *Queue) { size = len(q.Tracks()) })
	for {
		var t library.Track
		var ok bool
		c.withQ(func(q *Queue) { t, ok = q.Current() })
		if !ok {
			c.stopped()
			return nil
		}
		tries++
		if tries > size {
			c.stopped()
			return fmt.Errorf("nothing in the queue could be played")
		}
		src := c.takePrefetched(t.ID)
		var err error
		for attempt := 0; src == nil && attempt < 2; attempt++ {
			src, err = c.open(ctx, t)
		}
		if src != nil {
			c.mu.Lock()
			c.current, c.has, c.retried = t, true, ""
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

// takePrefetched returns the engine's queued source if it is for track id,
// and discards it otherwise.
func (c *Controller) takePrefetched(id string) Source {
	c.mu.Lock()
	queued := c.nextID
	c.nextID = ""
	c.mu.Unlock()
	src := c.eng.TakeNext()
	if src == nil {
		return nil
	}
	if queued != id {
		src.Close()
		return nil
	}
	return src
}

func (c *Controller) PlayTracks(ctx context.Context, ts []library.Track, start int) error {
	c.withQ(func(q *Queue) { q.Replace(ts, start) })
	return c.start(ctx)
}

func (c *Controller) Enqueue(ts ...library.Track) { c.withQ(func(q *Queue) { q.Append(ts...) }) }

// Clear empties the queue and stops playback.
func (c *Controller) Clear() {
	c.withQ(func(q *Queue) { q.Clear() })
	c.discardPrefetch()
	c.stopped()
}

// Remove drops the track at i from the queue. Removing the playing track
// moves playback to the one that followed it, or stops when it was last.
// A prefetched copy of the removed track is discarded so the gapless
// handover cannot play it.
func (c *Controller) Remove(ctx context.Context, i int) error {
	var removed library.Track
	var wasCurrent, wasLast, ok bool
	c.withQ(func(q *Queue) {
		if i >= 0 && i < len(q.Tracks()) {
			removed, ok = q.Tracks()[i], true
			wasCurrent, wasLast = q.Remove(i)
		}
	})
	if !ok {
		return nil
	}
	c.mu.Lock()
	prefetched := c.nextID == removed.ID
	c.mu.Unlock()
	if prefetched {
		c.discardPrefetch()
	}
	switch {
	case wasCurrent && wasLast:
		c.stopped()
		return nil
	case wasCurrent:
		return c.start(ctx)
	}
	return nil
}

// discardPrefetch closes whatever source is queued in the engine.
func (c *Controller) discardPrefetch() {
	if src := c.takePrefetched(""); src != nil {
		src.Close()
	}
}

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
		case <-c.eng.Handover():
			c.onHandover()
		case <-c.eng.Done():
			if err := c.eng.Err(); err != nil && c.recover(ctx, err) {
				continue
			}
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

// recover handles a track that ended on an error: the stream is reopened
// once from where it stopped; a second failure skips the track with a notice.
// It reports whether playback resumed.
func (c *Controller) recover(ctx context.Context, cause error) bool {
	t, has := c.Current()
	if !has {
		return false
	}
	c.mu.Lock()
	already := c.retried == t.ID
	c.retried = t.ID
	c.mu.Unlock()
	pos := c.eng.Position()
	log.Printf("track %s (%s) failed at %v: %v", t.Title, t.ID, pos, cause)
	if !already {
		if src, err := c.open(ctx, t); err == nil {
			c.eng.PlayAt(src, pos)
			return true
		} else {
			log.Printf("reopen %s: %v", t.ID, err)
		}
	}
	c.notify(fmt.Sprintf("Skipped %s: %v", t.Title, cause))
	return false
}

// onHandover runs when the engine has moved on to the queued source: the
// queue cursor follows, and the deck shows the new track.
func (c *Controller) onHandover() {
	c.mu.Lock()
	t := c.nextTrack
	c.current, c.has, c.retried, c.nextID = t, true, "", ""
	c.mu.Unlock()
	c.withQ(func(q *Queue) {
		if peek := peekQueue(q); peek != nil && peek.ID == t.ID {
			q.Next()
			return
		}
		for i, tr := range q.Tracks() { // the order changed since the prefetch
			if tr.ID == t.ID {
				q.Jump(i)
				return
			}
		}
	})
}

// maybePrefetch opens the next track's stream shortly before the current one
// ends and queues it in the engine for a gapless handover.
func (c *Controller) maybePrefetch(ctx context.Context) {
	l := c.eng.Length()
	if l == 0 || l-c.eng.Position() > prefetchAhead {
		return
	}
	if c.eng.PeekNext() != nil {
		return
	}
	var next *library.Track
	c.withQ(func(q *Queue) { next = peekQueue(q) })
	if next == nil {
		return
	}
	src, err := c.open(ctx, *next)
	if err != nil {
		log.Printf("prefetch %s: %v", next.ID, err)
		return
	}
	c.mu.Lock()
	c.nextID, c.nextTrack = next.ID, *next
	c.mu.Unlock()
	c.eng.SetNext(src)
}

// peekQueue returns the track Next() would move to, without moving. It builds
// the shuffle order first so the peek and the real Next agree.
func peekQueue(real *Queue) *library.Track {
	real.ensureOrder()
	q := *real
	if !q.Next() {
		return nil
	}
	t, _ := q.Current()
	return &t
}
