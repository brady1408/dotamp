// Package broadcast streams the player's audio into a Discord voice channel.
package stream

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

// Speaker decides, tick by tick, whether to send audio, one of Discord's
// silence frames, or nothing. The speaking flag is set once at join and
// never dropped: clearing it and raising it again was found to kill the
// stream on Discord's side (live test, 2026-10-17), while a run of silence
// frames followed by nothing resumes cleanly.
type Speaker struct {
	empty   int // consecutive ticks without a packet
	silence int // silence frames sent in this quiet spell
}

func (s *Speaker) Tick(havePacket bool) Send {
	if havePacket {
		s.empty, s.silence = 0, 0
		return SendAudio
	}
	s.empty++
	switch {
	case s.empty <= graceTicks:
		return SendNothing
	case s.silence < silenceFrames:
		s.silence++
		return SendSilence
	}
	return SendNothing
}
