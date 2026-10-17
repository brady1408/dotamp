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
	tags            []tag // who asked for each track; zero for the DJ's own rows
	idx             int   // index into tracks
	order           []int // shuffle permutation of indices; nil when not built
	opos            int   // position in order
}

// tag names the Discord requester of a queue row.
type tag struct{ by, name string }

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
	q.tags = make([]tag, len(ts))
	q.idx = max(0, min(start, len(ts)-1))
	q.order = nil
}

func (q *Queue) Append(ts ...library.Track) {
	q.tracks = append(q.tracks, ts...)
	q.tags = append(q.tags, make([]tag, len(ts))...)
	q.order = nil
}

func (q *Queue) Clear() { q.tracks, q.tags, q.idx, q.order = nil, nil, 0, nil }

// InsertNext puts t right after the current track, tagged with who asked,
// and makes it the next track even under Shuffle. It returns the index.
func (q *Queue) InsertNext(t library.Track, by, name string) int {
	if len(q.tracks) == 0 {
		q.tracks, q.tags, q.idx, q.order = []library.Track{t}, []tag{{by, name}}, 0, nil
		return 0
	}
	i := q.idx + 1
	q.tracks = append(q.tracks[:i], append([]library.Track{t}, q.tracks[i:]...)...)
	q.tags = append(q.tags[:i], append([]tag{{by, name}}, q.tags[i:]...)...)
	q.order = nil
	if q.Shuffle {
		q.buildOrder()
		rest := make([]int, 0, len(q.order))
		for _, o := range q.order[1:] {
			if o != i {
				rest = append(rest, o)
			}
		}
		q.order = append([]int{q.idx, i}, rest...) // current, the request, then the shuffle
		q.opos = 0
	}
	return i
}

// Tag is who asked for the track at i; empty for the DJ's own rows.
func (q *Queue) Tag(i int) (by, name string) {
	if i < 0 || i >= len(q.tags) {
		return "", ""
	}
	return q.tags[i].by, q.tags[i].name
}

// HasNext reports whether Next would move on without Repeat wrapping.
func (q *Queue) HasNext() bool {
	n := len(q.tracks)
	if n == 0 {
		return false
	}
	if !q.Shuffle {
		return q.idx+1 < n
	}
	q.ensureOrder()
	return q.opos+1 < n
}

// Remove drops the track at i. It reports whether that was the current
// track and, if so, whether it was the last in the list, in which case the
// cursor moves back onto the new last track. Otherwise the cursor keeps
// pointing at the same track, or at the one that followed the removed one.
func (q *Queue) Remove(i int) (wasCurrent, wasLast bool) {
	if i < 0 || i >= len(q.tracks) {
		return false, false
	}
	wasCurrent = i == q.idx
	wasLast = i == len(q.tracks)-1
	q.tracks = append(q.tracks[:i], q.tracks[i+1:]...)
	q.tags = append(q.tags[:i], q.tags[i+1:]...)
	q.order = nil
	if i < q.idx || (wasCurrent && wasLast) {
		q.idx--
	}
	if q.idx < 0 {
		q.idx = 0
	}
	return wasCurrent, wasCurrent && wasLast
}

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

// ensureOrder builds the shuffle order if Shuffle is on and none is current,
// so a peek and the Next that follows it agree on the next track.
func (q *Queue) ensureOrder() {
	if q.Shuffle && (q.order == nil || len(q.order) != len(q.tracks)) {
		q.buildOrder()
	}
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
