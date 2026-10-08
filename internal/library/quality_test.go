package library

import "testing"

func TestTrackQuality(t *testing.T) {
	cases := []struct {
		t    Track
		want string
	}{
		{Track{Codec: "flac", SampleRate: 44100, BitDepth: 16}, "FLAC 16/44.1"},
		{Track{Codec: "flac", SampleRate: 96000, BitDepth: 24}, "FLAC 24/96"},
		{Track{Codec: "flac", SampleRate: 48000}, "FLAC 48"},
		{Track{Codec: "flac"}, "FLAC"},
		{Track{Codec: "mp3", Bitrate: 320}, "MP3 320k"},
		{Track{Codec: "mp3"}, "MP3"},
		{Track{Codec: "aac", Bitrate: 256}, "AAC 256k"},
		{Track{Codec: "m4a", Bitrate: 256}, "AAC 256k"},
		{Track{Codec: "alac", SampleRate: 44100, BitDepth: 16}, "ALAC 16/44.1"},
		{Track{Codec: "opus", Bitrate: 128}, "OPUS 128k"},
		{Track{}, ""},
	}
	for _, c := range cases {
		if got := c.t.Quality(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.t, got, c.want)
		}
	}
}
