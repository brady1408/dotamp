package audio

import (
	"io"
	"sync"
	"time"
)

// NewSilentOutput returns an Output whose players consume audio at the
// sample rate and discard it. It lets dotamp run where there is no sound
// device, such as a headless box rendering the README's screenshots, with
// the visualizers moving exactly as they would over speakers.
func NewSilentOutput(rate int) Output { return silentOutput{rate: rate} }

type silentOutput struct{ rate int }

const silentTick = 10 * time.Millisecond

func (o silentOutput) NewPlayer(r io.Reader) Player {
	p := &silentPlayer{r: r, chunk: make([]byte, o.rate*frameBytes/100), stop: make(chan struct{})}
	go p.run()
	return p
}

type silentPlayer struct {
	mu      sync.Mutex
	r       io.Reader
	chunk   []byte // one tick's worth of float32 stereo
	playing bool
	stop    chan struct{}
	once    sync.Once
}

func (p *silentPlayer) run() {
	t := time.NewTicker(silentTick)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
		}
		p.mu.Lock()
		playing := p.playing
		p.mu.Unlock()
		if playing {
			io.ReadFull(p.r, p.chunk)
		}
	}
}

func (p *silentPlayer) Play()             { p.mu.Lock(); p.playing = true; p.mu.Unlock() }
func (p *silentPlayer) Pause()            { p.mu.Lock(); p.playing = false; p.mu.Unlock() }
func (p *silentPlayer) BufferedSize() int { return len(p.chunk) }
func (p *silentPlayer) Close() error      { p.once.Do(func() { close(p.stop) }); return nil }
