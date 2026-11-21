# dotamp-bot, the house bot

`dotamp-bot` is a separate program for a Discord server that wants music when nobody is running dotamp: it lives on a home server, joins on `/join`, and plays your Plex and Navidrome library. It shares dotamp's player, request lanes and Opus stream; dotamp's own `b` broadcast is unchanged. It uses its own bot application, so both can be in a server at once.

| Command | Who |
|---|---|
| `/join`, `/play`, `/queue [count] [public]`, `/np` | anyone in the bot's voice channel (`/join`: anyone in a voice channel) |
| `/skip` | your own song alone; anyone else's song needs a second vote; alone with the bot, anything; the DJ role at once |
| `/remove` | your own requests; the DJ role, anything |
| `/pause`, `/clear`, `/autoplay on/off`, `/leave` | the DJ role (and `/leave` for anyone alone with the bot) |

When requests run out it plays radio: songs that sound like the last one (Plex sonic analysis, Navidrome similar songs), shaped by what the server has played through and skipped, nothing heard in the last four hours. It leaves five minutes after the last listener goes. Only servers on its allowlist can use it; it leaves any other.

`/listen` gives anyone in the server a private link (good for 12 hours) to hear the bot in a browser while it's in a voice channel, with the song and the queue on the page; it runs a few seconds behind Discord. Configure it with the `web` block in the config and put it behind your reverse proxy.

Setup is in `deploy/dotamp-bot/`: a config like `config.example.json` (secrets in their own files, never in the config), and `deploy.sh` to build and run it in Docker. `dotamp-bot ctl …` inside the container speaks the same control API as `dotamp ctl`.

It is built from the same repository as dotamp (`cmd/dotamp-bot`) and shares its audio engine, library clients, request lanes and control API. Releases of dotamp cover both programs; a release note says which one moved.
