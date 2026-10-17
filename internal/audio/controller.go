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

	qmu sync.Mutex // guards q and mix; the ui goroutine and Run both touch them
	q   Queue
	mix mixer

	mu        sync.Mutex // guards current, has, nextID, nextTrack, retried
	current   library.Track
	has       bool
	nextID    string        // track whose source is queued in the engine, "" if none
	nextTrack library.Track // that track, for the handover
	retried   string        // track ID whose stream was already reopened once
	curBy     string        // who asked for the current track; "" for the DJ
	curName   string
	reported  bool // the current track's end has been reported
	onEnd     func(t library.Track, played time.Duration, by, name string)
}

func NewController(lib library.Library, eng *Engine, notify func(string)) *Controller {
	c := &Controller{lib: lib, eng: eng, notify: notify}
	c.mix.fedPos = -1
	return c
}

// SetMaxPending caps the songs one requester may have waiting; 0 = unlimited.
func (c *Controller) SetMaxPending(n int) { c.withQ(func(*Queue) { c.mix.max = n }) }

// SetTrackEnd is told every track that stops being current: how much of
// it was heard and who asked for it.
func (c *Controller) SetTrackEnd(fn func(t library.Track, played time.Duration, by, name string)) {
	c.mu.Lock()
	c.onEnd = fn
	c.mu.Unlock()
}

// Request queues songs for someone in the room. With nothing playing it
// starts the first one at once.
func (c *Controller) Request(ctx context.Context, by, name string, ts []library.Track) (int, error) {
	var pending int
	var refused, fed, startNow bool
	c.withQ(func(q *Queue) {
		pending = c.mix.waiting(by)
		if c.mix.max > 0 && pending+len(ts) > c.mix.max {
			refused = true
			return
		}
		l := c.mix.lane(by, name)
		l.Tracks = append(l.Tracks, ts...)
		pending = c.mix.waiting(by)
		c.mu.Lock()
		playing := c.has
		c.mu.Unlock()
		if c.mix.feed(q) {
			fed = true
			if !playing {
				q.Jump(c.mix.fedPos) // a played-out queue left the cursor on its last song
				startNow = true
			}
		}
	})
	if refused {
		return pending, ErrTooManyPending
	}
	if fed {
		c.dropPrefetch() // the DJ's next may already be queued in the engine
	}
	if startNow {
		// The request outlives the slash command that carried it.
		return pending, c.start(context.WithoutCancel(ctx))
	}
	return pending, nil
}

// Pending is every lane with its waiting songs, in rotation order.
func (c *Controller) Pending() (ls []Lane) {
	c.withQ(func(*Queue) {
		for _, l := range c.mix.lanes {
			ls = append(ls, Lane{By: l.By, Name: l.Name, Tracks: append([]library.Track(nil), l.Tracks...)})
		}
	})
	return
}

// RequesterOf is who asked for queue row i; empty for the DJ's own rows.
func (c *Controller) RequesterOf(i int) (by, name string) {
	c.withQ(func(q *Queue) { by, name = q.Tag(i) })
	return
}

// ClearRequests drops every lane; it returns the songs dropped.
func (c *Controller) ClearRequests() (n int) {
	c.withQ(func(*Queue) { n = c.mix.clear() })
	return
}

// ended tells the track-end hook what just finished and who asked for it.
func (c *Controller) ended(played time.Duration) {
	c.mu.Lock()
	t, has, fn, by, name := c.current, c.has && !c.reported, c.onEnd, c.curBy, c.curName
	c.reported = true
	c.mu.Unlock()
	if has && fn != nil {
		fn(t, played, by, name)
	}
}

// began records who asked for the track at the cursor and feeds the next
// request ahead of it.
func (c *Controller) began() {
	c.withQ(func(q *Queue) {
		by, name := q.Tag(q.idx)
		c.mu.Lock()
		c.curBy, c.curName, c.reported = by, name, false
		c.mu.Unlock()
		c.mix.feed(q)
	})
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
	c.ended(c.eng.Position())
	c.eng.Stop()
	c.mu.Lock()
	c.has = false
	c.mu.Unlock()
}

// start plays the queue's current track, trying twice before skipping
// forward. It gives up after one pass over the queue, so Repeat cannot turn
// a dead server into an endless loop.
func (c *Controller) start(ctx context.Context) error {
	c.ended(c.eng.Position())
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
			c.began()
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
	c.withQ(func(q *Queue) {
		q.Replace(ts, start)
		c.mix.fedPos, c.mix.fedBy = -1, "" // the fed row went with the old list; lanes stay
	})
	return c.start(ctx)
}

func (c *Controller) Enqueue(ts ...library.Track) { c.withQ(func(q *Queue) { q.Append(ts...) }) }

// Clear empties the queue and every request lane and stops playback. It
// returns how many requested songs were dropped.
func (c *Controller) Clear() (requests int) {
	c.withQ(func(q *Queue) {
		requests = c.mix.clear()
		q.Clear()
	})
	c.discardPrefetch()
	c.stopped()
	return requests
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
			c.mix.removed(i)
		}
	})
	if !ok {
		return nil
	}
	c.mu.Lock()
	prefetched := c.nextID == removed.ID
	c.mu.Unlock()
	if prefetched && (wasCurrent || !c.dropPrefetch()) {
		c.discardPrefetch() // about to restart anyway, or the decoder is already into it
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

// dropPrefetch forgets a prefetched next track without disturbing the
// playing one. It fails, leaving the prefetch in place, once the decoder
// has already started on it.
func (c *Controller) dropPrefetch() bool {
	if !c.eng.DropNext() {
		return false
	}
	c.mu.Lock()
	c.nextID, c.nextTrack = "", library.Track{}
	c.mu.Unlock()
	return true
}

// discardPrefetch closes whatever source is queued in the engine.
func (c *Controller) discardPrefetch() {
	if src := c.takePrefetched(""); src != nil {
		src.Close()
	}
}

func (c *Controller) Next(ctx context.Context) error {
	if !c.queueNext() {
		c.stopped()
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
	left := c.current
	c.mu.Unlock()
	c.ended(left.Duration) // a handover means the track played out
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
	c.began()
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
