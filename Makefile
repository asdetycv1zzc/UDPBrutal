VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build check install
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o udpbrutal ./cmd/udpbrutal

check:
	go vet ./...
	go build ./...

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/udpbrutal
