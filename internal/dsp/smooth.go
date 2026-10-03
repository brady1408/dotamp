package dsp

// Smoother gives bars a fast attack and exponential decay, and peak caps that
// hold for holdTicks updates and then fall at a constant rate.
type Smoother struct {
	Bars, Caps []float64
	hold       []int
	decay      float64
	holdTicks  int
	fall       float64
}

func NewSmoother(n int, decay float64, holdTicks int, fall float64) *Smoother {
	s := &Smoother{decay: decay, holdTicks: holdTicks, fall: fall}
	s.resize(n)
	return s
}

func (s *Smoother) resize(n int) {
	s.Bars = make([]float64, n)
	s.Caps = make([]float64, n)
	s.hold = make([]int, n)
}

func (s *Smoother) Update(levels []float64) {
	if len(levels) != len(s.Bars) {
		s.resize(len(levels))
	}
	for i, v := range levels {
		if v >= s.Bars[i] {
			s.Bars[i] = v
		} else {
			s.Bars[i] *= s.decay
		}
		if s.Bars[i] >= s.Caps[i] {
			s.Caps[i] = s.Bars[i]
			s.hold[i] = s.holdTicks
		} else if s.hold[i] > 0 {
			s.hold[i]--
		} else {
			s.Caps[i] -= s.fall
			if s.Caps[i] < s.Bars[i] {
				s.Caps[i] = s.Bars[i]
			}
		}
	}
}
