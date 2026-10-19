BIN      := kwall
VERSION  := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)
PREFIX   ?= $(HOME)/.local

.PHONY: build test vet install clean build-mac build-linux

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

test:
	go test ./...

vet:
	go vet ./...

# Universal-ish: build both Apple Silicon and Intel binaries from any host.
build-mac:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)-darwin-arm64 .
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)-darwin-amd64 .

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BIN)-linux-arm64 .

install: build
	mkdir -p $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)

clean:
	rm -rf $(BIN) dist
