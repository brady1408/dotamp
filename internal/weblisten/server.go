package weblisten

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"
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
	Full     bool       `json:"full,omitempty"` // every listener slot is taken
	Lanes    bool       `json:"lanes"`          // the link can manage its owner's songs
	Mine     []Song     `json:"mine"`
}

// Song is one of the link owner's waiting songs; Next means it is lined up
// in the queue for their next turn.
type Song struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Next   bool   `json:"next,omitempty"`
}

// Choice is a search hit: Label to show, Value to add.
type Choice struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Lanes lets a link act as its owner on their request lane. Song k is the
// k-th of Mine, checked against its id so a stale page cannot hit the wrong
// song. Errors are sentences for the page.
type Lanes interface {
	Mine(user string) []Song
	Search(ctx context.Context, q string) []Choice
	Add(ctx context.Context, user, value string) (string, error)
	Remove(ctx context.Context, user string, k int, id string) error
	Top(ctx context.Context, user string, k int, id string) error
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
	lanes  Lanes // nil: listen only
	secret []byte
	now    func() time.Time
	log    *slog.Logger
}

// NewServer serves the player page, the stream and the now-playing feed,
// and with lanes the owner's song controls.
func NewServer(hub *Hub, room Room, lanes Lanes, secret []byte, now func() time.Time, log *slog.Logger) http.Handler {
	s := &server{hub: hub, room: room, lanes: lanes, secret: secret, now: now, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /l/{tok}", s.page)
	mux.HandleFunc("GET /l/{tok}/stream.ogg", s.stream)
	mux.HandleFunc("GET /l/{tok}/now.json", s.nowJSON)
	if lanes != nil {
		mux.HandleFunc("GET /l/{tok}/search", s.search)
		mux.HandleFunc("POST /l/{tok}/add", s.add)
		mux.HandleFunc("POST /l/{tok}/remove", s.change(lanes.Remove))
		mux.HandleFunc("POST /l/{tok}/top", s.change(lanes.Top))
	}
	return mux
}

func (s *server) valid(w http.ResponseWriter, r *http.Request) bool {
	_, ok := s.user(w, r)
	return ok
}

// user is the link's owner; on a bad or expired link it answers 403.
func (s *server) user(w http.ResponseWriter, r *http.Request) (string, bool) {
	if user, ok := Verify(s.secret, r.PathValue("tok"), s.now()); ok {
		return user, true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(expiredHTML))
	return "", false
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
	user, ok := s.user(w, r)
	if !ok {
		return
	}
	n := s.room.Now()
	n.Full = s.hub.Full()
	n.Mine = []Song{}
	if s.lanes != nil {
		n.Lanes = true
		if m := s.lanes.Mine(user); m != nil {
			n.Mine = m
		}
	}
	writeJSON(w, http.StatusOK, n)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	if !s.valid(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []Choice{})
		return
	}
	cs := s.lanes.Search(r.Context(), q)
	if cs == nil {
		cs = []Choice{}
	}
	writeJSON(w, http.StatusOK, cs)
}

// body reads a small JSON request body into v.
func body(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(v)
}

func (s *server) add(w http.ResponseWriter, r *http.Request) {
	user, ok := s.user(w, r)
	if !ok {
		return
	}
	var in struct{ Value string }
	if body(r, &in) != nil || in.Value == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Pick a song from the search"})
		return
	}
	msg, err := s.lanes.Add(r.Context(), user, in.Value)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("web add", "user", user, "value", in.Value)
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

// change serves remove and top: song k of the owner's list, checked by id.
func (s *server) change(fn func(context.Context, string, int, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := s.user(w, r)
		if !ok {
			return
		}
		var in struct {
			K  int
			ID string
		}
		if body(r, &in) != nil || in.ID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Pick one of your songs"})
			return
		}
		if err := fn(r.Context(), user, in.K, in.ID); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "ok"})
	}
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
