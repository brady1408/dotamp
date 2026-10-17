package broadcast

import "github.com/disgoorg/snowflake/v2"

// VoiceStates is what Find needs from Discord's cache: every voice state
// the bot can see, as (guild, channel, user); channel 0 means not in voice.
type VoiceStates interface {
	Each(fn func(guild, channel, user snowflake.ID) bool)
}

// Location is a voice channel in a guild.
type Location struct{ Guild, Channel snowflake.ID }

// Find returns the voice channel the user is in; the first match wins.
func Find(vs VoiceStates, user snowflake.ID) (Location, bool) {
	var loc Location
	found := false
	vs.Each(func(guild, channel, u snowflake.ID) bool {
		if u == user && channel != 0 {
			loc, found = Location{guild, channel}, true
			return false
		}
		return true
	})
	return loc, found
}
