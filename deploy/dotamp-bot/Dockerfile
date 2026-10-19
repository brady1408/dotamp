# The binary is built by deploy.sh. It needs glibc: the audio package links
# oto, whose loader makes even a cgo-free build dynamically linked.
FROM gcr.io/distroless/base-debian12:nonroot
COPY dotamp-bot /dotamp-bot
ENTRYPOINT ["/dotamp-bot", "-config", "/config/config.json"]
