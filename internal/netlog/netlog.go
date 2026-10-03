// Package netlog is the --debug view of every HTTP request dotamp makes.
package netlog

import (
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Enabled turns request logging on. Off by default; main sets it for --debug.
var Enabled atomic.Bool

type transport struct{ base http.RoundTripper }

// New returns a RoundTripper that logs method, redacted URL, status and
// duration when Enabled, and is otherwise the default transport.
func New() http.RoundTripper { return transport{base: http.DefaultTransport} }

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !Enabled.Load() {
		return t.base.RoundTrip(req)
	}
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	switch {
	case err != nil:
		log.Printf("http %s %s -> error %v (%s)", req.Method, Redact(req.URL.String()), err, time.Since(start).Round(time.Millisecond))
	default:
		log.Printf("http %s %s -> %d (%s)", req.Method, Redact(req.URL.String()), resp.StatusCode, time.Since(start).Round(time.Millisecond))
	}
	return resp, err
}

// Redact drops the query string, which is where a token would be.
func Redact(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i]
	}
	return u
}
