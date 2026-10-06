# dotamp

A terminal music player drawn in dots. Plays your Plex music library with a braille spectrum analyzer, Winamp-shaped, in any modern terminal.

## Install

Homebrew builds it locally, so there is no unsigned-binary warning:

    brew install brady1408/tap/dotamp

With Go:

    go install github.com/brady1408/dotamp/cmd/dotamp@latest

Or take a prebuilt binary from the [releases](https://github.com/brady1408/dotamp/releases). Those are unsigned for now: macOS wants a right-click → Open on first launch, Windows SmartScreen wants "More info → Run anyway".

## First run

    dotamp

The first run asks two questions: sign in with Plex, and add a Navidrome or other Subsonic server. Say yes to either or both. Plex sign-in is a link to approve in your browser; from then on dotamp finds your servers through your account, the LAN address at home, the public address when you're away, Plex's relay as a last resort. Navidrome is a URL, a username and a password, asked once. Then the player opens.

Later, the same steps are available as commands: `dotamp login`, `dotamp navidrome URL USER`, and `dotamp servers` to list every server and how each one is reached.

With both kinds of library configured, search covers all of them, the Servers entry in the Library menu switches the one you browse, and the queue can mix them.

## Use

Run `dotamp`. The deck on top shows the track, format, clock and seek bar. The analyzer takes the middle. The bottom pane is Queue or Library.

| Key | Action |
|---|---|
| space | play / pause |
| `n` / `p` | next / previous track |
| ← / → | seek 5 s |
| `+` / `-` | volume |
| Tab | switch Queue / Library |
| `/` | search every server at once |
| Enter | open an artist or album; on a track, play its album from there |
| `a` | add the album or track to the queue |
| Backspace | back |
| `A`–`Z`, `#` | jump to a letter in the artist index |
| `s` / `r` | shuffle / repeat |
| `v` | spectrum bars / oscilloscope / spectrogram / stereo field |
| `?` | help |
| `q` | quit |

The mouse works too: click the seek bar, double-click a row, scroll the lists.

The Library opens on a menu: **Artists** is every artist on the current server with letter jumps, **Recently added** the latest albums, and **Servers** appears when your account can reach more than one server with music. Search always asks all of them and groups the results by server, marking copies that live on a remote or relayed server.

Tracks play as the original file, FLAC included, with gapless transitions between tracks. A file that would not fit through Plex's relay is transcoded to MP3 320 on the way.

## Config

`~/.config/dotamp/config.json` is written by `dotamp login`. Optional keys:

| Key | Meaning |
|---|---|
| `server_name` | which of the account's servers to browse by default; the first owned one otherwise |
| `remote_bitrate` | kbps to transcode to on any non-local connection, e.g. `192` to save mobile data; original when unset |
| `server`, `token` | a fixed server and token, which skip account discovery entirely |
| `navidrome` | `{"url", "user", "password"}` for a Subsonic server; written by `dotamp navidrome` |

The log lives at `~/Library/Caches/dotamp/dotamp.log` on macOS and `~/.cache/dotamp/dotamp.log` elsewhere. `dotamp --debug` adds every HTTP request to it.

## Terminals

Any terminal with truecolor, mouse reporting and a font that has braille: iTerm2, Ghostty, Kitty, WezTerm, Windows Terminal with Cascadia Mono. The analyzer's colours come from your terminal's palette.

On macOS 15 and later the terminal app needs Local Network access the first time (System Settings → Privacy & Security → Local Network). If you see `connect: no route to host` while `curl` to the same address works, grant it and relaunch the terminal; the permission is read at launch.

## Build

    make build      # this machine
    make mac        # darwin/arm64
    make windows    # windows/amd64
    make test

Pure Go, no cgo, on every platform.

## License

MIT
