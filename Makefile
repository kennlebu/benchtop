PREFIX ?= $(HOME)/.local
# The version comes from the newest git tag, for example v1.0.0 gives 1.0.0.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test install dist clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/benchtop ./cmd/benchtop

test:
	go vet ./...
	go test ./...

install: build
	install -d $(PREFIX)/bin
	install -m 755 bin/benchtop $(PREFIX)/bin/benchtop

# dist builds the release files for Apple Silicon and Intel Macs.
dist:
	rm -rf dist && mkdir -p dist/pkg
	cp LICENSE README.md dist/pkg/
	for arch in arm64 amd64; do \
		GOOS=darwin GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o dist/pkg/benchtop ./cmd/benchtop || exit 1; \
		tar -czf dist/benchtop-$(VERSION)-darwin-$$arch.tar.gz -C dist/pkg benchtop LICENSE README.md || exit 1; \
	done
	rm -rf dist/pkg
	cd dist && shasum -a 256 *.tar.gz > checksums.txt

clean:
	rm -rf bin dist
