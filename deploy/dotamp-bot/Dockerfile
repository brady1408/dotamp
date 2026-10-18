# The binary is built by deploy.sh.
FROM gcr.io/distroless/static-debian12:nonroot
COPY dotamp-bot /dotamp-bot
ENTRYPOINT ["/dotamp-bot", "-config", "/config/config.json"]
