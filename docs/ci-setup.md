# CI setup — one-time repository configuration

These steps cannot be automated from within the repository. **The pipeline is
not fully functional until they are done.**

## 1. The release GitHub App

release-please authenticates as an **organization-owned GitHub App**. Two
constraints force this shape, and both are easy to "simplify" away:

- **It cannot be `GITHUB_TOKEN`.** A pull request opened with `GITHUB_TOKEN`
  does not trigger other workflows, so merging the release PR would tag the
  repository without ever running `release.yml` — producing a **tag with no
  artifacts attached and no error explaining why**.
- **It cannot be an org-owned PAT, because no such thing exists.** Every PAT
  belongs to a user account. A fine-grained PAT merely *scopes* to an
  organization while still being that person's, expiring on a calendar and
  dying if they leave. An App is owned by the organization outright, mints a
  short-lived installation token per run, and authors its pull requests as a
  bot rather than as a person.

**Creating it is a web-UI action — GitHub exposes no API for creating Apps.**

1. Go to `https://github.com/organizations/rookery-ai/settings/apps/new`.
2. Owner `rookery-ai`; homepage `https://rookery.cloud`.
3. **Uncheck "Active" under Webhook** — the App is never called, it only issues
   tokens.
4. Repository permissions: **Contents: read and write** (create the tag, commit
   the changelog) and **Pull requests: read and write** (open and update the
   release PR). Nothing else.
5. Set "Where can this GitHub App be installed" to **Only on this account**.
6. **Install it on BOTH `rookery` and `rookery-web`.** An App created but never
   installed on one of them fails the token mint with a permissions error that
   reads like a bad private key — check the installation before regenerating.
7. Generate a private key (a `.pem`, shown once) and note the numeric App ID.
8. Store both as **organization** secrets with selected-repository access to
   both repos: `ROOKERY_APP_ID` and `ROOKERY_APP_PRIVATE_KEY` (the entire
   `.pem`, including the `-----BEGIN`/`-----END` lines).

`gh secret list` showing the two names proves only that they exist — not that
the App is installed, that its permissions are right, or that the key matches.
**The only real verification is a release-please run**, which happens on the
next push to `main`.

**These are the only secrets the pipeline needs.** GHCR authenticates with the
built-in `GITHUB_TOKEN`; cosign signs keylessly through GitHub's OIDC provider;
govulncheck, Trivy, gitleaks and CodeQL need no credentials. Do not add secrets
that have no consumer.

## 2. Branch protection on `main` — ENABLED

**Enabled 2026-08-12, when the repository went public.** It had never been
possible before: the API returned `403 — Upgrade to GitHub Pro or make this
repository public` for the whole of the repository's private life, so the seven
documented gates were a discipline rather than a guarantee. Protection is free
on public repositories.

`main` now requires the status checks listed below, and blocks force-pushes and
deletion. `required_pull_request_reviews` is deliberately **null** and
`enforce_admins` **false**: a solo maintainer cannot approve their own pull
request, so requiring a review would block every merge.

If the repository is ever made private again, protection disappears with it
unless the organization is on a paid plan — the alternatives then are GitHub
Pro, or accepting the gap and not merging red PRs.

The required checks:

- `Conventional commit title`
- `Go build and test`
- `Cross-compile linux/amd64`
- `Cross-compile linux/arm64`
- `Cross-compile darwin/amd64`
- `Cross-compile darwin/arm64`
- `Cross-compile windows/amd64`
- `Cross-compile windows/arm64`
- `Frontend`
- `Security scan`
- `Container smoke test`
- `Package smoke test`

Also enable:

- **Require a pull request before merging.**
- **Squash merging only.** The PR title is what the conventional-commit lint
  validates and what release-please reads to compute the version; allowing merge
  commits would let unlinted commit messages reach `main`.

**Squash-merge is a plain repository setting, not branch protection**:
*Settings → General → Pull Requests* → allow only "Squash merging". It is what
keeps `main`'s history readable by release-please regardless of whether
protection exists, so it is worth pinning even though protection is now on.

## 3. CodeQL — ACTIVE

`codeql.yml`'s job is gated on `github.event.repository.private == false`, so it
**skipped on every run** for the repository's entire private life. Going public
activated it by itself, and `Analyze go` / `Analyze javascript-typescript` now
run on each pull request. Code scanning is free on public repositories; on
private ones it needs GitHub Advanced Security.

**Still outstanding:** restore the weekly `schedule:` trigger that was removed.
A schedule event does not reliably populate the repository payload, so the
visibility guard cannot be trusted there — the `if:` condition needs rethinking
before the schedule comes back, not just re-adding.

## 4. GHCR package visibility

**Done — `ghcr.io/rookery-ai/rookery` is public as of v0.1.0**, verified by an
anonymous pull. This section is kept because the sequence is not discoverable
and applies to any future package.

While a package is private, a host pulling it needs a one-time login:

```bash
podman login ghcr.io -u <github-username>
# password: a PAT with read:packages
```

**Making it public takes TWO steps, not one, and the first is not obvious.**
A GHCR package is a separate object from its repository and keeps its own
visibility — publishing the repository does **not** publish the package.

1. **Allow public packages at the ORGANIZATION level** —
   `https://github.com/organizations/rookery-ai/settings/packages` → *Package
   creation* → tick **Public**. A new organization ships with this **off**, and
   until it is on, the package's own visibility control is greyed out with
   "Setting is disabled by organization administrators." Nothing indicates
   which setting is responsible.
2. **Then change the package** —
   `https://github.com/orgs/rookery-ai/packages/container/package/rookery` →
   *Package settings* → *Danger Zone* → *Change visibility* → **Public**.

**Neither step has a REST API.** `PATCH /orgs/{org}/packages/container/{name}`
with a `visibility` field returns 404, and no package-policy field appears on
the organization object. Both are web-UI only.

**Verify with an ANONYMOUS pull**, because an authenticated one succeeds either
way and proves nothing:

```bash
podman logout ghcr.io
podman pull ghcr.io/rookery-ai/rookery:latest
```

This matters because `README.md` and the documentation site both advertise a
`docker run ghcr.io/rookery-ai/rookery:latest` command that fails with
`unauthorized` for everyone until it is done — while the repository looks fully
published.

Cosign signatures, SBOMs and provenance attestations are produced either way.

## 5. Expected timings

Measured locally on a 4-thread i5-6200U; GitHub runners are typically faster.

| Job | Duration |
|---|---|
| Go build and test (`-race`) | ~8 min (the `web` package alone is ~6 min) |
| Frontend | ~3 min |
| Cross-compile (each) | <1 min |
| Security scan | ~3 min |

`go test -timeout` is set to **900s**, not 600s: the `web` package measures
~343s under `-race` locally and a slower runner needs the headroom. Keep this in
sync with the `ci-test` target in the `Makefile`.

### A note on Actions minutes

A full PR run is ~12 jobs, dominated by the ~8-minute `-race` job and the
container build. On a **private** repo those minutes are metered against the free
monthly allowance.

This bit immediately after the first merge: Dependabot opened **ten** PRs at
once, queueing ~110 jobs. `dependabot.yml` now groups minor/patch updates per
ecosystem and caps open PRs at 3 each, which turns that into roughly one PR per
ecosystem per week. If minutes still run short, the next levers are
`interval: monthly` and dropping the container job to `push` on `main` only.

## The first release was tagged by hand

`v0.1.0` was created manually rather than by release-please, and this is the
reason the pipeline is now stable.

With **529 commits and no tags**, release-please had no anchor and walked the
entire history on every run, backfilling a file list per commit via the GitHub
GraphQL API. That produced two failures, repeatedly:

- The workflow died with `release-please failed: We couldn't respond to your
  request in time.` — the API timing out mid-walk. Intermittent, and worsening
  as history grew.
- When it did succeed, it generated a **482-line changelog / 456 entries**
  covering every pre-release commit.

Two config attempts did not fix it. `bootstrap-sha` is scoped by the schema to
"the initial release of a library", and `.release-please-manifest.json` declared
`"." : "0.0.0"` — a release, as far as release-please is concerned — so the
option never fired. `last-release-sha` applies to "any release" and should have
worked, but the underlying history walk still timed out before it mattered.

Tagging `v0.1.0` by hand removed the cause rather than working around it: with a
real tag present, release-please anchors on it and scans only the commits since,
so runs are fast and each changelog contains only that release's work. No
`bootstrap-sha` or `last-release-sha` pin is needed, and both have been removed.

`.goreleaser.yaml` sets `changelog: disable: true`, so the manual tag published
artifacts without a duplicate commit dump in the GitHub Release body.

### The organization migration recreated this exact condition — read before releasing

Moving to `rookery-ai` **deleted every tag** (nothing built under the personal
account was transferred) and **reset the manifest to `0.0.0`**. That is precisely
the state described above, and worse: there are now **1035 commits**, against the
529 that first triggered it.

So the first release under the organization must be **tagged by hand again**, for
the same reason and with the same effect:

```bash
git tag v0.1.0 && git push origin v0.1.0
```

That fires `release.yml` directly. release-please then anchors on the real tag and
scans only what follows.

**Do not wait for release-please to propose `v0.1.0`.** With no tag to anchor on
it walks all 1035 commits through the GraphQL API on every run — the failure mode
is an intermittent `We couldn't respond to your request in time`, and when it does
succeed, a changelog covering the entire history. Neither `bootstrap-sha` nor
`last-release-sha` fixes it; both were tried (#58, #59) and both failed, for the
reasons recorded above.

### Releasing from here

Normal release-please flow, no manual steps — **once the anchor tag above exists**:

1. Merge a feature PR (Conventional Commit title).
2. release-please opens/updates a release PR computing the next version.
3. Merge that PR → it creates the tag → `release.yml` fires goreleaser and the
   GHCR image push.

## Tag format: `include-component-in-tag` must stay `false`

`release-please-config.json` sets `include-component-in-tag: false`. This is
load-bearing, not cosmetic.

The option **defaults to `true`**, which makes release-please tag releases
`rookery-v0.1.0` — the package name, then the version. But
`.github/workflows/release.yml` triggers on `tags: ["v*"]`, which that string
does not match. Left at the default, merging a release PR would create a tag
that **fires no workflow at all**: no binaries, no packages, no image. The
release would silently produce nothing.

It also breaks changelog scoping in the other direction. Because release-please
looked for `rookery-v0.1.0` and the real tag is `v0.1.0`, it could not find
any previous release, fell back to walking the entire history, and proposed
`0.2.0` with a 489-line changelog — after `v0.1.0` had already shipped. This was
the actual cause of the "482-line changelog" symptom that `bootstrap-sha` (#58)
and `last-release-sha` (#59) were both aimed at and both failed to fix.

If the tag format is ever changed, `release.yml`'s trigger has to change with it.
They are two halves of one contract.

