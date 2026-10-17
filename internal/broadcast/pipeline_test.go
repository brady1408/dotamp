package broadcast

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
)

func tone(rate int, seconds float64, left bool) []float32 {
	n := int(float64(rate) * seconds)
	pcm := make([]float32, 2*n)
	for i := 0; i < n; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		if left {
			pcm[2*i] = v
		} else {
			pcm[2*i+1] = v
		}
	}
	return pcm
}

func TestPipelineMakesFiftyStereoPacketsPerSecond(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		p, err := NewPipeline(rate)
		if err != nil {
			t.Fatal(err)
		}
		p.Push(tone(rate, 1.0, true))
		pk := p.Packets()
		if len(pk) < 49 || len(pk) > 50 { // the resampler's history can hold back the last frame
			t.Fatalf("rate %d: %d packets", rate, len(pk))
		}
		dec, _ := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: 2})
		out := make([]float32, 960*2)
		var l, r float64
		for _, b := range pk[10:] { // after the encoder settles
			n, err := dec.Decode(b, out)
			if err != nil || n != 960 {
				t.Fatalf("decode: n=%d err=%v", n, err)
			}
			for i := 0; i < n; i++ {
				l += float64(out[2*i] * out[2*i])
				r += float64(out[2*i+1] * out[2*i+1])
			}
		}
		if l < 100 || r > l/100 {
			t.Fatalf("rate %d: left-only input must stay left: L=%.1f R=%.1f", rate, l, r)
		}
		if len(p.Packets()) != 0 {
			t.Fatal("Packets drains")
		}
	}
}

func TestSpeakerGoesQuietAfterThreeEmptyTicksAndAnnouncesReturn(t *testing.T) {
	var s Speaker
	for i := 0; i < 3; i++ {
		if send, speaking, changed := s.Tick(true); send != SendAudio || !speaking || (i == 0) != changed {
			t.Fatalf("tick %d: %v %v %v", i, send, speaking, changed)
		}
	}
	for i := 0; i < 3; i++ { // grace: still "audio" state but nothing to send
		if send, _, _ := s.Tick(false); send != SendNothing {
			t.Fatalf("grace tick %d: %v", i, send)
		}
	}
	for i := 0; i < 5; i++ { // then five silence frames
		if send, speaking, _ := s.Tick(false); send != SendSilence || !speaking {
			t.Fatalf("silence tick %d: %v %v", i, send, speaking)
		}
	}
	if send, speaking, changed := s.Tick(false); send != SendNothing || speaking || !changed {
		t.Fatalf("speaking should drop after the silence frames: %v %v %v", send, speaking, changed)
	}
	if send, speaking, changed := s.Tick(true); send != SendAudio || !speaking || !changed {
		t.Fatalf("audio returning raises the flag first: %v %v %v", send, speaking, changed)
	}
}
