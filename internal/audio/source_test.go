package audio

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func drain(t *testing.T, s Source) (frames int, peak float32, crossings int) {
	t.Helper()
	buf := make([]float32, 4096)
	var prev float32
	for {
		n, err := s.Read(buf)
		for i := 0; i < n; i += 2 {
			v := buf[i]
			if v > peak {
				peak = v
			}
			if (v >= 0) != (prev >= 0) {
				crossings++
			}
			prev = v
		}
		frames += n / 2
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

func TestFLACDecodes(t *testing.T) {
	s, err := NewFLAC(openFixture(t, "sine440-44k.flac"))
	if err != nil {
		t.Fatal(err)
	}
	if s.SampleRate() != 44100 {
		t.Fatalf("rate = %d", s.SampleRate())
	}
	if l := s.Length(); l < 1990*time.Millisecond || l > 2010*time.Millisecond {
		t.Fatalf("length = %v", l)
	}
	frames, peak, crossings := drain(t, s)
	if frames < 88000 || frames > 88400 {
		t.Fatalf("frames = %d", frames)
	}
	if peak < 0.3 || peak > 1.0 {
		t.Fatalf("peak = %f (scaling wrong?)", peak)
	}
	if crossings < 1740 || crossings > 1780 { // 440 Hz × 2 s × 2
		t.Fatalf("crossings = %d", crossings)
	}
}

func TestMP3Decodes(t *testing.T) {
	s, err := NewMP3(openFixture(t, "sine440-48k.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	if s.SampleRate() != 48000 {
		t.Fatalf("rate = %d", s.SampleRate())
	}
	frames, peak, crossings := drain(t, s)
	if frames < 95000 || frames > 100000 { // encoder priming + padding allowed
		t.Fatalf("frames = %d", frames)
	}
	if peak < 0.3 || crossings < 1700 || crossings > 1900 { // encoder edge noise adds a few crossings
		t.Fatalf("peak=%f crossings=%d", peak, crossings)
	}
}

func TestSeekLeavesRemainder(t *testing.T) {
	for _, tc := range []struct {
		name, codec string
		rate        int
	}{
		{"sine440-44k.flac", "flac", 44100}, {"sine440-48k.mp3", "mp3", 48000},
	} {
		s, err := NewSource(tc.codec, openFixture(t, tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Seek(1500 * time.Millisecond); err != nil {
			t.Fatalf("%s seek: %v", tc.name, err)
		}
		frames, _, _ := drain(t, s)
		want := tc.rate / 2
		if frames < want-tc.rate/20 || frames > want+tc.rate/10 {
			t.Fatalf("%s: frames after seek = %d, want ~%d", tc.name, frames, want)
		}
	}
}

func TestHTTPFileRangeAndSeek(t *testing.T) {
	data, _ := os.ReadFile("testdata/sine440-44k.flac")
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		if r.Header.Get("X-Plex-Token") != "tok" {
			http.Error(w, "no token", 401)
			return
		}
		http.ServeContent(w, r, "a.flac", time.Time{}, bytesReader(data))
	}))
	defer srv.Close()
	hf, err := OpenHTTP(context.Background(), srv.URL+"/a.flac", map[string]string{"X-Plex-Token": "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if hf.Size() != int64(len(data)) {
		t.Fatalf("size = %d", hf.Size())
	}
	s, err := NewFLAC(hf)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seek(time.Second); err != nil {
		t.Fatal(err)
	}
	frames, _, _ := drain(t, s)
	if frames < 43000 || frames > 45500 {
		t.Fatalf("frames after seek over http = %d", frames)
	}
	if len(ranges) < 2 {
		t.Fatalf("expected a second Range request after seek, got %v", ranges)
	}
}
