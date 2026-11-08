package stream

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/thesyncim/gopus"

	"github.com/brady1408/dotamp/internal/audio"
)

type page struct {
	flags   byte
	granule int64
	serial  uint32
	seq     uint32
	packets [][]byte
}

// parsePages reads every Ogg page, checking the capture pattern and CRC.
func parsePages(t *testing.T, b []byte) []page {
	t.Helper()
	var out []page
	for len(b) > 0 {
		if len(b) < 27 || string(b[:4]) != "OggS" || b[4] != 0 {
			t.Fatalf("bad page header at %d bytes left", len(b))
		}
		nseg := int(b[26])
		lacing := b[27 : 27+nseg]
		size := 0
		for _, l := range lacing {
			size += int(l)
		}
		total := 27 + nseg + size
		raw := append([]byte(nil), b[:total]...)
		want := binary.LittleEndian.Uint32(raw[22:26])
		copy(raw[22:26], []byte{0, 0, 0, 0})
		if got := oggCRC(raw); got != want {
			t.Fatalf("page %d CRC %08x, header says %08x", len(out), got, want)
		}
		p := page{flags: b[5], granule: int64(binary.LittleEndian.Uint64(b[6:14])), serial: binary.LittleEndian.Uint32(b[14:18]), seq: binary.LittleEndian.Uint32(b[18:22])}
		data := b[27+nseg : total]
		var cur []byte
		for _, l := range lacing {
			cur = append(cur, data[:l]...)
			data = data[l:]
			if l < 255 {
				p.packets = append(p.packets, cur)
				cur = nil
			}
		}
		out = append(out, p)
		b = b[total:]
	}
	return out
}

func TestOggStreamIsWellFormed(t *testing.T) {
	var buf bytes.Buffer
	w := NewOggWriter(&buf)
	if err := w.WriteHeaders(); err != nil {
		t.Fatal(err)
	}
	big := bytes.Repeat([]byte{7}, 600) // longer than one lacing segment
	for _, pkt := range [][]byte{{1, 2, 3}, big, {9}} {
		if err := w.WritePacket(pkt, 960); err != nil {
			t.Fatal(err)
		}
	}
	pages := parsePages(t, buf.Bytes())
	if len(pages) != 5 {
		t.Fatalf("%d pages, want 2 headers + 3 audio", len(pages))
	}
	if pages[0].flags&2 == 0 || string(pages[0].packets[0][:8]) != "OpusHead" || pages[0].packets[0][9] != 2 {
		t.Fatalf("first page is not a BOS OpusHead for stereo: %+v", pages[0])
	}
	if string(pages[1].packets[0][:8]) != "OpusTags" {
		t.Fatal("second page is not OpusTags")
	}
	for i, p := range pages {
		if p.seq != uint32(i) || p.serial != pages[0].serial {
			t.Fatalf("page %d seq %d serial %d", i, p.seq, p.serial)
		}
	}
	if pages[2].granule != 960 || pages[4].granule != 2880 || !bytes.Equal(pages[3].packets[0], big) {
		t.Fatalf("granules %d %d, big packet intact %v", pages[2].granule, pages[4].granule, bytes.Equal(pages[3].packets[0], big))
	}
}

func TestEncodeLoopEmitsDecodableOpusEvery20ms(t *testing.T) {
	feed := audio.NewFeed(48000)
	go feedFor(feed)
	stop := make(chan struct{})
	var mu = make(chan []byte, 200)
	go EncodeLoop(stop, feed, 48000, func(p []byte) {
		select {
		case mu <- append([]byte(nil), p...):
		default:
		}
	})
	time.Sleep(1 * time.Second)
	close(stop)
	n := len(mu)
	if n < 40 || n > 60 {
		t.Fatalf("%d packets in 1 s, want about 50", n)
	}
	dec, _ := gopus.NewDecoder(gopus.DecoderConfig{SampleRate: 48000, Channels: 2})
	out := make([]float32, 5760*2)
	for i := 0; i < n; i++ {
		got, err := dec.Decode(<-mu, out)
		if err != nil || got != 960 {
			t.Fatalf("packet %d decoded to %d frames: %v", i, got, err)
		}
	}
}

func TestEncodeLoopKeepsGoingThroughSilence(t *testing.T) {
	feed := audio.NewFeed(48000) // nothing is ever written: paused, or between songs
	stop := make(chan struct{})
	count := make(chan struct{}, 200)
	go EncodeLoop(stop, feed, 48000, func([]byte) { count <- struct{}{} })
	time.Sleep(500 * time.Millisecond)
	close(stop)
	if n := len(count); n < 20 {
		t.Fatalf("%d packets in 0.5 s of silence; the stream must not go idle", n)
	}
}
