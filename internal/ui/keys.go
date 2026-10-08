package ui

import "github.com/gdamore/tcell/v2"

type Action int

const (
	ActNone Action = iota
	ActQuit
	ActTogglePause
	ActNext
	ActPrev
	ActSeekBack
	ActSeekFwd
	ActVolUp
	ActVolDown
	ActTab
	ActSearch
	ActEnter
	ActAppend
	ActBack
	ActShuffle
	ActRepeat
	ActHelp
	ActUp
	ActDown
	ActPageUp
	ActPageDown
	ActEscape
	ActVisual
	ActSave
	ActClear
	ActRemove
	ActToPlaylist
)

func ActionFor(ev *tcell.EventKey) Action {
	switch ev.Key() {
	case tcell.KeyEscape:
		return ActEscape
	case tcell.KeyEnter:
		return ActEnter
	case tcell.KeyTab:
		return ActTab
	case tcell.KeyLeft:
		return ActSeekBack
	case tcell.KeyRight:
		return ActSeekFwd
	case tcell.KeyUp:
		return ActUp
	case tcell.KeyDown:
		return ActDown
	case tcell.KeyPgUp:
		return ActPageUp
	case tcell.KeyPgDn:
		return ActPageDown
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return ActBack
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'q':
			return ActQuit
		case ' ':
			return ActTogglePause
		case 'n':
			return ActNext
		case 'p':
			return ActPrev
		case '+', '=':
			return ActVolUp
		case '-':
			return ActVolDown
		case '/':
			return ActSearch
		case 'a':
			return ActAppend
		case 's':
			return ActShuffle
		case 'r':
			return ActRepeat
		case '?':
			return ActHelp
		case 'v':
			return ActVisual
		case 'w':
			return ActSave
		case 'c':
			return ActClear
		case 'x':
			return ActRemove
		case 't':
			return ActToPlaylist
		case 'j':
			return ActDown
		case 'k':
			return ActUp
		}
	}
	return ActNone
}
