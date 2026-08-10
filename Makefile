# rookery — build & local deploy helpers.
#
# `make deploy` is the one-command rebuild+restart: it stops any running
# server, rebuilds the binary, and starts it in the background with stdout/stderr
# captured to logs/server.log. The Makefile owns process tracking via
# logs/server.pid (the app has no pidfile/lock of its own).
#
# Defaults used by `serve`: listen 0.0.0.0:8080, data dir ~/.rookery
# (DB auto-migrates on open). Override the port with ROOKERY_PORT, e.g.
#   ROOKERY_PORT=8081 make deploy

BIN := bin/rookery
PKG := ./cmd/rookery
LOG := logs/server.log
PID := logs/server.pid
SHELL := /bin/bash

# Build identity stamped into the binary via -ldflags. Overridable from the
# environment so a release build can pass an exact tag.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/ilijad1/rookery/internal/buildinfo.Version=$(VERSION) \
           -X github.com/ilijad1/rookery/internal/buildinfo.Commit=$(COMMIT) \
           -X github.com/ilijad1/rookery/internal/buildinfo.Date=$(DATE)

# Prefer podman, fall back to docker. Overridable: CONTAINER_ENGINE=docker make …
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null || echo podman)

# Keep in sync with .github/workflows/pr.yml. The web package measures ~343s
# under -race, so 600s leaves no headroom on a slower machine.
GOTEST_TIMEOUT ?= 900s

.PHONY: ui build-go build stop start deploy restart logs status clean test \
        ci ci-fmt ci-vet ci-test ci-cross ci-ui ci-package docker-build docker-run

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
		pkill -f '[b]in/rookery serve' 2>/dev/null && echo "stopped (pkill)" || echo "not running"; \
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
	@pgrep -af '[b]in/rookery serve' || echo "not running"

## test: run the unit tests (no -race; see ci-test for the gate's version)
test:
	go test ./... -count=1 -timeout $(GOTEST_TIMEOUT)

## clean: remove the built binary
clean:
	rm -f $(BIN)

## ci: run the same checks pr.yml runs, locally — catch it before you push
ci: ci-fmt ci-vet ci-test ci-cross ci-ui
	@echo "all PR checks passed"

ci-fmt:
	@unformatted="$$(gofmt -l . | grep -v '^\.claude/' || true)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi
	@echo "gofmt: clean"

ci-vet:
	go vet ./...

ci-test:
	go test ./... -race -count=1 -timeout $(GOTEST_TIMEOUT)

## ci-cross: the GOOS=windows regression guard — the reason this target exists
ci-cross:
	@for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		printf "%-16s" "$$t"; \
		CGO_ENABLED=0 GOOS=$${t%/*} GOARCH=$${t#*/} go build -o /dev/null $(PKG) \
			&& echo OK || { echo FAIL; exit 1; }; \
	done

ci-ui:
	cd web/ui && npm ci && npx tsc -b && npm run lint && npx vitest run

## ci-package: build a goreleaser snapshot and smoke-test the deb, rpm and tar.gz
##
## Deliberately NOT part of `make ci`: a snapshot rebuilds the SPA and all six
## binaries, so it runs in minutes rather than seconds. Run it when touching
## packaging, the Dockerfile, or anything the binary reads at startup.
ci-package:
	goreleaser release --clean --snapshot --skip=sign,sbom
	CONTAINER_ENGINE=$(CONTAINER_ENGINE) scripts/smoke-package.sh dist

## docker-build: build the slim container image locally (podman or docker)
docker-build:
	$(CONTAINER_ENGINE) build -t rookery:local \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

## docker-run: run the locally built image with a persistent data volume
docker-run:
	$(CONTAINER_ENGINE) run --rm -it -p 8080:8080 \
		-v rookery-data:/data rookery:local
