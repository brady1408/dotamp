package audio

import (
	"math/rand/v2"

	"github.com/brady1408/dotamp/internal/library"
)

// Queue is the ordered list of tracks and a cursor. With Shuffle on, Next walks
// a random permutation that starts at the current track and is rebuilt when
// the list changes.
type Queue struct {
	Shuffle, Repeat bool
	tracks          []library.Track
	idx             int   // index into tracks
	order           []int // shuffle permutation of indices; nil when not built
	opos            int   // position in order
}

func (q *Queue) Tracks() []library.Track { return q.tracks }

func (q *Queue) Index() int {
	if len(q.tracks) == 0 {
		return -1
	}
	return q.idx
}

func (q *Queue) Current() (library.Track, bool) {
	if len(q.tracks) == 0 {
		return library.Track{}, false
	}
	return q.tracks[q.idx], true
}

func (q *Queue) Replace(ts []library.Track, start int) {
	q.tracks = append([]library.Track(nil), ts...)
	q.idx = max(0, min(start, len(ts)-1))
	q.order = nil
}

func (q *Queue) Append(ts ...library.Track) {
	q.tracks = append(q.tracks, ts...)
	q.order = nil
}

func (q *Queue) Clear() { q.tracks, q.idx, q.order = nil, 0, nil }

func (q *Queue) Jump(i int) bool {
	if i < 0 || i >= len(q.tracks) {
		return false
	}
	q.idx = i
	q.order = nil
	return true
}

func (q *Queue) buildOrder() {
	n := len(q.tracks)
	q.order = make([]int, 0, n)
	q.order = append(q.order, q.idx)
	rest := make([]int, 0, n-1)
	for i := 0; i < n; i++ {
		if i != q.idx {
			rest = append(rest, i)
		}
	}
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	q.order = append(q.order, rest...)
	q.opos = 0
}

func (q *Queue) step(delta int) bool {
	n := len(q.tracks)
	if n == 0 {
		return false
	}
	if !q.Shuffle {
		next := q.idx + delta
		if next < 0 || next >= n {
			if !q.Repeat {
				return false
			}
			next = (next + n) % n
		}
		q.idx = next
		return true
	}
	if q.order == nil || len(q.order) != n {
		q.buildOrder()
	}
	next := q.opos + delta
	if next < 0 || next >= n {
		if !q.Repeat {
			return false
		}
		next = (next + n) % n
	}
	q.opos = next
	q.idx = q.order[next]
	return true
}

func (q *Queue) Next() bool { return q.step(1) }
func (q *Queue) Prev() bool { return q.step(-1) }
