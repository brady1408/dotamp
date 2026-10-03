package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
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
	body    io.ReadCloser
}

func OpenHTTP(ctx context.Context, url string, headers map[string]string) (*HTTPFile, error) {
	f := &HTTPFile{ctx: ctx, url: url, headers: headers, client: &http.Client{}}
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
	f.body = resp.Body
	return nil
}

func redact(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i]
	}
	return u
}

func (f *HTTPFile) Size() int64 { return f.size }

func (f *HTTPFile) Read(p []byte) (int, error) {
	if f.body == nil {
		if err := f.open(); err != nil {
			return 0, err
		}
	}
	n, err := f.body.Read(p)
	f.pos += int64(n)
	return n, err
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
	if abs == f.pos {
		return abs, nil
	}
	if f.body != nil {
		f.body.Close()
		f.body = nil
	}
	f.pos = abs
	return abs, nil
}

func (f *HTTPFile) Close() error {
	if f.body != nil {
		err := f.body.Close()
		f.body = nil
		return err
	}
	return nil
}
