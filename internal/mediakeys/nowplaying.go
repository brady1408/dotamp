package mediakeys

// macOS "Now Playing" values, kept platform-independent so the mapping is
// tested everywhere. The keys are the string values behind Apple's
// MPMediaItemProperty* and MPNowPlayingInfoProperty* constants.
const (
	nowPlayingPlaying = 1 // MPNowPlayingPlaybackStatePlaying
	nowPlayingPaused  = 2
	nowPlayingStopped = 3
)

func playbackState(s State) int {
	switch {
	case !s.HasTrack:
		return nowPlayingStopped
	case s.Playing:
		return nowPlayingPlaying
	}
	return nowPlayingPaused
}

// nowPlayingFields is the dictionary handed to MPNowPlayingInfoCenter:
// strings and float64s only.
func nowPlayingFields(s State) map[string]any {
	if !s.HasTrack {
		return map[string]any{}
	}
	f := map[string]any{
		"title":            s.Title,
		"artist":           s.Artist,
		"albumTitle":       s.Album,
		"playbackDuration": s.Length.Seconds(),
		"MPNowPlayingInfoPropertyElapsedPlaybackTime": 0.0,
		"MPNowPlayingInfoPropertyPlaybackRate":        0.0,
	}
	if s.Playing {
		f["MPNowPlayingInfoPropertyPlaybackRate"] = 1.0
	}
	return f
}
