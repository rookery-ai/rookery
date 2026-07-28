# simple-agents — build & local deploy helpers.
#
# `make deploy` is the one-command rebuild+restart: it stops any running
# server, rebuilds the binary, and starts it in the background with stdout/stderr
# captured to logs/server.log. The Makefile owns process tracking via
# logs/server.pid (the app has no pidfile/lock of its own).
#
# Defaults used by `serve`: listen 0.0.0.0:8080, data dir ~/.simple-agents-v2
# (DB auto-migrates on open). Override the port with SA_PORT, e.g.
#   SA_PORT=8081 make deploy

BIN := bin/simple-agents
PKG := ./cmd/simple-agents
LOG := logs/server.log
PID := logs/server.pid
SHELL := /bin/bash

# Build identity stamped into the binary via -ldflags. Overridable from the
# environment so a release build can pass an exact tag.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/ilijad1/simple-agents/internal/buildinfo.Version=$(VERSION) \
           -X github.com/ilijad1/simple-agents/internal/buildinfo.Commit=$(COMMIT) \
           -X github.com/ilijad1/simple-agents/internal/buildinfo.Date=$(DATE)

.PHONY: ui build-go build stop start deploy restart logs status clean test

## ui: build the SPA (web/ui/dist) — requires node; run before `build`
ui:
	cd web/ui && npm ci && npm run build

## build-go: compile the binary only (embeds whatever dist/ currently holds)
build-go:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

## build: full artifact — SPA + binary (spec §2)
build: ui build-go

## stop: stop the running server (pidfile first, pkill fallback)
stop:
	@if [ -f $(PID) ]; then \
		kill "$$(cat $(PID))" 2>/dev/null || true; \
		rm -f $(PID); \
		echo "stopped (pidfile)"; \
	else \
		pkill -f '[b]in/simple-agents serve' 2>/dev/null && echo "stopped (pkill)" || echo "not running"; \
	fi
	@sleep 0.5

## start: build (if stale) and launch the server in the background
start: build
	@mkdir -p logs
	@nohup ./$(BIN) serve > $(LOG) 2>&1 & echo $$! > $(PID)
	@echo "started pid $$(cat $(PID)); logs: $(LOG)"

## deploy: stop, rebuild, start (the rebuild + restart command)
deploy: stop build start
	@echo "deployed"

## restart: stop + start without rebuilding
restart: stop start
	@echo "restarted"

## logs: tail the server log
logs:
	@tail -f $(LOG)

## status: show the running server process
status:
	@pgrep -af '[b]in/simple-agents serve' || echo "not running"

## test: run the unit tests
test:
	go test ./... -count=1 -timeout 120s

## clean: remove the built binary
clean:
	rm -f $(BIN)