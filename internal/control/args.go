package control

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const Usage = "usage: ctl VERB [args]  (status, queue, requests, search Q, album ID, playlist ID, play IDS, add [--as NAME] IDS, remove INDEX, clear, next, prev, pause, playlists, playlist-create NAME IDS, playlist-add ID IDS)"

// ParseArgs turns argv into a request; hyphens in verbs become underscores.
func ParseArgs(args []string) (Request, error) {
	if len(args) == 0 {
		return Request{}, errors.New(Usage)
	}
	req := Request{Op: strings.ReplaceAll(args[0], "-", "_")}
	rest := args[1:]
	if len(rest) >= 2 && rest[0] == "--as" {
		req.As, rest = rest[1], rest[2:]
	}
	switch req.Op {
	case "search":
		req.Q = strings.Join(rest, " ")
	case "album", "playlist":
		if len(rest) > 0 {
			req.ID = rest[0]
		}
	case "remove":
		if len(rest) == 0 {
			return req, errors.New("usage: ctl remove INDEX")
		}
		i, err := strconv.Atoi(rest[0])
		if err != nil {
			return req, fmt.Errorf("index %q is not a number", rest[0])
		}
		req.Index = &i
	case "playlist_create":
		if len(rest) == 0 {
			return req, errors.New("usage: ctl playlist-create NAME IDS...")
		}
		req.Name, req.IDs = rest[0], rest[1:]
	case "playlist_add":
		if len(rest) == 0 {
			return req, errors.New("usage: ctl playlist-add PLAYLIST_ID IDS...")
		}
		req.ID, req.IDs = rest[0], rest[1:]
	default:
		req.IDs = rest
	}
	if len(req.IDs) == 0 {
		req.IDs = nil
	}
	return req, nil
}

