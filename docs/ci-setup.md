# CI setup — one-time repository configuration

These steps cannot be automated from within the repository. **The pipeline is
not fully functional until they are done.**

## 1. `RELEASE_PLEASE_TOKEN` secret

release-please needs a token that is NOT `GITHUB_TOKEN`. Pull requests opened
with `GITHUB_TOKEN` do not trigger other workflows, so merging the release PR
would tag the repository without ever running `release.yml` — producing a tag
with no artifacts attached to it.

1. Create a fine-grained PAT scoped to `ilijad1/rookery`.
2. Grant it **Contents: read and write** and **Pull requests: read and write**.
3. Add it as the repository secret `RELEASE_PLEASE_TOKEN`.

**This is the only secret the pipeline needs.** GHCR authenticates with the
built-in `GITHUB_TOKEN`; cosign signs keylessly through GitHub's OIDC provider;
govulncheck, Trivy, gitleaks and CodeQL need no credentials. Do not add secrets
that have no consumer.

## 2. Branch protection on `main` — NOT AVAILABLE ON THIS PLAN

**Verified 2026-07-28:** the API returns
`403 — Upgrade to GitHub Pro or make this repository public`. Branch protection
requires GitHub Pro (or an org plan) for a **private** repository; it is free on
public ones. Nothing to configure today.

The checks still **run** on every PR — they are simply not **enforced**, so a
red PR *can* be merged. Until one of the following is true, the gate is a
discipline, not a guarantee:

- go public at launch (protection becomes free, and CodeQL starts working too —
  see §3), or
- subscribe to GitHub Pro, or
- accept the gap and just don't merge red PRs.

When protection does become available, require these checks:

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

Also enable:

- **Require a pull request before merging.**
- **Squash merging only.** The PR title is what the conventional-commit lint
  validates and what release-please reads to compute the version; allowing merge
  commits would let unlinted commit messages reach `main`.

**Squash-merge can be set today** — it is a plain repository setting, not branch
protection: *Settings → General → Pull Requests* → allow only "Squash merging".
Worth doing now, because it is what keeps `main`'s history readable by
release-please regardless of whether protection exists.

## 3. CodeQL is dormant until the repo is public

`codeql.yml` is committed and correct, but its job is gated on
`github.event.repository.private == false`, so it **skips** today. Code scanning
on a *private* repository requires GitHub Advanced Security (a paid
per-committer add-on); on a *public* repository CodeQL is free.

Nothing to do now — it activates by itself when the repo goes public. At that
point, restore the weekly `schedule:` trigger that was removed (a schedule event
does not reliably populate the repository payload, so the visibility guard
cannot be trusted there).

If you *do* want CodeQL while private, buy GHAS and enable
**Settings → Code security → Code scanning**, then delete the `if:` line.

## 4. GHCR package visibility

The package `ghcr.io/ilijad1/rookery` is **private**. A host pulling it
needs a one-time login:

```bash
podman login ghcr.io -u <github-username>
# password: a PAT with read:packages
```

Making it public at launch is a visibility toggle in the package settings — no
pipeline change is required. Cosign signatures, SBOMs and provenance
attestations are produced either way.

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

### Releasing from here

Normal release-please flow, no manual steps:

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

