// Package room is one Discord server's player for the house bot: its own
// engine and queue, the voice stream, skip votes, autoplay radio and the
// idle timer.
package room

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/disgoorg/snowflake/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/history"
	"github.com/brady1408/dotamp/internal/library"
	"github.com/brady1408/dotamp/internal/radio"
	"github.com/brady1408/dotamp/internal/stream"
)

// rate is the engine's: Opus wants 48 kHz, so nothing is resampled twice.
const rate = 48000

// Conn is an open voice connection.
type Conn interface {
	UDP() stream.UDP
	Close(ctx context.Context)
}

type Deps struct {
	Open      func(ctx context.Context, channel snowflake.ID) (Conn, error) // opened, speaking set
	Listeners func(channel snowflake.ID) int                                // non-bot users in the channel, excluding this bot
	Post      func(channel snowflake.ID, msg string)                        // a message in the voice channel's chat
	Now       func() time.Time
}

// Library is the multi library: everything plus Similar.
type Library interface {
	library.Library
	library.Similar
}

type Config struct {
	Guild      snowflake.ID
	Library    Library
	History    *history.Writer
	HistoryDir string
	Autoplay   bool
	IdleLeave  time.Duration
	Logger     *slog.Logger
	Deps       Deps
}

type Room struct {
	cfg  Config
	eng  *audio.Engine
	ctrl *audio.Controller

	joinMu sync.Mutex // one Join, Leave or reopen at a time
	commit sync.Mutex // queue changes by Request, fill and Leave
	gen    uint64     // bumped by every Leave, guarded by commit; a fill from an older gen drops its pick

	mu        sync.Mutex
	channel   snowflake.ID
	name      string
	conn      Conn
	stop      chan struct{}
	autoplay  bool
	votes     map[string]bool
	voteFor   string // track ID the votes are for
	idleSince time.Time
	failures  int
	held      bool // cleared on purpose: no radio until someone requests
	recent    []history.Entry
	filling   atomic.Bool
}

func New(ctx context.Context, cfg Config) *Room {
	eng := audio.NewEngine(audio.NewSilentOutput(rate), rate)
	eng.SetVolume(1)
	r := &Room{cfg: cfg, eng: eng, autoplay: cfg.Autoplay}
	r.ctrl = audio.NewController(cfg.Library, eng, func(m string) { cfg.Logger.Info("room", "notice", m) })
	if es, err := history.Read(cfg.HistoryDir, cfg.Guild.String()); err == nil {
		r.recent = es
	}
	r.ctrl.SetTrackEnd(r.ended)
	r.ctrl.SetTrackStart(func(library.Track, string, string) {
		r.mu.Lock()
		r.votes, r.voteFor = nil, ""
		r.mu.Unlock()
		go r.fill(ctx)
	})
	go r.ctrl.Run(ctx)
	return r
}

// Join plays in channel, moving from wherever the bot was.
func (r *Room) Join(ctx context.Context, channel snowflake.ID, name string) error {
	r.joinMu.Lock()
	defer r.joinMu.Unlock()
	r.mu.Lock()
	same := r.channel == channel
	r.mu.Unlock()
	if same {
		return nil
	}
	r.leaveLocked(ctx)
	conn, err := r.cfg.Deps.Open(ctx, channel)
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	r.mu.Lock()
	r.channel, r.name, r.conn, r.stop, r.idleSince, r.failures = channel, name, conn, stop, time.Time{}, 0
	r.mu.Unlock()
	go stream.Drain(stop, conn.UDP())
	go r.run(stop, conn)
	go r.fill(context.WithoutCancel(ctx))
	return nil
}

// run streams the engine's feed; on a drop it tries once more, then leaves.
func (r *Room) run(stop chan struct{}, conn Conn) {
	opts := stream.Options{Rate: rate, Feed: r.eng.Feed(), Logger: r.cfg.Logger, Label: r.cfg.Guild.String()}
	if stream.Run(stop, conn.UDP(), opts) == nil {
		return
	}
	r.mu.Lock()
	ch, name := r.channel, r.name
	r.mu.Unlock()
	r.cfg.Logger.Warn("room stream dropped; reopening")
	time.Sleep(2 * time.Second)
	select {
	case <-stop:
		return
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn.Close(ctx)
	r.joinMu.Lock()
	select {
	case <-stop: // a /leave or /join got there first
		r.joinMu.Unlock()
		return
	default:
	}
	again, err := r.cfg.Deps.Open(ctx, ch)
	if err == nil {
		r.mu.Lock()
		r.conn = again
		r.mu.Unlock()
	}
	r.joinMu.Unlock()
	if err == nil {
		go stream.Drain(stop, again.UDP())
		if stream.Run(stop, again.UDP(), opts) == nil {
			return
		}
	}
	r.cfg.Deps.Post(ch, "Dropped from "+name+"; /join to bring me back")
	r.Leave(context.Background())
}

// Leave stops playing, clears the queue and lanes and returns the number
// of requests dropped. Autoplay returns to the config default.
func (r *Room) Leave(ctx context.Context) int {
	r.joinMu.Lock()
	defer r.joinMu.Unlock()
	return r.leaveLocked(ctx)
}

func (r *Room) leaveLocked(ctx context.Context) int {
	r.mu.Lock()
	if r.channel == 0 {
		r.mu.Unlock()
		return 0
	}
	stop, conn := r.stop, r.conn
	r.channel, r.name, r.conn, r.stop = 0, "", nil, nil
	r.autoplay, r.held = r.cfg.Autoplay, false
	r.mu.Unlock()
	close(stop)
	conn.Close(ctx)
	r.commit.Lock()
	r.gen++
	n := r.ctrl.Clear() // after channel = 0, so the cut track is not logged
	r.commit.Unlock()
	return n
}

// Where is the channel being played to, or 0.
func (r *Room) Where() (snowflake.ID, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.channel, r.name
}

// Request queues for a listener; radio makes way, whether its pick is fed
// ahead or still waiting in its lane.
func (r *Room) Request(ctx context.Context, by, name string, ts []library.Track) (int, error) {
	r.commit.Lock()
	defer r.commit.Unlock()
	if i := r.ctrl.Index(); i >= 0 {
		if _, who := r.ctrl.RequesterOf(i + 1); who == radio.RadioBy {
			_ = r.ctrl.Remove(context.WithoutCancel(ctx), i+1)
		}
	}
	r.ctrl.DropRequests(radio.RadioBy)
	r.mu.Lock()
	r.failures, r.held = 0, false
	r.mu.Unlock()
	return r.ctrl.Request(context.WithoutCancel(ctx), by, name, ts)
}

// Skip applies the rules: the requester alone, a DJ at once, a listener
// alone with the bot at once, anyone else's song with two votes.
func (r *Room) Skip(ctx context.Context, user, name string, dj bool) (string, error) {
	cur, ok := r.ctrl.Current()
	if !ok {
		return "Nothing is playing", nil
	}
	by, _ := r.ctrl.RequesterOf(r.ctrl.Index())
	ch, _ := r.Where()
	alone := r.cfg.Deps.Listeners(ch) <= 1
	if dj || alone || by == user {
		r.ensureNext(ctx)
		return "Skipped " + cur.Title, r.ctrl.Next(context.WithoutCancel(ctx))
	}
	r.mu.Lock()
	if r.voteFor != cur.ID || r.votes == nil {
		r.votes, r.voteFor = map[string]bool{}, cur.ID
	}
	r.votes[user] = true
	n := len(r.votes)
	r.mu.Unlock()
	if n < 2 {
		return name + " voted to skip; one more skips it", nil
	}
	r.ensureNext(ctx)
	return "Skipped " + cur.Title, r.ctrl.Next(context.WithoutCancel(ctx))
}

// ensureNext makes sure a skip has somewhere to go: a radio pick started
// by the song that just began may still be on its way, so wait for it,
// and pick one if nothing is next.
func (r *Room) ensureNext(ctx context.Context) {
	for i := 0; i < 200 && r.filling.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	r.fill(context.WithoutCancel(ctx))
}

// Clear empties the queue and every lane, and holds the radio until the
// next request: a clear is a wish for something fresh, not more radio.
// It returns the requests dropped.
func (r *Room) Clear() int {
	r.commit.Lock()
	defer r.commit.Unlock()
	r.mu.Lock()
	r.held = true
	r.mu.Unlock()
	return r.ctrl.Clear()
}

func (r *Room) SetAutoplay(on bool) {
	r.mu.Lock()
	r.autoplay, r.failures = on, 0
	r.mu.Unlock()
	if on {
		go r.fill(context.Background())
	}
}

func (r *Room) Autoplay() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.autoplay
}

// Tick leaves after the idle timeout and retries the radio; the bot calls
// it every few seconds.
func (r *Room) Tick(ctx context.Context) {
	r.mu.Lock()
	ch := r.channel
	r.mu.Unlock()
	if ch == 0 {
		return
	}
	now := r.cfg.Deps.Now()
	if r.cfg.Deps.Listeners(ch) == 0 {
		r.mu.Lock()
		if r.idleSince.IsZero() {
			r.idleSince = now
		}
		idle := now.Sub(r.idleSince)
		r.mu.Unlock()
		if idle >= r.cfg.IdleLeave {
			r.Leave(ctx)
			return
		}
	} else {
		r.mu.Lock()
		r.idleSince = time.Time{}
		r.mu.Unlock()
	}
	r.fill(ctx)
}

// waiting reports whether something will play next without the radio:
// a row after the cursor, or a listener's lane with songs.
func (r *Room) waiting() bool {
	ts, i := r.ctrl.Tracks(), r.ctrl.Index()
	if i >= 0 && i < len(ts)-1 {
		return true
	}
	for _, l := range r.ctrl.Pending() {
		if len(l.Tracks) > 0 && l.By != radio.RadioBy {
			return true
		}
	}
	return false
}

// fill feeds one radio pick when autoplay is on and nothing else waits.
// The pick is chosen without locks (it can be slow) and committed only if
// the room is still where it was and still has nothing next.
func (r *Room) fill(ctx context.Context) {
	if !r.filling.CompareAndSwap(false, true) {
		return
	}
	defer r.filling.Store(false)
	r.mu.Lock()
	ch, on, fails, held := r.channel, r.autoplay, r.failures, r.held
	recent := append([]history.Entry(nil), r.recent...)
	r.mu.Unlock()
	if ch == 0 || !on || held || fails >= 3 || r.waiting() {
		return
	}
	r.commit.Lock()
	gen := r.gen
	r.commit.Unlock()
	seed := uint64(time.Now().UnixNano())
	rng := rand.New(rand.NewPCG(seed, seed>>1))
	var last *library.Track
	if cur, ok := r.ctrl.Current(); ok {
		last = &cur
	} else {
		last = favourite(recent, r.cfg.Deps.Now(), rng)
	}
	t, err := radio.Pick(ctx, r.cfg.Library, radio.Input{Last: last, History: recent, Queue: r.ctrl.Tracks(), Now: r.cfg.Deps.Now(), Rand: rng})
	if err == nil {
		r.commit.Lock()
		r.mu.Lock()
		stale := r.gen != gen || r.channel != ch || !r.autoplay || r.held
		r.mu.Unlock()
		if stale || r.waiting() {
			r.commit.Unlock()
			return
		}
		_, err = r.ctrl.Request(context.WithoutCancel(ctx), radio.RadioBy, radio.RadioBy, []library.Track{t})
		r.commit.Unlock()
	}
	if err != nil {
		r.mu.Lock()
		r.failures++
		n := r.failures
		r.mu.Unlock()
		r.cfg.Logger.Warn("radio", "err", err)
		if n == 3 {
			r.cfg.Deps.Post(ch, "Can't reach the library; queue a song to try again")
		}
		return
	}
	r.mu.Lock()
	r.failures = 0
	r.mu.Unlock()
}

// favourite is where a cold radio starts: one of the room's ten best
// played-through songs, so a stray last line (a skipped radio pick, say)
// never sets the tone for the night. Nil when the room has no history.
func favourite(es []history.Entry, now time.Time, rng *rand.Rand) *library.Track {
	var top []library.Track
	for _, s := range history.Score(es, now) {
		if s.Score <= 0 || len(top) == 10 {
			break
		}
		top = append(top, s.Track)
	}
	if len(top) == 0 {
		return nil
	}
	t := top[rng.IntN(len(top))]
	return &t
}

// ended logs a finished track while joined.
func (r *Room) ended(t library.Track, played time.Duration, by, name string) {
	r.mu.Lock()
	ch, cname := r.channel, r.name
	r.mu.Unlock()
	if ch == 0 {
		return
	}
	e := history.Entry{At: r.cfg.Deps.Now().UTC(), Channel: cname, By: by, Name: name, Played: played.Seconds(), Track: t}
	r.mu.Lock()
	r.recent = append(r.recent, e)
	r.mu.Unlock()
	if r.cfg.History != nil {
		if err := r.cfg.History.Append(r.cfg.Guild.String(), e); err != nil {
			r.cfg.Logger.Warn("history", "err", err)
		}
	}
}

func (r *Room) Controller() *audio.Controller { return r.ctrl }
func (r *Room) Engine() *audio.Engine         { return r.eng }
