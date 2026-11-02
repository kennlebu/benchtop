PREFIX ?= $(HOME)/.local

.PHONY: build test install clean

build:
	go build -o bin/benchtop ./cmd/benchtop

test:
	go vet ./...
	go test ./...

install: build
	install -d $(PREFIX)/bin
	install -m 755 bin/benchtop $(PREFIX)/bin/benchtop

clean:
	rm -rf bin
