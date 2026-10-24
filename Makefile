.PHONY: build test vet install update clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/weread-cli ./cmd/weread-cli

test:
	go test -race ./...

vet:
	go vet ./...
	python3 scripts/check-secrets.py

install:
	go install -trimpath -ldflags "-s -w -X main.version=$(VERSION)" ./cmd/weread-cli

update:
	go install github.com/songquanpeng/weread-cli/cmd/weread-cli@latest

clean:
	rm -rf bin dist
