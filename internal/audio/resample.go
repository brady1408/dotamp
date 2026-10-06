package audio

import "math"

// Resampler converts interleaved stereo float32 between sample rates with a
// windowed-sinc filter: every output sample is a weighted sum of the nearby
// input samples, the weights being a sinc cut off at the lower of the two
// Nyquist frequencies under a Kaiser window. Downsampling therefore removes
// what cannot be represented instead of folding it back as aliasing, and
// a tone passes through within a fraction of a decibel.
//
// The filter looks sincTaps input frames ahead, so the last sincTaps frames
// of a stream are only produced once more input arrives; at the end of a
// track that is about a millisecond, which the next track's first frames
// cover.
type Resampler struct {
	step   float64 // input frames per output frame
	table  [][]float32
	hist   []float32 // interleaved frames carried between calls: 2*sincTaps of them
	pos    float64   // next output position, in frames, relative to hist's start
	buf    []float32
	primed bool
}

const (
	sincTaps   = 48  // taps per side; 96 in all
	sincPhases = 512 // fractional positions the kernel is tabulated at
	kaiserBeta = 9.0
)

func NewResampler(inRate, outRate int) *Resampler {
	r := &Resampler{step: float64(inRate) / float64(outRate)}
	if r.step == 1 {
		return r
	}
	cutoff := 0.5 * 0.97 // cycles per input sample; just under the input Nyquist
	if r.step > 1 {
		cutoff = 0.5 / r.step * 0.97 // downsampling: the output Nyquist is the limit
	}
	r.table = make([][]float32, sincPhases)
	for p := range r.table {
		frac := float64(p) / sincPhases
		coef := make([]float32, 2*sincTaps)
		sum := 0.0
		for j := range coef {
			x := float64(j-sincTaps+1) - frac // kernel argument for input index base-sincTaps+1+j
			h := 2 * cutoff * sinc(2*cutoff*x) * kaiser(x/sincTaps)
			coef[j] = float32(h)
			sum += h
		}
		for j := range coef { // unity gain at DC regardless of phase
			coef[j] = float32(float64(coef[j]) / sum)
		}
		r.table[p] = coef
	}
	r.hist = make([]float32, 2*2*sincTaps)
	r.pos = 2 * sincTaps // the first real frame sits after the zero history
	return r
}

func (r *Resampler) Ratio() float64 { return 1 / r.step }

// Delay is the alignment of output time to input time, in output frames: zero,
// since the kernel is centred on the exact fractional position.
func (r *Resampler) Delay() float64 { return 0 }

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	return math.Sin(math.Pi*x) / (math.Pi * x)
}

// kaiser is the Kaiser window for |x| <= 1.
func kaiser(x float64) float64 {
	if x <= -1 || x >= 1 {
		return 0
	}
	return besselI0(kaiserBeta*math.Sqrt(1-x*x)) / besselI0(kaiserBeta)
}

func besselI0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 50; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-12*sum {
			break
		}
	}
	return sum
}

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
	// Work on history followed by the new input.
	r.buf = append(r.buf[:0], r.hist...)
	r.buf = append(r.buf, in[:frames*2]...)
	total := len(r.buf) / 2
	out := make([]float32, 0, int(float64(frames)/r.step)*2+4)
	for {
		base := int(r.pos)
		if base+sincTaps > total-1 {
			break // the kernel would reach past the input we have
		}
		frac := r.pos - float64(base)
		coef := r.table[int(frac*sincPhases)]
		var l, rr float32
		first := base - sincTaps + 1
		for j, c := range coef {
			i := 2 * (first + j)
			l += r.buf[i] * c
			rr += r.buf[i+1] * c
		}
		out = append(out, l, rr)
		r.pos += r.step
	}
	// Keep the last 2*sincTaps frames for the next call.
	keep := 2 * sincTaps
	if total > keep {
		drop := total - keep
		r.hist = append(r.hist[:0], r.buf[2*drop:]...)
		r.pos -= float64(drop)
	} else {
		r.hist = append(r.hist[:0], r.buf...)
	}
	return out
}
