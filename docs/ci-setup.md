# CI setup — one-time repository configuration

These steps cannot be automated from within the repository. **The pipeline is
not fully functional until they are done.**

## 1. `RELEASE_PLEASE_TOKEN` secret

release-please needs a token that is NOT `GITHUB_TOKEN`. Pull requests opened
with `GITHUB_TOKEN` do not trigger other workflows, so merging the release PR
would tag the repository without ever running `release.yml` — producing a tag
with no artifacts attached to it.

1. Create a fine-grained PAT scoped to `ilijad1/simple-agents-v2`.
2. Grant it **Contents: read and write** and **Pull requests: read and write**.
3. Add it as the repository secret `RELEASE_PLEASE_TOKEN`.

**This is the only secret the pipeline needs.** GHCR authenticates with the
built-in `GITHUB_TOKEN`; cosign signs keylessly through GitHub's OIDC provider;
govulncheck, Trivy, gitleaks and CodeQL need no credentials. Do not add secrets
that have no consumer.

## 2. Branch protection on `main`

Require these status checks to pass before merging:

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

The package `ghcr.io/ilijad1/simple-agents-v2` is **private**. A host pulling it
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
