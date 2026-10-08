#!/bin/sh
# Re-records the README pictures. Needs vhs, ttyd, Chrome, ffmpeg, the Iosevka
# font, and a configured library that has The Outfield's Play Deep. Run from
# the repo root with a dotamp binary on PATH (make demo does both).
set -e
reset_visual() {
	cfg="${XDG_CONFIG_HOME:-$HOME/.config}/dotamp/config.json"
	[ -f "$HOME/Library/Application Support/dotamp/config.json" ] && cfg="$HOME/Library/Application Support/dotamp/config.json"
	python3 - "$cfg" <<'PY'
import json, sys
p = sys.argv[1]; c = json.load(open(p)); c["visual"] = "bars"; json.dump(c, open(p, "w"), indent=2)
PY
}
still() { ffmpeg -v error -y -ss "$2" -i "$1" -frames:v 1 "$3"; }

reset_visual
vhs docs/demo.tape
still docs/demo.mp4 6 docs/search.png
still docs/demo.mp4 16 docs/bars.png
still docs/demo.mp4 39 docs/spectrogram.png
still docs/demo.mp4 46 docs/stereo.png

reset_visual
vhs docs/scope.tape
still docs/scope.mp4 20 docs/scope.png
ffmpeg -v error -y -ss 16 -t 6 -i docs/scope.mp4 \
	-vf "fps=20,scale=960:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=64:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=3" \
	-loop 0 docs/scope.gif
rm -f docs/demo.mp4 docs/scope.mp4
