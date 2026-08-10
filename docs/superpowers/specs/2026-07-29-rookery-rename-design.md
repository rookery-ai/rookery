# Rename Simple Agents to Rookery

**Date:** 2026-07-29
**Status:** Approved

## Summary

Rename the project, its Go module, its binary, its environment prefix, its on-disk
layout, its release artifacts and its UI from **Simple Agents** to **Rookery**, and
add the two files a repository needs before anyone else can look at it: a README
and a LICENSE.

The work lands as two pull requests. **PR1** is the rename proper — every surface
that carries the old identity, including CI/CD and packaging, which must change
together or not at all. **PR2** is the launch-ready basics — README, LICENSE,
favicon, repository metadata — which cannot break a build and reviews as prose
rather than as a refactor.

## Why now

Three facts make this the cheapest the rename will ever be, and all three expire:

- The repository is **private**, with zero stars, zero forks and no external
  installs. There is no ecosystem to break and no deprecation path to maintain.
- The project is at **v0.1.0**, tagged the same day as this spec, with **no open
  pull requests** and the release-please manifest at `0.1.0`. There is no release
  PR to desynchronise.
- The only install in existence is the author's own development instance, and the
  author has confirmed it can be **wiped and recreated greenfield**. Workspaces,
  vaults, secrets and connections will all be re-created by hand.

That last point is the load-bearing one. It removes every migration concern from
this spec, which is what turns a dangerous refactor into a mechanical one. See
*Non-goals* for exactly what it deletes.

## Naming decisions

Each of these was decided explicitly; the reasoning is recorded so a later reader
does not reopen a settled question.

**Name: Rookery.** Availability was verified against repository descriptions, not
just repository names — the pass that previously eliminated other candidates. The
largest thing on GitHub called Rookery is a 33-star collection of court seals; the
AI-adjacent matches are hobby-scale (6, 8 and 2 stars). `npm rookery` exists but is
a dormant v0.3.0 micro-package, irrelevant to a Go project.

**The name carries no positioning, and that is fine.** Rookery says nothing about
the convergence of a knowledge base and connected services. Names rarely carry
positioning; taglines do. The name's job is to be memorable, pronounceable,
unclaimed and liked.

**Environment prefix: `ROOKERY_`, not `RK_`.** The stated objection to `SA_` is
that it is an abbreviation nobody can expand. Trading it for a different opaque
abbreviation would reproduce exactly the problem being paid for. Environment
variables are written once in a unit file and read many times. Precedent:
`DOCKER_`, `POSTGRES_`, `GITHUB_`.

**Domain: `rookery.cloud`.** `.com`, `.dev`, `.ai`, `.io`, `.app`, `.org` and `.net`
are all taken, and `.cloud` reads cleanly for a self-hosted platform. This is not
only branding: per `internal/publicurl`, a **publicly valid hostname decides
whether Google's OAuth consent flow works at all**, and a `.lan` host fails it. So
`rookery.cloud` becomes the documented `ROOKERY_PUBLIC_URL` example and the escape
hatch for self-hosters whose LAN hostname is rejected.

**License: Apache-2.0.** The repository currently has no LICENSE at all, which
makes it legally unusable by anyone who sees it. Apache-2.0 is the standard for
platform and infrastructure projects and carries an **express patent grant** that
MIT lacks — which matters for anything AI-adjacent and lets corporate adopters
skip a legal review. AGPL was considered and rejected: it measurably reduces
adoption, and its defensive value assumes a hosted offering that is not planned.

**Metaphor: name only.** No rooks, no colonies, no nesting — not in code, not in
schema, not in UI copy, not in the tagline. Domain nouns stay literal: workspace,
agent, vault, skill. Consequence: the previously drafted tagline used the metaphor
and must be rewritten (see *PR2*).

**Extensions renamed for the same reason as the env prefix.** `.sab` expands to
*Simple Agents Backup* and `.sa_out` to *Simple Agents output*; both become
orphaned abbreviations after the rename. Both are free to change only because the
data is being wiped, and both live in files this PR already edits.

## Identity mapping

This table is the authoritative source for every string change in PR1.

| Surface | From | To |
|---|---|---|
| Product name | Simple Agents | Rookery |
| GitHub repository | `ilijad1/simple-agents-v2` | `ilijad1/rookery` (renamed in place) |
| Go module | `github.com/ilijad1/simple-agents` | `github.com/ilijad1/rookery` |
| Command directory | `cmd/simple-agents/` | `cmd/rookery/` |
| Binary | `simple-agents` | `rookery` |
| Environment prefix | `SA_*` (16 live) | `ROOKERY_*` |
| Data directory | `~/.simple-agents-v2` | `~/.rookery` |
| Database file | `simple-agents.db` | `rookery.db` |
| Lock file | `simple-agents.pid` | `rookery.pid` |
| Snapshot file | `simple-agents-<ts>.sab` | `rookery-<ts>.rkb` |
| Restore temp file | `sa-restore-*.sab` | `rookery-restore-*.rkb` |
| Staged snapshot | `snapshot.sab` | `snapshot.rkb` |
| Spill directory | `.sa_out` | `.rookery_out` |
| Browser storage keys | `sa-theme`, `sa.paneWidth`, `sa.kb.recent` | `rookery-theme`, `rookery.paneWidth`, `rookery.kb.recent` |
| Container image | `ghcr.io/ilijad1/simple-agents-v2` | `ghcr.io/ilijad1/rookery` |
| Docker volume | `simple-agents-data` | `rookery-data` |
| systemd unit | `simple-agents.service` | `rookery.service` |
| Domain | — | `rookery.cloud` |
| License | *(none)* | Apache-2.0 |

The Go module path and the repository name **already disagree** today
(`simple-agents` versus `simple-agents-v2`). The rename is the moment to make them
agree. There is no `/v2` module suffix to carry forward, so
`github.com/ilijad1/rookery` is clean.

### Frozen: the OAuth callback path

`GET /dashboard/connectors/services/callback/:provider` **does not change**, in
this or any future rename. That exact path is the redirect URI registered inside
external OAuth applications at Google, GitHub, Notion and others. Changing it
breaks every existing connection with an error that surfaces only at consent time.
It is the one route that survived the SPA cutover for this reason, and the rename
does not get to touch it either.

## PR1 — the rename

Scope: everything that carries the old identity in code, configuration, CI/CD and
packaging. These belong in one pull request because the Go module path, the
`cmd/` directory, goreleaser's `project_name`/`binary`/`main`, the cosign
certificate-identity regexp and the GHCR image path **must all agree at the same
commit**. Splitting them creates a window in which a release succeeds, reports
green, and produces a signature nobody can verify.

Commit and PR title: `feat!: rename to rookery`. Under `bump-minor-pre-major` the
breaking-change marker makes the next release **v0.2.0**, which is the correct
signal for a changed binary name, environment prefix and data directory.

### Mechanical bulk

Approximately 344 references across 150 files: the import path, the `cmd/`
directory rename, binary references in the Makefile and Dockerfile, the sixteen
`os.Getenv` sites in `internal/config/config.go`, and `buildphase.EnvVar`.

Two `SA_*` names are **deleted rather than renamed**: `SA_TEMPLATES_DIR` and
`SA_STATIC_DIR`. The server-rendered template UI was removed during the SPA
cutover and these survive only in prose in `CLAUDE.md`. They have no consumer.

### The five that fail silently

These must each be verified individually. A find-and-replace can produce a green
build while leaving any of them wrong.

1. **Cosign certificate identity.** `.goreleaser.yaml` pins
   `https://github\.com/ilijad1/simple-agents-v2/.*`. This fails at *verification*
   time, not at build time: release CI passes, artifacts publish, and the
   signature is unverifiable. Detectable only by running `cosign verify` against a
   produced artifact.
2. **`prompts.ConnectorBin` fallback.** `internal/prompts/prompts.go` falls back to
   the bare string `"simple-agents"` — the command a CLI coder is instructed to
   invoke as `<bin> connector exec`. Wrong here means every connector call from a
   CLI coder fails at runtime. `internal/prompts/connected_tools_test.go` pins the
   literal and is the guard.
3. **Platform name inside prompts.** `internal/prompts/prompts.go` emits
   `"for the simple-agents platform"`, and
   `internal/skilllibrary/skills/skill-creator/SKILL.md` carries the same phrase.
   Both are model-facing text describing the product by name.
4. **The theme storage key is read twice.** An inline `<script>` in
   `web/ui/index.html` reads `sa-theme` *before React mounts*, to set the dark
   class without a flash; `web/ui/src/theme.tsx` reads it again. Changing only one
   produces a theme flash on every page load — a defect that passes every test.
5. **`agentdesigner.RenderStateTemplate`** writes *"Managed by Simple Agents"* into
   every `state.md`. No migration is required given the greenfield reset, but the
   asymmetry is worth recording: `WriteState` splices only the JSON fence and
   preserves the heading and intro byte-for-byte, so any file that did survive
   would keep the old brand string permanently.

### CI/CD and packaging

- **`.goreleaser.yaml`** — `project_name`, `builds.id`, `builds.binary`,
  `builds.main` (follows the `cmd/` rename), nfpm `homepage`, and the cosign
  identity regexp above.
- **`release-please-config.json`** — `package-name` becomes `rookery`. The
  manifest stays at `0.1.0`; the `feat!:` commit is what opens v0.2.0.
- **GHCR** — new pushes land at `ghcr.io/ilijad1/rookery`. A repository rename
  **does not move an existing GHCR package**; the old `simple-agents-v2` package
  persists and must be deleted manually. Workflow references to the image path are
  updated to derive from `${{ github.repository }}` where they do not already.
- **`packaging/systemd/rookery.service`** — unit filename, `Description`,
  `Documentation`, `ExecStart`, `Environment=ROOKERY_DATA_DIR`, and
  `ReadWritePaths`.
- **`Makefile`** — `bin/rookery`, the `rookery-data` volume, the `rookery:local`
  image tag, and every target referencing the binary path.
- **`Dockerfile`, `.dockerignore`, `docs/ci-setup.md`.**
- **`cmd/livecheck/main.go`** hardcodes the old data directory. It is an
  uncommitted development harness but it is in the tree and follows the rename.

### The GitHub repository rename

`simple-agents-v2` → `rookery`, renamed **in place** rather than recreated.
In-place preserves tags, releases, issues, pull requests and release-please state,
and GitHub installs a redirect from the old path so existing git remotes keep
working. It must happen before the next release fires, since the cosign identity
regexp and GHCR path in PR1 both assume the new name.

### Explicitly not renamed

- **Git history.** Commit messages are a record of what was true when written.
- **`CHANGELOG.md` and `CHANGES.md`.** Release-please-managed history.
- **Prior specs and plans under `docs/superpowers/`.** Dated records, not live
  instructions. Rewriting them would falsify the archive and produce a large
  diff of pure noise.

`CLAUDE.md` is the exception: it is live instruction that agents read every
session, so it is rewritten completely and accurately.

## PR2 — launch-ready repository basics

Scope: the assets a repository needs before another person can usefully look at
it. Nothing here can break a build, and it reviews as prose and design rather
than as a refactor — which is why it is separated.

Commit and PR title: `docs: launch-ready repo basics`.

**`README.md`** — the repository has none today. Contents: the name and tagline,
what the project is in a paragraph, a quickstart (install, `rookery owner
bootstrap`, `rookery serve`), a short feature summary, the configuration table of
`ROOKERY_*` variables, and a license line.

**Tagline.** The previously drafted line used the metaphor and is therefore
rewritten. The plain form:

> **Rookery** — self-hosted AI agents that live on your knowledge base and act
> through your connected services.

**`LICENSE`** — the full Apache-2.0 text, plus the copyright line naming Ilija
Dimitrovski and the year.

**`web/ui/public/favicon.svg`** — the current file is a generic purple
lightning-bolt glyph unrelated to either name. It is replaced with one
purpose-made minimal mark, reused as the favicon, the login lockup and the
repository avatar. The **existing purple accent is kept**, so the design system is
untouched; this is one SVG, not a redesign.

**Repository metadata** — description and topics on GitHub. The current
description still describes a "Multi-user Agents Control Plane" with Telegram and
sandboxed Python, which predates the workspace model, the connector layer and the
SPA.

## Non-goals

Recorded so they are not silently re-added.

- **No data migration of any kind.** No `mv`, no refuse-to-boot guard, no
  `system.key` preservation step, no dual-prefix snapshot regex, no `state.md`
  intro rewrite. The greenfield reset removes the need for all of it. This is
  worth stating loudly because `system.key` is the single most dangerous object in
  the install: it encrypts every workspace master password, every OAuth token and
  every bot token, and a data directory that moves without it produces an install
  that boots, reports healthy, and has silently lost all of them.
- **No `SA_*` compatibility shim.** A deprecation path has no one to serve.
- **No collapsing of `migrations/001…007`.** There is precedent in this repository
  and "we are wiping anyway" is the moment for it, but it is unrelated to the
  rename and hand-transcribing a schema risks a real defect for no functional
  gain. If wanted, it is its own small spec.
- **No metaphor in identifiers.** `workspace` → `colony` and `agent` → `rook`
  would be a second rename larger than the first — `workspace_id` is in every
  tenant table, every API route and the two-level session model — for zero
  functional gain.
- **Deferred to a follow-on "go public" spec:** the docs site on `rookery.cloud`,
  `install.sh` / `install.ps1`, the Homebrew tap, Windows service registration,
  making the GHCR package public, and flipping repository visibility. Release
  assets on a private repository require an authenticated request, so `curl | sh`
  cannot work until visibility changes — which is why those are gated on it.

## Verification

The rename is mechanical, so the risk is not "does it work" but "did the sweep
miss something that no test covers". Verification is therefore split between the
existing gate and one new one.

**A new merge gate: `TestNoLegacyBrandStrings`.** A Go test asserting zero
occurrences of `simple-agents`, `SimpleAgents`, `Simple Agents`, `SA_`, `.sa_out`
and `.sab` across the tree, with an explicit allowlist for exactly the paths named
in *Explicitly not renamed*: `CHANGELOG.md`, `CHANGES.md`, and `docs/superpowers/`
(which contains this spec and every prior dated record). This mirrors the role
`TestAPIParityInventory` plays for routes: it
converts "we think we got them all" into something CI can prove, and it keeps the
old strings from creeping back.

**The existing gate**, run locally via `make ci` before pushing:

- `gofmt`, `go vet`
- `go test -race` with the **900-second** timeout — the `web` package alone
  measures ~343s under `-race`, thirteen times its non-race time
- the **cross-compile matrix**, all six GOOS/GOARCH pairs. This is the guard that
  keeps `GOOS=windows` compiling, and the `cmd/` directory rename is exactly the
  kind of change that can break a build path
- the frontend job: `tsc -b`, `oxlint`, `vitest`, `vite build`
- the container smoke test — the project's only end-to-end coverage. It builds the
  image, runs it, and asserts `/healthz`, the SPA root and the session endpoint
  all answer. It exercises the renamed binary, the renamed data directory and the
  renamed image path in one pass.

**Named tests that specifically pin renamed strings:**
`internal/prompts/connected_tools_test.go` (the `connector exec` literal),
`web/api_parity_test.go` (route inventory unchanged by the rename — a useful
negative check), `internal/backup/*_test.go` (snapshot naming),
`web/ui/src/components/shell/panewidth.test.tsx` (storage key).

**Manual greenfield acceptance**, performed once after PR1 merges:

1. Delete `~/.simple-agents-v2`.
2. `make build` and confirm the artifact is `bin/rookery`.
3. `rookery owner bootstrap -u <name> -p <pw>`, confirm `~/.rookery` is created
   and contains `system.key` and `rookery.db`.
4. `rookery serve`; check `/healthz` reports the sandbox status and no capability
   warnings.
5. Create a workspace, enter it, connect one service via OAuth — this exercises
   the frozen callback path and `ROOKERY_PUBLIC_URL`.
6. Create and run one agent, confirming `[CHAT]` delivery and that `state.md` is
   written with the new intro string.
7. Take a backup and confirm the snapshot is named `rookery-<ts>.rkb`, then
   restore it.
8. `cosign verify` a release artifact against the new identity regexp — the one
   check that cannot be inferred from a green pipeline.

## Risks

**A release fires mid-rename.** Mitigated by sequencing: no release PR is open,
and PR1 changes the release identity atomically. The GitHub repository rename must
land before the next tag.

**The cosign identity is wrong and nobody notices.** This is the highest-severity
residual risk, because every automated signal is green. Mitigated only by the
explicit manual `cosign verify` step above.

**A missed string reaches a model rather than a compiler.** The prompt and
SKILL.md strings are not compiled and not covered by type checking. Mitigated by
`TestNoLegacyBrandStrings` plus the existing `connected_tools_test.go` pin.
