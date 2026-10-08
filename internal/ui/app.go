package ui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

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
	saver   Saver
	saving  bool // the input field is the save prompt, not search
	help    bool
	tick    int

	notice      string
	noticeUntil time.Time
	layout      Layout

	lastClick    time.Time
	lastClickRow int

	// OnVisual is told the new mode's name when v changes it, to persist it.
	OnVisual func(name string)
}

// SetVisual selects the analyzer mode by its saved name; unknown names are ignored.
func (a *App) SetVisual(name string) {
	if m, ok := ParseMode(name); ok {
		a.an.SetMode(m)
	}
}

const doubleClick = 400 * time.Millisecond

type noticeEvent struct {
	tcell.EventTime
	msg string
}

func New(s tcell.Screen, ctrl *audio.Controller, eng *audio.Engine, lib library.Library, onVolume func(float64)) *App {
	a := &App{s: s, ctrl: ctrl, eng: eng, lib: lib, onVolume: onVolume,
		an: NewAnalyzer(eng), browser: NewBrowser(lib)}
	_ = a.browser.LoadRoot(context.Background())
	return a
}

// Saver saves a queue to the servers its tracks live on.
type Saver interface {
	SaveQueue(ctx context.Context, name string, tracks []library.Track) ([]library.Saved, error)
}

// SetSaver enables w, saving the queue as a playlist.
func (a *App) SetSaver(s Saver) { a.saver = s }

// SetSwitcher enables the Servers menu for a multi-server library.
func (a *App) SetSwitcher(sw Switcher) {
	a.browser.SetSwitcher(sw)
	_ = a.browser.LoadRoot(context.Background())
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
		a.an.Update(a.layout.Analyzer.W, a.layout.Analyzer.H)
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
		submit, cancel := a.search.Key(ev)
		switch {
		case a.saving && (submit || cancel):
			a.saving, a.search.Label = false, ""
			name := strings.TrimSpace(a.search.Text)
			a.search.Text = ""
			if ev.Key() == tcell.KeyEscape {
				return false
			}
			if name == "" {
				a.setNotice("No name, not saved")
				return false
			}
			a.saveQueue(ctx, name)
		case submit:
			if err := a.browser.Search(ctx, a.search.Text); err != nil {
				a.setNotice("Search failed: " + err.Error())
			}
			a.search.Text = ""
		}
		return false
	}
	if a.tab == tabLibrary && !a.help && ev.Key() == tcell.KeyRune {
		if r := ev.Rune(); r == '#' || unicode.IsUpper(r) {
			a.browser.JumpLetter(ctx, r)
			return false
		}
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
	case ActVisual:
		m := a.an.NextMode()
		if a.OnVisual != nil {
			a.OnVisual(m.String())
		}
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
	case ActSave:
		if a.saver == nil {
			a.setNotice("Saving is not available")
			return false
		}
		if len(a.ctrl.Tracks()) == 0 {
			a.setNotice("Queue is empty")
			return false
		}
		a.saving = true
		a.search.Label, a.search.Text, a.search.Open = "Save queue as:", "", true
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

func (a *App) saveQueue(ctx context.Context, name string) {
	saved, err := a.saver.SaveQueue(ctx, name, a.ctrl.Tracks())
	a.setNotice(saveNotice(name, saved, err, a.browser.serverLabel))
	if len(saved) > 0 {
		a.browser.ReloadPlaylists(ctx)
	}
}

// saveNotice words the result of a save: every server that took it, then
// the first failure if there was one.
func saveNotice(name string, saved []library.Saved, err error, serverName func(string) string) string {
	if len(saved) == 0 {
		return "Save failed: " + err.Error()
	}
	parts := make([]string, len(saved))
	for i, s := range saved {
		parts[i] = fmt.Sprintf("%s (%d)", serverName(s.Server), s.Tracks)
	}
	out := "Saved " + name + " to " + strings.Join(parts, " and ")
	if err != nil {
		out += "; " + err.Error()
	}
	return out
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
	case row.Menu == MenuArtists:
		a.run(a.browser.OpenArtists(ctx))
	case row.Menu == MenuRecent:
		a.run(a.browser.LoadRecent(ctx))
	case row.Menu == MenuServers:
		a.run(a.browser.OpenServers(ctx))
	case row.Menu == MenuPlaylists:
		a.run(a.browser.OpenPlaylists(ctx))
	case row.ServerID != "":
		if a.browser.SwitchServer(ctx, row.ServerID) {
			a.setNotice("Browsing " + a.browser.CurrentServerName())
		}
	case row.Track != nil:
		if appendOnly {
			a.ctrl.Enqueue(*row.Track)
			a.setNotice("Added " + row.Track.Title)
			return
		}
		if ts, ok := a.browser.AlbumContext(); ok { // play the album or playlist from this track
			start := -1
			for i := range ts {
				if &ts[i] == row.Track { // rows point into the view's own slice
					start = i
					break
				}
				if start < 0 && ts[i].ID == row.Track.ID { // a playlist may repeat a track
					start = i
				}
			}
			if start >= 0 {
				a.run(a.ctrl.PlayTracks(ctx, ts, start))
				return
			}
		}
		a.run(a.ctrl.PlayTracks(ctx, []library.Track{*row.Track}, 0))
	case row.Playlist != nil:
		if appendOnly {
			ts, err := a.browser.PlaylistTracks(ctx, *row.Playlist)
			if err != nil {
				a.setNotice("Playlist failed: " + err.Error())
				return
			}
			a.ctrl.Enqueue(ts...)
			a.setNotice(fmt.Sprintf("Added %s (%d)", row.Playlist.Name, len(ts)))
			return
		}
		a.run(a.browser.OpenPlaylist(ctx, *row.Playlist))
	case row.Album != nil:
		if appendOnly {
			ts, err := a.browser.AlbumTracks(ctx, *row.Album)
			if err != nil {
				a.setNotice("Album failed: " + err.Error())
				return
			}
			a.ctrl.Enqueue(ts...)
			a.setNotice("Added " + row.Album.Title)
			return
		}
		a.run(a.browser.OpenAlbum(ctx, *row.Album))
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
		i := list.RowAt(body, y)
		if i < 0 || list.Rows[i].Header {
			return
		}
		now := time.Now()
		second := i == a.lastClickRow && now.Sub(a.lastClick) < doubleClick
		list.Sel = i
		if second {
			a.lastClick = time.Time{} // a third click starts over
			a.activate(context.Background(), list.Selected(), false)
			return
		}
		a.lastClick, a.lastClickRow = now, i
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
		st.Artist, st.Title, st.Album, st.Codec = cur.Artist, cur.Title, cur.Album, cur.Codec
		st.SampleRate, st.BitDepth, st.Bitrate = cur.SampleRate, cur.BitDepth, cur.Bitrate
		// What the decoder sees beats what the server remembered.
		if f := a.eng.Format(); f.SampleRate > 0 {
			st.Codec, st.SampleRate, st.BitDepth = f.Codec, f.SampleRate, f.BitDepth
			if f.Bitrate > 0 {
				st.Bitrate = f.Bitrate
			}
		}
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
		if name := a.browser.CurrentServerName(); name != "" {
			title += "  ·  " + name
		}
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
	} else {
		a.browser.RefreshRoot(context.Background())
		a.browser.Prepare(context.Background(), body.H)
	}
	a.activeList().Draw(a.s, body, !a.search.Open)
	if a.help {
		DrawHelp(a.s, l.Pane)
	}
	a.s.Show()
}
