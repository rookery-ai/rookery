# Contributing to Rookery

## Getting set up

```bash
git clone https://github.com/rookery-ai/rookery.git
cd rookery
make hooks     # installs the commit-msg hook — see "Hooks" below
make build     # builds the SPA into the binary, then the binary
make ci-fmt    # seconds; gofmt, and the one people forget
```

**Do not try to reproduce the pipeline locally before opening a pull request.**
There is no aggregate `ci` target, deliberately: it took about fifteen minutes,
the pipeline runs the same work again on every push, and it never covered four
of the seven PR jobs — the Conventional-commit title check needs the pull
request itself, and the security scan, container smoke test and package smoke
test were never part of it. A green local run never meant a green PR.

Run the targeted checks for what you actually touched: `make ci-fmt`,
`make ci-vet`, `make ci-ui` and `make ci-docs` take seconds each, and
`go test ./internal/<pkg>/` is the fast inner loop. `make ci-package` builds a
goreleaser snapshot and smoke-tests the deb, rpm and tar.gz — minutes, so run it
when you touch packaging.

The one case where reproducing CI locally is warranted is a **stacked** pull
request: one based on another branch rather than `main` runs no checks at all.

## Branching

Always branch off `main`. `main` only ever advances through merged pull requests.

Branch names must match:

```
^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$
```

For example `feat/oauth-redirect-pinning`, `fix/slack-socket-reconnect`.
This is enforced in CI. Bot branches (`release-please--*`, `dependabot/*`) are
exempt, because neither bot lets you name its branches.

## Commits and pull request titles

Every commit message and every PR title must be a
[Conventional Commit](https://www.conventionalcommits.org/): `type(scope): summary`.

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
Scope is optional but preferred — `feat(connectors):`, `fix(web/chat):`.

**The PR title matters more than the individual commits**, because merges are
squashes: the title becomes the commit that lands on `main` and the input
release-please reads to compute the next version.

## Hooks

`make hooks` points `core.hooksPath` at the committed `.githooks/` directory,
installing a `commit-msg` hook that checks three things, at two severities:

1. **Credentials** — provider key formats (`sk-…`, `gh[pousr]_…`,
   `xox[baprs]-…`, `AKIA…`, `AIza…`), a PEM private-key header, a Telegram
   bot-token shape, and `API_KEY=`/`PASSWORD=`/`SECRET=` assignments.
   **Blocking** — the commit is rejected. GitHub's push protection scans file
   *content* only; it never reads a commit message or a PR description, so
   this hook and its CI counterpart cover what GitHub does not.
2. **Local-environment leakage** — home directory paths, `.lan` hostnames, and
   RFC1918/CGNAT address literals. **Advisory only** — printed, but the commit
   proceeds.
3. **Non-conventional commit messages.** Blocking.

The two severities are deliberate: a credential in a commit message is a
permanent, public security incident that requires rotating the secret,
whereas a home directory path or an internal hostname is untidiness — this
repository's own history routinely and legitimately describes self-hosted
testing against RFC1918 addresses and `.lan` hosts, so blocking those
messages outright would just train contributors to disable the hook, and a
disabled hook protects nothing.

The hook is pure `grep -E` with no external dependency, deliberately: a hook that
shells out to a tool the contributor does not have installed silently succeeds,
which is worse than no hook. Patterns live in `.githooks/patterns-block.txt`
(credentials) and `.githooks/patterns-warn.txt` (local-environment leakage),
both read by the hook and the CI job, so local and CI enforcement cannot drift.

## Tests

Write the failing test first. `make ci-test` runs the Go suite with `-race`.
AST guardrail tests shell out to `python3` and self-skip without it — a skipped
security test is worse than a failing one, so install python3.

## Documentation

Four surfaces describe this project and each can be wrong without anything
failing: `README.md`, `CLAUDE.md`, the documentation site and the landing page
(the last two in [`rookery-ai/rookery-web`](https://github.com/rookery-ai/rookery-web)).

`make docs-sync-check` mechanises the checkable half — counts, variable names,
command names, provider names — against the source rather than against other
prose. Verify every claim against source, never against another document.

## Adding a connector

A connector is two YAML files — `internal/connectors/providers/<name>.yaml`
(auth) and `internal/connectors/connectors/<name>.yaml` (actions) — and no Go
code. Read the existing ones first; `CLAUDE.md` records the traps (a connector
answers in JSON, credentials cannot go in a request body, sending email is
`public_write` rather than merely `mutating`).

Mark a provider `unverified: true` unless you have exercised it against the live
API.

## Code of conduct

By participating you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
