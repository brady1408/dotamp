package control

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

var (
	readTimeout = 5 * time.Second // a client that connects and says nothing is dropped
	callTimeout = 30 * time.Second
	seenFor     = time.Hour // how long a listed track stays resolvable by id
)

// Server answers control requests for one running player.
type Server struct {
	ln    net.Listener
	path  string
	token string
	p     Player
	wg    sync.WaitGroup

	mu   sync.Mutex
	seen map[string]seen // typed track id -> the track, from the last listing
}

type seen struct {
	t  library.Track
	at time.Time
}

// Listen binds a loopback port, publishes it with a fresh token to path
// (mode 0600) and serves in the background.
func Listen(path string, p Player) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		return nil, err
	}
	s := &Server{ln: ln, path: path, token: hex.EncodeToString(tok), p: p, seen: map[string]seen{}}
	b, _ := json.Marshal(map[string]any{"port": ln.Addr().(*net.TCPAddr).Port, "token": s.token})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	go s.serve()
	return s, nil
}

func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops listening, waits for calls in flight and removes the file.
func (s *Server) Close() error {
	err := s.ln.Close()
	s.wg.Wait()
	os.Remove(s.path)
	return err
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			s.handle(conn)
		}()
	}
}

func (s *Server) handle(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(readTimeout))
	var req Request
	if err := json.NewDecoder(io.LimitReader(conn, 1<<20)).Decode(&req); err != nil {
		return // silent or malformed: dropped
	}
	conn.SetWriteDeadline(time.Now().Add(readTimeout))
	enc := json.NewEncoder(conn)
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.token)) != 1 {
		enc.Encode(Reply{Error: "bad token"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	res, err := s.dispatch(ctx, req)
	conn.SetWriteDeadline(time.Now().Add(readTimeout))
	if err != nil {
		enc.Encode(Reply{Error: err.Error()})
		return
	}
	enc.Encode(Reply{OK: true, Result: res})
}

func (s *Server) dispatch(ctx context.Context, req Request) (any, error) {
	switch req.Op {
	case "status":
		st := s.p.Status()
		return st, nil
	case "queue":
		ts, names := s.p.Queue(), s.p.QueueNames()
		s.remember(ts...)
		rows := make([]Row, len(ts))
		for i, t := range ts {
			rows[i] = Row{Index: i, TrackInfo: TrackInfoOf(t)}
			if i < len(names) {
				rows[i].By = names[i]
			}
		}
		return rows, nil
	case "requests":
		var out []LaneInfo
		for _, l := range s.p.Lanes() {
			if len(l.Tracks) > 0 {
				out = append(out, LaneInfo{Name: l.Name, Waiting: len(l.Tracks), Next: l.Tracks[0].Title})
			}
		}
		if out == nil {
			out = []LaneInfo{}
		}
		return out, nil
	case "search":
		res, pls, err := s.p.Search(ctx, req.Q)
		if err != nil {
			return nil, err
		}
		s.remember(res.Tracks...)
		info := SearchInfo{Tracks: []TrackInfo{}, Albums: []AlbumInfo{}, Playlists: []PlaylistInfo{}}
		for _, t := range res.Tracks {
			info.Tracks = append(info.Tracks, TrackInfoOf(t))
		}
		for _, a := range res.Albums {
			info.Albums = append(info.Albums, albumInfo(a))
		}
		for _, p := range pls {
			info.Playlists = append(info.Playlists, playlistInfo(p))
		}
		return info, nil
	case "album", "playlist":
		ts, err := s.resolve(ctx, []string{requireKind(req.ID, req.Op[:1])})
		if err != nil {
			return nil, err
		}
		return trackInfos(ts), nil
	case "play":
		ts, err := s.resolve(ctx, req.IDs)
		if err != nil {
			return nil, err
		}
		if len(ts) == 0 {
			return nil, errors.New("nothing to play")
		}
		if err := s.p.Play(ctx, ts); err != nil {
			return nil, err
		}
		s.p.Notify("Playing " + ts[0].Title)
		return map[string]any{"queued": len(ts)}, nil
	case "add":
		ts, err := s.resolve(ctx, req.IDs)
		if err != nil {
			return nil, err
		}
		if len(ts) == 0 {
			return nil, errors.New("nothing to add")
		}
		pending := 0
		if req.As != "" {
			n, err := s.p.Request(ctx, req.As, ts)
			if errors.Is(err, audio.ErrTooManyPending) {
				return nil, fmt.Errorf("You have %d songs waiting; wait for some to play", n)
			}
			if err != nil {
				return nil, err
			}
			pending = n
			s.p.Notify(fmt.Sprintf("%s added %s (%d)", req.As, label(ts), len(ts)))
		} else {
			s.p.Add(ts)
			s.p.Notify(fmt.Sprintf("Added %s (%d)", label(ts), len(ts)))
		}
		return map[string]any{"queued": len(ts), "pending": pending}, nil
	case "remove":
		if req.Index == nil {
			return nil, errors.New("missing index")
		}
		if err := s.p.Remove(ctx, *req.Index); err != nil {
			return nil, err
		}
		return s.p.Status(), nil
	case "clear":
		n := s.p.Clear()
		if n > 0 {
			s.p.Notify(fmt.Sprintf("Queue cleared (and %d requests)", n))
		} else {
			s.p.Notify("Queue cleared")
		}
		return map[string]any{"dropped_requests": n}, nil
	case "next":
		if err := s.p.Next(ctx); err != nil {
			return nil, err
		}
		return s.p.Status(), nil
	case "prev":
		if err := s.p.Prev(ctx); err != nil {
			return nil, err
		}
		return s.p.Status(), nil
	case "pause":
		s.p.TogglePause()
		return s.p.Status(), nil
	case "playlists":
		pls, err := s.p.Playlists(ctx)
		if err != nil {
			return nil, err
		}
		out := []PlaylistInfo{}
		for _, p := range pls {
			out = append(out, playlistInfo(p))
		}
		return out, nil
	case "playlist_create":
		if req.Name == "" {
			return nil, errors.New("missing name")
		}
		ts, err := s.resolve(ctx, req.IDs)
		if err != nil {
			return nil, err
		}
		saved, err := s.p.CreatePlaylist(ctx, req.Name, ts)
		if err != nil && len(saved) == 0 {
			return nil, err
		}
		out := []PlaylistInfo{}
		for _, sv := range saved {
			out = append(out, playlistInfo(sv.Playlist))
		}
		s.p.Notify(fmt.Sprintf("Saved %s (%d)", req.Name, len(ts)))
		return map[string]any{"playlists": out}, nil
	case "playlist_add":
		kind, rest, ok := kindOf(req.ID)
		if !ok || kind != "p" {
			return nil, fmt.Errorf("bad playlist id %q: use p:", req.ID)
		}
		ts, err := s.resolve(ctx, req.IDs)
		if err != nil {
			return nil, err
		}
		if err := s.p.AddToPlaylist(ctx, rest, ts); err != nil {
			return nil, err
		}
		return map[string]any{"added": len(ts)}, nil
	}
	return nil, fmt.Errorf("unknown op %q", req.Op)
}

// requireKind lets album and playlist take a bare library id as well as
// a typed one.
func requireKind(id, kind string) string {
	if _, _, ok := kindOf(id); ok {
		return id
	}
	return kind + ":" + id
}

// resolve expands typed ids into tracks: albums and playlists through the
// player, tracks from what has been listed.
func (s *Server) resolve(ctx context.Context, ids []string) ([]library.Track, error) {
	var out []library.Track
	for _, id := range ids {
		kind, rest, ok := kindOf(id)
		switch {
		case ok && kind == "a":
			ts, err := s.p.AlbumTracks(ctx, rest)
			if err != nil {
				return nil, err
			}
			s.remember(ts...)
			out = append(out, ts...)
		case ok && kind == "p":
			ts, err := s.p.PlaylistTracks(ctx, rest)
			if err != nil {
				return nil, err
			}
			s.remember(ts...)
			out = append(out, ts...)
		case ok && kind == "t":
			t, found := s.recall(id)
			if !found {
				return nil, fmt.Errorf("unknown track %s; search first", id)
			}
			out = append(out, t)
		default:
			return nil, fmt.Errorf("bad id %q: use t:, a: or p:", id)
		}
	}
	return out, nil
}

func (s *Server) remember(ts ...library.Track) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.seen {
		if now.Sub(v.at) > seenFor {
			delete(s.seen, k)
		}
	}
	for _, t := range ts {
		s.seen[TrackID(t)] = seen{t: t, at: now}
	}
}

func (s *Server) recall(id string) (library.Track, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.seen[id]
	return v.t, ok && time.Since(v.at) <= seenFor
}

func trackInfos(ts []library.Track) []TrackInfo {
	out := make([]TrackInfo, 0, len(ts))
	for _, t := range ts {
		out = append(out, TrackInfoOf(t))
	}
	return out
}

// label names a batch for a notice: the album when every track shares
// one, else the first title.
func label(ts []library.Track) string {
	if len(ts) == 0 {
		return ""
	}
	album := ts[0].Album
	for _, t := range ts {
		if t.Album != album {
			return ts[0].Title
		}
	}
	if album == "" || len(ts) == 1 {
		return ts[0].Title
	}
	return album
}
