package audio

import (
	"io"
	"time"

	"github.com/ebitengine/oto/v3"
)

type otoOutput struct{ ctx *oto.Context }

// NewOtoOutput opens the process's single audio context. Call once.
func NewOtoOutput(rate int) (Output, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:      rate,
		ChannelCount:    2,
		Format:          oto.FormatSignedInt16LE,
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
	return &otoOutput{ctx: ctx}, nil
}

func (o *otoOutput) NewPlayer(r io.Reader) Player { return o.ctx.NewPlayer(r) }
