package ui

import (
	"context"
	"log"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/brady1408/dotamp/internal/audio"
	"github.com/brady1408/dotamp/internal/library"
)

const (
	tabQueue = iota
	tabLibrary
)

type App struct {
	s        tcell.Screen
	ctrl     *audio.Controller
	eng      *audio.Engine
	lib      library.Library
	onVolume func(float64)

	an      *Analyzer
	browser *Browser
	queue   List
	tab     int
	search  SearchField
	help    bool
	tick    int

	notice      string
	noticeUntil time.Time
	layout      Layout
}

type noticeEvent struct {
	tcell.EventTime
	msg string
}

func New(s tcell.Screen, ctrl *audio.Controller, eng *audio.Engine, lib library.Library, onVolume func(float64)) *App {
	a := &App{s: s, ctrl: ctrl, eng: eng, lib: lib, onVolume: onVolume,
		an: NewAnalyzer(eng), browser: NewBrowser(lib)}
	if err := a.browser.LoadRecent(context.Background()); err != nil {
		log.Printf("recent albums: %v", err)
		a.setNotice("Library unavailable: " + err.Error())
	}
	return a
}

// Notify shows msg in the deck for a few seconds. Safe from any goroutine.
func (a *App) Notify(msg string) {
	_ = a.s.PostEvent(&noticeEvent{msg: msg})
}

func (a *App) setNotice(msg string) {
	a.notice, a.noticeUntil = msg, time.Now().Add(4*time.Second)
}

func (a *App) Run(ctx context.Context) error {
	if err := a.s.Init(); err != nil {
		return err
	}
	defer a.s.Fini()
	a.s.EnableMouse()
	a.s.HideCursor()
	go func() {
		t := time.NewTicker(time.Second / 30)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = a.s.PostEvent(tcell.NewEventInterrupt(nil))
			}
		}
	}()
	a.Draw()
	for {
		if ctx.Err() != nil {
			return nil
		}
		ev := a.s.PollEvent()
		if ev == nil {
			return nil
		}
		if a.Handle(ev) {
			return nil
		}
		a.Draw()
	}
}

func (a *App) Handle(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *noticeEvent:
		a.setNotice(ev.msg)
	case *tcell.EventInterrupt:
		a.tick++
	case *tcell.EventResize:
		a.s.Sync()
	case *tcell.EventMouse:
		a.mouse(ev)
	case *tcell.EventKey:
		return a.key(ev)
	}
	return false
}

func (a *App) key(ev *tcell.EventKey) bool {
	ctx := context.Background()
	if a.search.Open {
		submit, _ := a.search.Key(ev)
		if submit {
			if err := a.browser.Search(ctx, a.search.Text); err != nil {
				a.setNotice("Search failed: " + err.Error())
			}
			a.search.Text = ""
		}
		return false
	}
	act := ActionFor(ev)
	if a.help {
		if act != ActNone {
			a.help = false
		}
		return false
	}
	list := a.activeList()
	switch act {
	case ActQuit:
		return true
	case ActHelp:
		a.help = true
	case ActTogglePause:
		a.ctrl.TogglePause()
	case ActNext:
		a.run(a.ctrl.Next(ctx))
	case ActPrev:
		a.run(a.ctrl.Prev(ctx))
	case ActSeekBack:
		a.ctrl.SeekBy(-5 * time.Second)
	case ActSeekFwd:
		a.ctrl.SeekBy(5 * time.Second)
	case ActVolUp, ActVolDown:
		d := 0.05
		if act == ActVolDown {
			d = -d
		}
		a.eng.SetVolume(a.eng.Volume() + d)
		a.onVolume(a.eng.Volume())
	case ActShuffle:
		a.ctrl.ToggleShuffle()
	case ActRepeat:
		a.ctrl.ToggleRepeat()
	case ActTab:
		a.tab = (a.tab + 1) % 2
	case ActSearch:
		a.tab = tabLibrary
		a.search.Open = true
	case ActUp:
		list.Move(-1)
	case ActDown:
		list.Move(1)
	case ActPageUp:
		list.Move(-a.layout.Pane.H)
	case ActPageDown:
		list.Move(a.layout.Pane.H)
	case ActBack:
		if a.tab == tabLibrary {
			a.browser.Back(ctx)
		}
	case ActEnter:
		a.activate(ctx, list.Selected(), false)
	case ActAppend:
		a.activate(ctx, list.Selected(), true)
	}
	return false
}

func (a *App) run(err error) {
	if err != nil {
		a.setNotice(err.Error())
	}
}

// activate plays (or appends) the selected row.
func (a *App) activate(ctx context.Context, row *Row, appendOnly bool) {
	if row == nil {
		return
	}
	switch {
	case a.tab == tabQueue && row.Track != nil:
		a.run(a.ctrl.Jump(ctx, a.queue.Sel)) // queue rows map 1:1 to tracks
	case row.Track != nil:
		if appendOnly {
			a.ctrl.Enqueue(*row.Track)
			a.setNotice("Added " + row.Track.Title)
		} else {
			a.run(a.ctrl.PlayTracks(ctx, []library.Track{*row.Track}, 0))
		}
	case row.Album != nil:
		ts, err := a.browser.AlbumTracks(ctx, *row.Album)
		if err != nil {
			a.setNotice("Album failed: " + err.Error())
			return
		}
		if appendOnly {
			a.ctrl.Enqueue(ts...)
			a.setNotice("Added " + row.Album.Title)
		} else {
			a.run(a.ctrl.PlayTracks(ctx, ts, 0))
		}
	case row.Artist != nil:
		if err := a.browser.OpenArtist(ctx, *row.Artist); err != nil {
			a.setNotice("Artist failed: " + err.Error())
		}
	}
}

func (a *App) mouse(ev *tcell.EventMouse) {
	x, y := ev.Position()
	btn := ev.Buttons()
	l := a.layout
	switch {
	case btn&tcell.WheelUp != 0:
		a.activeList().Move(-3)
	case btn&tcell.WheelDown != 0:
		a.activeList().Move(3)
	case btn&tcell.Button1 != 0 && y == l.Deck.Y+2:
		if f := SeekBarHit(l.Deck, x); f >= 0 {
			a.eng.Seek(time.Duration(float64(a.eng.Length()) * f))
		}
	case btn&tcell.Button1 != 0 && y == l.Pane.Y:
		if x < l.Pane.X+10 {
			a.tab = tabQueue
		} else if x < l.Pane.X+22 {
			a.tab = tabLibrary
		}
	case btn&tcell.Button1 != 0 && y > l.Pane.Y:
		list := a.activeList()
		body := Rect{l.Pane.X, l.Pane.Y + 1, l.Pane.W, l.Pane.H - 1}
		if i := list.RowAt(body, y); i >= 0 && !list.Rows[i].Header {
			if list.Sel == i {
				a.activate(context.Background(), list.Selected(), false)
			}
			list.Sel = i
		}
	}
}

func (a *App) activeList() *List {
	if a.tab == tabLibrary {
		return a.browser.List()
	}
	return &a.queue
}

func (a *App) Draw() {
	w, h := a.s.Size()
	a.layout = Compute(w, h)
	if a.layout.TooSmall {
		DrawTooSmall(a.s, w, h)
		a.s.Show()
		return
	}
	l := a.layout
	cur, has := a.ctrl.Current()
	st := DeckState{
		Playing: a.eng.Playing(), Position: a.eng.Position(), Length: a.eng.Length(),
		Volume: a.eng.Volume(), Shuffle: a.ctrl.Shuffle(), Repeat: a.ctrl.Repeat(), Tick: a.tick,
	}
	if has {
		st.Artist, st.Title, st.Codec = cur.Artist, cur.Title, cur.Codec
		st.SampleRate, st.BitDepth, st.Bitrate = cur.SampleRate, cur.BitDepth, cur.Bitrate
	}
	if a.notice != "" && time.Now().Before(a.noticeUntil) {
		st.Notice = a.notice
	}
	DrawDeck(a.s, l.Deck, st)
	a.an.Draw(a.s, l.Analyzer)

	tabs := " ▸Queue   Library "
	if a.tab == tabLibrary {
		tabs = "  Queue  ▸Library "
	}
	title := ""
	if a.tab == tabLibrary {
		title = a.browser.Title()
	}
	PutStr(a.s, l.Pane.X, l.Pane.Y, Fit(tabs+"  "+title, l.Pane.W), tcell.StyleDefault.Bold(true))
	body := Rect{l.Pane.X, l.Pane.Y + 1, l.Pane.W, l.Pane.H - 1}
	if a.search.Open {
		a.search.Draw(a.s, Rect{body.X, body.Y, body.W, 1})
		body = Rect{body.X, body.Y + 1, body.W, body.H - 1}
	}
	if a.tab == tabQueue {
		a.queue.Rows = QueueRows(a.ctrl.Tracks(), a.ctrl.Index())
		if a.queue.Sel < 0 || a.queue.Sel >= len(a.queue.Rows) {
			a.queue.Sel = a.ctrl.Index()
		}
	}
	a.activeList().Draw(a.s, body, !a.search.Open)
	if a.help {
		DrawHelp(a.s, l.Pane)
	}
	a.s.Show()
}
