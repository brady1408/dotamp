# dotamp

A terminal music player drawn in dots. Plays your Plex music library with a braille spectrum analyzer.

Config lives at `~/.config/dotamp/config.json`:

    {"server": "http://192.168.23.23:32400", "token": "..."}

Run `dotamp`. Press `?` for keys.

## macOS

dotamp reaches Plex over your LAN, which macOS 15 and later gate per app. If you see `connect: no route to host` while `curl` to the same address works, give your terminal app Local Network access under System Settings → Privacy & Security → Local Network, then quit and relaunch the terminal. The permission is read when the app starts.

Build for an Apple Silicon Mac with `make mac`; the binary is `dist/dotamp-darwin-arm64` and needs no code signing beyond `codesign -s -`.
