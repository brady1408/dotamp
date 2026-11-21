# The binary is built by deploy.sh with CGO_ENABLED=0. Nothing in the bot
# links oto (that lives in audio/otoout, player only), so it is static.
FROM gcr.io/distroless/static-debian12:nonroot
COPY dotamp-bot /dotamp-bot
ENTRYPOINT ["/dotamp-bot", "-config", "/config/config.json"]
