package audio

import (
	"math"
	"testing"
)

func stereoSine(frames int, rate, hz float64) []float32 {
	out := make([]float32, frames*2)
	for i := 0; i < frames; i++ {
		v := float32(math.Sin(2 * math.Pi * hz * float64(i) / rate))
		out[2*i], out[2*i+1] = v, v
	}
	return out
}

func TestResamplePassthrough(t *testing.T) {
	r := NewResampler(44100, 44100)
	in := stereoSine(100, 44100, 440)
	out := r.Process(in)
	if len(out) != len(in) || out[5] != in[5] {
		t.Fatal("passthrough changed data")
	}
}

func TestResampleLengthAcrossChunks(t *testing.T) {
	r := NewResampler(48000, 44100)
	total := 0
	for i := 0; i < 100; i++ { // 100 chunks of 480 frames = 1 s at 48 kHz
		total += len(r.Process(stereoSine(480, 48000, 440))) / 2
	}
	// The filter holds its last sincTaps input frames until more arrive.
	tail := sincTaps * 44100 / 48000 // the filter's held frames, in output frames
	if total < 44100-tail-2 || total > 44110 {
		t.Fatalf("1 s at 48k -> %d frames at 44.1k, want ~44100 less the filter's tail", total)
	}
}

func TestResampleKeepsPitch(t *testing.T) {
	r := NewResampler(48000, 44100)
	out := r.Process(stereoSine(48000, 48000, 1000))
	// count zero crossings on the left channel: 1000 Hz for 1 s ≈ 2000 crossings
	crossings := 0
	for i := 2; i < len(out); i += 2 {
		if (out[i] >= 0) != (out[i-2] >= 0) {
			crossings++
		}
	}
	if crossings < 1990 || crossings > 2010 {
		t.Fatalf("crossings = %d, want ~2000", crossings)
	}
}

// A 1 kHz tone resampled from 96 kHz must come out as a clean 1 kHz tone:
// within a fraction of a dB in level and within -40 dB RMS of the ideal.
func TestResampleSincIsTransparent(t *testing.T) {
	r := NewResampler(96000, 44100)
	var out []float32
	for i := 0; i < 50; i++ { // 50 chunks of 960 frames = 0.5 s
		chunk := make([]float32, 960*2)
		for f := 0; f < 960; f++ {
			v := float32(math.Sin(2 * math.Pi * 1000 * float64(i*960+f) / 96000))
			chunk[2*f], chunk[2*f+1] = v, v
		}
		out = append(out, r.Process(chunk)...)
	}
	frames := len(out) / 2
	// Skip the first 1000 frames (filter warm-up) and compare against the ideal.
	var errSum, sigSum, peak float64
	for f := 1000; f < frames; f++ {
		ideal := math.Sin(2 * math.Pi * 1000 * (float64(f) + r.Delay()) / 44100)
		got := float64(out[2*f])
		errSum += (got - ideal) * (got - ideal)
		sigSum += ideal * ideal
		if math.Abs(got) > peak {
			peak = math.Abs(got)
		}
	}
	if 20*math.Log10(peak) < -0.5 || 20*math.Log10(peak) > 0.5 {
		t.Fatalf("level off: peak = %.3f", peak)
	}
	if snr := 10 * math.Log10(sigSum/errSum); snr < 40 {
		t.Fatalf("error vs ideal too high: %.1f dB SNR", snr)
	}
}

// A 30 kHz tone at 96 kHz lies above 44.1 kHz's Nyquist: a real resampler
// removes it, a linear one folds it back in as a 14 kHz alias.
func TestResampleSincRejectsAliases(t *testing.T) {
	r := NewResampler(96000, 44100)
	var out []float32
	for i := 0; i < 50; i++ {
		out = append(out, r.Process(stereoSine(960, 96000, 30000))...)
	}
	var peak float64
	for f := 1000; f < len(out)/2; f++ {
		if v := math.Abs(float64(out[2*f])); v > peak {
			peak = v
		}
	}
	if 20*math.Log10(peak+1e-12) > -40 {
		t.Fatalf("alias leaked through at %.1f dB", 20*math.Log10(peak))
	}
}
