package broadcast

import "time"

const (
	slot        = 20 * time.Millisecond
	burst       = 5  // most slots sent in one go after a late tick
	resyncSlots = 50 // a second behind: start over instead of rushing
)

// Pacer hands out 20 ms slots against the wall clock, so a late tick sends
// what it owes instead of letting the stream slip behind one slot per miss.
type Pacer struct{ next time.Time }

func NewPacer(now time.Time) *Pacer { return &Pacer{next: now} }

// Due is how many slots fall due at now: normally 0 or 1, up to burst after
// a late tick, and one fresh slot after a stall too long to catch up on.
func (p *Pacer) Due(now time.Time) int {
	if now.Before(p.next) {
		return 0
	}
	n := int(now.Sub(p.next)/slot) + 1
	switch {
	case n > resyncSlots:
		p.next = now.Add(slot)
		return 1
	case n > burst:
		n = burst
	}
	p.next = p.next.Add(time.Duration(n) * slot)
	return n
}
