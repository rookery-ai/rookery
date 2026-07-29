# Dependabot backlog triage — 2026-07-29

Eleven Dependabot PRs were open. This records which were taken, which were
rejected, and why — so the rejected two are not re-litigated every week, and so
the one finding that no Dependabot PR contained is not lost.

## Verdict

| PR | Bump | Verdict |
|---|---|---|
| #27 | actions/checkout 5→7 | **take** |
| #28 | docker/login-action 3→4 | **take** |
| #29 | gitleaks-action 2→3 | **take** — deadline |
| #30 | actions/setup-go 6→7 | **take** |
| #31 | docker/setup-buildx-action 3→4 | **take** |
| #47 | golang.org/x/net 0.56→0.57 | **take** |
| #48 | telebot beta.9→beta.10 | **take** |
| #49 | npm-minor-patch group (13 × @tiptap/\*) | **take** |
| #51 | @testing-library/jest-dom 6→7 | **take** |
| #50 | typescript 6→7 | **reject** |
| #26 | node 24-alpine→26-alpine | **reject** |

Nine taken, shipped as three PRs rather than nine.

## Why three PRs and not nine

Every Dependabot PR runs the full 11-job gate, including an ~8-minute
`go test -race` and a container build. On a private repo those are metered
Actions minutes — the same concern that made `dependabot.yml` group updates in
the first place. Nine PRs is ~99 jobs for what is, in content, three
independent changes.

They are split by blast radius rather than merged into one, so that a failure
in one does not block the others and a bisect stays cheap:

1. **CI actions** — the Node 24 runtime migration. Self-validating: these are
   the actions the PR gate itself runs on, so a green gate *is* the test.
2. **Go deps** — `x/net` and `telebot`.
3. **npm** — the tiptap group, jest-dom, and the audit fix.

## The finding no Dependabot PR contained

All five action majors are the same change: GitHub is removing the Node 20
runtime from hosted runners on **2026-09-16**, and every one of these actions
released a major solely to move to Node 24. None changes inputs, outputs or
behaviour.

Auditing `runs.using` for *every* action in the workflows — not just the ones
Dependabot flagged — found **four more still on node20**, with no PR open for
any of them:

- `docker/build-push-action@v6` → v7
- `docker/metadata-action@v5` → v6
- `goreleaser/goreleaser-action@v6` → v7
- `googleapis/release-please-action@v4` → v5

Three of those four are in `release.yml`/`release-please.yml`, i.e. the entire
release pipeline would have failed on the first tag pushed after 2026-09-16,
and nothing in the PR gate would have warned us.

The cause is that `open-pull-requests-limit: 3` is a **cap, not a queue**. The
five already-open majors filled it, so Dependabot stopped proposing. A quiet
Dependabot is not evidence of an up-to-date tree.

`dependabot.yml` also asserted that "gitleaks v2→v3 changes its licensing
model". That is false — v3's release notes state no change to inputs, outputs
or behaviour — and the belief was actively steering us away from a fix with a
hard deadline. The comment is corrected in the same PR.

## Rejections

**#50 TypeScript 6→7 — reject.** TS 7 is the native Go port: a different
compiler binary, which is why the lockfile diff adds 300+ lines of
platform-specific optional dependencies. `tsc -b` is load-bearing in the build
script and the Frontend CI job, and `vitest`, `oxlint` and `@types/react` all
resolve against the TS version. Swapping the compiler implementation to chase a
version number is the definition of risk without benefit. Revisit when the
toolchain has settled and there is a reason to move.

**#26 node 24→26-alpine — reject.** It desyncs `Dockerfile` from `.nvmrc` (24),
so CI would build the SPA on Node 24 and the container on Node 26 — the same
artifact from two toolchains. Node 26 is April-2026 Current, not yet LTS. The
benefit is nil: this is the build stage only, and no Node reaches the runtime
image. Bumping `.nvmrc` alongside it would fix the desync but move the whole
project onto a non-LTS runtime, which is a separate decision nobody asked for.

## The audit fix

Not from any Dependabot PR, and the most valuable single change here.

`npm audit` reported four advisories on `main` — two of them HIGH
(`brace-expansion`, `fast-uri`), both transitive under the `shadcn` CLI
devDependency, and both **with a fix available**. The PR gate's Trivy
filesystem scan covers the npm tree at `severity: CRITICAL,HIGH` with
`ignore-unfixed: true` and `exit-code: 1` — so "fix available" means Trivy
would *not* have ignored them.

Dependabot's grouped version-update PRs do not reach transitive dependencies;
only security updates would, and those were not producing PRs. `npm audit fix`
resolves all four within existing semver ranges, lockfile-only. Audit is now
clean.

## Verification

- **Go**: `go build ./...` and `go test ./internal/gateway/...` pass. telebot is
  a beta→beta bump on a pre-release line, so the API was checked rather than
  assumed; `internal/gateway/telegram.go` and its test are the only consumers.
- **npm**: the Frontend job run locally end to end — `npm ci`, `tsc -b`,
  `oxlint`, `vitest` (81 files / 734 tests pass), `vite build`, `npm audit`
  (0 vulnerabilities). `NoteEditor.test.tsx` mounts the real tiptap editor
  rather than a mock, so 3.29.2 is genuinely exercised.
- **Actions**: every input and output the workflows pass was checked against
  each new major's `action.yml` — `build-push-action`'s ten inputs and its
  `digest` output, `metadata-action`'s `tags`/`labels` outputs,
  `goreleaser-action`'s `version`/`args`, `release-please-action`'s
  `token`/`config-file`/`manifest-file`. All present.
- The two behaviour changes in the set are no-ops here: checkout v7 blocks
  fork-PR checkout for `pull_request_target`/`workflow_run`, and no workflow
  uses either trigger; setup-buildx v4 removes deprecated inputs, and it is
  invoked with none.

**Verification boundary, stated plainly:** `release.yml` and
`release-please.yml` run on tag/main push, not on the PR gate, so the
`goreleaser`, `metadata-action` and `release-please` bumps are verified by
inspection (tags exist, inputs and outputs still declared) and *not* by
execution. They will first execute on the next release. The alternative —
leaving them on a runtime that stops existing in seven weeks — is worse.
