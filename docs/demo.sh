#!/bin/sh
# Re-records the README pictures. Drives `dotamp --silent` inside tmux,
# records with asciinema, renders with agg (which draws braille itself, so
# the dots tile at any line height), and converts single frames to PNG with
# ffmpeg. Needs tmux, asciinema, agg, ffmpeg, the Iosevka font, a dotamp on
# PATH, and a configured library that has Michael Jackson's Thriller.
# RENDER_ONLY=1 reuses the recordings in docs/*.cast.
set -e
cd "$(dirname "$0")/.."
AGG="agg --font-family Iosevka --font-size 16 --line-height 1.2 --fps-cap 20 --idle-time-limit 100 -q"
# bg, fg, then the 16 ANSI colours dotamp draws with
THEME="141414,d4d4d4,000000,f85149,3fb950,d29922,58a6ff,bc8cff,39c5cf,c9d1d9,6e7681,ff7b72,56d364,e3b341,79c0ff,d2a8ff,56d4dd,ffffff"
SIZE=140x26

reset_visual() {
	cfg="${XDG_CONFIG_HOME:-$HOME/.config}/dotamp/config.json"
	[ -f "$HOME/Library/Application Support/dotamp/config.json" ] && cfg="$HOME/Library/Application Support/dotamp/config.json"
	python3 - "$cfg" <<'PY'
import json, sys
p = sys.argv[1]; c = json.load(open(p)); c["visual"] = "bars"; json.dump(c, open(p, "w"), indent=2)
PY
}
keys() { tmux send-keys -t dotamp-demo "$@"; }
record() { # start recording docs/$1.cast in a detached tmux session
	rm -f "docs/$1.cast"
	tmux kill-session -t dotamp-demo 2>/dev/null || true
	tmux new-session -d -s dotamp-demo -x 140 -y 26 \
		"asciinema rec --window-size $SIZE -c 'dotamp --silent' docs/$1.cast"
}
# search, open the album, play Billie Jean (track 6), show the Queue: about 11 s
open_track() {
	sleep 3
	keys / ; sleep 0.3; keys 'thriller' Enter; sleep 3
	keys Enter; sleep 2
	for _ in 1 2 3 4 5; do keys Down; sleep 0.1; done
	keys Enter; sleep 1; keys Tab; sleep 0.5
}
seek() { for _ in $(seq "$1"); do keys Right; sleep 0.1; done; }   # 5 s per step
finish() { keys q; sleep 2; }
still() { # still CAST SECONDS NAME
	$AGG --theme "$THEME" --select "$2" --last-frame-duration 0.1 "docs/$1.cast" docs/_still.gif
	ffmpeg -v error -y -i docs/_still.gif -frames:v 1 "docs/$3.png"
	rm -f docs/_still.gif
}

if [ -z "$RENDER_ONLY" ]; then
	reset_visual
	record demo
	open_track
	seek 14                    # into the chorus
	sleep 8                    # bars
	keys v; sleep 8            # scope
	keys v; sleep 12           # spectrogram
	keys v; sleep 8            # stereo
	finish

	reset_visual
	record scope
	open_track
	seek 2                     # the bass-and-drums intro
	sleep 2
	keys v; sleep 14           # scope, held for the clip
	finish
fi

still demo 5 search
still demo 18 bars
still demo 38 spectrogram
still demo 41 stereo
still scope 22 scope
$AGG --theme "$THEME" --select 17..23 --last-frame-duration 0.05 docs/scope.cast docs/scope.gif
