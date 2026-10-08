package library

import (
	"fmt"
	"strings"
)

// Quality is a short label for the track's format, the kind a player
// prints beside an album so two copies at different quality read apart:
// "FLAC 16/44.1", "FLAC 24/96", "MP3 320k", "AAC 256k". Lossless codecs
// show bit depth and sample rate; lossy ones show the bitrate. Empty when
// the codec is unknown.
func (t Track) Quality() string {
	codec := strings.ToUpper(t.Codec)
	switch codec {
	case "":
		return ""
	case "M4A", "MP4":
		codec = "AAC"
	}
	switch codec {
	case "FLAC", "ALAC", "WAV", "AIFF", "APE", "WV":
		switch {
		case t.SampleRate > 0 && t.BitDepth > 0:
			return fmt.Sprintf("%s %d/%s", codec, t.BitDepth, khz(t.SampleRate))
		case t.SampleRate > 0:
			return codec + " " + khz(t.SampleRate)
		}
		return codec
	}
	if t.Bitrate > 0 {
		return fmt.Sprintf("%s %dk", codec, t.Bitrate)
	}
	return codec
}

// khz prints a sample rate the way audio gear does: 44.1, 48, 96.
func khz(rate int) string {
	s := fmt.Sprintf("%.1f", float64(rate)/1000)
	return strings.TrimSuffix(s, ".0")
}
