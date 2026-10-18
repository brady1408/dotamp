package main

import (
	"strings"

	"github.com/disgoorg/snowflake/v2"
)

type reply struct {
	Text   string
	Public bool
}

// allowed is the permission table agreed for the house bot.
func allowed(cmd string, isDJ, inChannel, alone bool) bool {
	switch cmd {
	case "join":
		return true
	case "play", "queue", "np", "skip", "remove":
		return inChannel || isDJ
	case "pause", "clear", "autoplay":
		return isDJ
	case "leave":
		return isDJ || (inChannel && alone)
	}
	return false
}

// isDJ: the owner, or anyone holding the configured role by name or ID.
func isDJ(roleIDs []snowflake.ID, roleName func(snowflake.ID) string, user, owner, djRole string) bool {
	if user != "" && user == owner {
		return true
	}
	if djRole == "" {
		return false
	}
	for _, id := range roleIDs {
		if id.String() == djRole || strings.EqualFold(roleName(id), djRole) {
			return true
		}
	}
	return false
}

// joinReply decides a /join: move (true), or the refusal to give.
func joinReply(cur snowflake.ID, curName string, asker snowflake.ID, listenersThere int) (reply, bool) {
	switch {
	case cur == 0:
		return reply{}, true
	case cur == asker:
		return reply{Text: "Already playing in " + curName}, false
	case listenersThere > 0:
		return reply{Text: "Busy in " + curName + "; join there or wait"}, false
	}
	return reply{}, true
}

// gateIn is what a command is judged on before any work: all of it comes
// from Discord's caches, so the answer is instant.
type gateIn struct {
	cmd            string
	dj, alone      bool
	roomCh         snowflake.ID // where the bot plays, 0 when it does not
	roomName       string
	userCh         snowflake.ID // where the asker is, 0 when not in voice
	listenersThere int          // people with the bot, for /join
}

// gate answers a command's refusal (private, sent at once) or, when it
// may run, whether its reply must be deferred because the work can
// outlast Discord's three seconds.
func gate(in gateIn) (refusal string, deferred bool) {
	inChannel := in.roomCh != 0 && in.userCh == in.roomCh
	switch in.cmd {
	case "join":
		if in.userCh == 0 {
			return "Join a voice channel first", false
		}
		if r, move := joinReply(in.roomCh, in.roomName, in.userCh, in.listenersThere); !move {
			return r.Text, false
		}
		return "", true
	case "play", "queue", "np", "skip", "remove":
		if in.roomCh == 0 {
			return "I'm not playing; use /join", false
		}
		if !inChannel && !in.dj {
			return "Join " + in.roomName + " to request songs", false
		}
		return "", in.cmd == "play" || in.cmd == "skip"
	case "pause", "clear", "autoplay":
		if !in.dj {
			return "That's for the DJ role", false
		}
		return "", false
	case "leave":
		if in.roomCh == 0 {
			return "I'm not playing; use /join", false
		}
		if !allowed("leave", in.dj, inChannel, in.alone) {
			return "That's for the DJ role", false
		}
		return "", true
	}
	return "Unknown command", false
}
