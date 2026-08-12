# Public release and organization migration — design

**Date:** 2026-08-12
**Repositories:** `ilijad1/rookery`, `ilijad1/rookery-web`
**Target owner:** `rookery-ai` (GitHub organization, already created, currently 0 public repos)

Move both repositories from a personal account to the `rookery-ai` organization and
publish them, without a window in which unreviewed content is world-readable and
without silently breaking release signing, container pulls, or the install scripts.

## Scope

Three phases, gated. Phase A is preparation while both repositories are still
private; Phase B is the transfer and organization setup, still private; Phase C
flips visibility to public. The gate between B and C is a human review, because
publication is the one step that cannot be undone — a clone or fork can happen
within minutes of the flip.

Out of scope: deploying the website. `rookery.cloud` does not currently resolve,
so no host is linked to `rookery-web` and no deployment breaks on transfer.
Deployment is separate work.

## Decisions

Four questions were settled before this document was written.

**The Go module path is renamed** to `github.com/rookery-ai/rookery`. GitHub's
transfer redirect would keep the old path resolving, so the rename is not
technically required — but the project is pre-1.0 with `bump-minor-pre-major`,
nothing has ever imported it (the repository has always been private), and the
cost never gets lower than it is today. 425 occurrences across 188 files.

**Committed binaries are deleted at HEAD, not purged from history.** `simple-agents`
(32 MB) and `livecheck` (16 MB) remain clonable from the pack forever. They are
compiled Go containing no credentials, so the only cost is clone size. Rewriting
history would invalidate every commit SHA, breaking all of `CHANGELOG.md`'s commit
links, the six existing tags, and the association between released artifacts and
their cosign signatures. Not worth it for bloat.

**`docs/superpowers/` stays public**, scrubbed of local-environment references.
It is the most complete documentation the project has and `CLAUDE.md` already
treats it as reference material.

**The maintainer and security contact is `Rookery <security@rookery.cloud>`,**
replacing a personal work address that is currently baked into every `.deb` and
`.rpm`. This requires the mailbox to exist before the domain goes live;
`SECURITY.md` names GitHub's private vulnerability reporting first, so the
address is a fallback rather than the only channel.

## Findings that shape the plan

**No credentials exist anywhere.** All 153 `rookery` PR bodies, all 12
`rookery-web` PR bodies, and every commit message across both repositories'
full history were scanned for credential patterns, provider key formats, and
local-environment markers. Commit messages are clean — zero hits. Seven PR
bodies carry local-environment noise only: `/home/rookie/rookery-web-sync`,
`agents.rookie.lan`, and a curl example using a token literally spelled
`INVALIDTESTTOKEN` to demonstrate an error response.

Editing those PR bodies is **tidying, not a security control** — GitHub retains
and displays edit history. That is acceptable here precisely because nothing
found is a credential. Should the full-history scan surface a real one, the
response is rotation, not redaction.

**Branch protection has never been enforced.** `main` cannot be protected today:
free plan, private repository. The seven required checks documented in
`CLAUDE.md` have therefore been convention, not a merge gate. Publication
unlocks branch protection, making this a net gain of the migration.

**GitHub's push protection does not read commit messages or pull request
descriptions.** It scans file content only. Preventing credentials in those two
surfaces requires mechanisms GitHub does not provide, designed in Phase A.

**Actions secrets do not survive a repository transfer.** `RELEASE_PLEASE_TOKEN`
is the pipeline's only secret and must be re-added after Phase B.

## Phase A — preparation, while private

Every change lands as a pull request into `main`, per the project's standing rule
that `main` only advances through merged PRs. Work is grouped so each PR is
reviewable on its own terms.

### A1. Remove development artifacts

| Path | Action | Reason |
|---|---|---|
| `simple-agents` | delete | 32 MB ELF, pre-rename build artifact, no source in tree |
| `livecheck` | delete | 16 MB ELF, build output of `cmd/livecheck` |
| `cmd/livecheck/` | **keep** | live dev harness; referenced by 8 provider YAMLs, `registry.go`, and three `//go:build livecheck` tests, invoked as `go run ./cmd/livecheck` |
| `.server.pid` | delete | runtime artifact |
| `CHANGES.md` | delete | stale pre-rename changelog superseded by release-please's `CHANGELOG.md`; still references `bin/simple-agents` |
| `AGENT_DESIGNER_TEST_PROMPTS.md` | delete | development scratch; carries a personal email address |
| `plans/` (2 files) | delete | reference `cmd/simple-agents/main.go`, a path that stopped existing at the rename; superseded by `docs/superpowers/plans/` |

`.gitignore` gains `/livecheck`, `/simple-agents`, and `*.pid`. `.dockerignore`
gains the same three, since 48 MB of binaries currently enter the build context
on every image build.

### A2. Community health files

New in `rookery`: `CONTRIBUTING.md` (branch, Conventional Commits, PR, `make ci`
— derived from the existing `CLAUDE.md` rules rather than invented),
`SECURITY.md`, `CODE_OF_CONDUCT.md` (Contributor Covenant 2.1),
`.github/PULL_REQUEST_TEMPLATE.md`, and `.github/ISSUE_TEMPLATE/` with bug and
feature forms. `LICENSE` (Apache-2.0) already exists.

`rookery-web` gains `LICENSE` (Apache-2.0, matching the product),
`CONTRIBUTING.md`, `SECURITY.md`, and `CODE_OF_CONDUCT.md`.

### A3. Rename `ilijad1` to `rookery-ai`

The mechanical half is `go mod edit -module github.com/rookery-ai/rookery`
followed by a sweep across 188 Go files, verified by `make ci`.

Five sites are not mechanical and each breaks something specific if missed:

1. **`.goreleaser.yaml:121`** — `--certificate-identity-regexp
   'https://github\.com/ilijad1/rookery/.*'`. The OIDC identity changes on
   transfer, so cosign verification of *new* releases fails unless this changes
   with it.
2. **`src/content/docs/docs/installation/binary.md:61`** in `rookery-web` — the
   same regexp, in the copy users actually run.
3. **Releases v0.1.0 through v0.4.0 were signed under the old identity** and
   verify only against the old regexp. This is documented rather than papered
   over; one regexp cannot cover both.
4. **`README.md:8` and `README.md:64`** hardcode `ghcr.io/ilijad1/rookery`. The
   workflow itself uses `ghcr.io/${{ github.repository }}` and follows the
   transfer automatically; the documentation does not.
5. **`public/_redirects:19-20`** in `rookery-web` points `/install.sh` and
   `/install.ps1` at `raw.githubusercontent.com/ilijad1/rookery/main/...`.

Also updated: `.goreleaser.yaml:43` (`homepage:`), `Makefile`, `Dockerfile`,
`install.sh`, `install.ps1`, `docs/ci-setup.md`, `cmd/rookery/*.go` user-facing
strings, and in `rookery-web` `astro.config.mjs:32`, `src/pages/index.astro:142`,
`src/components/InstallBlock.tsx:44,54`, `README.md`, `FONTS.md`, and
`docs/website-design-spec.md`.

**Deliberately not rewritten:** `CHANGELOG.md`'s historical links, which
release-please generated and GitHub redirects, and dated historical records under
`docs/superpowers/plans/`. New prose gets the new URL; the historical record keeps
its own. Rewriting them is churn that also makes the changelog disagree with what
release-please will generate next.

### A4. Scrub local-environment references

Four real identifiers appear in tracked files:

| Identifier | Where | Replacement |
|---|---|---|
| `agents.rookie.lan` | `providers/github.yaml:8` (**user-facing setup text**), `packaging/README.md`, `internal/publicurl/policy_test.go` | `rookery.example.com` |
| `192.168.1.194` | tests, `packaging/README.md` | `192.168.1.50`, already the generic example elsewhere in the docs |
| `/home/rookie/...` | 8 test and comment sites | `/home/user` |
| `1843540314` (Telegram ID) | `internal/gateway/router_test.go`, `web/api_connectors_test.go` | an obviously-synthetic id |

Generic RFC1918 examples such as `http://192.168.1.10:8123` in the self-hosted
connector YAMLs **stay** — they are correct documentation of a supported
deployment, not leakage. The distinction is whether the value is *this
developer's* address or *an* address.

`internal/coder/smoke_test.go:16,43` hardcodes `/home/rookie/.opencode/bin/opencode`.
That test can only pass on one machine, so scrubbing it means fixing it to resolve
the binary via `exec.LookPath` with a skip when absent.

Twenty-eight files under `docs/superpowers/` carry the same four identifiers and
receive the same treatment.

### A5. Clean the seven PR descriptions

PRs #16, #20, #21, #56, #92, #117, and #137 in `rookery`. PR review comments and
issue comments across both repositories are swept in the same pass — they are
equally public and were not covered by the body scan.

### A6. Prevention

Three separate mechanisms, because the three surfaces have three different
enforcement points.

**File content** — enable GitHub secret scanning and push protection as an
organization default for new repositories and directly on both repositories. The
existing `gitleaks` job in `pr.yml` remains as the second layer.

**Commit messages** — a `commit-msg` hook. Git does not share hooks, so this is a
committed `.githooks/` directory, a `make hooks` target setting `core.hooksPath`,
and a line in `CONTRIBUTING.md`. It rejects credential patterns and
local-environment patterns (`/home/`, `.lan` hostnames, RFC1918 literals) with a
message naming the offending line.

**Pull request descriptions** — a job in `pr.yml` reading
`github.event.pull_request.body`, triggered on
`types: [opened, edited, reopened, synchronize]`. The `edited` trigger is the
load-bearing one: a clean description can be edited dirty after the first run.

The scanning patterns are shared between the hook and the CI job by keeping them
in one file the hook sources and the workflow reads, so the two cannot drift.

**One-time full-history scan** — gitleaks against the complete history of both
repositories before Phase C. Gitleaks is not installed on the development host; it
runs via a container so nothing is installed.

### A7. `rookery-web` CI

A workflow running `astro check` and `astro build`. The repository currently has
no CI at all, so nothing prevents a broken build from merging.

### A8. Documentation sync

The `docs-sync` skill runs before these PRs open, as the project's rules require:
this change touches `README.md`, `CLAUDE.md`, the landing page, and the
documentation site simultaneously. It also corrects `CLAUDE.md:1751`, which
describes `cmd/livecheck` as "uncommitted" when it is tracked.

## Phase B — transfer and organization setup

### B1. Clear in-flight work

Close PR #114 — release-please cut v0.4.0 at 07:29:56 and opened this redundant
release PR six seconds later; the manifest already reads `0.4.0`. It regenerates
from the commits since the tag on the next push to `main`.

Dependabot PRs #149–#152: merge those whose checks pass, close the rest.
Dependabot re-raises closed ones against the organization repository.

### B2. Transfer

`gh api -X POST repos/ilijad1/<repo>/transfer -f new_owner=rookery-ai` for both.
Local remotes in both checkouts are updated afterwards.

### B3. Organization identity

Confirmed available via API — a probe `PATCH /orgs/rookery-ai` succeeded.

- Display name: `rookeryai` becomes `Rookery`
- Description: `Self-hosted AI agents that live on your knowledge base and act
  through your connected services` — the product tagline verbatim, so the
  organization and the repository carry one message
- Blog: `https://rookery.cloud/` (already set)
- Security defaults for new repositories, all currently **off**: secret scanning,
  push protection, Dependabot alerts, Dependabot security updates, dependency
  graph — enabled at the organization level and directly on both repositories
- A `rookery-ai/.github` repository holding organization-wide default community
  health files and the profile README

### B4. Requires the owner

No API path exists for these:

- **Organization avatar.** GitHub exposes no REST endpoint; it is web-UI only, at
  `github.com/organizations/rookery-ai/settings/profile`. A correctly-sized PNG is
  rendered from `RookeryMark.tsx` so the step is a drag-and-drop.
- **Require 2FA for the organization.** Attempted via API; typically needs the web
  UI and fails if any member lacks 2FA.
- **`RELEASE_PLEASE_TOKEN`.** The secret can be *set* via API, but only the owner
  can mint the PAT. Verified with `gh secret list` after transfer.

### B5. Post-transfer verification

Before handing back for review: Actions secrets present; all four workflows
registered; the GHCR package moved or re-pushable; `go build` green against the new
module path; `make ci` green; `make docs-sync-check` green; local remotes updated.

## Phase C — publication

Gated on human review of the prepared repositories.

1. `gh repo edit rookery-ai/<repo> --visibility public` for both
2. Branch protection on `main` in both — required status checks matching the seven
   documented PR gates, required PR review, no force push
3. Enable private vulnerability reporting
4. **Run `install.sh` end to end.** Publication is precisely what activates
   `curl | sh`: release assets on a private repository require an authenticated
   request, so an anonymous download returns 404. Both installers name that case
   first in their failure text. This is the one path that cannot be tested before
   the flip, and the first thing a new user runs.
5. Verify `docker pull ghcr.io/rookery-ai/rookery:latest` anonymously
6. Verify cosign against a new release built under the new identity

## Risks

**Cosign identity split.** Releases signed before the transfer verify only against
the old regexp. Documented in the installation guide rather than hidden; there is
no way to re-sign a released artifact under a new identity.

**GHCR package transfer.** Container packages are owned by the account, not the
repository, and do not always follow a transfer cleanly. If the package does not
move, the fix is a fresh push under the new owner — which the release workflow
does on the next tag anyway. Verified in B5 rather than assumed.

**Redirect dependence during the gap.** Between Phase B and Phase C the old URLs
serve via redirect. Nothing external depends on them today, since the repositories
have always been private.

**The website is unbuilt and undeployed.** `rookery.cloud` does not resolve, so
`/install.sh` and `/install.ps1` redirects are inert until deployment. This is
pre-existing, not caused by the migration, and is noted so it is not mistaken for
migration breakage.

## Not doing

- Rewriting git history (see Decisions)
- Deploying the website
- Moving `docs/superpowers/` to a private repository
- Renaming either repository
- Any change to the release process itself beyond the identity strings
