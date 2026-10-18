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
