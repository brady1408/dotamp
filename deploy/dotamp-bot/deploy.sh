#!/usr/bin/env bash
# Build dotamp-bot on this machine, ship it to m80q and restart the container.
# Secrets and config.json live only in ~/services/dotamp-bot/config on m80q.
set -euo pipefail
cd "$(dirname "$0")/../.."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty)" \
  -o deploy/dotamp-bot/dotamp-bot ./cmd/dotamp-bot
ssh m80q 'mkdir -p ~/services/dotamp-bot/config/history && chmod 700 ~/services/dotamp-bot/config'
scp -q deploy/dotamp-bot/Dockerfile deploy/dotamp-bot/docker-compose.yml deploy/dotamp-bot/dotamp-bot m80q:services/dotamp-bot/
rm -f deploy/dotamp-bot/dotamp-bot
ssh m80q 'cd ~/services/dotamp-bot && docker compose up -d --build && docker compose ps'
