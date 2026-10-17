// Package history keeps what dotamp played while broadcasting to Discord,
// one JSON-lines file per Discord server, and ranks it for a replay.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/brady1408/dotamp/internal/library"
)

// Top is how many tracks a night replay holds.
const Top = 50

// Entry is one track that stopped being current while live. The whole
// track record rides along because the library cannot fetch by ID.
type Entry struct {
	At      time.Time     `json:"t"`
	Channel string        `json:"channel"`
	By      string        `json:"by,omitempty"`   // Discord user ID; empty for the DJ's own queue
	Name    string        `json:"name,omitempty"` // display name when asked
	Played  float64       `json:"played"`         // seconds heard
	Track   library.Track `json:"track"`
}

type Writer struct{ dir string }

func NewWriter(dir string) *Writer { return &Writer{dir: dir} }

// Append adds one line to the guild's file, creating the directory and
// file on first use.
func (w *Writer) Append(guild string, e Entry) error {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(w.dir, guild+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// Read returns every entry in the guild's file. A torn last line from a
// crash is skipped.
func Read(dir, guild string) ([]Entry, error) {
	f, err := os.Open(filepath.Join(dir, guild+".jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var es []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		es = append(es, e)
	}
	return es, sc.Err()
}

// Night summarises one guild's file: how many distinct dates were played
// and when the last one was.
type Night struct {
	Guild  string
	Nights int
	Last   time.Time
}

// Nights lists the guilds with history, newest first. A missing directory
// is simply no history.
func Nights(dir string) ([]Night, error) {
	files, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ns []Night
	for _, f := range files {
		guild, ok := strings.CutSuffix(f.Name(), ".jsonl")
		if !ok {
			continue
		}
		es, err := Read(dir, guild)
		if err != nil || len(es) == 0 {
			continue
		}
		dates := map[string]bool{}
		var last time.Time
		for _, e := range es {
			dates[e.At.Local().Format("2006-01-02")] = true
			if e.At.After(last) {
				last = e.At
			}
		}
		ns = append(ns, Night{Guild: guild, Nights: len(dates), Last: last})
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].Last.After(ns[j].Last) })
	return ns, nil
}

type Scored struct {
	Track library.Track
	Score float64
	Last  time.Time
}

// Score ranks tracks by plays weighted 0.9 per week of age; a play that
// stopped before half the track counts −0.5, so skips count against.
func Score(es []Entry, now time.Time) []Scored {
	byID := map[string]*Scored{}
	for _, e := range es {
		weeks := now.Sub(e.At).Hours() / (24 * 7)
		w := math.Pow(0.9, math.Max(weeks, 0))
		v := 1.0
		if dur := e.Track.Duration.Seconds(); dur > 0 && e.Played/dur < 0.5 {
			v = -0.5
		}
		s, ok := byID[e.Track.ID]
		if !ok {
			s = &Scored{Track: e.Track}
			byID[e.Track.ID] = s
		}
		s.Score += w * v
		if e.At.After(s.Last) {
			s.Last = e.At
		}
	}
	out := make([]Scored, 0, len(byID))
	for _, s := range byID {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Last.After(out[j].Last)
	})
	return out
}
