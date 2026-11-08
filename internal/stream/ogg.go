package stream

import (
	"crypto/rand"
	"encoding/binary"
	"io"
)

// OggWriter frames Opus packets as an Ogg Opus stream (RFC 7845): an
// OpusHead page, an OpusTags page, then one packet per page. Each writer is
// a complete stream of its own, so a listener joining late still gets a
// well-formed file.
type OggWriter struct {
	w       io.Writer
	serial  uint32
	seq     uint32
	granule int64
}

func NewOggWriter(w io.Writer) *OggWriter {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return &OggWriter{w: w, serial: binary.LittleEndian.Uint32(b[:])}
}

// preSkip is the decoder warm-up Opus streams declare, in 48 kHz samples.
const preSkip = 312

// WriteHeaders writes the identification and comment headers.
func (o *OggWriter) WriteHeaders() error {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1 // version
	head[9] = 2 // channels
	binary.LittleEndian.PutUint16(head[10:], preSkip)
	binary.LittleEndian.PutUint32(head[12:], opusRate)
	// output gain 0, channel mapping family 0
	if err := o.page(head, 0x02, 0); err != nil {
		return err
	}
	vendor := "dotamp"
	tags := make([]byte, 8+4+len(vendor)+4)
	copy(tags, "OpusTags")
	binary.LittleEndian.PutUint32(tags[8:], uint32(len(vendor)))
	copy(tags[12:], vendor)
	return o.page(tags, 0, 0)
}

// WritePacket writes one Opus packet covering samples 48 kHz samples.
func (o *OggWriter) WritePacket(pkt []byte, samples int) error {
	o.granule += int64(samples)
	return o.page(pkt, 0, o.granule)
}

func (o *OggWriter) page(pkt []byte, flags byte, granule int64) error {
	var lacing []byte
	n := len(pkt)
	for n >= 255 {
		lacing = append(lacing, 255)
		n -= 255
	}
	lacing = append(lacing, byte(n)) // a final value under 255 ends the packet
	b := make([]byte, 27+len(lacing)+len(pkt))
	copy(b, "OggS")
	b[5] = flags
	binary.LittleEndian.PutUint64(b[6:], uint64(granule))
	binary.LittleEndian.PutUint32(b[14:], o.serial)
	binary.LittleEndian.PutUint32(b[18:], o.seq)
	b[26] = byte(len(lacing))
	copy(b[27:], lacing)
	copy(b[27+len(lacing):], pkt)
	binary.LittleEndian.PutUint32(b[22:], oggCRC(b))
	o.seq++
	_, err := o.w.Write(b)
	return err
}

var oggTable = func() (t [256]uint32) {
	for i := range t {
		r := uint32(i) << 24
		for range 8 {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04c11db7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return
}()

// oggCRC is Ogg's CRC-32: polynomial 0x04c11db7, not reflected, zero start,
// computed with the checksum field zeroed.
func oggCRC(b []byte) uint32 {
	var crc uint32
	for _, c := range b {
		crc = crc<<8 ^ oggTable[byte(crc>>24)^c]
	}
	return crc
}
