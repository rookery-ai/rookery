# CI/CD and distribution: native binaries, container image, and a PR gate

**Date:** 2026-07-28
**Status:** approved

## Problem

The repository has no CI. There is no `.github/` directory, no Dockerfile, and no
git tags across 523 commits. Every check that CLAUDE.md describes as part of the
workflow — tests, conventional commits, "main only advances through merged PRs" —
is enforced by convention and memory alone. Nothing runs on a pull request.

Distribution does not exist either. `make deploy` builds a binary on the machine
that will run it and launches it with `nohup`, tracked by a pidfile the Makefile
owns. That is a development loop, not a way to ship software. There is no
artifact a user could install, no version anyone can name, and no way for the
process to survive a reboot.

The goal is to make the project installable and its main branch trustworthy:

- **Native binaries are the product.** The app is server-first but must also run
  on a workstation. Users should eventually install it with one command and have
  it persist across restarts.
- **A container image is the secondary artifact**, supporting both Docker and
  Podman, on macOS, Linux and Windows.
- **CI gates every PR** — build, test, vulnerability scanning — before merge.
- **Releases are automated** from Conventional Commits, with versioning derived
  from commit history rather than chosen by hand.

Three findings from investigating the codebase shape the design, and each one is
load-bearing.

**Windows does not compile.** `GOOS=windows go build ./cmd/simple-agents` fails
with four errors across two files. `internal/coder/coder.go:452` and
`internal/coder/hosttools.go:1061` contain the same eight-line block:

```go
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
cmd.Cancel = func() error {
    if cmd.Process == nil {
        return nil
    }
    return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
```

`Setpgid` and `syscall.Kill` are Unix-only. `darwin/amd64`, `darwin/arm64` and
`linux/arm64` all cross-compile clean, so this is the single obstacle to a
complete artifact matrix. It is a code change, not a packaging one, and every
release job depends on it.

**Landlock survives containerization; it does not survive leaving Linux.** The
host reports `CONFIG_SECURITY_LANDLOCK=y` and Landlock ABI v8. A static probe run
inside a rootless Podman container under the default seccomp profile also reports
**ABI v8** — so the sandbox is not lost by containerizing, which was the main risk
to the container path. Off Linux it is lost entirely: `internal/sandbox/landlock_other.go`
returns `Supported() == false` and callers do not wrap. A macOS or Windows native
binary therefore executes LLM-generated Python with no filesystem confinement.
That was an acceptable development caveat; as a shipped, installable artifact it
must be stated plainly.

**Missing host tools degrade behaviour silently.** `internal/agentdesigner/guardrails.go:74`
probes for `python3` and the AST guardrail self-skips when it is absent. On a
developer machine that reads as a skipped test. On an end-user install it is a
security control quietly turning itself off. `rg`, `pdftotext` and `tesseract`
degrade too (to a pure-Go searcher, a weaker PDF extractor, and no OCR
respectively), but only python3's absence weakens a guardrail.

Two facts constrain the workflow files themselves. The SPA embed
(`web/ui/embed.go`, `//go:embed all:dist`) stays valid on a clean checkout because
`dist/.gitkeep` is committed — so `go build` and `go test` do **not** require
Node, and only the artifact-producing jobs need it. And the baseline is green:
`go vet` is clean, 24 Go packages pass, and the frontend suite passes 732 tests
across 81 files. Only `gofmt` disagrees, on two test files
(`internal/connectors/openai_test.go`, `internal/vault/links_test.go`).

## Design

### Part 1 — Platform support, stated honestly

| Target | Binary | Filesystem sandbox | Service integration | Tier |
|---|---|---|---|---|
| linux amd64/arm64 | yes | Landlock (ABI v8, verified) | systemd user unit + linger | 1 |
| container (linux) | yes | Landlock (v8 verified, rootless Podman) | runtime-managed | 1 |
| darwin amd64/arm64 | yes | **none** | launchd (deferred) | 2 |
| windows amd64/arm64 | after Part 2 | **none** | SCM (deferred) | 2 |

Tier 2 means the binary is built, tested and published, but runs without
filesystem confinement. This design does not attempt to build a macOS or Windows
sandbox; it makes the gap visible (Part 3) and documents it.

### Part 2 — `setProcGroup`: make Windows compile

Extract the duplicated block into a single helper with two build-tagged
implementations in `internal/coder`:

- `procgroup_unix.go` (`//go:build !windows`) — today's behaviour verbatim: own
  process group via `Setpgid`, cancel via `syscall.Kill(-pid, SIGKILL)`.
- `procgroup_windows.go` (`//go:build windows`) — creates the child in a new
  process group and assigns it to a **job object** with
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, so cancelling terminates the whole tree.
  `golang.org/x/sys/windows` is already an indirect dependency.

Both call sites become `setProcGroup(cmd)`. The intent is unchanged and stated
once instead of twice: a coder subprocess must never orphan its children, because
`exec.CommandContext` signals only the direct child.

A `GOOS=windows` entry in the CI cross-compile matrix (Part 6) is the regression
guard. Without it this breaks again the next time someone reaches for `syscall`.

### Part 3 — `/healthz` and the startup capability report

Add `GET /healthz` — unauthenticated, outside `/api/v1`, returning JSON:

```json
{
  "status": "ok",
  "version": "0.1.0",
  "commit": "ea73b1b",
  "sandbox": {"supported": true, "enabled": true, "abi": 8},
  "coder_mode": "slim",
  "tools": {"python3": true, "rg": true, "pdftotext": true, "tesseract": false}
}
```

The same information is logged once at startup. When `python3` is missing the log
line is a **warning** naming the consequence — the AST guardrail is inactive —
rather than a neutral fact, because that is the one absence that weakens a
security control. When the sandbox is unsupported (any non-Linux install) the
startup log says so explicitly.

`/healthz` has three consumers, which is why it exists rather than reusing
`/api/v1/auth/session`: the container `HEALTHCHECK`, the CI container smoke test
(Part 6), and an operator diagnosing an install. It must not require a session.

Being unauthenticated, it discloses the version, commit and installed-tool set to
anyone who can reach the port. That is accepted: the app binds a LAN or loopback
interface by default, and an operator debugging an install is exactly the person
who cannot authenticate yet. It deliberately reports **only** booleans for tool
presence — never paths, never configuration values.

Also add a `simple-agents version` subcommand reporting version, commit and build
date, injected at link time via `-ldflags -X`. An installed binary that cannot say
what it is cannot be supported.

### Part 4 — `SA_CODER_MODE`: policy, not detection

`SA_CODER_MODE` takes `full` (default) or `slim`, parsed in `config.applyEnv`
alongside `SA_SANDBOX`. The slim container image sets `ENV SA_CODER_MODE=slim`;
native binaries and any future `:full` image leave it unset.

In slim mode the CLI coder kind does not exist. Hiding it in the SPA alone would
be a fake setting — a stale client or a plain `curl` would still POST
`coder_kind=local`, and a workspace row already configured `local` would fail at
run time with a confusing "binary not found". So it is enforced at four layers:

1. **Config** — the mode is parsed and carried on `config.CoderConfig`.
2. **Settings API** — `/api/v1/settings` reports the mode and, in slim, skips the
   `coder.DetectInstalled()` host-filesystem probe entirely, returning an empty
   list. That endpoint re-probes on every call, so this is also a small win.
3. **SPA** — the coder-kind picker omits "local"; `#coder_local` and its
   setup-wizard step do not render.
4. **Write path and runtime** — `handleSaveWorkspaceCoder` and `handleSetupCoder`
   reject `coder_kind=local` with a 400 explaining the build does not support it;
   `coder.ForWorkspace` fails loudly for a workspace already set to `local`
   (realistic when a full install's database is restored into a slim container)
   with a message naming the fix: switch this workspace to the API engine.

This is **policy** — "this build does not support CLI coders" — and is
deliberately distinct from the existing **detection** — "no coder binaries are on
PATH right now". They are not merged. Auto-hiding the section whenever detection
comes back empty would make the UI mysterious for someone who installs `claude`
afterwards, so detection continues to behave exactly as it does today inside full
mode.

The flag also makes a `:full` image nearly free later: the same Dockerfile with an
additional target that installs Node and the coder CLIs and does not set the env
var.

### Part 5 — Release artifacts

Built by **goreleaser** on tag:

- Binaries for linux, darwin and windows on amd64 and arm64, `CGO_ENABLED=0`,
  version metadata injected via `-ldflags`.
- `checksums.txt`, signed with **cosign** keylessly via GitHub OIDC.
- **`.deb` and `.rpm`** via nfpm, shipping the binary plus a **systemd user unit
  template** and a README covering `loginctl enable-linger`. Linux therefore has a
  real "survives reboot" story from the first release, matching the pattern the
  project's own host already uses for its containers.
- An **SBOM** per artifact (syft).
- A GitHub Release whose notes come from release-please.

Deferred to a later phase, after the repository and its packages are public:
`install.sh` and `install.ps1` one-command installers, a Homebrew tap,
Scoop/winget, and Windows Service Control Manager registration. A private
repository cannot serve an anonymous `curl … | sh`, because release assets on a
private repository require an authenticated request. Building the installer now
would mean building something untestable in its real form.

**The split is artifacts now, installers later.** Windows gets a compiling,
published, CI-guarded binary in this phase; its service wrapper lands with the
installer phase.

### Part 6 — Container image

A single slim image, multi-arch (`linux/amd64`, `linux/arm64`), built with a
three-stage Dockerfile:

1. **SPA stage** — `node:24-alpine`, `npm ci && npm run build`, producing
   `web/ui/dist`.
2. **Go stage** — `--platform=$BUILDPLATFORM`, cross-compiling with
   `GOARCH=$TARGETARCH`. Because the build is CGo-free, this needs **no QEMU**;
   multi-arch builds stay fast instead of emulating a foreign architecture.
3. **Runtime stage** — `debian:trixie-slim` with `python3`, `ripgrep`,
   `poppler-utils` and `tesseract-ocr`. Debian rather than Alpine for the
   tesseract language data and to keep glibc available for any tooling a future
   `:full` target adds.

Runtime properties: a non-root UID, `SA_DATA_DIR=/data` as a declared volume,
`HOME` inside that volume so per-workspace `claude-homes` are writable,
`ENV SA_CODER_MODE=slim`, OCI source/description/licence labels, and a
`HEALTHCHECK` against `/healthz`. Stages are named so a `:full` target is an
addition rather than a restructure.

Published to **GHCR**, private for now. Nothing in the pipeline depends on the
package being private: cosign signatures, SBOM and provenance attestations are all
produced regardless, and making it public later is a visibility toggle. The one
consequence is that a consuming host needs a one-time `podman login ghcr.io` with
a `read:packages` token.

Podman and rootless operation are first-class: the image assumes no capabilities
beyond the default set, and the Landlock probe above confirms the sandbox still
applies under rootless Podman.

### Part 7 — CI workflows

Four files in `.github/workflows/`.

Toolchain versions are single-sourced rather than repeated across jobs: Go comes
from `go-version-file: go.mod` (currently 1.26.4) and Node from a committed
`.nvmrc` pinning the major already in use locally (24). A version that drifts
between CI and a developer's machine produces failures that reproduce nowhere.

**`pr.yml`** — on pull request, with a concurrency group cancelling superseded
runs and caching for the Go build/module caches and npm:

- **Conventional-commit PR title lint.** The title is linted rather than
  individual commits because merges are squashes, making the PR title the commit
  that lands on main and the input to versioning.
- **Go:** `gofmt` check, `go vet`, `go test -race`.
- **Cross-compile matrix** across all six OS/arch pairs — the Windows regression
  guard from Part 2.
- **Frontend:** `npm ci`, `tsc -b`, `oxlint`, `vitest run`.
- **Container smoke test:** build the image without pushing, run it, poll
  `/healthz` until ready, and assert HTTP 200, `coder_mode: "slim"`, and an empty
  local-coder list from `/api/v1/settings`. This is the only item in this design
  that closes an existing documented gap ("No integration or e2e test coverage")
  rather than adding new machinery, and it fails the build if the Dockerfile ever
  forgets its `SA_CODER_MODE` line.
- **Security:** `govulncheck` (Go), Trivy in both filesystem and image modes,
  `gitleaks` for committed secrets, and CodeQL for Go and JavaScript.

**`release-please.yml`** — on push to main, maintains a release pull request that
accumulates Conventional Commits and computes the next semantic version. Merging
that PR creates the tag. release-please is chosen over semantic-release
specifically because it releases *through a merged PR*; semantic-release pushes
tags directly from CI, which would violate the project's rule that main only ever
advances through merged PRs.

There are no existing tags, so the first release is pinned explicitly to
**`v0.1.0`** via release-please's `initial-version`, rather than accepting its
default of `1.0.0`. The repository is private and pre-launch; `0.x` states that
the interface is not yet stable, and reaching `1.0.0` becomes a deliberate act at
public release. Under `0.x`, a `feat:` commit bumps the minor version and a
breaking change bumps the minor rather than the major — which is the intended
semantics for a project still changing shape.

**`release.yml`** — on tag: goreleaser for Part 5's artifacts, plus a buildx
multi-arch build and push to GHCR, cosign signing of the image, and a provenance
attestation.

**`dependabot.yml`** — weekly updates for `gomod`, `npm`, `github-actions` and
`docker`.

### Part 8 — Secrets

The pipeline needs approximately none, and this design does not invent plumbing
without a consumer. GHCR authenticates with the built-in `GITHUB_TOKEN`; cosign
signs keylessly through GitHub's OIDC provider; govulncheck, Trivy, gitleaks and
CodeQL require no credentials.

One is likely necessary: **`RELEASE_PLEASE_TOKEN`**, a PAT used to open the
release PR. Pull requests created with the default `GITHUB_TOKEN` do not trigger
other workflows, so without it, merging the release PR would not fire
`release.yml` and no artifacts would be produced.

If a live LLM smoke test is added later it will need a provider API key. It is not
part of this design.

### Part 9 — CLAUDE.md

A new **CI/CD and release process** section covering: branch from main, write
Conventional Commits, open a PR, PR checks must pass, squash-merge, release-please
opens a release PR, merging it tags and publishes. Plus container usage and the
supported environment variables including `SA_CODER_MODE`.

Add a **`make ci`** target that runs the same checks as `pr.yml` locally — gofmt,
vet, `go test -race`, the cross-compile matrix, and the frontend suite — so a
contributor discovers a failure before pushing rather than after.

## Testing

- **Part 2** is verified by the cross-compile matrix itself: `GOOS=windows` moving
  from failing to passing is the test. Process-group termination behaviour is
  covered by a unit test asserting a spawned child tree is killed on context
  cancel, skipped on platforms where it cannot be observed.
- **Part 3** gets a handler test for `/healthz` shape and the no-auth requirement,
  and a test that a missing `python3` produces the warning rather than silence.
- **Part 4** gets tests at each of the four layers, most importantly that the
  write path rejects `coder_kind=local` in slim mode and that `ForWorkspace`
  returns a named error for an already-`local` workspace.
- **Parts 5–7** are verified by the pipeline running: a tag produces the full
  artifact set, and the container smoke test exercises a real running binary.
- The two `gofmt` offenders are fixed so the new check passes on the first run.

## Risks and accepted costs

- **Non-Linux installs have no filesystem sandbox.** Accepted and documented
  rather than solved. Mitigation is visibility: the startup log and `/healthz`
  both report it. A macOS/Windows sandbox is a separate future project.
- **`bash -n` script checking** (`internal/skilldesigner/flow.go:684`) and the
  `python3` dependency remain unavailable on a bare Windows host. The Windows
  binary compiles and serves, but skill-script verification degrades there. Not
  addressed in this design.
- **Frontend suite runtime** is roughly 130 seconds locally and will dominate PR
  check latency. Acceptable; splitting it into a separate job is available if it
  becomes painful.
- **Trivy and govulncheck can fail a PR on a newly disclosed vulnerability in an
  unchanged dependency**, blocking work unrelated to the finding. Accepted: the
  alternative is a scanner nobody acts on. Dependabot exists to shorten the
  window.
- **The one-command install, the headline user-facing goal, is not delivered by
  this design** — it is blocked on the repository becoming public. What is
  delivered is every artifact it will need.

## Phases

1. `setProcGroup` extraction; Windows compiles. Fix the two `gofmt` files.
2. `/healthz`, the startup capability report, and `simple-agents version`.
3. `SA_CODER_MODE` across all four enforcement layers.
4. `pr.yml`: build, test, cross-compile matrix, frontend, caching, concurrency.
5. Security scanning: govulncheck, Trivy, gitleaks, CodeQL, Dependabot.
6. release-please and the Conventional-commit PR title lint.
7. goreleaser: binaries, checksums, cosign, `.deb`/`.rpm` with the systemd unit,
   SBOM.
8. Dockerfile, GHCR publish, and the container smoke test in `pr.yml`.
9. CLAUDE.md CI/CD section and the `make ci` target.

Phase 1 comes first because it is the only change to shipped behaviour that
everything else depends on. Phases 4 and 5 deliver the PR gate, which is the most
valuable single outcome, before any release machinery exists.
