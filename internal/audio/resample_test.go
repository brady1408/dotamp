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
	if total < 44090 || total > 44110 {
		t.Fatalf("1 s at 48k -> %d frames at 44.1k, want ~44100", total)
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
