package audio

// Resampler converts interleaved stereo float32 between sample rates by linear
// interpolation. Good enough for v1; a windowed-sinc version is a later upgrade.
type Resampler struct {
	step  float64 // input frames per output frame
	pos   float64 // fractional position, relative to the carried frame (index -1)
	lastL float32 // last input frame carried across calls
	lastR float32
	prime bool
}

func NewResampler(inRate, outRate int) *Resampler {
	return &Resampler{step: float64(inRate) / float64(outRate)}
}

func (r *Resampler) Ratio() float64 { return 1 / r.step }

func (r *Resampler) Process(in []float32) []float32 {
	if r.step == 1 {
		out := make([]float32, len(in))
		copy(out, in)
		return out
	}
	frames := len(in) / 2
	if frames == 0 {
		return nil
	}
	get := func(i int) (float32, float32) {
		if i < 0 {
			return r.lastL, r.lastR
		}
		return in[2*i], in[2*i+1]
	}
	if !r.prime {
		r.lastL, r.lastR = in[0], in[1]
		r.prime = true
	}
	out := make([]float32, 0, int(float64(frames)/r.step)*2+4)
	for r.pos < float64(frames) {
		i := int(r.pos) - 1
		f := float32(r.pos - float64(int(r.pos)))
		l0, r0 := get(i)
		l1, r1 := get(i + 1)
		out = append(out, l0+(l1-l0)*f, r0+(r1-r0)*f)
		r.pos += r.step
	}
	r.pos -= float64(frames)
	r.lastL, r.lastR = in[2*(frames-1)], in[2*(frames-1)+1]
	return out
}
