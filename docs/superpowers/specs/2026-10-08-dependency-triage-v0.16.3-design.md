# Dependency triage and the v0.16.3 release

Date: 2026-10-08

## Goal

Merge every open pull request that can land without breaking anything, then cut
one release containing all of them.

## What was open

Six Dependabot pull requests:

| PR | Change | Why it matters |
|---|---|---|
| #328 | undici → 8.10.2 | Dependabot alert #40 (high): cross-origin cache poisoning |
| #327 | ip-address → 10.7.2 | Dependabot alert #39 (medium): NAT64 local-use range bypasses SSRF classification |
| #329 | Go minor/patch group (4 updates) | routine |
| #330 | npm minor/patch group (26 updates) | routine |
| #324 | @types/node 24 → 26 | major, type-only, dev-only |
| #323 | @testing-library/jest-dom 6 → 7 | major, test-only |

## The blocker none of them caused

#329 and #330 failed `Security scan`. The finding was not in either diff: Trivy
scans the whole tree, and `main` carried `source-map-js` 1.2.1, which became
CVE-2026-93749 (high, denial of service via malformed indexed source maps,
fixed in 1.2.2) after the other four pull requests had last run CI. Those four
were green only because they ran before the advisory was published; rebased,
they would have failed identically. No Dependabot alert or pull request covered
it, and #330's group did not bump it.

#330 also failed `Container smoke test`, on a transient `proxy.golang.org`
stream error during `go mod download` — infrastructure, not the change.

## Approach

1. **Fix the blocker first** — this pull request: `npm update source-map-js` in
   `web/ui`, a lockfile-only change (1.2.1 → 1.2.2, transitive, build-time).
   Merging it first turns `Security scan` green for every other pull request.
   Being a `fix:` commit it is also what lets release-please open a release pull
   request at all: a batch of `chore(deps)` commits alone produces none.
2. **Merge the Dependabot pull requests serially.** `main` requires branches to
   be up to date, and auto-merge does not update a branch, so each one is
   rebased (`@dependabot rebase`, which also regenerates its lockfile), waits for
   the eight required checks on the new head, then squash-merges. Security fixes
   first: #328, #327, then #329, #330, #323, #324. A pull request that fails for
   a real reason after rebase is left open and reported rather than forced.
3. **Release once.** Merge the release-please pull request (v0.16.3) after all
   of the above, then rewrite the GitHub Release body with `gh release edit`:
   release-please hides `chore` sections, so its generated notes would list only
   this fix and none of the dependency or security work.

## Verification

- This change: `make ci-ui` (npm ci, tsc, oxlint, vitest — 1311 tests pass).
- Every merge: the eight required checks green on the exact head being merged.
- Release: the tag's `release.yml` run succeeds and the release lists every
  merged change.

## Out of scope

`fix/backup-close-errors` and `fix/container-base-cves` exist on the remote but
were squash-merged long ago (#191, #212); they are stale branches, not pending
work.
