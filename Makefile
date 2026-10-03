.PHONY: build test mac
build:
	CGO_ENABLED=0 go build -o dotamp ./cmd/dotamp
test:
	go test ./...
mac:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/dotamp-darwin-arm64 ./cmd/dotamp
