package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brady1408/dotamp/internal/netlog"
)

// HTTPFile is an io.ReadSeekCloser over an HTTP resource that honours Range.
// A Seek closes the current body; the next Read opens a new request at the
// new offset. Decoders that seek through the seek table or by byte offset
// therefore cost one request per seek and nothing while streaming.
type HTTPFile struct {
	ctx     context.Context
	url     string
	headers map[string]string
	client  *http.Client // no timeout: a track streams for as long as it is
	size    int64
	pos     int64

	mu          sync.Mutex // guards body and interrupted: Interrupt and Close run on another goroutine than Read
	body        io.ReadCloser
	interrupted bool // set by Interrupt; cleared by Seek. A Read in this state fails instead of reconnecting.
}

const reconnectAttempts = 3

func OpenHTTP(ctx context.Context, url string, headers map[string]string) (*HTTPFile, error) {
	f := &HTTPFile{ctx: ctx, url: url, headers: headers, client: &http.Client{Transport: netlog.New()}}
	if err := f.open(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *HTTPFile) open() error {
	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return err
	}
	for k, v := range f.headers {
		req.Header.Set(k, v)
	}
	if f.pos > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(f.pos, 10)+"-")
	}
	resp, err := f.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // url.Error echoes the full URL, query string included
			return fmt.Errorf("audio: GET %s: %w", redact(f.url), ue.Err)
		}
		return err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if f.pos > 0 { // server ignored Range; skip forward by reading
			if _, err := io.CopyN(io.Discard, resp.Body, f.pos); err != nil {
				resp.Body.Close()
				return err
			}
		}
		if resp.ContentLength > 0 && f.size == 0 {
			f.size = resp.ContentLength
		}
	case http.StatusPartialContent:
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i > 0 {
				if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
					f.size = n
				}
			}
		}
	default:
		resp.Body.Close()
		return fmt.Errorf("audio: GET %s: HTTP %d", redact(f.url), resp.StatusCode)
	}
	f.mu.Lock()
	f.body = resp.Body
	f.mu.Unlock()
	return nil
}

// Interrupt closes the in-flight body so a blocked Read returns. The next
// Read reopens at the current offset; a Seek reopens at the new one.
func (f *HTTPFile) Interrupt() {
	f.mu.Lock()
	body := f.body
	f.body = nil
	f.interrupted = true
	f.mu.Unlock()
	if body != nil {
		body.Close()
	}
}

var errInterrupted = errors.New("audio: read interrupted")

func (f *HTTPFile) currentBody() (io.ReadCloser, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body, f.interrupted
}

func (f *HTTPFile) dropBody() {
	f.mu.Lock()
	body := f.body
	f.body = nil
	f.mu.Unlock()
	if body != nil {
		body.Close()
	}
}

func redact(u string) string { return netlog.Redact(u) }

func (f *HTTPFile) Size() int64 { return f.size }

// Read streams from the current body. A connection that ends before the
// known size, or fails outright, is reopened at the current offset up to
// reconnectAttempts times, so a server that dropped an idle transfer (Plex
// does, after a long pause) is never seen by the decoder.
func (f *HTTPFile) Read(p []byte) (int, error) {
	for attempt := 0; ; attempt++ {
		body, interrupted := f.currentBody()
		if interrupted {
			return 0, errInterrupted
		}
		if body == nil {
			if err := f.open(); err != nil {
				if attempt >= reconnectAttempts {
					return 0, err
				}
				time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
				continue
			}
			body, interrupted = f.currentBody()
			if interrupted || body == nil {
				return 0, errInterrupted
			}
		}
		n, err := body.Read(p)
		f.pos += int64(n)
		if n > 0 {
			return n, nil // any error comes back on the next call
		}
		if err == nil {
			continue
		}
		if _, interrupted := f.currentBody(); interrupted {
			return 0, errInterrupted
		}
		if err == io.EOF && (f.size == 0 || f.pos >= f.size) {
			return 0, io.EOF // the real end
		}
		if attempt >= reconnectAttempts {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		f.dropBody()
		time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond)
	}
}

func (f *HTTPFile) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = f.pos + offset
	case io.SeekEnd:
		if f.size == 0 {
			return 0, errors.New("audio: size unknown; cannot seek from end")
		}
		abs = f.size + offset
	}
	if abs < 0 {
		abs = 0
	}
	f.mu.Lock()
	same := abs == f.pos && f.body != nil && !f.interrupted
	f.interrupted = false
	f.mu.Unlock()
	if same {
		return abs, nil
	}
	f.dropBody()
	f.pos = abs
	return abs, nil
}

func (f *HTTPFile) Close() error {
	f.Interrupt()
	return nil
}
