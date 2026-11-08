package weblisten

import (
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/brady1408/dotamp/internal/stream"
)

// Now is what the page shows: the song, where it is, and what is next.
type Now struct {
	Live     bool       `json:"live"`
	Channel  string     `json:"channel"`
	Track    *Track     `json:"track,omitempty"`
	Position float64    `json:"position"`
	Upcoming []Upcoming `json:"upcoming"`
}

type Track struct {
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Duration float64 `json:"duration"`
}

type Upcoming struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	By     string `json:"by"`
}

//go:embed page.html
var pageHTML string

var page = template.Must(template.New("page").Parse(pageHTML))

var expiredHTML = `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>dotamp</title>
<style>body{font:16px/1.5 system-ui,sans-serif;background:#10161a;color:#e3ece9;display:grid;place-items:center;min-height:100vh;margin:0;padding:16px}</style>
<p>This link has expired. Type <b>/listen</b> in Kicarus for a new one.</p>`

type server struct {
	hub    *Hub
	room   Room
	secret []byte
	now    func() time.Time
	log    *slog.Logger
}

// NewServer serves the player page, the stream and the now-playing feed.
func NewServer(hub *Hub, room Room, secret []byte, now func() time.Time, log *slog.Logger) http.Handler {
	s := &server{hub: hub, room: room, secret: secret, now: now, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /l/{tok}", s.page)
	mux.HandleFunc("GET /l/{tok}/stream.ogg", s.stream)
	mux.HandleFunc("GET /l/{tok}/now.json", s.nowJSON)
	return mux
}

func (s *server) valid(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := Verify(s.secret, r.PathValue("tok"), s.now()); ok {
		return true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(expiredHTML))
	return false
}

func (s *server) page(w http.ResponseWriter, r *http.Request) {
	if !s.valid(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := page.Execute(w, map[string]string{"Token": r.PathValue("tok")}); err != nil {
		s.log.Warn("page", "err", err)
	}
}

func (s *server) nowJSON(w http.ResponseWriter, r *http.Request) {
	if !s.valid(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(s.room.Now())
}

func (s *server) stream(w http.ResponseWriter, r *http.Request) {
	if !s.valid(w, r) {
		return
	}
	sub, err := s.hub.Subscribe()
	switch {
	case errors.Is(err, ErrNotLive):
		http.Error(w, "dotamp isn't in a voice channel right now", http.StatusServiceUnavailable)
		return
	case errors.Is(err, ErrFull):
		http.Error(w, "the web player is full right now", http.StatusTooManyRequests)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer s.hub.Unsubscribe(sub)
	w.Header().Set("Content-Type", "audio/ogg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	ogg := stream.NewOggWriter(w)
	if ogg.WriteHeaders() != nil {
		return
	}
	flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case p, ok := <-sub.C:
			if !ok {
				return
			}
			if ogg.WritePacket(p, 960) != nil {
				return
			}
			flush()
		}
	}
}
