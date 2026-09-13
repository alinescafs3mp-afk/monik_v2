SHELL := /bin/bash
export PATH := $(HOME)/.local/go/bin:$(PATH)
GO ?= go
VERSION ?= 0.1.0
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X github.com/alinescafs3mp-afk/monik_v2/internal/version.Version=$(VERSION) -X github.com/alinescafs3mp-afk/monik_v2/internal/version.Commit=$(COMMIT)
DIST := dist
UI := web

.PHONY: all ui bin test test-race vet dist linux windows agent-linux agent-windows clean

all: ui bin

ui:
	cd $(UI) && npm ci --no-audit --no-fund && node node_modules/esbuild/install.js && npm run build

bin: ui
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-amd64/monik-server ./cmd/monik-server
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-amd64/monik-agent ./cmd/monik-agent
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-amd64/monik-service-host ./cmd/monik-service-host
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-amd64/monik-release ./cmd/monik-release

linux: bin

windows:
	test -f internal/webui/dist/index.html || $(MAKE) ui
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/monik-server.exe ./cmd/monik-server
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/monik-agent.exe ./cmd/monik-agent
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/monik-service-host.exe ./cmd/monik-service-host
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/monik-release.exe ./cmd/monik-release

agent-linux:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-amd64/monik-agent ./cmd/monik-agent
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/linux-amd64/monik-service-host ./cmd/monik-service-host

agent-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/monik-agent.exe ./cmd/monik-agent
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/windows-amd64/monik-service-host.exe ./cmd/monik-service-host

test:
	CGO_ENABLED=0 $(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

dist: linux windows
	cd $(DIST)/linux-amd64 && sha256sum monik-server monik-agent monik-service-host monik-release > SHA256SUMS
	cd $(DIST)/windows-amd64 && sha256sum monik-server.exe monik-agent.exe monik-service-host.exe monik-release.exe > SHA256SUMS

clean:
	rm -rf $(DIST) $(UI)/dist
