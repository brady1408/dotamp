// Package broadcast streams the player's audio into a Discord voice channel.
package broadcast

import (
	"github.com/thesyncim/gopus"

	"github.com/brady1408/dotamp/internal/audio"
)

const (
	opusRate  = 48000
	frameSize = 960 // samples per channel per 20 ms
	bitrate   = 128000
)

// Silence is the Opus packet Discord documents for keeping a stream warm
// while nothing plays.
var Silence = []byte{0xF8, 0xFF, 0xFE}

// Pipeline turns engine-rate stereo float32 into 20 ms Opus packets.
type Pipeline struct {
	rs  *audio.Resampler
	enc *gopus.Encoder
	acc []float32 // interleaved 48 kHz samples not yet a full frame
	out [][]byte
	pkt []byte
}

func NewPipeline(inRate int) (*Pipeline, error) {
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{SampleRate: opusRate, Channels: 2, Application: gopus.ApplicationAudio})
	if err != nil {
		return nil, err
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(true)
	return &Pipeline{rs: audio.NewResampler(inRate, opusRate), enc: enc, pkt: make([]byte, 4000)}, nil
}

// Push feeds source frames; packets become available as full frames fill.
func (p *Pipeline) Push(stereo []float32) {
	p.acc = append(p.acc, p.rs.Process(stereo)...)
	for len(p.acc) >= frameSize*2 {
		n, err := p.enc.Encode(p.acc[:frameSize*2], p.pkt)
		p.acc = p.acc[frameSize*2:]
		if err != nil || n == 0 {
			continue
		}
		p.out = append(p.out, append([]byte(nil), p.pkt[:n]...))
	}
}

// Packets returns every packet ready since the last call.
func (p *Pipeline) Packets() [][]byte {
	out := p.out
	p.out = nil
	return out
}

type Send int

const (
	SendNothing Send = iota
	SendAudio
	SendSilence
)

const (
	graceTicks    = 3 // empty ticks tolerated before going quiet
	silenceFrames = 5 // Discord's recommended run of silence before stopping
)

// Speaker tracks Discord's speaking flag tick by tick: after a few empty
// ticks it sends five silence frames and drops the flag; the first audio
// after that raises it again.
type Speaker struct {
	speaking bool
	empty    int // consecutive ticks without a packet
	silence  int // silence frames sent in this quiet spell
	started  bool
}

// Tick reports what to send this tick, whether the speaking flag should be
// set, and whether that flag changed (so the caller tells Discord).
func (s *Speaker) Tick(havePacket bool) (send Send, speaking, changed bool) {
	was := s.speaking
	if havePacket {
		s.empty, s.silence, s.speaking = 0, 0, true
		send = SendAudio
	} else {
		s.empty++
		switch {
		case s.empty <= graceTicks:
			send = SendNothing
		case s.silence < silenceFrames && s.speaking:
			s.silence++
			send = SendSilence
		default:
			s.speaking = false
			send = SendNothing
		}
	}
	changed = s.speaking != was || (!s.started && s.speaking)
	s.started = true
	return send, s.speaking, changed
}
