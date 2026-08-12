# Public release and organization migration — design

**Date:** 2026-08-12
**Repositories:** `ilijad1/rookery`, `ilijad1/rookery-web`
**Target owner:** `rookery-ai` (GitHub organization, already created, currently 0 public repos)

Move both repositories from a personal account to the `rookery-ai` organization and
publish them, having first destroyed every shippable artifact produced under the
personal account, so that versioning restarts cleanly under the organization.

## Scope

Three phases, gated. Phase A is content preparation and the teardown of existing
artifacts, while both repositories are still private. Phase B is the transfer, the
`ilijad1` → `rookery-ai` rename, and organization setup, still private. Phase C
flips visibility to public and cuts the first release under the new owner.

The gate between B and C is a human review, because publication is the one step
that cannot be undone — a clone or fork can happen within minutes of the flip.

The rename sits in Phase B rather than Phase A on purpose: it can only be verified
against a repository that actually lives at the new path. Everything in Phase A is
true regardless of who owns the repository.

Out of scope: deploying the website. `rookery.cloud` does not currently resolve,
so no host is linked to `rookery-web` and no deployment breaks on transfer.
Deployment is separate work, and is the reason `rookery-web` is versioned (below).

## Decisions

**Nothing shippable is transferred.** All six GitHub releases (v0.1.0 through
v0.4.0) and their assets, all six git tags, and the GHCR container package are
deleted before the transfer. `CHANGELOG.md` is deleted and
`.release-please-manifest.json` is reset, so the first release under the
organization is **v0.1.0**, cut fresh. Nothing built under the personal account
survives — which also removes every `.deb` and `.rpm` carrying
`ilija.dimitrovski@kroute.ai` as its package maintainer.

**The Go module path is renamed** to `github.com/rookery-ai/rookery`. GitHub's
transfer redirect would keep the old path resolving, so the rename is not
technically required — but the project is pre-1.0, nothing has ever imported it
(the repository has always been private), and the cost never gets lower than it is
today. 425 occurrences across 188 files.

**Committed binaries are deleted at HEAD; history is not rewritten.** This was
re-examined once the artifact teardown removed the original objections (broken
tags, changelog links, and cosign associations), and the answer held for a
different and stronger reason — see *A purge would not purge* below. The binaries
contain no credentials; the only sensitive string in them is a
`/home/rookie/go/pkg/mod/...` build path.

**PR history is preserved.** All 153 closed pull requests transfer with the
repository, so their descriptions are cleaned rather than discarded.

**`docs/superpowers/` stays public**, scrubbed of local-environment references.
It is the most complete documentation the project has and `CLAUDE.md` already
treats it as reference material.

**The maintainer and security contact is `Rookery <security@rookery.cloud>`,**
replacing a personal work address currently baked into every `.deb` and `.rpm`.
`SECURITY.md` names GitHub's private vulnerability reporting first, so the address
is a fallback rather than the only channel; the mailbox must exist before the
domain goes live.

**`rookery-web` gets full release-please**, matching the product: conventional
commits, semver tags, a changelog, and versioned GitHub releases. The rationale is
deployment — deploy scripts will target a released version, not a branch, so the
website needs a version to name.

**Conventional Commits are enforced mechanically on three surfaces:** PR titles
(which become the squashed commit on `main`), branch names, and local commit
messages.

**release-please authenticates as an organization-owned GitHub App**, not as a
personal access token. GitHub has no org-owned PAT — every PAT belongs to a user
account, and a fine-grained one merely *scopes* to an organization while still
being the user's, expiring on a calendar and dying if they leave. An App is owned
by the organization outright: it holds the permissions, the workflow mints a
short-lived installation token per run via `actions/create-github-app-token`, and
no long-lived credential is stored anywhere. Release PRs are authored by the bot
identity rather than by a person, which is also the correct look on a public
repository. The App ID and private key are stored **once as organization
secrets**, shared with both repositories, so rotation happens in one place.

## Findings that shape the plan

**No credentials exist anywhere.** All 153 `rookery` PR bodies, all 12
`rookery-web` PR bodies, and every commit message across both repositories' full
history were scanned for credential patterns, provider key formats, and
local-environment markers. Commit messages are clean — zero hits. Seven PR bodies
carry local-environment noise only: `/home/rookie/rookery-web-sync`,
`agents.rookie.lan`, and a curl example using a token literally spelled
`INVALIDTESTTOKEN` to demonstrate an error response.

Editing those PR bodies is **tidying, not a security control** — GitHub retains
and displays edit history. That is acceptable here precisely because nothing found
is a credential. Should a real one ever surface, the response is rotation, not
redaction.

**`kroute.ai` is not in the committed binaries.** Both were checked directly: zero
occurrences in either. The address reaches users through `.goreleaser.yaml:44`'s
`maintainer:` field, which is compiled into every `.deb` and `.rpm` on the
releases page. Deleting the releases is what removes it; deleting the binaries
does not.

**A purge would not purge.** GitHub permanently retains a `refs/pull/N/head` ref
for every pull request ever opened. All 153 are present on the remote — verified
with `git ls-remote`, and confirmed by fetching `refs/pull/16/head`, whose tree
still contains `simple-agents` and `.server.pid` at the root. Those refs point at
the original commits and are untouched by a force-push, so `git filter-repo`
would rewrite `main` while leaving every purged blob fetchable by anyone running
`git fetch origin 'refs/pull/*/head'`. A true purge therefore requires either
abandoning the pull request history or a GitHub Support intervention on an
uncontrolled timeline. Since the binaries hold nothing sensitive, neither price is
worth paying — but the rewrite must not be sold as a purge, because it is not one.

**Branch protection has never been enforced.** `main` cannot be protected today:
free plan, private repository. The seven required checks documented in `CLAUDE.md`
have therefore been convention, not a merge gate. Publication unlocks branch
protection, making this a net gain of the migration.

**GitHub's push protection does not read commit messages or pull request
descriptions.** It scans file content only. Covering those two surfaces requires
mechanisms GitHub does not provide (A8).

**Actions secrets do not survive a repository transfer**, and the existing one is
being replaced regardless. `ilijad1/rookery` currently holds a single secret,
`RELEASE_PLEASE_TOKEN`, created 2026-07-28; `ilijad1/rookery-web` holds none. Both
are superseded by the organization-owned App above, so the migration does not
carry the old token forward — it is deleted, not re-added.

The reason release-please cannot simply use the built-in `GITHUB_TOKEN` still
holds and is worth restating, since it is the sort of thing that gets "simplified"
later: a pull request opened with `GITHUB_TOKEN` does not trigger other workflows,
so merging the release PR would create a tag that `release.yml` never sees,
producing a **tag with no artifacts attached and no error explaining why**.

**Deleting all releases means `curl | sh` has nothing to download** until v0.1.0
is cut under the organization. This reorders Phase C: publish, release, *then*
test the installers.

## Phase A — teardown and preparation, while private

Every file change lands as a pull request into `main`, per the project's standing
rule that `main` only advances through merged PRs.

### A1. Destroy the existing artifacts

- **Close PR #114.** release-please cut v0.4.0 at 07:29:56 and opened this
  redundant release PR six seconds later. It is stale and its subject is about to
  be deleted.
- **Delete all six GitHub releases** and their attached assets: v0.1.0, v0.2.0,
  v0.3.0, v0.3.1, v0.3.2, v0.4.0. This is what removes the `.deb`/`.rpm` artifacts
  carrying the `kroute.ai` maintainer address, the checksums, the cosign
  signatures and the SBOMs.
- **Delete all six git tags**, local and remote.
- **Delete the GHCR container package** — see A2, this one is an owner action.

### A2. Requires the owner — package deletion

The authenticated token holds `repo`, `workflow`, `read:org`, `gist` and
`admin:public_key`. It has **no `read:packages` or `delete:packages`**, so
`GET /user/packages` returns 403 and the container package can be neither
enumerated nor deleted from here.

Two ways forward; either is fine:

- `gh auth refresh -h github.com -s read:packages,delete:packages`, after which
  the deletion is scripted with the rest of A1, or
- delete it in the web UI at `github.com/users/ilijad1/packages`.

The package must be gone before the transfer, or a stale `ghcr.io/ilijad1/rookery`
remains published under the personal account with no repository behind it.

### A3. Reset the version line

- Delete `CHANGELOG.md`. It documents releases that will no longer exist and every
  entry links to `github.com/ilijad1`. release-please regenerates it from the
  first conventional commit after the reset.
- Reset `.release-please-manifest.json` from `{".": "0.4.0"}` to `{".": "0.0.0"}`,
  so the first `feat:` cuts **v0.1.0**.
- `bump-minor-pre-major` stays on, so a breaking change bumps the minor while
  pre-1.0. Reaching 1.0.0 remains the deliberate act `CLAUDE.md` already describes.

This lands **after** A1, so release-please never sees a manifest that disagrees
with the tags present.

### A4. Remove development artifacts

| Path | Action | Reason |
|---|---|---|
| `simple-agents` | delete at HEAD | 32 MB ELF, pre-rename build artifact, no source in tree |
| `livecheck` | delete at HEAD | 16 MB ELF, build output of `cmd/livecheck` |
| `cmd/livecheck/` | **keep** | live dev harness; referenced by 8 provider YAMLs, `registry.go`, and three `//go:build livecheck` tests, invoked as `go run ./cmd/livecheck` |
| `.server.pid` | delete | runtime artifact |
| `CHANGES.md` | delete | stale pre-rename changelog; still references `bin/simple-agents` |
| `AGENT_DESIGNER_TEST_PROMPTS.md` | delete | development scratch; carries the `kroute.ai` address |
| `plans/` (2 files) | delete | reference `cmd/simple-agents/main.go`, a path that stopped existing at the rename; superseded by `docs/superpowers/plans/` |

`.gitignore` gains `/livecheck`, `/simple-agents`, and `*.pid`. `.dockerignore`
gains the same three, since 48 MB of binaries currently enter the build context on
every image build.

Deleting them at HEAD is worth doing on its own merits even though history keeps
them: it removes them from every fresh checkout, from the Docker build context,
and from the tree a reader browses.

### A5. Community health files

New in `rookery`: `CONTRIBUTING.md` (branching, Conventional Commits, the branch
naming rule, `make ci`, `make hooks` — derived from existing `CLAUDE.md` rules
rather than invented), `SECURITY.md`, `CODE_OF_CONDUCT.md` (Contributor Covenant
2.1), `.github/PULL_REQUEST_TEMPLATE.md`, and `.github/ISSUE_TEMPLATE/` with bug
and feature forms. `LICENSE` (Apache-2.0) already exists.

`rookery-web` gains `LICENSE` (Apache-2.0, matching the product),
`CONTRIBUTING.md`, `SECURITY.md`, and `CODE_OF_CONDUCT.md`.

### A6. Scrub local-environment references

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
developer's* address or *an* address. Whoever executes this must not flatten it
into "remove all private IPs."

`internal/coder/smoke_test.go:16,43` hardcodes `/home/rookie/.opencode/bin/opencode`.
That test can only pass on one machine, so scrubbing it means fixing it to resolve
the binary via `exec.LookPath` and skip when absent.

Twenty-eight files under `docs/superpowers/` carry the same identifiers and get
the same treatment.

### A7. Clean the seven PR descriptions

PRs #16, #20, #21, #56, #92, #117 and #137 in `rookery`. PR review comments and
issue comments across both repositories are swept in the same pass — they are
equally public and were not covered by the body scan.

### A8. Enforcement: credentials and Conventional Commits

**Five checks across three surfaces.** All apply to both repositories.

*Credential and local-environment leakage:*

1. **File content** — GitHub secret scanning and push protection, enabled as an
   organization default (done, B4) and directly on both repositories after
   transfer. The existing `gitleaks` job in `pr.yml` stays as the second layer.
2. **Commit messages** — a `commit-msg` hook rejecting credential patterns and
   local-environment patterns (`/home/`, `.lan` hostnames, RFC1918 literals),
   naming the offending line.
3. **PR descriptions** — a `pr.yml` job reading `github.event.pull_request.body`,
   triggered on `types: [opened, edited, reopened, synchronize]`. The `edited`
   trigger is load-bearing: a clean description can be edited dirty afterwards.

*Conventional Commits:*

4. **PR titles** — already enforced in `rookery`; added to `rookery-web`. This is
   the important one, because merges are squashes, so the title becomes the commit
   release-please reads.
5. **Branch names** — a new job in both repositories:

   ```
   ^(feat|fix|docs|refactor|test|chore|perf|build|ci)/[a-z0-9._-]+$
   ```

   **Bot branches are exempt, and the exemption is mandatory rather than a
   preference:** neither release-please nor Dependabot lets you name its branches,
   so without it every bot PR fails CI permanently. Exempt
   `release-please--*` and `dependabot/*`.

The `commit-msg` hook also validates the Conventional Commits shape locally,
matching the rule `CLAUDE.md` already states for every commit.

**The hook is pure POSIX `grep -E`, with no gitleaks dependency.** This is a
deliberate constraint, not a shortcut: gitleaks is not installed on the
development host and would not be on a contributor's either, so a hook that shells
out to it silently succeeds wherever the binary is absent. That is the same
failure shape `CLAUDE.md` records for the gitleaks `[[allowlists]]` syntax — a
check that loads, does nothing, and reports success. A check that cannot run must
fail loudly or not exist.

Git does not share hooks, so distribution is a committed `.githooks/` directory, a
`make hooks` target setting `core.hooksPath`, and a line in `CONTRIBUTING.md`.
Patterns live in one committed file, `.githooks/patterns.txt`, which the hook
feeds to `grep -Ef` and the workflow reads directly, so local and CI enforcement
cannot drift apart.

**One-time full-history gitleaks scan** of both repositories before Phase C, run
in a container since gitleaks is not installed on the host.

### A9. `rookery-web` CI and versioning

The repository has no CI at all today, so nothing prevents a broken build from
merging. It gains:

- a PR workflow running `astro check` and `astro build`
- the conventional-commit title and branch-name checks from A8
- `release-please-config.json` + `.release-please-manifest.json` seeded at
  `0.0.0`, and a `release-please.yml` authenticating as the org App
- a `release.yml` that builds the site and attaches the built `dist` as a release
  asset, so a deploy script can fetch a *version* rather than checking out a branch

### A10. Switch release-please to the org App

Both `release-please.yml` workflows mint an installation token instead of reading
a stored PAT:

```yaml
- uses: actions/create-github-app-token@v2
  id: app-token
  with:
    app-id: ${{ secrets.ROOKERY_APP_ID }}
    private-key: ${{ secrets.ROOKERY_APP_PRIVATE_KEY }}
- uses: googleapis/release-please-action@v4
  with:
    token: ${{ steps.app-token.outputs.token }}
    config-file: release-please-config.json
    manifest-file: .release-please-manifest.json
```

The inline comment explaining why this is not `GITHUB_TOKEN` stays — the reasoning
survives the change of mechanism, and `docs/ci-setup.md` is rewritten to describe
the App rather than the PAT.

The old `RELEASE_PLEASE_TOKEN` secret is deleted from `rookery` after the first
successful App-authenticated run, not before: deleting it while it is still the
only working credential would leave no path to cut a release if the App setup
needs a second attempt.

### A11. Documentation sync

The `docs-sync` skill runs before each of these PRs opens, as the project's rules
require: this work touches `README.md`, `CLAUDE.md`, the landing page and the
documentation site simultaneously. It runs again on the Phase B rename PR, which
changes install commands and image names across all four surfaces at once.

It also corrects two `CLAUDE.md` claims this work invalidates: line 1751 describes
`cmd/livecheck` as "uncommitted" when it is tracked, and the CI/CD section
describes a release history that will no longer exist.

## Phase B — transfer, rename, and organization setup

### B1. Clear in-flight work

Dependabot PRs #149–#152: merge those whose checks pass, close the rest.
Dependabot re-raises closed ones against the organization repository.

### B2. Transfer

`gh api -X POST repos/ilijad1/<repo>/transfer -f new_owner=rookery-ai` for both.
Local remotes in both checkouts are updated afterwards.

### B3. Rename `ilijad1` to `rookery-ai`

Deliberately **after** the transfer. Renaming while the repository still lives
under `ilijad1` means every verification runs against a module path whose
canonical location does not yet exist — the build passes, but the module proxy
behaviour and the cosign identity are checked against a fiction. The cost is one
extra PR.

The mechanical half is `go mod edit -module github.com/rookery-ai/rookery`
followed by a sweep across 188 Go files, verified by `make ci`.

Four sites are not mechanical:

1. **`.goreleaser.yaml:121`** — `--certificate-identity-regexp
   'https://github\.com/ilijad1/rookery/.*'`. The OIDC identity changes on
   transfer, so cosign verification of releases fails unless this changes with it.
   Because every prior release is deleted, there is no old-identity artifact left
   to verify and **no identity split to document** — one regexp now covers
   everything that exists.
2. **`src/content/docs/docs/installation/binary.md:61`** in `rookery-web` — the
   same regexp, in the copy users actually run.
3. **`README.md:8` and `README.md:64`** hardcode `ghcr.io/ilijad1/rookery`. The
   workflow's own `ghcr.io/${{ github.repository }}` resolves to the new path on
   its next run, which publishes a genuinely new package — the old one having been
   deleted in A1/A2.
4. **`public/_redirects:19-20`** in `rookery-web` points `/install.sh` and
   `/install.ps1` at `raw.githubusercontent.com/ilijad1/rookery/main/...`.

Also updated: `.goreleaser.yaml:43` (`homepage:`) and `:44` (`maintainer:`),
`Makefile`, `Dockerfile`, `install.sh`, `install.ps1`, `docs/ci-setup.md`,
`cmd/rookery/*.go` user-facing strings, and in `rookery-web`
`astro.config.mjs:32`, `src/pages/index.astro:142`,
`src/components/InstallBlock.tsx:44,54`, `README.md`, `FONTS.md` and
`docs/website-design-spec.md`.

**Deliberately not rewritten:** dated historical records under
`docs/superpowers/plans/`. New prose gets the new URL; the historical record keeps
its own. (`CHANGELOG.md`, previously the main exception here, no longer exists.)

### B4. Organization identity

**Profile fields — verified writable.** A live `PATCH /orgs/rookery-ai` succeeded.

- Display name: `rookeryai` becomes `Rookery`
- Description: `Self-hosted AI agents that live on your knowledge base and act
  through your connected services` — the product tagline verbatim
- Blog: `https://rookery.cloud/` (already set)

**Security defaults — verified writable, and already applied.** All five were
`false`; a live probe set each to `true` and read the value back:
`secret_scanning_enabled_for_new_repositories`,
`secret_scanning_push_protection_enabled_for_new_repositories`,
`dependabot_alerts_enabled_for_new_repositories`,
`dependabot_security_updates_enabled_for_new_repositories`,
`dependency_graph_enabled_for_new_repositories`. These are defaults for *new*
repositories, so they must also be enabled directly on both repositories after
transfer — an inherited default does not apply retroactively.

`advanced_security_enabled_for_new_repositories` stays `false`: GitHub Advanced
Security is not available on the free plan.

A `rookery-ai/.github` repository holds organization-wide default community health
files and the profile README.

### B5. Requires the owner

- **Organization avatar.** GitHub exposes no REST endpoint at all; it is web-UI
  only, at `github.com/organizations/rookery-ai/settings/profile`. A
  correctly-sized PNG is rendered from `RookeryMark.tsx` so the step is a
  drag-and-drop.
- **Require 2FA for the organization.** Verified *not* settable via API, and the
  failure mode is a trap: `PATCH` with `two_factor_requirement_enabled=true`
  returns **200 with a full org body**, and the field reads back `false`. It is
  silently ignored, so a script checking only the HTTP status reports a success
  that never happened. Web UI only, and it fails there if any member lacks 2FA.
- **Create the release GitHub App**, owned by `rookery-ai`. GitHub Apps cannot be
  created through the API at all — it is the web UI or the app-manifest browser
  flow. Settings, so the step is mechanical:

  | Field | Value |
  |---|---|
  | Owner | `rookery-ai` |
  | Name | e.g. `Rookery Release` (must be globally unique) |
  | Homepage URL | `https://rookery.cloud` |
  | Webhook | **uncheck Active** — the App is never called, it only issues tokens |
  | Repository permission: Contents | Read and write (create the tag, commit the changelog) |
  | Repository permission: Pull requests | Read and write (open and update the release PR) |
  | Where can this be installed | Only on this account |

  Then install it on `rookery` and `rookery-web`, generate a private key, and
  store `ROOKERY_APP_ID` and `ROOKERY_APP_PRIVATE_KEY` as **organization**
  secrets with selected-repository access to both.

  The private key is a `.pem` downloaded once and never shown again; paste it
  directly into GitHub rather than routing it through any other tool. The App ID
  is not secret, but lives beside the key for symmetry.
- **`delete:packages` scope or the web UI**, for A2.

### B6. Post-transfer verification

All workflows registered in both repositories; `go build` green against the new
module path; `make ci` green; `make docs-sync-check` green; local remotes updated;
security settings confirmed on the repositories themselves, not merely as org
defaults; zero releases, zero tags and zero packages present.

Two checks are weaker than they look and are recorded as such:

- **A present secret is not a working credential.** `gh secret list` shows
  `ROOKERY_APP_ID` and `ROOKERY_APP_PRIVATE_KEY` exist; it cannot show that the
  App is installed on the repository, that its permissions are right, or that the
  key matches. The only real verification is a release-please run, which does not
  happen until the next push to `main`. A common failure here is an App created
  but never *installed* on one of the two repositories — the token mint then fails
  with a permissions error that reads like a bad key.
- **The container image is verified by an anonymous pull after Phase C**, not by
  its presence in an authenticated API listing.

## Phase C — publication and first release

Gated on human review of the prepared repositories. The order matters: with every
release deleted, the installers have nothing to fetch until v0.1.0 exists.

1. `gh repo edit rookery-ai/<repo> --visibility public` for both
2. Branch protection on `main` in both — required status checks matching the
   documented PR gates, required PR review, no force push
3. Enable private vulnerability reporting on both
4. **Cut v0.1.0**: merge the release-please PR, which tags the repo and fires
   `release.yml` — goreleaser publishes the archives, `.deb`/`.rpm`, checksums,
   cosign signatures and SBOMs, and buildx pushes the image to
   `ghcr.io/rookery-ai/rookery`
5. **Run `install.sh` and `install.ps1` end to end.** Publication is what
   activates `curl | sh` — release assets on a private repository require an
   authenticated request, so an anonymous download returns 404, which is why both
   installers name that case first in their failure text. This is the one path
   that cannot be tested before the flip, and the first thing a new user runs.
6. Verify `docker pull ghcr.io/rookery-ai/rookery:v0.1.0` anonymously
7. Verify cosign against the v0.1.0 artifacts using the new identity regexp
8. Cut `rookery-web` v0.1.0 the same way

## Risks

**The binaries remain in history and in PR refs.** Accepted deliberately: they
hold no credentials, only a `/home/rookie/go/pkg/mod/...` build path, and no
available mitigation actually removes them without discarding the pull request
history. Recorded here so nobody later reads "deleted the binaries" as "purged the
binaries."

**A window with no downloadable release.** Between Phase C step 1 and step 4 the
repository is public with zero releases, so the documented install commands 404.
Minutes, not days, and nobody is watching yet — but the steps must not be
reordered.

**release-please starting from a reset manifest.** The manifest says `0.0.0` while
the repository has no tags. This is the supported cold-start path, but it is worth
confirming the first release PR proposes `0.1.0` and not something else before
merging it.

**The App is unverifiable until a real run.** See B6. The old
`RELEASE_PLEASE_TOKEN` is deliberately retained until the App is proven, so there
is always one working path to cut a release.

**The website is unbuilt and undeployed.** `rookery.cloud` does not resolve, so
the `/install.sh` and `/install.ps1` redirects are inert until deployment. This is
pre-existing, not caused by the migration, and is noted so it is not mistaken for
migration breakage.

## Not doing

- Rewriting git history (see *A purge would not purge*)
- Deploying the website
- Moving `docs/superpowers/` to a private repository
- Renaming either repository
- Preserving any release, tag, package or changelog entry created under the
  personal account
