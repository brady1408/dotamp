.PHONY: build test mac windows
build:
	CGO_ENABLED=0 go build -o dotamp ./cmd/dotamp
test:
	go test ./...
mac:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o dist/dotamp-darwin-arm64 ./cmd/dotamp
windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/dotamp-windows-amd64.exe ./cmd/dotamp
