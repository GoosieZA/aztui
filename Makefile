MODULE  := github.com/GoosieZA/aztui
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo v0.1.0-dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X $(MODULE)/internal/version.Version=$(VERSION) -X $(MODULE)/internal/version.Commit=$(COMMIT)

.PHONY: build run install lint test release clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/aztui ./cmd/aztui

run: build
	./bin/aztui

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/aztui

lint:
	gofmt -l .
	go vet ./...

test:
	go test ./...

# Cut a release from the current tag: builds all platforms, publishes the
# GitHub release, and pushes the brew formula to GoosieZA/homebrew-tap.
release:
	GITHUB_TOKEN=$$(gh auth token) goreleaser release --clean

clean:
	rm -rf bin
