// Package otoout plays the engine's output on the machine's sound device.
// It is the only package that imports oto; a program that never touches a
// speaker (dotamp-bot) stays clear of it and links statically.
package otoout

import (
	"io"
	"time"

	"github.com/ebitengine/oto/v3"

	"github.com/brady1408/dotamp/internal/audio"
)

type output struct{ ctx *oto.Context }

// New opens the process's single audio context. Call once.
func New(rate int) (audio.Output, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:      rate,
		ChannelCount:    2,
		Format:          oto.FormatFloat32LE,
		BufferSize:      100 * time.Millisecond,
		ApplicationName: "dotamp",
	})
	if err != nil {
		return nil, err
	}
	<-ready
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &output{ctx: ctx}, nil
}

func (o *output) NewPlayer(r io.Reader) audio.Player { return o.ctx.NewPlayer(r) }
