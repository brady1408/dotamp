.PHONY: build test mac windows demo
build:
	CGO_ENABLED=0 go build -o dotamp ./cmd/dotamp
test:
	go test ./...
mac:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/dotamp-darwin-arm64 ./cmd/dotamp
windows:
	go run github.com/tc-hib/go-winres@v0.3.3 make --in cmd/dotamp/winres/winres.json --out cmd/dotamp/rsrc --product-version 0.0.0 --file-version 0.0.0 --arch amd64
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/dotamp-windows-amd64.exe ./cmd/dotamp
	rm -f cmd/dotamp/rsrc_windows_*.syso
# Re-records the README pictures headless: needs tmux, asciinema, agg, ffmpeg,
# the Iosevka font, and a configured library with Michael Jackson's Thriller.
demo: build
	PATH="$(CURDIR):$$PATH" docs/demo.sh
