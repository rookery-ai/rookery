# syntax=docker/dockerfile:1

# ── SPA ──────────────────────────────────────────────────────────────────────
FROM node:24-alpine AS ui
WORKDIR /src/web/ui
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci
COPY web/ui/ ./
# The UI font is NOT under web/ui/: it lives in internal/fonts/ as a single copy
# shared with the Go export path (go:embed cannot reach outside its own package
# dir, so the font has to live there, and a second copy would drift silently).
# index.css reaches it through the "@fonts" Vite alias → ../../internal/fonts,
# which is outside this stage's build context unless it is copied in. Without
# this line `npm run build` fails with
#   [postcss] ENOENT ... /src/internal/fonts/InterVariable.woff2
# — and `make ci` does not catch it, because the container is built only by the
# separate "Container smoke test" CI job.
COPY internal/fonts/ /src/internal/fonts/
RUN npm run build

# ── Go ───────────────────────────────────────────────────────────────────────
# Pinned to BUILDPLATFORM and cross-compiled to TARGETARCH. Because the project
# is CGo-free this needs no QEMU: a multi-arch build stays as fast as a native
# one instead of emulating a foreign architecture.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
ARG VERSION=0.0.0-dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
COPY --from=ui /src/web/ui/dist ./web/ui/dist

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/ilijad1/rookery/internal/buildinfo.Version=${VERSION} \
        -X github.com/ilijad1/rookery/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/ilijad1/rookery/internal/buildinfo.Date=${BUILD_DATE}" \
      -o /out/rookery ./cmd/rookery

# ── Runtime ──────────────────────────────────────────────────────────────────
# Debian rather than Alpine: tesseract's language data packaging is saner here,
# and glibc stays available for whatever a future :full target installs.
FROM debian:trixie-slim AS runtime

# python3 is not optional: without it the agent-tool AST guardrail self-skips,
# which /healthz reports as a warning. The rest prevent silent degradation of
# KB search, PDF extraction and OCR.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      python3 \
      ripgrep \
      poppler-utils \
      tesseract-ocr \
      tesseract-ocr-eng \
    && rm -rf /var/lib/apt/lists/*

# A fixed uid/gid keeps volume ownership predictable across rootless Podman and
# Docker, which map users differently.
RUN groupadd --gid 10001 app \
    && useradd --uid 10001 --gid app --create-home --home-dir /home/app app

COPY --from=build /out/rookery /usr/bin/rookery
# Beside the binary on purpose: resolveDir() looks EXE-relative first and only
# then falls back to a CWD-relative path, so this is found no matter what
# working directory the container is started with.
COPY migrations /usr/bin/migrations

# HOME must sit inside the volume: the per-workspace claude-homes trees live
# under the data dir and must be writable and persistent.
# ROOKERY_PUBLIC_URL is REQUIRED behind any reverse proxy that rewrites Host: the app
# reads the Host header directly and does not consult X-Forwarded-Host, so
# without it every OAuth redirect URI points at the wrong address. Left unset,
# the instance URL is inferred from the browser's request (and can be set in
# Settings → Owner → Instance URL instead).
#   -e ROOKERY_PUBLIC_URL=https://agents.example.com
ENV ROOKERY_DATA_DIR=/data \
    ROOKERY_HOST=0.0.0.0 \
    ROOKERY_PORT=8080 \
    ROOKERY_CODER_MODE=slim \
    HOME=/data

RUN mkdir -p /data && chown -R app:app /data
VOLUME ["/data"]
WORKDIR /data
USER app
EXPOSE 8080

# Shells the binary's own subcommand rather than curl, which this image does not
# ship and which would be dead weight added purely for a health probe.
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/bin/rookery", "healthcheck"]

ENTRYPOINT ["/usr/bin/rookery"]
CMD ["serve"]

ARG VERSION=0.0.0-dev
ARG COMMIT=none
LABEL org.opencontainers.image.title="rookery" \
      org.opencontainers.image.description="Multi-workspace AI agents control plane" \
      org.opencontainers.image.source="https://github.com/ilijad1/rookery" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
