package audio

import (
	"errors"

	"github.com/brady1408/dotamp/internal/library"
)

// Lane is one requester's waiting songs.
type Lane struct {
	By, Name string
	Tracks   []library.Track
}

var ErrTooManyPending = errors.New("too many pending")

// mixer holds the lanes and feeds one request ahead of the cursor. Whose
// turn it is follows from the current track: the lane after the one that
// asked for it, with the DJ's queue as a member of the rotation. On the
// DJ's turn nothing is fed and the queue's own next track plays.
type mixer struct {
	lanes  []*Lane
	fedPos int // queue index of the request fed ahead, -1 when none
	fedBy  string
	max    int // pending cap per requester, 0 = unlimited
}

func (m *mixer) lane(by, name string) *Lane {
	for _, l := range m.lanes {
		if l.By == by {
			l.Name = name
			return l
		}
	}
	l := &Lane{By: by, Name: name}
	m.lanes = append(m.lanes, l)
	return l
}

// waiting is how many songs by has in the lane plus the one fed ahead.
func (m *mixer) waiting(by string) int {
	n := 0
	for _, l := range m.lanes {
		if l.By == by {
			n = len(l.Tracks)
		}
	}
	if m.fedPos >= 0 && m.fedBy == by {
		n++
	}
	return n
}

func (m *mixer) songs() int {
	n := 0
	for _, l := range m.lanes {
		n += len(l.Tracks)
	}
	return n
}

// feed inserts the next lane's song after the cursor unless a request is
// already fed ahead. It reports whether it inserted.
func (m *mixer) feed(q *Queue) bool {
	if m.fedPos >= 0 && m.fedPos == q.idx {
		m.fedPos, m.fedBy = -1, "" // the fed request is playing: its turn is spent, never rescue it
	}
	m.rescue(q)
	if m.fedPos > q.idx || m.songs() == 0 {
		return false
	}
	m.fedPos, m.fedBy = -1, ""
	n := len(m.lanes) + 1 // the lanes plus the DJ, whose slot is len(lanes)
	cur := len(m.lanes)
	if by, _ := q.Tag(q.idx); by != "" {
		for i, l := range m.lanes {
			if l.By == by {
				cur = i
			}
		}
	}
	for step := 1; step <= n; step++ {
		i := (cur + step) % n
		if i == len(m.lanes) { // the DJ's turn
			if q.HasNext() {
				return false // the queue's own next plays
			}
			continue // the DJ has nothing after the cursor: skip the turn
		}
		l := m.lanes[i]
		if len(l.Tracks) == 0 {
			continue
		}
		t := l.Tracks[0]
		l.Tracks = l.Tracks[1:]
		m.fedPos, m.fedBy = q.InsertNext(t, l.By, l.Name), l.By
		return true
	}
	return false
}

// rescue moves a fed request that a handover jumped past back to right
// after the cursor, so it plays next instead of being orphaned behind it.
func (m *mixer) rescue(q *Queue) {
	if m.fedPos < 0 || m.fedPos >= q.idx {
		return
	}
	t := q.tracks[m.fedPos]
	by, name := q.Tag(m.fedPos)
	q.Remove(m.fedPos) // the cursor moves down one with it
	m.fedPos = q.InsertNext(t, by, name)
}

// removed keeps fedPos in step with a queue removal.
func (m *mixer) removed(i int) {
	switch {
	case i == m.fedPos:
		m.fedPos, m.fedBy = -1, ""
	case i < m.fedPos:
		m.fedPos--
	}
}

// clear drops every lane and forgets the fed row; it returns the songs dropped.
func (m *mixer) clear() int {
	n := m.songs()
	if m.fedPos >= 0 {
		n++
	}
	m.lanes, m.fedPos, m.fedBy = nil, -1, ""
	return n
}
