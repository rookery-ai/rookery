# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Tenancy model: single-owner, multi-workspace

The platform has **one owner** (the installer; a single row in the `owner` table) who logs in and
manages **workspaces**. A **workspace** is a fully isolated tenant — its own vault, claude-home,
secrets, agents, connector, and inlined coder config — and replaces the old per-user account.
Workspaces have no login of their own: the owner **enters** a workspace by typing that workspace's
**master password** (re-entered on every switch). The web session is two-level: `owner_id`
(logged in) + `active_workspace_id` (entered). All tenant-scoped tables key off `workspace_id`.
Bootstrap the owner with `rookery owner bootstrap -u <name> -p <pw>`.

Terminology map (fully renamed throughout): user → **workspace**, admin → **owner**,
`user_id` → `workspace_id`, `db.User` → `db.Workspace` (+ new `db.Owner`).

## Commands

```bash
# Build
go build -o bin/rookery ./cmd/rookery

# Run all tests
go test ./... -count=1 -timeout 120s

# Run a specific package's tests
go test -v ./internal/agentdesigner/... -run TestFlow

# Run the server (after build)
./bin/rookery serve

# Guided first-run setup (keys, owner, host tools, coder report, systemd unit)
./bin/rookery onboard
./bin/rookery onboard --non-interactive   # report what to do, never prompt or act

# Bootstrap the owner account (first run only)
./bin/rookery owner bootstrap -u <username> -p <password>

# Reset the owner password (single-owner model, no login required)
./bin/rookery owner reset-password -p <new-password>

# Migrations are applied automatically when the database is opened —
# there is no separate migration command.

# Deploy / restart the server (build + run in background, logs to logs/server.log)
make deploy    # stop existing server, rebuild, start in background
make restart   # stop + start (no rebuild)
make stop      # stop the running server
make logs      # tail -f logs/server.log
make status    # show running server process
make test      # run the unit tests
make ci-fmt / ci-vet / ci-ui / ci-docs   # targeted checks, seconds each
               # There is no aggregate `ci` target — see the CI/CD section.
make docker-build / docker-run   # slim container image (podman or docker)

# Frontend (web/ui): build the SPA into the binary
make ui        # npm ci + vite build → web/ui/dist (embedded on next go build)
make build     # ui + go build (full artifact); make build-go for Go-only
# Dev loop: cd web/ui && npm run dev  (Vite on :5173, proxies /api to :8080)
```

AST guardrail tests shell out to `python3`. If Python is not available, those tests self-skip.

> **Deploy workflow:** When the user says "restart the server", "rebuild", or
> "deploy", run `make deploy` — it stops the running server, rebuilds, and
> starts it in the background with logs captured to `logs/server.log`. The
> server listens on `0.0.0.0:8080` by default (override host with `ROOKERY_HOST=…`, port
> with `ROOKERY_PORT=…`). Set `ROOKERY_PUBLIC_URL` to the externally-reachable base URL so OAuth
> callbacks are correct. `rookery connector exec <tool> --args '<json>'` is the
> subcommand CLI coders use to reach the connector bridge (not for manual use).
> The UI is the embedded React SPA served at `http://host:8080/` (build it into the
> binary with `make ui` before `go build`; `/app` + `/app/*` 301-redirect to `/`).
> Verify with `make status` / `make logs`; smoke-test the SPA + API with
> `curl -sS http://127.0.0.1:8080/` (200 HTML) and
> `curl -sS http://127.0.0.1:8080/api/v1/auth/session` (200 JSON).

## Git workflow

- **Always branch, never commit directly to `main`.** All work happens on a
  feature branch off `main`. When the work is finished, open a **pull request**
  back into `main` — `main` only ever advances through merged PRs.
- **Conventional Commits.** Structure every commit message as
  `type(scope): summary` (e.g. `feat(gateway): …`, `fix(web/chat): …`,
  `refactor(vault): …`, `docs: …`). Types: `feat`, `fix`, `refactor`, `docs`,
  `test`, `chore`, `perf`, `build`, `ci`. Scope is optional but preferred.
- **Deploy from `main` for production** — only after the work is finished and
  merged. `make deploy` on `main` is the production path.
- **Local branch deploys are fine for testing.** When a development phase or a
  group of tasks is complete and needs to be exercised on the running server,
  it's OK to `make deploy` from the feature branch locally before the PR merges —
  that's for testing, not production.

## Documentation sync

Four surfaces describe this project and each can be wrong without anything
failing: `README.md`, `CLAUDE.md`, the documentation site and the landing page
(both in `rookery-ai/rookery-web`, checked out at `~/rookery-web`).

**Before opening a pull request, use the `docs-sync` skill.** It holds the
change-to-page trigger map and the cross-repository procedure. A change that
alters a connector provider, a `ROOKERY_*` variable, a CLI subcommand, a core
skill, a chat adapter, a backup destination, an `/api/v1` route or a packaging
target has a documentation obligation in both repositories.

`make docs-sync-check` mechanises the checkable half — counts, variable
names, command names, provider names, logo coverage — against the source
rather than against other prose, and runs as `make ci-docs`; it
resolves the website checkout via `ROOKERY_WEB_DIR`, then a sibling of the
main checkout derived from git's common dir, then `~/rookery-web`, and skips
website assertions when none of those exist — set `ROOKERY_WEB_DIR` to point
it at an unmerged website branch from inside a worktree. It does not check
whether a paragraph describes a feature correctly.

Verify every claim against source, never against another document. The
provider count in `README.md` once drifted for months because it was copied
forward instead of measured.

**Three of its assertions exist because a real command can be the wrong one.**
`check_cli` only ever asked whether the documentation invokes something the
source lacks, so `serve` — a genuine command — passed every gate for as long as
every installation page told new users to run it instead of `rookery onboard`,
and `upgrade`/`uninstall` shipped undocumented because adding a command broke
nothing. So: `check_cli_coverage` asserts every top-level command in
`cmd/rookery` has a `## ` section in `reference/cli.md` (read from main.go's
root `Commands:` slice and each constructor's first literal `Name:` **after**
its `return &cli.Command{` — a constructor may hoist a shared flag first, and
`backupCommand` does, which is how an early draft reported a command called
`dir`; a command named by a *constant* rather than a literal is exempt, which
is exactly and only the `Hidden: true` sandbox helper).
`check_install_pages_onboard` requires every installation page to name
`rookery onboard`, stated positively rather than as a ban on `owner bootstrap`
because that command is still legitimately documented as the scriptable
alternative — `docker.md` is the one exemption, since `docker run -d` has no
interactive terminal to onboard from. `check_windows_winget_ids` reads the
winget ids out of `install.ps1`'s own host-tool table and requires the Windows
page to show exactly those: the page had offered `Python.Python.3.12` against
the installer's `3.13` and claimed Poppler had no winget package while the
installer was installing `oschwartz10612.Poppler`. Comparing the page against a
second list in the checker would only have proved the two lists agree.

## CI/CD and release process

**Every change ships through this path. There are no manual tags and no manual
image pushes.**

1. **Branch** off `main`. Never commit to `main` directly.
2. **Commit** using Conventional Commits (`type(scope): summary`).
3. **Open a PR.** Its **title** must itself be a valid Conventional Commit —
   merges are squashes, so the title becomes the commit that lands on `main` and
   is what release-please reads to compute the next version.
4. **PR checks must pass** (`.github/workflows/pr.yml`, seven jobs):
   - `Conventional commit title`
   - `Go build and test` — gofmt, `go vet`, `go test -race` (**900s timeout**,
     not 600s: the `web` package alone measures ~343s under `-race`, 13× its
     non-race time)
   - `Cross-compile` — all six GOOS/GOARCH pairs. **This is the guard that keeps
     `GOOS=windows` compiling**; it was broken for the repo's entire history
     precisely because nothing ever built it.
   - `Frontend` — `npm ci`, `tsc -b`, `oxlint`, `vitest`, `vite build`
   - `Security scan` — govulncheck, Trivy (fs), gitleaks. CodeQL runs in its own
     workflow because it needs `security-events: write`.
   - `Container smoke test` — builds the image, Trivy-scans it, runs it, and
     asserts `/healthz`, the SPA root and the session endpoint all answer. **One
     of the project's two end-to-end gates** — this one covers the container
     image.
   - `Package smoke test` — builds a goreleaser snapshot, then installs the
     **rpm** (in a Fedora container), the **deb** (in a Debian container) and
     extracts the **tar.gz**, running `owner bootstrap` + `serve` + `healthcheck`
     from a working directory unrelated to the source tree. **The project's other
     end-to-end gate** — this one covers the native deb/rpm/tar.gz artifacts;
     nothing had ever installed one before it existed, which is exactly how they
     shipped unable to open their own database. Run it locally with
     `make ci-package` — it takes minutes, because a snapshot rebuilds the SPA
     and all six binaries.
5. **There is no aggregate `ci` target, and reproducing the pipeline locally is
   not a step.** The gate runs here, on push. The removed target took ~15
   minutes (the `web` package alone measures ~343s under `-race`, 13× its
   non-race time) and every one of those minutes was spent again on the runner.

   It also never covered four of the seven jobs: `Conventional commit title`
   (needs the pull request, not anything runnable locally), `Security scan`,
   `Container smoke test` and `Package smoke test`. So a green local run never
   meant a green PR — which is the real reason it is gone, not merely the
   duration. Nothing in `.github/workflows` invokes `make`; each job runs its
   own steps, so removing the target changed no gate.

   Run the **targeted** pieces for the code you actually touched:
   `make ci-fmt` / `ci-vet` / `ci-ui` / `ci-docs` are seconds each, and
   `go test ./internal/<pkg>/` is the fast inner loop. Those catch the
   formatting and obvious-breakage class of failure without paying for a
   full-matrix run. The documentation check (`ci-docs`) runs as a step inside
   the `Go build and test` job rather than as a job of its own, so the gate
   count stays at seven.

   **The one exception is a stacked pull request** — one based on another branch
   rather than `main` runs zero checks, so there the local targets are the only
   signal available before the base merges.
6. **Squash-merge.** release-please then maintains a release PR on `main`.
7. **Merging the release PR** tags the repo, which fires
   `.github/workflows/release.yml`: goreleaser publishes binaries, `.deb`/`.rpm`,
   checksums, cosign signatures and SBOMs, and buildx pushes the multi-arch
   image to GHCR.

Versioning starts at **v0.1.0** with `bump-minor-pre-major`, so a breaking
change bumps the minor while the project is pre-1.0. Reaching 1.0.0 is a
deliberate act at public release.

**Versioning restarted at v0.1.0 when the project moved to the `rookery-ai`
organization.** Every release, tag and container image built under the previous
personal account was **deleted rather than transferred**, so nothing shippable
predates the organization — and `.release-please-manifest.json` was reset to
`0.0.0` so release-please computes from an empty history rather than from tags
that no longer exist.

**Secrets:** release-please authenticates as an **organization-owned GitHub App**
(`ROOKERY_APP_ID` + `ROOKERY_APP_PRIVATE_KEY`, stored once as org secrets and
shared with both repositories), not as a personal access token — GitHub has no
org-owned PAT, and a fine-grained one merely *scopes* to an org while still
belonging to a user, expiring on a calendar and dying if they leave. The workflow
mints a short-lived installation token per run via `actions/create-github-app-token`.

It still cannot be `GITHUB_TOKEN`: a pull request opened with it does not trigger
other workflows, so merging the release PR would create a **tag with no artifacts
attached and no error explaining why**. See `docs/ci-setup.md`. GHCR authenticates
with the built-in `GITHUB_TOKEN`, cosign signs keylessly via OIDC, and the
scanners need no credentials. **Do not add secrets that have no consumer.**

## Distribution

The project is **Rookery** (`github.com/rookery-ai/rookery`); the binary, module and
package are all lowercase `rookery`, and every environment variable is prefixed
`ROOKERY_`. The project domain is **rookery.cloud** — it is the documented
`ROOKERY_PUBLIC_URL` example because OAuth providers reject redirect URIs on
non-public hostnames, so a `.lan` address fails Google's validation outright.

**Native binaries are the primary artifact**; the container image is secondary.

| Target | Sandbox | Service | Tier |
|---|---|---|---|
| linux amd64/arm64 | Landlock | systemd **user** unit + `enable-linger` | 1 |
| container (linux) | Landlock (verified ABI 8 under rootless Podman) | runtime-managed | 1 |
| darwin amd64/arm64 | **none** | launchd **user** agent (at login, not boot) | 2 |
| windows amd64/arm64 | **none** | Task Scheduler logon task | 2 |

**Off Linux there is no filesystem sandbox at all** — `sandbox.Supported()`
returns false and callers do not wrap, so coder subprocesses run unconfined.
`/healthz` and the startup log both report this.

**One-command installers ship at the repository root** — `install.sh` (POSIX sh,
Linux + macOS) and `install.ps1` (Windows). Each does exactly one job: fetch the
goreleaser archive for the detected platform, verify it against the release's
`checksums.txt`, put the binary on `PATH`, offer the four host tools, and hand
off to `rookery onboard`. Configuration lives in Go, not in two shell dialects.
A Homebrew tap remains deferred.

**macOS autostart is a launchd USER AGENT, not a launch daemon, and it starts at
login rather than at boot.** The mechanism is decided by the same constraint that
chose a Task Scheduler logon task on Windows and a systemd *user* unit on Linux:
the server's data lives under the user's own profile, so anything running as
another principal cannot reach it. A LaunchDaemon in `/Library/LaunchDaemons`
does start at boot, but it needs administrator rights to install and runs as
root — reintroducing exactly that problem.

**The accepted cost is stated rather than engineered around: a headless Mac that
reboots does not start Rookery until someone signs in.** Linux gets boot-start
from `loginctl enable-linger`; launchd has no equivalent for an agent. The macOS
installation page and `rookery service install` both say so.

**Four plist keys are load-bearing, and for three of them the obvious spelling is
wrong** (`internal/onboard/launchd.go`, generated against the running binary for
the same reason `UnitFileFor` is). `KeepAlive` is a dict with
`SuccessfulExit=false`, never a bare `<true/>`: bare true restarts the server
after a CLEAN exit too, so a deliberate stop — or `rookery uninstall` — brings it
straight back and it cannot be stopped without unloading the agent; the dict form
mirrors the systemd unit's `Restart=on-failure`. `StandardOutPath` and
`StandardErrorPath` are mandatory because launchd has no journal, so a job that
names no file has its output discarded entirely — and their directory must
already exist, since launchd cannot create it and a job whose redirect target is
missing **fails to spawn**, reporting it only to the system log (so from the
outside the install looked fine and the server simply never started).
`PATH` is set explicitly because a launchd-started process inherits a minimal one
containing neither `/opt/homebrew/bin` nor `/usr/local/bin` — the same trap
`coder.coderSearchDirs` records from the other direction, except that detection
can search harder while anything that later shells out cannot. And `ProcessType`
is deliberately **not** `Background`, which would let launchd throttle CPU and
I/O on a process serving HTTP and firing scheduled runs.

`installAutostart` **boots the agent out before bootstrapping it**, ignoring the
failure: `bootstrap` refuses a label that is already loaded, so reinstalling over
an existing agent — an upgrade, or a changed data directory — would otherwise
fail with "service already loaded" and leave the OLD plist running while the new
file sat on disk unused. It then calls `launchctl enable` separately, because an
agent the user previously disabled stays disabled across a bootstrap and the
install would otherwise appear to succeed and start nothing. None of this runs
here — there is no macOS host — so like the Windows half it is authored,
unit-tested for the generated document's content, and checked by the
cross-compile gate.

**Windows autostart is a Task Scheduler logon task, not an SCM service, and
`rookery service` is what registers it.** Windows had no autostart at all: the
installer finished, the machine was restarted, and nothing came back — silently,
because nothing had ever been registered. The mechanism is decided by one
constraint: it must work for a standard non-administrator, with no stored
credentials, and reach a data directory under the user's own profile. An SCM
service needs administrator rights and then runs as a different principal, which
reintroduces exactly the problem the Linux side avoids by using a systemd **user**
unit; relying on `S4U` would need a batch-logon right a standard user may not
hold. The accepted cost is a visible console window — every way of hiding it
trades a cosmetic problem for a credential prompt or an elevation requirement.

**Four Task Scheduler defaults are wrong for a long-running server and all four
fail silently**, which is why `TaskXMLFor` sets them explicitly and a test pins
each: `DisallowStartIfOnBatteries` defaults **true**, so on a laptop — the machine
this project documents as its common case — the task usually would not start at
all; `StopIfGoingOnBatteries` defaults **true**, killing the server when the
charger is unplugged; `ExecutionTimeLimit` defaults to **72 hours**, after which
the task is terminated with no error; and `MultipleInstancesPolicy` decides
whether a second server is started that cannot bind the port. The XML is written
**UTF-16 with a BOM** because the declaration says UTF-16 — schtasks rejects a
mismatch with an "incorrectly formatted" error that names nothing useful — and
paths and account names are XML-escaped, since `&` is legal in both.

**`ServiceSupport.Restart` is a field, not a string built at the call site.**
`rookery upgrade` hardcoded `systemctl --user restart` behind `if svc.Managed`,
which was correct only while Linux was the sole managed platform; the moment
Windows became one, the same branch would have told a Windows operator to run a
command that does not exist — the precise bug the comment at that call site
records having already fixed once for macOS and Windows.

**The installers ASK and Go registers, and that boundary is load-bearing.**
External dependencies (python3, ripgrep, Poppler, Tesseract) are ordinary OS
packages and both installers install them directly through the host's package
manager. Autostart is Rookery's own configuration — it means generating a unit or
a task document against the binary's real path — so it lives in Go, where it is
tested once and also serves the operator who installed from an rpm, a deb or a
tarball and never ran a script. `packaging/autostart_test.go` fails if either
script starts writing a unit or calling `schtasks` itself, because the tempting
fix when something misbehaves is to inline it into the shell, where nothing can
exercise it — `install.ps1` is not even syntax-checked here.

**Everything after first install is a Go subcommand, not a third and fourth shell
script.** `rookery upgrade` and `rookery uninstall` follow the rule above: the
installers' job is identical in both dialects, but removal has to decide what a
package manager owns, what a service manager owns, and what is unrecoverable user
data — and `install.ps1` cannot even be syntax-checked on the development host, so
two more PowerShell files would double a surface nothing verifies. Four things are
load-bearing:

- **`onboard.OwnerOf` gates both commands.** `rm /usr/bin/rookery` under a deb or
  rpm leaves the package database claiming a file that is gone, repairable only by
  a `reinstall` nobody thinks to run, because from the outside the uninstall looked
  fine. So both commands ask `rpm -qf` / `dpkg -S` first and hand over the package
  manager's own command instead. Its failure policy is deliberately the opposite of
  the one that feels safe: **an inconclusive probe reports NOT managed**, because
  assuming managed would make uninstall impossible for archive and `install.sh`
  users — the majority, and the only ones with no package manager to fall back on.
  `Runner` is injectable for the same reason `LookPath` is: the hosts this logic is
  about are not the host it is developed on.
- **`--purge` asks for the data directory typed back, not `y`.** The risk is
  deleting a directory the user did not realise was live, and one keystroke cannot
  distinguish "I read that path" from "I pressed y". The prompt names `system.key`
  explicitly — the same fact the config data-dir warning must state, for the same
  reason: it is the one thing here a backup of the database cannot recover.
- **`upgrade` replaces the binary by rename into the same directory.** Rename is
  atomic only within a filesystem, and `/tmp` is frequently a different one, so the
  temporary file is created beside the target. That is also why no rollback copy is
  kept: the failure this produces already *is* "the old binary is still there". It
  then reports the version the binary on disk claims, rather than the one it meant
  to install — an upgrade that silently left the old process serving is the failure
  worth spending a check on.
- **That rename is POSIX-only, and assuming otherwise made `upgrade` impossible
  on Windows.** Windows holds a running executable with a share mode denying
  delete, so renaming over it fails — and `upgrade` is *always* replacing the
  image it is itself executing from, so this was never a matter of stopping the
  server first: the upgrade process is the lock. `swapBinary` is therefore
  per-platform (`swap_unix.go` renames straight over; `swap_windows.go` moves
  the target aside to `<binary>.old`, installs, and restores on failure so the
  outcome still can never be "neither binary"). The displaced file cannot be
  deleted while this process runs it; the NEXT upgrade clears it. `removeSelf`
  splits for the same reason and returns a **caveat string** the caller prints,
  because on Windows `uninstall` moves the binary aside rather than deleting it
  — reporting a clean removal while leaving a stray `.old` nobody was told about
  is the quiet half-success this command exists to avoid. The old failure
  message advised re-running "with the privileges that installed it", which on
  Windows is the wrong diagnosis entirely. `upgrade`'s closing line also printed
  `systemctl --user restart` on all three platforms; it now asks
  `onboard.CurrentService()` and names the foreground command where there is no
  service. None of the Windows half is exercised on a real host — the
  cross-compile gate is what checks it.
- **`extractBinary` selects the member BY NAME.** The archive arrives over the
  network and its contents are about to run as the user, so "the first file" or
  "the biggest one" would be a substitution primitive. `internal/release` is the
  reference implementation of resolve/name/verify; the two shell installers cannot
  import it, so `release_test.go` reads them and asserts all three build the same
  archive name.

**`curl | sh` works — the repository is public and `v0.1.0` is released.** It
could not before: release assets on a private repo require an authenticated
request, so an anonymous download returned `404`, not `401`.

**Both installers used to lead their 404 message with that private-repo case**,
which had become the one cause it cannot be. Corrected in 2026-08: the message
now leads with the platform case (the only one the script can name precisely,
since it already knows the tag and the OS/arch it asked for), then the network
and the draft-release cases.
`TestInstallersDoNotBlameAPrivateRepositoryForA404` pins it — the wording is
otherwise unreachable by any check, and it drifted once already.

**`install.ps1`'s failure path throws; it must never `exit`.** The script is
advertised as `irm … | iex`, which runs it in the CALLER's session rather than a
child scope, so `exit` terminates the whole PowerShell session — closing the
window and taking the error text with it, at exactly the moment the user needs
to read it. A checksum mismatch was the worst case: the refusal worked and the
explanation vanished. `TestWindowsInstallerDoesNotExitTheCallersSession` bans
the pattern. Relatedly, `iex` cannot pass arguments at all, so `-Version` and
`-BinDir` are unreachable through the advertised one-liner; the script block
idiom (`& ([scriptblock]::Create((irm …))) -Version v0.2.0`) is documented in the
file itself and pinned, because a parameter nobody can reach reads as supported.

`packaging/scripts_test.go` pins what breaks silently — that both files exist at
all (the website advertised them for the repo's whole life while neither did),
that both build goreleaser's `rookery_<version>_<os>_<arch>` archive name and
strip the tag's leading `v`, that all four host tools appear on all four delivery
surfaces, and that both refuse a checksum mismatch. Neither script can be
executed in CI, and `install.ps1` is not even syntax-checked — there is no
PowerShell on the development host. That is a real gap, recorded as one.

**The nfpms `recommends` list must be declared per format.** A single list
shipped the Debian spelling to both, and Fedora has no `tesseract-ocr` — its
package is `tesseract`. `dnf` drops a weak dependency it cannot resolve **without
saying anything**, so the rpm installed no OCR for its entire life and produced
no error to explain it. `scripts/smoke-package.sh` now reads the declared names
back off the built artifact (`rpm -qp --recommends`, `dpkg-deb -f … Recommends`)
*and* resolves each against the distribution's own metadata — the second half is
what catches a plausible name for a package nobody publishes. Reading from the
artifact rather than from `.goreleaser.yaml` is deliberate: a test that parses the
config it is checking only proves the YAML says what the YAML says.

Release artifacts (`.goreleaser.yaml`): six binary archives, `.deb`/`.rpm`
carrying the systemd user unit, `checksums.txt` + cosign keyless signature, and
an SBOM per archive.

### Container

```bash
make docker-build           # honours podman or docker, whichever is installed
make docker-run             # port 8080, data in the rookery-data volume

podman run -d --name rookery -p 8080:8080 \
  -v rookery-data:/data ghcr.io/rookery-ai/rookery:latest
```

The image is **slim**: it contains no CLI coder binary and sets
`ROOKERY_CODER_MODE=slim`, so workspaces must use the `api` coder kind. It does ship
python3, ripgrep, poppler-utils and tesseract, so `/healthz` reports no
capability warnings inside it. ~270 MB.

Two container notes worth knowing: **Podman ignores `HEALTHCHECK`** unless built
with `--format docker` (Docker/buildx honours it), and the image no longer
copies `migrations/` beside the binary — the SQL is embedded (root `migrations`
package, `//go:embed *.sql`), so the container and the native binaries run the
identical code path. That copy existed to satisfy an exe-relative lookup which
made the deb, rpm and every archive fail on first use with `read migrations
dir`; embedding removed the lookup and the whole class of bug. `//go:embed`
fails the build when it matches nothing, so a missing migration set can no
longer reach a user.

### Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `ROOKERY_HOST` | `0.0.0.0` | bind address; `127.0.0.1` for loopback-only |
| `ROOKERY_PORT` | `8080` | listen port |
| `ROOKERY_DATA_DIR` | `~/.rookery` | data root; also relocates the DB |
| `ROOKERY_SESSION_KEY` | generated, then pinned to `<data_dir>/session.key` | hex 32-byte session key |
| `ROOKERY_PUBLIC_URL` | — | externally reachable base URL for OAuth callbacks; validated at use (`internal/publicurl.Normalize`) and overridden by the instance URL in owner settings |
| `ROOKERY_SANDBOX` | `1` | `0`/`false`/`off` disables Landlock confinement |
| `ROOKERY_BROWSER_ALLOW_PRIVATE` | `0` | `1`/`true`/`on` lets the headless browser reach private/loopback space. Parsed as an opt-IN (the mirror of `ROOKERY_SANDBOX`'s opt-out) so an unrecognised value leaves the guard ON |
| `ROOKERY_CODER_MODE` | `full` | `slim` removes the local CLI coder kind entirely |
| `ROOKERY_CODER_BIN` | `claude` | default coder binary for workspaces that have not set `coder_bin` |

**Relocating the data dir must carry the database with it, and the config file
used not to.** `defaults()` computes `Database.Path` from the data dir, but only
`applyEnv` recomputed it — so `ROOKERY_DATA_DIR` moved everything while a
`config.yaml` `data.dir` moved the vaults, claude-homes, backups and *both keys*
and left the database at `~/.rookery/rookery.db`. The relocated dir then
generated its own `system.key`, so every stored master password, OAuth token and
bot token in that database — encrypted under the previous key — silently stopped
decrypting, on a server that still booted and still reported `ok`. `Load` now
derives the path after the yaml merge, deciding "did the file set this?" by
parsing it a **second time into a zero-valued `Config`**: comparing the merged
result against the defaults cannot tell *unset* from *the user typed the default*,
and getting that backwards would override a `database.path` chosen deliberately.
An explicit `database.path` still wins over `data.dir`; `ROOKERY_DATA_DIR` still
wins over both, because env-over-file is the ordinary precedence and the variable
is documented as relocating the database too. `Config.Warnings` reports a database
left stranded at the old default (a warning, not a refusal — a fresh install may
have an unrelated `~/.rookery`), and `Load` logs it itself rather than leaving it
to its four call sites, since a fifth that forgot would be the same drift-between-
two-copies that caused the bug.

**That warning's remediation wording is load-bearing, and the obvious phrasings
are wrong.** It must say to move the **whole data directory**. "Move the database
to the new path" and "set `database.path` back to the old one" both sound correct
and both reproduce the undecryptable-secrets failure, because
`secrets.SystemKey` reads `<dataDir>/system.key` and never follows
`Database.Path`: under the first the database arrives beside a *different* key,
under the second the data dir — and therefore the key — is still the relocated
one. Only moving the directory intact, or not relocating, keeps a database with
its key. `TestStrandedDatabaseWarningSaysToMoveTheWholeDataDir` pins it, because
a plausible-sounding rewrite is exactly how this regresses.

**`ROOKERY_CODER_BIN` was `ROOKERY_CLAUDE_BIN`** (and `coder.claude_bin` in `config.yaml`,
`CoderConfig.ClaudeBin` in Go), from when Claude Code was the only supported CLI. It never
selected Claude — it names the **default** binary a workspace gets when it has not set
`coder_bin` of its own — and five CLIs are supported now, so the old name described the
wrong thing. Both retired spellings still work and emit a deprecation through
`Config.Warnings`; refusing to start would punish an install for following documentation
that shipped. The yaml alias is resolved against the **second parse** of the file, not the
merged result, because `defaults()` fills `Bin` — so "did the file set the current key?"
cannot be asked of the merged value, and a config carrying both keys would otherwise let
the retired one win. `LegacyClaudeBin` is cleared after `Load` so the binary never has a
second apparent home.

`ROOKERY_CODER_MODE` is **policy** ("this build has no CLI coder"), deliberately
distinct from **detection** (`coder.DetectInstalled` — "none is on PATH right
now"). Slim is enforced at four layers: config parsing (an unknown value is a
startup error), the settings API (skips the host probe), the SPA (hides the
local engine), and both write paths + `coder.ForWorkspace`, which returns
`ErrLocalCoderDisabled` naming the fix rather than spawning a missing binary.

### Health

`GET /healthz` is unauthenticated (outside `/api/v1`) and reports version,
commit, sandbox status including Landlock ABI, coder mode, and host-tool
presence — booleans only, never paths. It backs the container `HEALTHCHECK`
(via the `rookery healthcheck` subcommand), the CI smoke test, and
operator triage.

**A `python3` warning is not cosmetic**: without it the agent-tool AST guardrail
in `internal/agentdesigner/guardrails.go` self-skips, so generated tool scripts
run unchecked. `rg`, `pdftotext` and `tesseract` degrade KB search, PDF
extraction and OCR respectively.

**Host tools are resolved once at startup, and the directories holding any that
PATH cannot reach are appended to it.** `cmd/rookery`'s `augmentHostToolPath`
runs before the command tree is built, calling `onboard.AugmentProcessPath`.

This exists because a first Windows install installed ripgrep, Poppler and
Tesseract and `rookery onboard` offered to install all three again — twice over.
`onboard.Missing` was `exec.LookPath` and nothing else, while winget writes a
portable package's shim into a `Links` directory the current process resolved
before it existed, and Tesseract's installer never touches PATH at all. Setup
also probed `python3` while `install.ps1` probed and installed `python` — and
python.org's distribution ships no `python3.exe` — so that one could never be
satisfied at all, on any run, forever.

**Fixing detection alone would have been the more dangerous half of the bug.**
Every consumer resolves these through `exec.LookPath`: `internal/health`'s
`have()`, `internal/convert`, `internal/vault`'s searcher,
`agentdesigner/guardrails.go`, and `internal/coder/hosttools.go`, which uses the
resolved path to grant the interpreter's directory read+execute inside Landlock.
A setup that searched harder on its own would report "all present" while OCR,
PDF extraction and the AST guardrail stayed broken — silent where the bug it
replaced was loud. Extending PATH fixes every one of those at once, including
the ones not written yet, and makes the property testable:
`internal/health`'s `TestSetupAndHealthzAgreeAboutHostTools` asserts the two
surfaces give the same answer. An earlier draft asserted it *before* augmenting
and failed on all four tools, which is why the call sits ahead of everything
else in `main` and why `cmd/rookery` pins that ordering.

Three details are load-bearing. `HostTool.Bins` returns per-platform spellings
(`python3`, `python`, `py` on Windows). `VerifyByRunning` is set for python3
alone and requires the candidate to actually run and name itself, because stock
Windows ships an App Execution Alias at
`%LOCALAPPDATA%\Microsoft\WindowsApps\python3.exe` that resolves like a real
command and opens the Microsoft Store — accepting it would report a missing tool
as PRESENT, and `WindowsApps` is deliberately excluded from the directory search
for the same reason. And the directory search applies **no executable-bit test
on Windows**, where Go synthesises mode from file attributes and never sets
`0o111` — the identical trap `coder.binCandidates` records. `install.ps1`
carries the same spellings and the same run-to-verify rule, pinned against the
Go side by `packaging/hosttools_agreement_test.go`, because neither file is
wrong when read alone.

## Architecture

### Entry point & wiring

`cmd/rookery/main.go` loads `config.yaml` via `internal/config`, wires all services, and
delegates subcommands via `github.com/urfave/cli/v3`. The `serve` subcommand:
1. Opens/migrates SQLite DB
2. Creates secrets service, coder, agent designer, agent runner, skill designer (`skilldesigner.Flow`)
3. Starts `GatewayManager` (loads all `platform_connections` from DB, starts per-workspace adapters)
4. Starts scheduler and reminder background goroutines (nightly GC also sweeps expired `skill_drafts` + orphaned staging dirs)
5. Starts Echo web server

### Inbound message pipeline

```
Per-workspace chat adapter (Telegram, Discord)
  → GatewayManager.route()
    → IdentityResolver  (platform_user_id → internal workspace_id via platform_identities table)
    → Router.Handle()
      → /agent  → agentdesigner.Flow (conversational FSM)
      → /skill  → skilldesigner.Flow (conversational FSM; list/create/cancel)
      → /run    → agentrunner.Runner
      → /secret → SecretStore
      → /remind → reminder.Service (create/list/delete)
      → /chat → db.Chat (start/list/stop/resume/delete)
      → /memory → memory.Store (add/list/delete bullets in GENERAL.md)
      → plain text → one-off chat (coder.Coder with read+write KB tools + the workspace's connector tools; see "Chat knowledge-base access" + "Chat connector access")
```

### Key packages

| Package | Responsibility |
|---|---|
| `internal/config` | YAML config + env overrides |
| `internal/db` | SQLite via `modernc.org/sqlite`; `DB`, models, per-table query helpers. Pragmas (`busy_timeout`, `foreign_keys`, `journal_mode`) are declared in the **DSN**, never `Exec`'d after opening — see "SQLite pragmas belong in the DSN" below |
| `internal/auth` | `BootstrapOwner`, `Authenticate` (owner login), `ChangePassword` (owner), `CreateWorkspace(name, about)`, `GenerateSecretsSalt`, bcrypt |
| `internal/rbac` | `CanPerform(db, workspaceID, permission)` — reads `workspace_permissions` table |
| `internal/secrets` | AES-256-GCM store; Argon2id key derivation; `GetAll()` decrypts all for env injection; `Proxy()` resolves `${NAME}` in-memory only |
| `internal/gateway` | `Gateway` interface, `GatewayManager`, `Router`, `IdentityResolver`; adapters `TelegramGateway` + `DiscordGateway` (DM-only, discordgo, user-id identity + DM-channel resolution, mandatory delete; opaque **string** message IDs throughout) + `SlackGateway` (DM-only, Socket Mode, two-token credentials — bot token + app-level token routed via `encrypted_config` — mrkdwn renderer, mandatory delete). An **adapter registry** (`RegisterAdapter`/`AdapterFactory`/`DispatchFunc`) replaced the hard-coded platform `switch` in `GatewayManager.start()` — a new platform registers its factory from an `init()`. A **render subsystem** (`internal/gateway/render`: `Renderer` interface + registry + `render.For(platform)`) decouples formatting from the router: `Router.Handle()` emits neutral CommonMark and each adapter renders on send — Telegram via a goldmark-AST MarkdownV2 renderer, Discord via CommonMark passthrough (native support). A declarative **`CredSpec`** framework (`credspec.go`: fields + `Label`/`Blurb`/`SetupSteps` + `SplitCreds` token/`encrypted_config` split) drives both the connect flow and the SPA connectors page (backed by the `/api/v1/connectors` JSON endpoints; one card per registered platform). |
| `internal/convert` | Bytes + filename/MIME → markdown. Pure function: no vault, no network, no LLM — which is what makes it testable against golden fixtures and identical across hosts. `ToMarkdown(data, Options) (Result, error)` + `Detect` + `IsTextual`. Handles html (real `x/net/html` parse, prefers `<main>`/`<article>`, drops nav/footer/script), csv/tsv, docx/pptx/xlsx (stdlib `archive/zip`+`encoding/xml`, no vendor SDK), pdf (prefers `pdftotext -layout` when on PATH, pure-Go fallback, **warns whenever extraction looks thin** so a scanned PDF cannot pass as a clean one, recovers `-layout` column blocks as markdown tables, and **falls back to OCR** via `pdftoppm`+`tesseract` when there is no usable text layer), json, and images (OCR via tesseract when installed; an honest stub naming what is missing when not). Embedded images are extracted from docx/pptx and returned in `Result.Assets` — the package stays pure, so the CALLER stores them (see "KB import fidelity" below). `Result.Warnings` is load-bearing: it flows into the note's frontmatter so a lossy conversion declares itself. Typed sentinel `ErrUnsupportedFormat`. Conversion is ONE-DIRECTIONAL (into markdown); the sanctioned reverse is `internal/export`. |
| `internal/export` | Markdown note → HTML, DOCX or PDF, the sanctioned reverse of `internal/convert` and a separate package precisely so convert stays into-markdown-only. HTML and DOCX are pure Go and always available; PDF shells out to a headless renderer. **The renderer is looked for in Playwright's cache BEFORE PATH** (`browser.ChromiumExecutable`), because the platform installs its own Chromium via `rookery browser install` and probing PATH alone reported "PDF unavailable" on a host whose `/healthz` said `"browser": true`. `pandoc` is deliberately NOT a supported engine, and has now been rejected TWICE for different reasons: as a PDF engine it cannot render HTML→PDF without a LaTeX engine it does not bundle, so probing it turned an honest "unavailable" into a button that failed with an opaque 500; as a DOCX engine it is unverifiable here and would produce stacked cells where the grid should be (see "Export fidelity" below). A `layout.go` AST transformer gives both renderers columns, alignment and real image widths from one pass, so a `pandoc` route would add a host dependency to be strictly worse. Chromium's argv carries `--no-pdf-header-footer`, or every page is stamped with the print date and the source `file://` temp path. |
| `internal/websearch` | Query → `[]Result` via a provider cascade. Optional keyed provider first (`SEARCH_KEY_BRAVE`/`SEARCH_KEY_TAVILY`, resolved as ordinary encrypted secrets), then a keyless cascade (DDG html → DDG lite → Mojeek → Bing). A provider returning ZERO results means "try the next engine", not "the answer is nothing" — a 200-OK JS-challenge page is indistinguishable from genuine no-results, which is the whole reason the cascade exists. Transient failures (429/5xx/network) retry INSIDE one provider; exhausting every provider is a NON-error empty slice, because the coder's tool loop treats any `error:` as a failing call worth blocking. |
| `internal/nethttp` | The single private-address dial guard (`GuardedClient`, `DenyPrivateAddr`, `IsBlockedIP`). Enforced at DIAL time via `net.Dialer.Control`, not by URL inspection — the only approach that catches a hostname RESOLVING into private space and every redirect hop. Blocks loopback/RFC1918/link-local/unique-local/CGNAT-tailscale/cloud-metadata, plus the NAT64/6to4/Teredo transition ranges that embed an IPv4 address (partial by nature — a network-specific NAT64 prefix cannot be enumerated). Load-bearing because chat can now reach the web and the loopback interface hosts the connector + KB bridges and their per-run bearer tokens. `internal/coder/netguard.go` delegates here; do not fork a second copy. |
| `internal/fonts` | The single copy of the UI font (`InterVariable.woff2`, latin subset, ~48 KB). Its own package because `go:embed` cannot reach outside its own directory and TWO consumers need these exact bytes: `internal/export` (which base64-inlines it into exported HTML/PDF) and the SPA (via the `@fonts` Vite alias). A second checked-in copy would drift silently, so there is deliberately only one. A test asserts the embedded bytes are a real woff2 (`wOF2` magic) and not a truncated or LFS-pointer checkout. |
| `internal/onboard` | The platform knowledge behind `rookery onboard`: the four `HostTools` (with `Critical` marking python3 alone, whose absence disables the AST guardrail rather than merely degrading a feature), `Missing`/`DetectManager`/`PackageFor`/`InstallCommands` over six package managers, and `ServiceFor`/`UnitFileFor`/`SystemdUnitPath`/`TaskXMLFor`/`LaunchAgentPlistFor`. The three service documents are **pure functions with no build tag**, so a Windows task and a macOS agent can be tested on a Linux host — which is the only place they are checked at all, since this project has neither machine. Its own package, and its `LookPath` is injectable, because the package-name mapping is exactly what shipped wrong in the rpm and a host we cannot run has to be describable in a test. `UnitFileFor` **generates** the unit against the running binary rather than copying the packaged one — that file hardcodes `/usr/bin/rookery`, so an `install.sh` user with the binary in `~/.local/bin` would enable a service that starts nothing. Also `Resolve`/`MissingOn`/`ToolDirs`/`AugmentProcessPath` — see "Host tools are resolved once, then put on PATH" below. |
| `internal/iolimit` | `ReadCapped` + `ErrTooLarge` — the shared capped read every ingest door uses (KB upload, web-chat attachment, Telegram/Discord/Slack attachment, KB bridge, `save_to_kb` URL fetch), all enforcing one 25 MiB cap. Reads `cap+1` and REJECTS rather than truncating: a silently truncated import writes a note whose frontmatter states a byte count that is not the source's. `CappingWriter` is the write-side analogue — bounds a stream written into an `io.Writer` (Slack's `slack.Client.GetFile` insists on an `io.Writer` and has no size bound; there is no stdlib `io.LimitWriter`), rejecting at the same `cap+1` boundary. |
| `internal/coder` | `Coder`: two engines behind one API. **CLI engine** — runs a coder CLI subprocess with full per-workspace isolation (`CoderBackend` interface: one struct per coder — Claude/OpenCode/Codex/Gemini/Cursor, plus a generic fallback). **API engine** (`api_engine.go`+`hosttools.go`, `coder_kind=="api"`) — an in-process LLM tool-calling loop (via `internal/llm`) that offers the model host tools (`read_file`/`write_file`/`edit_file`/`list_dir` + read-only discovery `search_files`/`glob` + exec tools `run_script`/`bash`/`web_fetch`/`web_search`) scoped+sandboxed to the vault, no subprocess. `WithNoTools()` text-only; `WithExtraEnv()` secret injection; `WithAPIConfig`/`WithSecretsLookup`/`WithVault`/`WithProgress`/`IsAPI()` for the API engine; `ForWorkspace(w, …)` builds a coder (local or api) from the workspace's inlined config |
| `internal/llm` | Thin, reusable transport over provider chat-completion/messages APIs with native function-calling (tool use). **`Usage.Add` is the ONE place usage is summed** — there were two, and the second (in `internal/agentrunner`) enumerated three fields, so `CachedTokens`/`CacheReported` were parsed correctly, carried out of the engine correctly, and discarded one layer up: the run log reported `n/a` for a provider that reports cache statistics on *every* response. A reflection test walks the struct, so a field added later fails until `Add` carries it. `Usage` also carries `Cost`/`CostReported`, read from the provider (OpenRouter reports it on every response) rather than computed from a price table — a table is a second copy of someone else's pricing and goes stale in silence. `Provider` interface + registry (`openai`, `openrouter`, `anthropic`, `generic` OpenAI-compatible, plus ~35 further providers registered against the OpenAI schema — see `coder.APIProviders()`); `Request`/`Response`/`Message`/`Tool`/`ToolCall`/`Usage`; shared HTTP plumbing with rate-limit-aware backoff (`ErrRateLimit` transient 429 → retry across a per-minute window; `ErrQuotaExhausted` 402 → no retry; `ErrAuth`, `ErrToolsUnsupported`). Knows nothing about vaults/sandboxes/protocol — the agentic loop lives in `internal/coder`. |
| `internal/connectors` | Self-managed-OAuth + API-key connector layer (replaces Composio). Embedded `providers/*.yaml` (auth config) + `connectors/*.yaml` (curated action manifests) for **136 providers** (Google-family incl. Calendar/Tasks/AdSense/GA4/Search Console, YouTube, GitHub, Slack, OpenAI, Notion, Outlook/Teams, Jira, HubSpot, Dropbox, Calendly, Asana, ClickUp, Airtable, Intercom, SendGrid, Monday, Salesforce, Shopify, Mailchimp, Zendesk, Stripe, Twilio, Trello); `Registry` (+ `OAuthProvider` for `auth_parent` aliasing, `ProviderNames()` backing the connections page), `Execute` (typed choke point), `applyAuth` (Bearer/api-key header/query/Basic + templated Basic username + AWS SigV4 request signing), `renderBody`/`renderForm`/`body_arg` body kinds, `ActiveBoundConns`/`ConnectInput`/`token_extra`/`key_extra` per-connection value sources, `OAuthClient`, `DBTokenStore` (+ headless `RunRefreshLoop`), `Bridge` (loopback HTTP so CLI coders reach `Execute` — used by runs AND chat), `ToolDefs`/`ResolveTool` (single-source tool naming for both coder kinds). All tokens `secrets.EncryptWithSystemKey`-encrypted. |
| `internal/mcp` | Model Context Protocol client layer — a deliberate **peer of `internal/connectors`**, mirroring it shape-for-shape so both coder kinds, the per-agent binding and the approval gate treat an MCP tool and a connector action identically. `Client` (SDK-backed, one pooled session per server + one reconnect-and-retry, since a self-hosted server that slept has dropped its session and the first call after must not read as a failure), `Catalog`/`Sync` (DB-cached `tools/list`, reconciled by upsert so the owner's read_only/approval/enabled columns survive a re-sync; a vanished tool is MARKED missing, never deleted), `ToolDefs`/`ResolveTool` (naming defined once for both paths), `Execute` (+ `Policy{BuildPhase, Parker}`, the single typed choke point), `Bridge` (loopback HTTP so CLI coders reach the same `Execute` via `rookery mcp exec`). **The structural difference from connectors: nothing about an MCP server ships in the binary** — the owner pastes a URL and the server itself supplies the action list. Tokens are `secrets.EncryptWithSystemKey`-encrypted. |
| `internal/browser` | Headless browser for JavaScript-rendered pages — a deliberate **peer of `internal/connectors` and `internal/mcp`**, mirroring them shape-for-shape (one typed choke point, one availability probe, one loopback `Bridge` so both coder kinds converge). `Probe`/`Available` (side-effect-free: playwright-go's own driver check MkdirAlls, which disqualifies it from `/healthz`), `Manager` (lazy start, idle shutdown, one helper process per server, fresh incognito context per read), `Render`/`Act`, `CheckAct` (the single permission choke point), `Classify` (cloudflare/captcha/login/bot-check, REPORTED never bypassed), `ParseAriaSnapshot` + `FilterInteractive` (element refs for the acting tools), `ResolveSecretValue`, `Bridge`, `Install`. **Chromium runs in a sandboxed helper process** (`__browser-host` under `sandbox.Wrap`), never in the host process — see "The browser" below. |
| `internal/buildphase` | Tiny package holding `ROOKERY_BUILD_PHASE`/`generation` marker (set during agent/skill builds; the connector `Execute` build-guard refuses mutating actions when present). Its own package so it outlives any one integration. |
| `internal/connalert` | Delivers the "this connection needs reconnecting" alert to the inbox AND chat when `DBTokenStore` flips a connection to `NEEDS_REAUTH`. Its own package because the alert needs the DB and the gateway and `internal/connectors` deliberately knows about neither — the same shape as `internal/approval`, and it takes the same narrow `SendToUser` interface so tests need no gateway. See "Connection re-auth alerting" below. |
| `internal/agentdesigner` | `Flow` FSM (Describing→Designing→Verifying→Done); conversational design shared between web and Telegram; auto-schedule; `RunFullGuardrails`/`RunToolGuardrails` (ethics + AST only); `toolstree.go` recursive path-safe `WriteToolsTree`/`ReadToolsTree` for multi-file projects; `isTestArtifact` classifier + `cleanupTestArtifacts` (post-save junk removal); `statefile.go` (`StateFilePath`/`ReadState`/`WriteState`/`RenderStateTemplate`) owns an agent's `state.md` format (see "Agent state" below); `migrate_files.go` (`MigrateAgentFilesToMarkdown`) is the idempotent startup migration off the old `state.json`/`agent.json` pair; `ParseRequiredSecrets` (`flow.go`) parses AGENT.md's `# Required secrets:` header — the only source of an agent's declared secrets now that `agent.json` is gone |
| `internal/skilldesigner` | Conversational skill-creator wizard mirroring `agentdesigner.Flow` (FSM Idle→AwaitingResume→Describing→Designing→Verifying→Done, SSE progress, 7-day drafts, approval triggers); `SkillSaver` writes SKILL.md+scripts/ to vault + DB upsert; generation runs with the `skill-creator` core skill, vetting runs the `skill-vetter` core skill as a text-only audit; `vettingBlocksSave()` parses the verdict line. Wired to BOTH surfaces: the SPA (`/api/v1/skills/design`) and chat platforms (`/skill`). `Start` is the chat entry point (opens in `StateDescribing`, asks for a description, no coder call); `StartDesign` is the web one (its form collects the description up front). |
| `internal/skilllibrary` | Embedded core skill catalog (`go:embed skills/*/SKILL.md`) — always-on for every user, no DB rows, no admin gate. `LoadBundled()`, `CoreSkillContent(slug)`, `IsCoreSkill()`, `ParseMeta()` (Anthropic+openclaw YAML frontmatter: requires.bins/anyBins/env, install specs). Supersedes the admin-catalog approach dropped in migration 009. |
| `internal/agentrunner` | Load agent → decrypt secrets into env via `WithExtraEnv` → coder subprocess → capture `[CHAT]` lines → send via GatewayManager; timestamped run logs; `RunInput.OnProgress` per-turn hook for live SSE streaming. Skills pool = core skills (embedded) + user skills; the agent's DECLARED skills come from the `agent_skills` DB table (`db.ListAgentSkillNames`, the source of truth), never from AGENT.md; `resolveSkillBins` resolves declared tools' paths for the runtime `<skill_environment>` block; `loadDeclaredSkillContent` reads core skills from the embed. **Reliable delivery**: `parseCoderOutput` (blank lines don't end `[CHAT]`; empty `[CHAT]` dropped; a stray `[/CHAT]` close tag weak models sometimes emit is stripped and never delivered; `[SILENT]` detected; `[STATE]` merged and saved via `agentdesigner.WriteState` into `state.md`'s json fence) + `extractProseMessage` fallback when no `[CHAT]` emitted and not silent → visible warning when nothing deliverable. Covered by `runner_test.go`. |
| `internal/sandbox` | Self-contained Landlock filesystem confinement for coder subprocesses (Linux). `Spec`, `Supported()`, `Wrap()` (re-exec via the hidden `__sandbox-exec` helper), `Exec()` (applies Landlock + rlimits, then `execve`). No external dependency. |
| `internal/scheduler` | Cron scheduler: polls `agent_schedules`, fires runner, decrypts stored master password for secret injection; `WithSender()` delivers output to users; `WithRecovered()`/`RecoverInterrupted()` retry runs the last shutdown killed mid-flight; runs are capped at `maxConcurrentRuns` — see "Missed runs and the laptop case" below |
| `internal/reminder` | Creates/lists/fires reminders; background polling goroutine. Reminders live only in the DB and the reminders UI tab — they are NOT reflected to the vault. |
| `internal/chat` | `Chat` create/list/stop/resume/delete; 30-min idle auto-stop; `BuildUserContext` (shared **identity-only** context builder for one-off chat — profile/memory/agents/MCP; the broader KB is retrieved on demand via tools, not injected here); `CleanReply`/`CleanHistory` strip agent output-protocol markers out of conversational replies — see "Chat must never show the output-protocol markers" below |
| `internal/prompts` | Central home for all LLM prompt construction: `BuildDesignSystemPrompt` (+ `<knowledge_base>` block + `KBManifest`), `BuildImplementationPrompt`, `BuildEditImplementationPrompt` (diagnose-before-fix), `BuildCoderPrompt` (+ `<skill_instructions>` + `<skill_environment>` blocks), `BuildChatSystemPrompt` (chat read+write KB instruction), `BuildChildAgentFollowUpPrompt`, `BuildSkillMetaPrompt`, `BuildReminderParsePrompt`, skill-creator prompts (`BuildSkillDesignSystemPrompt`, `BuildSkillImplementationPrompt`, `BuildSkillVettingPrompt`, `SkillEnvBlock`). `SkillRef`/`SkillBin` types. No inline prompt text exists outside this package. Shared single-source blocks: `agentPhilosophyBlock` (three-tier), `platformContextBlock`, `coderCapabilitiesBlock` (backend-aware), `agentArchitectureGateBlock`, `testingRulesBlock` (one bounded smoke test + dry run; real secrets at build time, no outbound sends), `shellSafetyBlock`, `scriptRobustnessBlock`, `connectedToolsBlock` (backend-aware native-tools vs `connector exec` guidance). `ChatAppsForPlatforms` + `MapCoderBackend` bridge callers to prompt params. |
| `internal/memory` | Per-user structured context store. Memory lives as named `.md` files in `memory/` (`USER.md`, `SOUL.md`, `GENERAL.md`, etc.) — editable via the KB browser. `ContextString()` reads all files, skips placeholder-only ones, and returns sectioned markdown for LLM injection. `Append/List/Delete` target GENERAL.md bullet lines (used by Telegram `/memory` command). `MigrateToStructuredFiles()` consolidates legacy UUID-keyed entries at startup. |
| `internal/vault` | Per-user Obsidian-style knowledge base: `Vault` (paths + `Resolve` safety + file IO), `Reflector` (chats→markdown+sidecar), `LinkIndex` ([[wikilinks]]), `Searcher` (ripgrep), `Guard` (post-run write-scope enforcement), `WriteJournal` (records and reverts what an agent BUILD or DRY RUN writes into the user's knowledge base — see "A create build RUNS the agent once before showing it to you"), `MigrateLegacyLayout`, `MigrateSessionsToChats`. |
| `internal/audit` | Structured audit event writer → `audit_logs` table |
| `internal/backup` | Owner-level snapshot/restore of the WHOLE install (database + every workspace vault) into one passphrase-encrypted `.rkb` file. `Snapshot` (`VACUUM INTO` → tar+gzip → chunked AES-256-GCM, staged to a temp file then uploaded), `StageRestore`/`ApplyPendingRestore`/`CancelRestore`/`Verify`, `Destination` interface + `LocalDestination`/`S3Destination` (hand-rolled `signV4`, no AWS SDK), `DefaultLocalDir(dataDir)` (the one definition of `<data_dir>/backups` — the local folder is not configurable), `Config` in `system_settings` (`backup.config`; passphrase + S3 secret encrypted under the system key), `Scheduler` (own ticker; daily/weekly, missed runs collapse), `Prune` (keep-last-N), `AcquireLock` (flock). See "Backup and restore" below. |
| `internal/profile` | Per-user personalization (name, email, location, timezone, tone, language, notes); stored in the generic `settings` table; `Load()`/`Save()`/`ContextString()` for LLM injection; `LoadLocation()` for timezone-aware reminder parsing |
| `internal/skillstore` | `SkillStore`: install/load/delete SKILL.md based skills per workspace. `SkillDir(base, workspaceID, name)` is the path helper shared with the skill designer (staging dirs use the `.staging-<name>` convention). |
| `web/` | Echo v4 web server: the `/api/v1` JSON API + the embedded React SPA (`web/ui`, served at `/`). The old server-rendered template UI was deleted — the SPA is the only front end. Handler files now hold API handlers + shared cores (e.g. `saveConnector`, `loadAgentDetail`, `saveWorkspaceCoderCore`, `handleOAuthCallback`) reused by the JSON layer. |

### Per-user knowledge base (vault)

Every user has one Obsidian-style vault — a single directory of interlinked markdown notes.
`internal/vault` owns all vault path/IO/safety logic. The SQLite DB remains the system-of-record;
chats are *reflected* into the vault as markdown + JSON sidecars.

```
<data_dir>/vaults/<workspaceID>/
├── README.md                       # vault home note (scaffolded)
├── notes/                          # user-authored notes/journals/plans/todos
├── memory/
│   ├── USER.md                     # workspace profile — name, location, role, background
│   ├── SOUL.md                     # communication style and preferences
│   ├── GENERAL.md                  # quick notes added via /memory Telegram command
│   └── <any>.md                    # additional context files the user creates
├── skills/<name>/SKILL.md          # per-workspace skills
├── agents/<agentID>/               # an agent's OWN writable area
│   ├── AGENT.md  state.md
│   ├── tools/*.py  notes/*.md
│   └── logs/run_<ts>.md
├── chats/<id>.md                   # reflected chat transcripts
└── .kb/                            # internal: db-export/ JSON sidecars, links.json (hidden)
```

`claude-homes/<workspaceID>/` stays OUTSIDE the vault (holds `.claude` credentials — never backed up).

**Memory injection.** All `.md` files in `memory/` are automatically injected into every LLM
context (design sessions, agent runs, one-off chat) via `memory.ContextString()`. Files whose
body is only headings and HTML placeholder comments are silently skipped until the user fills
them in. `EnsureScaffold` creates `USER.md` and `SOUL.md` with placeholder content on first visit.

Key types in `internal/vault`:
- **`Vault`** — `Root/AgentsDir/AgentDir/MemoryDir/SkillsDir`; **`Resolve(workspaceID, rel)`** is the security primitive every read/write path uses (rejects `..`/absolute escapes); `WriteNote` (atomic), `Read/Delete/Rename/List/EnsureScaffold`. `List` hides dotfiles.
- **`Reflector`** — `ReflectChat/ReflectAgentRun`: markdown note + `.kb/db-export/<table>/<id>.json` sidecar. **Reminders and inbox notifications are NOT reflected.** An inbox message is a delivery record, not knowledge: the row lives in `inbox_messages`, the Home inbox renders it, and an agent run's delivered text is already archived in `agents/<id>/logs/run_<ts>.md` under "Output sent to user". The old `inbox/<uuid>.md` projection was a third copy that gave every note a non-distinguishing heading ("⏰ Reminder", "🤖 weather (cron)"), grew one file per notification forever, and — because `inbox` was never added to `kbExcludedDirs` — fed a stream of "🌤 25°C, clear sky" into the agent-/skill-designer retrieval meant to quote the user's own knowledge. `vault.RemoveLegacyInboxNotes` (startup, idempotent) sweeps `inbox/` + `.kb/db-export/inbox_messages/` from installs that had it; deleting rather than archiving is safe because every note's source row is still in the DB. Consequently `inbox`/`reminders` are no longer in `protectedTopDirs`, `kbSystemFolderLabels`, `kbDisplayTitle` or `links.go`'s priority/exclusion lists — the platform does not own those names, so a user folder called `inbox` is an ordinary user folder.
- **`LinkIndex`** — `[[wikilink]]` parsing/resolution + `RenderHTMLLinks`; `Backlinks`.
- **`Searcher`** — `ripgrepSearcher` (rg `--json`, pure-Go fallback). Snippets go through
  `snippetFor`, not a flat trim — see "Table retrieval" below.
- **`Guard`** — detective post-run write-scope enforcement (snapshot/revert). No longer wired into agent runs (the policy changed to let agents edit the KB directly — see "Agent access model"); the type + tests remain as a reusable utility.
- **`MigrateLegacyLayout()`** — idempotent startup migration of pre-vault `agents/`, `memory/` (jsonl→md), `skills/` into vaults.

**Table retrieval: headers travel with the rows, and this does NOT enable aggregation.**
A converted CSV is one heading followed by the whole table, so `ChunkMarkdown` turned the
reporting install's 155 KB note into ~190 chunks of bare rows, and `trimSnippet` cut a hit at a
flat 200 bytes — about a tenth of one 1774-character row, mid-cell. Both are technically correct
and tell the model nothing: no column names, so it cannot tell an amount from a date. That is
why referencing table data in that note returned nothing while referencing prose in the same
vault worked fine.

Both paths now carry the header. `splitOversized` repeats it on every fragment holding table
rows, and `snippetFor` prepends the header of the table the hit is IN — not the note's first
table, because labelling a row with another table's columns reads as authoritative and is worse
than no header. `snippetFor` also unwraps the block constructs the KB editor produces (callout,
toggle, columns, alignment) rather than returning raw HTML, and drops images; a bare structural
wrapper yields no hit at all. Four details worth keeping:

- **The repeated header is paid for OUT of the per-chunk budget**, and that budget has to reach
  `hardSplitWindow`, not just the accumulator: a single row can exceed a whole chunk, so that
  function decides the row's size. Cutting there at the full bound and then prepending a header
  put 96 of the real note's 191 chunks over `targetChunkChars`, which is a hard bound the
  byte-capped tool result depends on.
- **`sectionTableHeader` SCANS rather than checking offset 0.** A section almost never opens
  with its table — this note begins with an italic "Converted from …" line — and an offset-0
  check found headers for none of its chunks while every synthetic fixture passed.
- **A section with two tables gets no header at all**, deliberately. Picking the first would
  mislabel the second's rows.
- **Prose keeps the 200-byte snippet budget**; only a table hit gets the larger one, since only
  it must also carry a header. Raising it for every hit would spend the shared byte budget on
  fewer results.

**Big files: map before you read, and never do the arithmetic yourself.**
An earlier version of this section claimed the reporting note had "~1000 rows" and that
*"how much have I spent in total"* could not be answered in chat. **Both were wrong**, and the
correction is the whole design. That note has **98 rows**; it is 155 KB because ONE column
(`apiTransaction`, a raw JSON payload per row) holds **88% of the bytes**, while the nine
columns that answer real questions total **8.3 KB**. The model never needed a big file — it
needed 8 KB — and it exhausted its 30-turn budget paging a blob at 8 KiB a time before
returning an empty completion.

The root cause was one thing, not three: `read_file` was a byte window, so ANY file over the
cap — table, long note, mixed markdown — had to be paged blindly from offset 0. Retrieval
machinery already existed (`ChunkMarkdown`, the BM25 `Indexer`) and simply was not addressable
per-file. Four things close it:

- **`kb_file_map(path)`** returns the shape before the content: columns and row count for a
  table, a heading outline for a document, the reading cost in tokens, and a warning when one
  column or section exceeds `dominantShare` (40%). On the real note the map is **984 bytes** and
  says outright that `apiTransaction` is 88% of the file. That sentence is the fix.
- **`read_file` takes `section:`** and **`search_files` takes `path:`** — fetch a heading, or
  search inside one file. Scoping had to land in all THREE retrieval paths (ripgrep, the Go
  fallback, and the ranked BM25 pass, which could previously only EXCLUDE prefixes), with a
  larger per-file cap (`MaxHitsInFile`) since capping matches per file is pointless once the
  caller named the file. The rg/Go split is a per-CALL fallback, not per-host — `Search` falls
  through on any rg error — so divergence would surface as nondeterminism on one machine;
  `parseRipgrepJSON` is now shared and a test asserts the two agree.
- **`kb_table_query`** does the arithmetic host-side. The model fills PARAMETERS, never SQL:
  this platform runs small models, SQL needs valid syntax plus exact column names (one here has
  a space in it), and each malformed query costs a turn from the budget already at issue. Once
  the interface is fixed parameters a database earns nothing — it would mean generating SQL from
  them anyway — so it is plain Go over one vault file, and emphatically **not** `rookery.db`,
  which holds every stored credential.
- **An empty completion is no longer a finished answer** (`api_engine.go`). It used to return
  `Text:""` with `StopReason:""`, so a dead turn was recorded as a success and logged nowhere;
  chat now also logs a `chat: turn finished` line with an `empty` field.
- **A TRUNCATED completion is a different failure from an empty one, and conflating them cost
  four wrong diagnoses.** A reasoning model bills its thinking against the same completion
  budget as its answer, so on a hard synthesis it can spend the whole cap before emitting one
  content token: the provider returns `finish_reason: "length"` with empty `content` and the
  thinking in a separate field. Three things had to change, and each was independently
  invisible. `internal/llm` never read that field — **both** spellings are now parsed into
  `Response.Reasoning` (OpenRouter normalizes to `reasoning`, DeepSeek's own API emits
  `reasoning_content`, `generic` can be either, and reading one leaves the other invisible).
  `FinishReason` was parsed and read by **nothing**. And the empty-answer nudge was actively
  wrong here — it re-asks under the SAME cap, so it truncates again, and the extra turn grows
  the context making truncation likelier still; a `length` finish now **re-issues the same
  request with a raised cap** (`truncationRetryMaxTokens`, once) and appends nothing to
  history, since a blank assistant turn is few-shot evidence that answering with nothing is
  acceptable. `Reasoning` is captured for DIAGNOSIS ONLY and must never be delivered: it is
  mid-thought on a truncated turn, and this repo has already shipped model internals to a real
  user twice (`chat.CleanReply`, `LooksLikeToolScaffolding`).

  **The old fallback message asserted a cause and the cause was wrong** — *"the request needed
  more of a large file than fits"* — so a truncating reasoning model was investigated as a
  large-file problem four times, by its own error text. `emptyAnswerFallback` now names no
  cause; `truncatedAnswerFallback` names the real one. `agentrunner: run finished` carries
  `stop_reason` (keeping the LAST non-empty one, since `""` is the engine's explicit statement
  that a turn ended normally and must not erase an earlier cut-short reason), because a run
  delivering a fallback looks identical from the outside whatever produced it.

Two details are what keep the obvious call from reproducing the bug: the default projection
drops any dominant column (`ModestColumns`), and a date grouping orders by its KEY rather than
by value — on the real data `order: asc` had produced 08, 06, 05, 07, an ascending sort of the
wrong column.

**What remains true:** chat still has **no arbitrary compute**. `includeExecTools` is
`filepath.Clean(workDir) != filepath.Clean(vaultRoot)` and chat sets `WithDir(root)`, so
`run_script`/`bash` stay off there by design (CLI parity; see "Chat knowledge-base access").
Anything `kb_table_query`'s closed operation set cannot express falls back to projection —
hand back the table with the fat columns dropped and let the model read it. That is why the
operation set can stay small instead of growing one entry per question.

**Agent access model.** An agent's run CWD is its own vault dir; the coder prompt (`BuildCoderPrompt`, `<knowledge_base>` block) tells it to READ the whole vault and WRITE to both its own dir and the user's knowledge base (notes, memory, user files) — durable knowledge is persisted into the KB across runs. The Landlock sandbox grants RW over the whole vault root (confined to that user's vault + HOME; the DB, config, and other users' vaults stay out of reach). System-managed dirs (`.kb/`, `chats/`, other agents' `agents/<id>/`) are off-limits by prompt, not hard-enforced. The chat uses the same model (see `prompts.BuildChatSystemPrompt`).

**Agent state (`state.md`).** An agent's memory between runs is a markdown document, not a bare JSON file: `agents/<agentID>/state.md` — a `# State — <name>` heading, an italic intro paragraph, a ```` ```json ```` fence holding the machine state, and an optional `## Notes` section the agent may add human-facing context to (`internal/agentdesigner/statefile.go`: `StateFilePath`/`ReadState`/`WriteState`/`RenderStateTemplate`). The `[STATE]` output marker is unchanged (see "Agent output protocol" below) — the runner still merges JSON on every `[STATE]` block — but the merge now targets the fence inside this file. `WriteState` splices only the fence, preserving the heading/intro/`## Notes` byte-for-byte; the fence is located with a line-based scanner (`findStateFence`), deliberately not a regex, which could not disambiguate a corrupted fence from a legitimate later one in `## Notes`; a damaged or absent fence NO LONGER degrades to empty state — see the recovery paragraph below. All three decode sites use `json.Number` (not `float64`), preserving integer fidelity above 2^53 — e.g. a 64-bit Discord snowflake ID, which silently truncated under the old `state.json` decode. The KB refuses to save a running agent's `state.md` (`PUT /api/v1/kb/note` → 409 `agent_running`), checked on the finalized save path server-side since the frontend can't be trusted to send the well-behaved form; the guard is check-then-write (a run can still start in the gap) and covers only `PUT` — a delete/rename of `state.md` mid-run is unguarded.

**The format has ONE owner (`internal/agentstate`) because it has more than one writer.** An agent can record memory two ways and both are legitimate: emit a `[STATE]` block, or edit `state.md` with its own file tools (it may keep prose in `## Notes`, and the owner hand-edits the file from the KB browser). Those two doors disagreed on four things — what shapes the reader accepts, whether a write merges or replaces, whether the heading and prose survive, and what happens when one run uses both — and the disagreement was not theoretical. Two of four agents built on 2026-08-19 wrote their JSON one line BELOW the fence. `ReadState` returned `{}`, so each run concluded it had never run, re-derived a baseline, emitted `[SILENT]`, and wrote the same broken file again. Permanently, invisibly (`exit=0`, `warnings=0`, and a silent run is a legitimate outcome), and at ~930k tokens an hour on an hourly schedule. `agentstate.Get`/`Apply`/`Replace`/`Merge` are now the only code that touches the file; `agentdesigner`'s four exported functions are thin delegates so no call site moved.

**`Get` recovers, and the narrowness of the scan is the design.** A fence holding parseable JSON is read exactly as before. Only when the fence is empty or absent does it scan for the first parseable JSON object and adopt it, skipping the fence's own region so an empty `{}` cannot be "recovered" as the answer; `Apply` then re-renders the file canonically, moving the data into the fence and keeping the surrounding prose. One run repairs the file for good. The residual risk is recorded rather than designed away: an agent with genuinely empty state AND a JSON example in its `## Notes` would adopt the example — unlikely, self-correcting on the next patch, and far better than a correct agent going silent forever. Recovery cannot help an agent that recorded its baseline as English prose, which is what the tools below exist for.

**`understood` is deliberately distinct from "the state is empty", and `ReadState` still reports it as an ERROR.** A fresh agent legitimately has empty state; a damaged file does not look like one. `applyAndSaveState`'s no-update turn declines to write when the run's initial read failed, and the runner derives that from `stateReadOK := err == nil` — so a delegate that returned `(empty, nil)` for an unparseable file would flip the guard to "fine" and let the next turn replace hand-recoverable bytes with `{}`, reintroducing the exact failure the guard exists to prevent. An unrecoverable ORPHANED fence also reports not-understood, because a fence someone wrote and damaged is not the same as no fence at all.

**Three doors, one implementation.** `[STATE]` (the runner), `get_state`/`set_state` (API engine host tools, `internal/coder/statetools.go`), and `rookery state get|set` over a loopback bridge (`internal/agentstate/bridge.go`) — the fourth instance of the pattern `internal/connectors`, `internal/mcp` and `internal/vault` already use. All three land on `agentstate.Apply`, so all three MERGE a patch with `null` deleting a key; `Replace` is the whole-state write `WriteState` has always been. The state tools are gated by `includeExecTools` — agent builds and runs only, never chat, which has no agent and no state. The bridge token is scoped per (agent directory, agent name), NOT per workspace: `state.md` is per-agent, and a workspace-scoped token would let one agent read and overwrite another's. `ROOKERY_STATE_URL`/`ROOKERY_STATE_TOKEN` are internal (written INTO a subprocess's env), so they live in `scripts/docs-sync-internal-env.txt` rather than README's configuration table.

**`WithAgentName` is not decoration.** Nothing seeds a `state.md` — a brand-new agent genuinely has none until something writes one. So the first `set_state` of a first run CREATES the file from the template, and every later write only splices the fence: an empty name written into the heading at that moment is permanent, not self-healing. Both construction sites (`agentrunner`'s run coder and `agentdesigner`'s `generationCoder`) pass it, and `agentstate`'s blank-name test pins the consequence.

**Startup migration.** `agentdesigner.MigrateAgentFilesToMarkdown` (`migrate_files.go`, run in `serve` before the scheduler starts — scheduled runs read `state.md` via the new runner path) walks every workspace's `agents/*/` dirs, including `draft_<slug>` dirs, and for each: (1) converts `state.json` → `state.md` with a verify-then-delete gate — write, read back with `ReadState`, deep-compare against the original (also decoded with `json.Number`, so the comparison itself can't paper over a rounding loss), and only then remove `state.json`; any failure at any step leaves both files in place and logs loudly, never silently dropping state; (2) reconciles `agent.json`'s legacy `Skills` field into `agent_skills` (the old `ReconcileSkillAttachmentsToDB` job, absorbed here because it must run before `agent.json` is deleted); (3) deletes `agent.json`. Idempotent — an agent dir with `state.md` present and no `agent.json` is a no-op on every subsequent boot.

**KB file kinds.** The note endpoint (`GET /api/v1/kb/note`) sniffs content rather than trusting the extension — `kind: "markdown"` for `.md` files (the existing WYSIWYG/raw editor, unchanged), `"code"` for any other file that decodes as valid UTF-8 under the 1 MiB inline cap (a read-only monospace view, no save affordance), or `"binary"` otherwise (a download-only panel; content omitted). A file exactly at the 1 MiB boundary is classified `"code"`. Navigation carries an explicit `dir` hint instead of guessing from the filename, so extensionless files still open correctly.

**KB selection assist (`POST /api/v1/kb/assist`).** One blocking, text-only coder call over a
passage the user selected in the editor — the backend half of the editor's Improve/Proofread/
Explain/Reformat panel (`AIActions.tsx`, surfaced from `BubbleToolbar.tsx`'s bubble menu; see
"KB rich-text editor: five formatting/AI constructs" below for the panel itself). The action set is closed
(`prompts.KBAssistActions()`: `improve`, `proofread`, `explain`, `reformat`) and the prompt text
lives entirely in `prompts.BuildKBAssistPrompt` (`internal/prompts/kbassist.go`), per the
project's standing rule that no prompt text lives outside `internal/prompts`. Three actions ask
for a straight replacement passage; `explain` deliberately does not — its prompt tells the model
NOT to rewrite, because the result is shown read-only and must never be pasted over the user's
prose. The selected passage is capped at `maxAssistSelectionBytes` (16 KiB) and an over-cap
selection is **rejected, not truncated** — the same reject-not-truncate contract as
`internal/iolimit`, but intentionally a separate, much smaller constant: iolimit's 25 MiB governs
ingest doors (uploads, attachments, the KB bridge), not a single LLM call. `path` is only prompt
context (the call runs `WithNoTools`, so the model cannot open the file itself) but still passes
through `vault.Resolve` — an endpoint that echoes an unvalidated path into a model prompt is
exactly the kind of thing that quietly becomes a real read later. Quota/rate-limit/auth coder
failures reuse `agentrunner.FriendlyRunError` (exported for this reason) so a workspace out of
quota gets the identical sentence here as it does from a scheduled agent run, returned as a 503
`coder_unavailable` rather than a generic 500.

**The API engine's kickoff message is chosen by whether tools are offered, and the reason is a bug
that looked like a KB bug.** `runAPI` sends the caller's prompt as the SYSTEM message and a fixed
USER message — and that message used to be `prompts.APIEngineKickoffMessage` unconditionally,
which ends *"Emit your final result using the output protocol ([CHAT], [STATE], [SILENT])."* So
every one-shot `Generate` was explicitly told to wrap its answer in `[CHAT]`, and a well-behaved
model did. The reported symptom was a stray marker in the KB rewrite panel; the quieter half was
that **`BuildSkillMetaPrompt` and `BuildReminderParsePrompt` both ask for a bare JSON object**,
got a protocol-wrapped body, failed to parse and fell back **silently**. It is API-engine-only —
a CLI coder's `Generate` never sees this message — so the same install exhibits it or not
depending on `coder_kind`, which is how it read as flakiness. A `noTools` call now gets
`APIEngineTextKickoffMessage` ("reply with the requested content only") instead. Keyed on
`noTools` because the two coincide exactly today and every `WithNoTools` caller was audited; a
future caller wanting protocol markers WITHOUT tools needs an explicit opt-in rather than
inheriting one by accident, and a test pins both halves because deleting the protocol clause
outright would look like a tidy-up and break every agent run. `prompts.StripProtocolMarkers` is
defence in depth on the assist endpoint only: a prompt steers and does not guarantee, and that
endpoint's result is prose the user pastes over their own writing.

**The KB editor adopts a change made to the open note by something else — clean silently, dirty on
request.** The chat coder holds Read/Write/Edit over the vault (that is what "Edit with AI" drives),
but nothing invalidated the note query, and `NoteEditor`'s seeding effect latches on `initializedRef`
and ignores every later `data` — so a rewrite the user had just asked for was invisible until they
reloaded the page. `ChatWindow.sendTurn` — the single point at which any chat turn completes on any
surface — now invalidates `["kb-note"]` and `["kb-tree"]`. The whole prefix, not one path: the
browser has no idea which file the model touched, and React Query refetches only ACTIVE queries, so
in practice it is one request for the open note. It hangs off the TURN, not off the panel closing,
because the user watches the reply land and expects the note to follow.

`NoteEditor` compares incoming content against **`lastSyncedRef`** — the exact bytes last loaded or
last successfully PUT — and not against "any new data": `useSaveNote` invalidates `["kb-note", path]`
on success, so **every autosave already causes a refetch**, and a naive comparison would fire on
every keystroke pause and toast the user about their own typing. Clean ⇒ adopt silently (`editorKey`
remounts `WysiwygEditor`, because TipTap's `useEditor` reads `content` only at creation; **the caret
is lost**, accepted). Dirty ⇒ adopt nothing, toast with a Reload action. The clean/dirty split is not
politeness — this file has a recorded data-loss history around `dirtyRef`, and an unconditional swap
would discard unsaved work to apply a change the user may not have asked for yet. A file changed by
something outside this browser (an agent run, another tab) is still only picked up on the next load.

**"Edit with AI" and "Chat about this file" BOTH auto-send.** `ChatWindow` already had
`autoSend` (built for the setup wizard's closing action, with its per-mount ref and
empty-history guards); `GlobalChatPanel` simply did not forward it. The message had to change too:
`selectionChatPrompt` ends in a blank line — a citation waiting for an instruction — so sending it
alone asks the model nothing. `selectionEditPrompt` is its sent counterpart, and its closing
*"apply the change to the file directly"* is load-bearing: without it the model proposes a rewrite
in chat and writes nothing, so there is no external change to pick up and the feature reads as
broken from the other end.

`ChatAboutFileButton` used to park its citation in the composer instead, and the consequence
showed up one surface away: the chat it created held **zero messages**, so "Open full page"
navigated to that chat correctly and looked like it had started a *different, new* one — the
prefill lived in component state and did not survive the remount at `/chats`. Auto-sending makes
the citation a real persisted turn, which fixes both halves at once. `chatPrompt` had to gain an
instruction for the same reason `selectionEditPrompt` has one; a test pins that it does not end
in a dangling separator, which is the property that makes auto-sending safe.

**Block alignment is a `<div align>` WRAPPER, and the obvious spelling is a trap.**
`nodes/align.ts` (`kbAlign`, `content: block+`) is how text and images centre or right-align.
Markdown has no alignment, so persisting it means raw HTML — and
`<p style="text-align:center">…</p>`, the shape `@tiptap/extension-text-align` produces, fails
for a reason invisible until you try it: markdown-it treats a `<p …>` line as a CommonMark
**type 6** raw HTML block and does **not** parse inline markdown inside it, so aligning a
paragraph would turn its `**bold**` into literal asterisks (the toggle's `<summary>` hits exactly
this and has to serialize its marks as HTML tags to survive). It would also walk into the
`renderSpec`/`cssText` hazard `marks/colors.ts` documents. A `<div align="…">` wrapper with a
**blank-line-separated body** avoids both: markdown-it closes the type-6 block at the blank line,
so the body stays ordinary markdown — real paragraph, list, table and image nodes with real
marks — and it is the standard README centring idiom, so pasted snippets and vault-writing agents
already produce it. Same canonical-form rule as the toggle: the glued (`<div align="center">Hello</div>`)
and `style=`-spelled forms still PARSE and normalise on the first save, opening read-only until
then. That is a **strict improvement**, not a regression — measured before the work started,
EVERY div spelling previously lost its wrapper entirely and had its alignment discarded on save.
`parseHTML`'s `div[style]` rule carries a `getAttrs` guard so it claims only a div that is
actually aligned; without it every styled div in the vault would be wrapped in an alignment
nobody asked for. **The `div[align]` rule declines a div that also carries `data-cols`**, which
the columns node owns: ProseMirror tries parse rules in extension-REGISTRATION order, so the same
input would otherwise parse differently depending on which node was imported first, with the
loser's attribute silently dropped. Nesting the two wrappers round-trips cleanly in both
directions (measured) and is what the interface actually produces, so the combined-attribute form
only arises from a hand edit — and `checkFidelity` catches it, opening the note read-only rather
than letting a save discard the attribute. `setBlockAlign` tries `updateAttributes` BEFORE `wrapIn` (wrapping twice
serializes as two nested divs that read as one) and deliberately does not gate on
`editor.isActive`, which reports false for an AllSelection; `clearBlockAlign` builds its lift range over the
WHOLE node's content rather than calling `commands.lift(name)`, which lifts the range around the
SELECTION and so takes the first block out of a two-block wrapper and leaves the second aligned. Alignment lives in the fidelity corpus, not beside it.

**In `@tiptap/react` v3, `editor.isActive()` read during render is FROZEN.**
`useEditor`'s `shouldRerenderOnTransaction` now defaults to **false**, so a component computing
`isActive` in its body evaluates it once and never again. `BubbleToolbar` did exactly that, so
Bold, Italic, Underline, Strike, Code, both headings, Link, both lists and Quote all highlighted
according to whatever the state was when the bubble menu appeared. Nothing failed — the commands
worked, only the indicators lied, which is why it went unnoticed. It reads its flags through
**`useEditorState`** now (the v3 idiom, which re-renders only this subtree) rather than
`shouldRerenderOnTransaction: true`, which would re-render `WysiwygEditor` and everything it
holds on every keystroke. The regression test toggles **Bold**, not an alignment button, because
it is the row's oldest control: if this breaks again it breaks for all of them.

**Alignment's export degradation is the mildest of the set** — `internal/export`'s goldmark
(built without `WithUnsafe()`) replaces the two `<div>` lines with `<!-- raw HTML omitted -->`
while the body, being ordinary markdown OUTSIDE the HTML block, survives with its formatting
intact. Alignment is lost; content and marks are not. Compare the toggle, which loses its summary
text along with its wrapper.
**Columns: each DIRECT CHILD of the wrapper is one cell — there is no cell node.**
`nodes/columns.ts` (`kbColumns`, `content: block+`, `data-cols` 2–4, slash-menu entries
"2/3/4 columns") is how two images or two paragraphs sit side by side. The one-block-per-cell
shape is what makes it round-trip: the obvious alternative — a wrapper div containing one div per
cell — nests raw HTML blocks inside raw HTML blocks, while this reuses exactly the mechanism
`nodes/align.ts` proves (one `<div …>` opening tag, blank line, ordinary markdown blocks, blank
line, `</div>`), so markdown-it closes the type-6 block at the first blank line and every cell is
parsed as real markdown with real marks. Three costs, all stated rather than designed around:
**a cell is one block** (a heading AND a paragraph in one cell needs nesting, which this does not
do); **two adjacent lists cannot be two cells**, because `- a\n- b\n\n- c\n- d` is ONE loose list
in CommonMark — verified outside any wrapper, where it fails identically, and the corpus pins both
so the next reader does not "fix" the wrapper; and **no div-based grid renders as a grid outside
Rookery**, since GitHub's sanitiser strips `class`, `data-*` and `style` alike (only a `<table>`
would, and a markdown table forces a header row this editor promotes on save) — so a columns block
degrades elsewhere to its cells stacked in order with every image and mark intact, which is the
right failure and why `class="kb-columns"` is added by `renderHTML` and never written to the file.
`insertColumns` inserts N EMPTY cells rather than wrapping, because the slash menu runs on the
empty paragraph left after the `/query` range is deleted and wrapping there yields a one-cell
layout. **`clearColumns` builds its lift range over the whole node's content**, not
`commands.lift(name)`: that lifts the range around the SELECTION, so on a two-cell block it takes
the first cell out and leaves the second wrapped. **Cell boundaries are drawn on hover only**
(`editor.css`): with prose in both cells the block was indistinguishable from one wide wrapping
paragraph, and there was no handle saying a layout was there at all. The divider is a
`border-left` present on every cell but the first and merely TRANSPARENT until
`:hover`/`:focus-within` — colouring an existing border cannot reflow the grid, whereas adding one
on hover would shift every cell by a pixel as the pointer arrives. The CSS uses `minmax(0, 1fr)`, never a bare
`1fr` — a grid item's automatic minimum size is content-based (CSS Grid §6.6), the same trap
`DialogContent`'s `grid-cols-1` fix records.

**KB rich-text editor: five formatting/AI constructs.** The WYSIWYG editor (`web/ui/src/pages/kb/`)
adds underline, two colour marks, callouts, toggle lists, resizable images, and the AI actions panel
above, all as TipTap/ProseMirror extensions layered on `buildExtensions()` (`editor.ts`). Three
constraints turned out to be load-bearing enough that they're worth knowing before touching any of
this — each currently documented only inline, in the file it governs:

- **Mark registration order sets DOM nesting, which sets colour precedence.** `buildExtensions()`
  registers `KBBgColor` before `KBTextColor` — TipTap ranks marks by registration order, and the
  lower-rank (earlier) mark renders as the OUTER span on serialize, the higher-rank (later) mark as
  the INNER one. Since an element's own `color` is applied directly rather than inherited, whichever
  span sits closest to the text wins — so `KBTextColor` must be innermost for a text colour applied
  inside a highlight to override the highlight's pinned foreground, while a highlight with no text
  colour still shows its own pinned foreground (nothing nested inside it to override it). Reordering
  those two registrations silently flips which colour wins wherever both are applied to the same
  text. See the comment above `KBBgColor` in `editor.ts`.
- **ProseMirror's `renderSpec` hazard forces the colour marks to build real DOM nodes instead of spec
  arrays.** Returning the usual `["span", attrs, 0]` tuple from `renderHTML` breaks colour fidelity:
  `prosemirror-model`'s `renderSpec` special-cases an attrs key literally named `style` by assigning
  it via `dom.style.cssText = …` rather than `dom.setAttribute("style", …)`, and `cssText` assignment
  round-trips through the CSSOM, which canonicalizes any recognized colour into `rgb(...)` — so
  `"#ef4444"` comes back as `"rgb(239, 68, 68)"` the moment the mark serializes, independent of
  parsing, and `checkFidelity`'s byte-for-byte comparison then fails and the note opens read-only.
  `KBTextColor.renderHTML`/`KBBgColor.renderHTML` in `marks/colors.ts` sidestep this by constructing
  the `<span>` element themselves and calling `setAttribute` directly, preserving the literal hex.
  `kbImage.ts`'s width attribute hits the identical hazard for the same reason (see its
  `renderHTML` comment) — its NodeView applies the pixel width as inline style directly on the DOM
  element rather than through an attrs-keyed `style`.
- **The toggle's canonical serialized form is `<details>`/`<summary>` on SEPARATE lines, and the
  glued-together spelling (`<details><summary>...`) is not a fixed point alongside it.** Both forms
  parse to the identical ProseMirror doc (markdown-it treats each as CommonMark "type 6" raw HTML
  blocks), but a serializer can only ever reproduce ONE canonical spelling — parsing throws away
  whether the source had them glued or on separate lines — so the two are mutually exclusive
  canonical choices, not a matter of preference. Separate lines won because it's GitHub's own
  documented convention and the form real-world markdown (a pasted README snippet, a vault-writing
  agent) actually produces. `nodes/toggle.ts`'s top comment has the full reasoning, including the
  prior reverted attempt that glued them — do not "fix" this back to gluing, it would only move the
  read-only-until-first-save gap onto the more common input.

Also worth carrying over: `AIActions.tsx`'s `selectionMarkdown`/`accept()` are what make the AI
actions panel selection-aware rather than document-wide (captured range remapped through every
editor transaction while the bubble menu is unmounted, verified live before writing); `lib/copyText`
is the ONE clipboard write in the whole app for the reason given at its top (`navigator.clipboard`
is undefined over plain HTTP on a LAN, the normal way to reach a self-hosted install) — a KB or chat
surface reaching for `navigator.clipboard` directly instead is a bug, not a style choice.

**KB import fidelity: a converter that emits the wrong markdown makes the note
UNEDITABLE, not merely untidy.** `checkFidelity` (`editor.ts`) round-trips a
note's body through a real parse/serialize cycle and compares; a mismatch opens
it read-only, so no keystroke marks it dirty and no save path runs. Converters
wrote extracted document text verbatim, and driving the real editor over 43
realistic converter outputs, **17 failed** — a Word document saying `a < b`, a
PDF citing `[12]`, a path like `C:\Users` each produced a note nobody could edit.
`convert.EscapeInline` fixes that, and three of its properties are the kind that
get "simplified" back into bugs:

- **The escaped forms must be FIXED POINTS.** Changing the bytes is not enough;
  the escaped form must itself round-trip, or escaping trades one unopenable note
  for another. Each was verified against the real editor.
- **The characters left ALONE are as load-bearing as the ones escaped**, and the
  list is not the intuitive one. `_` survives untouched (escaping it puts a
  backslash inside every `snake_case` identifier), and `&` must NOT be escaped —
  `&amp;` round-trips back to a bare `&`, so escaping it CREATES the failure it
  is meant to prevent.
- **A table cell, a link label and a link destination take different rules.**
  `escapeCell` adds the pipe; `escapeDestination` escapes parens and encodes
  spaces and must never HTML-escape, since `&lt;` in a path is a broken path.

**The fidelity corpus deliberately spans two languages, because neither half can
check this alone.** The editor runs only in vitest under jsdom; the converters
run only in Go. `TestFidelityCorpus` writes what `ToMarkdown` ACTUALLY produced
into `internal/convert/testdata/fidelity/`, and `convertFidelity.test.ts` runs
the real `checkFidelity` over those bytes. The frontend already had a test
asserting converter-shaped markdown survives the editor — but its fixtures were
hand-written approximations, so it pinned a string rather than the package, and
Go had drifted away from it without ever failing. Regenerate with
`go test ./internal/convert/ -run TestFidelityCorpus -update-fidelity`. It found
a real docx bug within minutes of existing: a table written straight after a list
item was absorbed INTO that bullet and every row was lost, because markdown
continues a list item across a single newline.

**Embedded images: `internal/convert` returns them, it does not store them.**
The package is a pure function of its input, which is what makes it testable
against golden fixtures — so it hands images back in `Result.Assets`, referenced
as `rookery-asset:<n>`, and `vault.ImportFile` (the one choke point the web
upload, chat attachments, `save_to_kb` and the CLI bridge all funnel through)
writes them into `uploads/` and rewrites the references. That is the folder and
path shape the editor's image picker and the export inliner already consume, so
an extracted image renders, exports and can be re-inserted with no new storage
location and no new route. A distinct SCHEME rather than a plausible relative
path is deliberate: a caller that ignores `Assets` leaves a visibly unresolved
reference instead of one that silently points at nothing. An `External`
relationship target is ignored outright — following it would turn a document
import into a network fetch.

**Export fidelity is NOT uniform across the five constructs** — `internal/export`'s HTML/PDF/DOCX
path (goldmark built without `html.WithUnsafe()`, so raw HTML is replaced with the literal comment
`<!-- raw HTML omitted -->` rather than rendered, precisely so a note can never inject a `<script>`)
degrades each one differently depending on whether it's raw HTML on the wire or plain markdown:
- **Toggle** — worst case: `<details>`/`<summary>` are both raw HTML on the wire, so the wrapper
  AND the summary TEXT are dropped together (the summary's words live inside the omitted block, not
  beside it). The body survives, but as an ordinary paragraph with no indication it was ever inside
  a collapsible.
- **Underline, both colour marks** — the `<span style>`/`<u>` wrapper is raw HTML and is dropped,
  but the enclosed TEXT is an ordinary child node the renderer still walks, so the words survive with
  formatting stripped.
- **Callouts** — markdown, not raw HTML, so they survive structurally but degraded: a callout
  serializes as a plain `> [!kind] title` blockquote (`nodes/callout.ts`), which goldmark renders as
  an ordinary `<blockquote>` with the literal `[!kind]` marker text visible, since it has no notion
  of Obsidian's callout syntax.
- **Columns, alignment and resized images NO LONGER degrade** — see below.

See `marks/colors.ts`'s top comment for the toggle/colour-mark case specifically.

**Three of those degradations were one reported bug, and the fix is an AST transformer rather
than `WithUnsafe()`.** A note with resized images in a two-column grid exported as full-size
images stacked in PDF, and in DOCX with no images at all. Three independent causes:

- **An image's width was written and never read.** The editor serializes a resized image as
  `![alt|420](src)` with the width in the ALT SLOT (`kbImage.ts`), and nothing on the export side
  split it — `inlineVaultAssets` copies alt through verbatim, so goldmark emitted
  `alt="before|420"` with no width and the literal `|420` as visible noise. `export.SplitAltWidth`
  mirrors the editor's TypeScript `splitAltWidth` rule for rule (split on the LAST pipe, only when
  the tail is a bare integer, so an alt containing a pipe survives); a shared test corpus is not
  possible across the language boundary, so `TestSplitAltWidthAgreesWithTheEditor` enumerates the
  contract instead.
- **The layout wrappers were dropped as raw HTML.** `internal/export/layout.go` is a goldmark
  `ASTTransformer` that matches exactly `<div data-cols="N">` and `<div align="…">`, finds the
  matching `</div>` sibling, and moves what is between them into a real node.
- **DOCX had no image support at all** — its block switch had no `ast.Image` case, and the handler
  deliberately skipped inlining for it. It now takes the same inlined markdown the other two
  formats do.

**The transformer is NOT a `WithUnsafe()` in disguise, and the distinction is the whole point:
user HTML is never passed through.** Two known shapes are recognised and OUR OWN markup is emitted
from a fixed whitelist; every other raw HTML block still renders as `<!-- raw HTML omitted -->`.
`TestOtherRawHTMLIsStillDropped` pins it, because the tempting future "simplification" is to turn
unsafe mode on and delete the file.

**It works at all because of a measured fact about goldmark**, and checking it was what decided the
design: goldmark parses the wrapper as a CommonMark **type-6** HTML block that CLOSES at the blank
line, so the opener and closer arrive as SEPARATE sibling `HTMLBlock` nodes with the body between
them as ordinary block nodes carrying real inline marks. The wrapper is addressable and the content
is not trapped inside it. (markdown-it behaves identically, which is why the editor's
blank-line-separated form round-trips there — see `nodes/columns.ts`.) Had goldmark swallowed the
region into one block, this approach would have been impossible.

**One transformation feeds BOTH renderers**, because the DOCX writer walks the same AST. That is the
property a converter-based approach could not have had, and it is why **pandoc was scoped and
dropped** — recorded here so it is not re-proposed: it cannot be verified (not installed on the
development host, so every claim about its output would be untested, which is an `unverified: true`
connector's standing applied to the one component whose whole justification is fidelity), and
**Word has no CSS grid**, so pandoc converting a `<div data-cols>` produces the stacked cells that
are the reported symptom. It would also have turned `AvailableFormats()`'s unconditional
`DOCX: true` into a host probe.

Three implementation details are load-bearing:

- **An unbalanced opener leaves the AST untouched.** Consuming the rest of the document into a
  wrapper its author never closed turns a cosmetic defect into a note that has visibly lost its
  ending. The two wrappers also NEST (the editor produces align inside columns), so the scan
  recurses into a node it has just built.
- **HTML emits `repeat(N, minmax(0, 1fr))` and never a bare `1fr`** — a grid item's automatic
  minimum size is content-based (CSS Grid §6.6), the same trap already recorded for `DialogContent`
  and `PageContainer`.
- **DOCX renders a grid as a BORDERLESS single-row table**, which is Word's only side-by-side
  construct, with every border explicitly `none` (a `w:tbl` without `tblBorders` inherits the
  document default and arrives with visible gridlines). Alignment is a PARAGRAPH property there, so
  `applyJustification` reaches each `<w:p>` rather than wrapping — a `w:jc` outside `w:pPr` makes
  Word report the whole document as damaged. An image whose bytes cannot be decoded is **degraded
  to its alt text rather than embedded at a guessed size**: DOCX requires an explicit extent in EMU
  (`px × 9525`), and a stretched picture is worse than an absent one and much harder to attribute.
  The DOCX tests **unzip the output** and assert the media part, the image-type relationship, the
  content-type declaration and the real EMU extent, because every one of those can be wrong while
  the file is still a well-formed zip of well-formed XML that Word opens and renders incorrectly.

**Attachments are LISTED, not embedded, and that is a constraint rather than an oversight.**
`inlineVaultAssets` is image-only because goldmark deliberately blanks a `data:` URI in an
`<a href>` — a security property this path keeps — so a linked PDF cannot ride the same mechanism.
`web.collectAttachments` gathers non-image vault-relative link destinations and both renderers
append an **Attachments** section naming each file AND its path: the reader of a downloaded
document cannot follow a relative link, and the path is what lets them ask for the right thing.
Images are excluded from the list, since the document already carries them.

**KB table editing is a control surface, not a capability.** TipTap already implements
`addRowAfter`/`deleteColumn`/etc.; nothing reached them, so a table was inserted at a fixed 3x3 and
never changed again. The slash item now dispatches `kb:insertTable` (the same window-event pattern
Image and File attachment use, since a React dialog cannot open from an editor command) and
`TableSizePicker` offers a hover grid up to 8x8; `TableControls` renders hover handles carrying
insert-before/insert-after/delete. Four things are load-bearing:
- **Every action goes through an editor command, never a DOM edit** — the commands produce the
  canonical document, and a subtly different one makes `checkFidelity` open the note READ-ONLY on
  the next load while the table still looks correct on screen. `tableEditing.test.ts` round-trips
  every operation, every picker size 1x1–8x8, and a pipe-bearing cell (the `pipeSafeTable` case).
- **Hovering sets the caret** into the cell, because TipTap's commands are selection-relative.
  Without it the buttons operate on whichever cell was clicked LAST — the worst failure available,
  since it looks like it worked and edits the wrong row.
- **`tableGeometry.ts` is pure** for the same reason `placeMenu` is: jsdom reports zeroes for every
  rect, so a test driving the real editor proves a handle MOUNTS but never where it lands.
  `clampToViewport` pushes a handle back inside the edge rather than hiding it — an overlapping
  handle is usable, an invisible one is not. `cellCoords` honours `colSpan`, or a merged cell
  inserts the column in the wrong place on exactly the tables hardest to repair by hand.
- **The header-row checkbox states its own caveat**: markdown has no way to express a table WITHOUT
  a header row (the delimiter line is mandatory), so a headerless table has its first row promoted
  on the next save.
Merged cells are deliberately not offered — `pipeSafeTable` already drops a `colspan`/`rowspan` note
to the HTML/placeholder path, so a merge button would be a button that makes the note read-only.

**Onboarding ends in ONE action, and the chat had to learn the product first.**
The wizard's Done screen used to offer two co-equal buttons — *Create your first agent* and
*Explore the knowledge base* — asking a brand-new owner to choose between things they have no
basis to compare, one of which led to a knowledge base that is empty at exactly that moment.
It now offers exactly one, chosen by `web.workspaceCoderReady`: with a coder, **"Explore what
you can do!"** opens a new chat with an opening question already sent; without one, **"Create
your first agent"**. Four things are load-bearing:

- **`workspaceCoderReady` is not `CoderKind != ""`.** `coderKindOrDefault` fills that column on
  every write, so it is non-empty for a workspace that skipped the coder step entirely. The
  predicate asks for the fields the engine needs (provider+model, or a binary) and is computed
  server-side; it deliberately does **not** probe the filesystem, because detection ("is one on
  PATH now") is a different question from configuration — the same split `ROOKERY_CODER_MODE`
  draws. The SPA defaults it to false, so a missing flag offers the agent builder rather than a
  chat with nothing behind it.
- **The coder step gained a Skip, and that is what makes the second branch reachable at all.**
  The server has always accepted `{step:3, skip:true}`; nothing rendered a control for it, so
  no user could arrive at Done without a coder.
- **`BuildChatSystemPrompt` now injects `platformContextBlock`, and the block takes the SURFACE
  as a parameter.** Chat previously got `productIdentityBlock(SurfaceChat)` alone, which names
  the knowledge base, agents, skills, reminders and connections and says nothing about secrets,
  MCP servers, providers, coders or chat apps — so a button inviting the owner to ask what the
  platform can do would have opened a conversation that could not answer. The block embeds
  `productIdentityBlock`, so hardcoding `SurfaceAgent` (as it did) would tell a chat "right now
  you are an AGENT run" and license the output-protocol markers at a human; a test pins that it
  does not. It goes in the system prompt, not per-turn context: identical across turns,
  therefore cacheable, and chat is the highest-frequency coder surface.
- **Fixing the surface was necessary and was not sufficient — the block wrote the output
  protocol unconditionally.** So chat still carried `## Output protocol (how agents
  communicate)`, a standing instruction to wrap replies in `[CHAT]`, and models obliged: on a
  live install **30 of 192 assistant rows had leaked at least one marker**. It read as model
  flakiness because compliance with a system instruction varies by model family, by strength
  and by turn depth — one session answered "what is the purpose of this platform?" cleanly
  while the turns either side of it were wrapped. Asking chat to be quiet reproduced it on
  **every** model tested, because `[SILENT]` was described there as the way to say nothing, so
  a request for silence steered straight into the protocol. Asked about the markers, the model
  told the owner it could not remove them because they were "part of the platform's protocol" —
  it was reciting this block. The section is now emitted for `SurfaceAgent` only (extracted to
  `outputProtocolSection` so the two variants cannot drift); chat is told instead that it is
  not an agent run and that a request to say nothing wants a short sentence, not a marker.
  Deleting it from the agent surface would silence every agent on the install, so a test pins
  both halves.
- **Fixing the surface and the protocol still left chat unable to answer the one question the
  button invites, and the gap had the same shape a third time.** Everything in the primer
  teaches the agent FILE — the `agents/<id>/` layout, `state.md`, the `# Suggested schedule:`
  header — because an agent run needs it, while **nothing in `internal/prompts` named the agent
  designer at all**. So a new owner who clicked *Explore what you can do!* and asked about
  agents was told to create one by hand and write AGENT.md: the model was reciting this block,
  because the file was the only concrete thing in its context and the designer did not exist as
  far as it knew. A hand-written file is not a registered agent — no schedule row, no bound
  connections, and it never runs — so the advice was not merely unhelpful. `platformContextBlock`
  now carries an **`## Agents — how they are created`** section that is surface-split like the
  output protocol: chat gets the navigation path (*Agents → New Agent → describe it in plain
  language → Build*) plus an explicit ban on telling the owner to author AGENT.md or touch
  `agents/`, and the **`## Agent schedule`** section drops the header syntax for chat while
  keeping it for agents, since that line is the most file-shaped thing in the primer and chat
  had been handed it verbatim. The agent surface is told about the designer too, for a different
  reason: an agent that rewrites its own AGENT.md loses the change on the owner's next edit,
  with neither side reporting the conflict. Both halves are pinned
  (`internal/prompts/agentcreation_test.go`) — deleting the authoring detail from `SurfaceAgent`
  would break every build — and a third test keeps the section out of the DESIGNER's own prompt,
  which must not be told to point at the screen it is already running on. This is the same shape
  as `TestInboxBlockPromisesNoChannelSelection`: a capability the product has, absent from the
  prompt, yields a confident answer pointing somewhere else.
- **The opening message is sent by `ChatWindow` AFTER navigation, never by the wizard before
  it.** `handleChatMessage` is a blocking coder call, so sending first would freeze the wizard
  on a dead button for as long as the model takes. Two guards make it once-only: a ref (per
  mount) and an empty-history check (per chat), the latter because `?intro=1` survives in the
  URL and a refresh would otherwise re-ask and spend another coder call. The text lives in
  `pages/chats/introPrompt.ts` as **UI copy, not a prompt** — `internal/prompts` owns what the
  model is told about itself, not a sentence attributed to the user and rendered in their own
  bubble.

**Chat knowledge-base access (on-demand retrieval + editing).** The one-off chat coder runs with `WithDir(vaultRoot).WithAllowedTools("Read,Write,Edit,Glob,Grep")` and a system instruction (`prompts.BuildChatSystemPrompt`) naming the vault root. The LLM retrieves and edits the user's notes **on demand** — only on turns that touch the KB — instead of having the vault injected every prompt. `chat.BuildUserContext` now returns identity-only context (profile/memory/agents/MCP); the old always-on `[Related knowledge base]` keyword-snippet block was removed. The tool set is file-only (no `Bash`/`WebFetch`): the chat can create/edit/read notes but cannot delete, rename, or run shell commands. The same applies to agents (RW over the vault via the sandbox). The detective `Guard` is no longer wired into agent runs — it would revert the KB edits that are now intentional — so agent/chat KB edits persist.

**Chat connector access.** One-off chat (both web `handleChatMessage` and Telegram) also exposes the workspace's **ACTIVE** service connections to the chat coder (`connectors.ActiveBoundConns` — all of them; chat isn't an agent so there's no per-agent binding), wired identically to how the API/CLI split works elsewhere: the **API engine** gets them as native function tools (`coder.WithConnectors`), a **CLI coder** reaches them via the loopback bridge (`bridge.Register` → `ROOKERY_CONNECTOR_URL`/`ROOKERY_CONNECTOR_TOKEN` env → `rookery connector exec`, plus a scoped `Bash(<bin> connector exec:*)` grant since chat is otherwise file-only). Both paths hit the same `connectors.Execute` (mutating allowed — chat is like a run, `buildPhase=false`). `BuildChatSystemPrompt(vaultRoot, backendType, conns, connToolNames, connectorBin)` appends `connectedToolsBlock` so the model knows the tools exist; with no active connections / no bridge, chat behaves exactly as the file-only default.

**Chat must never show the output-protocol markers, and the prompt gate alone does not
guarantee it.** `[CHAT]`, `[/CHAT]`, `[SILENT]` and `[STATE]` blocks belong to an agent RUN,
where `agentrunner.parseCoderOutput` consumes them as structure. Chat has no such parser —
`handleChatMessage` and the chat-platform handler passed `result.Text` straight to the
response, to `AddChatMessage` and to `MaybeAutoTitle` — so anything a model emitted was read
verbatim by a human. Removing the instruction (see the `SurfaceChat` gate above) is the cause
fixed; `chat.CleanReply` is the guarantee, because a prompt steers and does not bind.

**Every rule in it is LINE-ANCHORED, and that is the whole design.** A marker that OPENS a line
is protocol; a marker inside a sentence or in backticks is the model explaining itself. Both
shapes occur in real transcripts — `\n\n[STATE]{"last_email_search": …}[/STATE]` is a leak,
`` - **`[STATE]{"key": "value"}[/STATE]`** — saves data between runs `` is documentation. A
substring replace cannot tell them apart: it was the first implementation, and on a real reply
enumerating the four markers it emptied the code span and left a bullet describing something no
longer named. Verified against all 192 assistant rows of a live install: 30 carried markers, 0
residual leaks, 0 clean-prose rows rewritten. Do not "simplify" this back to `strings.ReplaceAll`.

**It is deliberately NOT shared with `prompts.StripProtocolMarkers`.** That one serves KB assist,
where the input is a passage the owner is about to paste over their own writing, so content
between markers IS the answer and a `[STATE]` body is kept (its own test pins this). Here the
input is a conversation and a leaked state block is machine memory, so the block goes whole.
Same tokens, opposite policy, because the inputs differ — merging them is the obvious future
cleanup and would reintroduce one bug or the other.

**A reply that was nothing but markers gets a placeholder, never the raw text.** Ten live rows
are a bare `[SILENT]` — the model complying with "don't say anything" using the one marker the
prompt had given it. Showing raw re-displays the exact leak; an empty bubble reads as being
ignored, the lesson `UserFacingDesignText` already records. `CleanHistory` is the other half and
is not cosmetic: history is fed back as prior turns, so a leaked reply is few-shot evidence to
keep leaking, and one transcript shows the escalation plainly — clean early, wrapped on nearly
every turn after the first leak. It cleans ASSISTANT turns only (the owner's words are not ours
to rewrite) and leaves a marker-only turn raw, because that output is for the MODEL while the
placeholder is a message for a human. `toAPIChatMessage` cleans on the READ path too, which
repairs conversations that leaked before the fix shipped without touching a single stored row.
Both designers share the cleaner via `UserFacingDesignText`, where **`stripTechnicalSpec` must
stay ahead of it** — the cleaner would remove a line-opening `[TECHNICAL SPEC]` delimiter, after
which the block can no longer be found and the whole machine-facing spec renders to the user.

**A genuinely EMPTY reply gets its own placeholder too, and that is a different case from
the one above.** `CleanReply` returned `""` when the model produced no text at all, and the
handler persisted it unguarded — so the owner got a blank bubble. Four such rows exist on the
reporting install, one of them the answer to a question about a 155 KB table the model could
not read within its tool-result cap, which is how it read as the model ignoring the question.
`#242` covered only the marker-only case. `emptyReplyPlaceholder` is deliberately distinct
from `markerOnlyPlaceholder`: nothing came back at all, so retrying is the useful next step,
whereas a marker-only reply was a deliberate (if malformed) decision to say nothing. The empty
return also fed the NEXT turn — a blank assistant row in the stored transcript is few-shot
evidence that answering with nothing is acceptable here. `UserFacingDesignText` is unaffected
because it guards `shown != ""` before calling in.

**A chat turn is DURABLE: it outlives the request that started it.** `handleChatMessage` used
to run the whole turn inline — the coder executed on `c.Request().Context()` and **both**
messages were persisted only after it returned. So for the entire turn the owner's message
existed nowhere but the browser's component state, and leaving the page destroyed it; closing
the tab cancelled the context and killed the turn outright. Navigating *within* the SPA kept
the fetch alive, so a turn that happened to finish while they were away did land both
messages — which is why it read as flakiness rather than as a bug.

`web/chat_turn_tracker.go` follows `run_tracker.go`, which already solved this for manual agent
runs: persist first, run on a detached `context.Background()`, track in memory keyed by chat id,
stream over SSE. `POST /chats/:id/messages` answers **202 + `turn_id`** (no longer
`{"response": …}`); `GET /chats/:id/turn/progress` carries the milestones; `GET /chats/:id`
gains **`in_flight`** and **`turn_lines`**. Six things are load-bearing:

- **History is read BEFORE the message is persisted.** It comes from `ListChatMessages`, so
  writing first feeds the turn its own text twice — once as a prior turn, once as the message.
- **A failed turn KEEPS the user's message.** Not persisting on failure was defensible while
  the browser held the bubble in memory; now that it is durable, deleting it would be worse —
  they typed it, and it is the retry's context. The reason arrives as the stream's last
  milestone (`⚠️ …`) and is promoted to the banner, preserving the error visibility the inline
  path had.
- **The stream REPLAYS milestones already emitted** before following the live channel, or a
  client attaching to a busy turn watches an empty card until the next tool call — the same
  "nothing is happening" impression this change exists to remove.
- **`sendTurn` still resolves on turn COMPLETION**, not on the POST. `attachFiles` sends one
  confirmation per file serially and the server refuses a concurrent second turn, so returning
  early would make a multi-file batch collide with itself and collect its own 409s.
- **The server's named `error` event means the turn died; `onerror` means the TRANSPORT
  dropped** — which EventSource also fires during its own transparent reconnect. Conflating
  them reports healthy turns as failures.
- **One turn, one stream.** `in_flight` is false in the cache when a turn starts (the send does
  not refetch), so a refetch *during* the turn — window focus, which TanStack does by default —
  flips that dep and fires the re-attach effect for a turn `sendTurn` is already following.
  Without `streamOpenRef` that opens a second stream: every milestone lands twice and
  `finishTurn` runs twice. Leaving the page and coming back is exactly the scenario this
  feature exists for, so it is also the one that triggers it.

The progress UI is the **existing `ActivityCard`** — chat simply had no stream to feed it. It
gained `defaultCollapsed` so a chat shows the current action with the history one click away,
while an agent build keeps the full log it wants. `toolMilestone` already shortened vault and
`$HOME` paths; it now also masks canonical UUIDs, before truncation so a 36-character id cannot
spend the whole 60-character budget — which fixes agent builds and runs at the same time.

**The concurrency guard is web-only, and the chat-platform path is still inline.**
`cmd/rookery/main.go`'s Telegram/Discord/Slack turn was deliberately left as it was — there is
no page to leave on a chat platform — but the two paths have now diverged, and the comment in
`runChatCoder` about keeping them in step refers to the coder WIRING, not the lifecycle. One
consequence worth knowing: a chat-platform turn and a web turn can still run concurrently on
the same chat. That was already true before this change, so it is not a regression; the new
guard simply does not cover it.

**Agent designer KB awareness.** The designer is text-only (`WithNoTools`) but its system prompt (`BuildDesignSystemPrompt`, `<knowledge_base>` block) now knows the app has a built-in vault that agents read/write, and is told to prefer it over Notion/external note apps for the user's own knowledge. Each design turn injects a fresh retrieval-backed block via `Flow.WithVault(v)` → `vault.BuildKBContext(v, workspaceID, query)` → `DesignSystemParams.KBManifest` — a folder-shape summary (`Vault.FolderSummary`, one line per folder regardless of how many files it holds — note this bounds bytes PER FOLDER, not in total as folder COUNT grows, which is why `BuildKBContext` gives the summary its own 2 KiB budget with a `…and N more folders` marker; unlike the old exhaustive path list that capped at 60 files/rendered 30) plus the passages most relevant to the conversation so far (via `Indexer().Search`, scored against the session's own recent user turns + the current message — the designer has no search tool of its own, so this is done for it on every turn). When nothing matches, the block says so explicitly and the prompt tells the designer to ask the user rather than invent a path. `skilldesigner.Flow` mirrors this identically (`WithVault`, its own `loadKBManifest`/`retrievalQuery`) — `BuildKBContext` lives in `internal/vault`, not `agentdesigner`, precisely so both designers can reach it without an awkward cross-designer import. `vault.NotePaths`/`Flow.WithKBLister`/the `kbLister` interface are gone — `BuildKBContext` was their only consumer.

### Unified conversational agent creation

Agent creation uses a single `agentdesigner.Flow` FSM shared between Telegram and web. No agent types — every agent is the same structure.

**FSM states:** `StateDescribing` (Telegram only) → `StateDesigning` → `StateVerifying` → `StateDone`

**`[TECHNICAL SPEC]` is emitted with the PROPOSAL, and that timing is load-bearing twice over.**
The prompt used to say "after the user approves, append this block" — a turn that does not exist,
because `stepDesigning` matches `isApproval` and calls `startGeneration` without another
`callCoder`. So the block was never written, while `BuildImplementationPrompt` refers to it by name
("the design's `[TECHNICAL SPEC]` proposed a Tier:") and had been reading a block nothing produced.
The designer now appends it to the message that proposes the plan, which fixes that AND supplies
the one signal the browser lacked: **whether the conversation has moved from questions to a settled
plan.** `fsmState === "designing"` cannot say — a clarifying question and a finished proposal are
the same state — which is why the Build button used to offer itself under "Which page should I
watch?".

Handling mirrors `roleNote` exactly (`internal/agentdesigner/technicalspec.go`): **History stores
the raw text**, block included, so `dbMessagesToPrompt` still feeds it to the generator; the block
is stripped at the two edges the USER reads from — `callCoder`'s return value and
`web.designHistoryDTO`, both through the exported `StripTechnicalSpec`, so the live transcript and a
resumed one cannot disagree. Strip-before-store is the tempting simplification and would silently
re-break the implementation prompt in the same invisible way.

`DesignSnapshot.PlanReady`/`PendingSpec` are **derived** by `planFromHistory`, not stored:
`agent_drafts` has fixed columns so a flag would need a migration, while History is already
persisted every turn by `saveDraft` — so a resumed draft recovers plan-readiness for free and the
flag cannot drift from the artifact it describes. It reads the **last** assistant turn only, which
is what makes the signal RETRACT: a follow-up question carries no block, so the button withdraws. A
latch-once-true flag would be a worse defect than the one it replaces. `extractTechnicalSpec`
requires a CLOSER (a response truncated by a token cap must not arm the button) while
`stripTechnicalSpec` drops an unterminated opener to end-of-string (a half-written block is not
prose either) — the asymmetry is deliberate.

**A designer turn is never allowed to be empty, and moving the spec block is what made that
possible.** When the model answers a small correction by re-emitting ONLY the `[TECHNICAL SPEC]`
block — which it does once a plan is already settled — stripping it for display leaves `""`, and
the browser rendered a **blank assistant bubble**: it reads as being ignored, offers no way
forward, and (since History stores the raw text) came back on every reload. `UserFacingDesignText`
is now the one edge both `callCoder` and `web.designHistoryDTO` go through, and it distinguishes
the two causes because they need opposite answers — a spec-only reply means the plan IS ready and
points at **View spec**, while a genuinely empty reply must ask again rather than claim progress
that did not happen. The replay path applies it to **assistant turns only**: the fallback is a
substituted sentence, and putting words in the USER's mouth would be worse than the blank.

`plan_ready`/`pending_spec` ship on **every** path returning a design body (`designTurnResponse`,
`handleDesignState`, `handleResumeDraft`), asserted on RAW response bytes, because the SPA coerces a
missing field to false and this codebase has already shipped one bug of exactly that shape.
**`isApproval` is deliberately NOT gated on `PlanReady`** — the button is the affordance, the typed
word is the gate; a model that forgets the marker costs discoverability, never the ability to build.
`DesignerSurface`'s `gateBuildOnPlanReady` is an explicit opt-in (agent pages only) rather than
"gate whenever the flag is absent", because the **skill** designer shares the component, returns its
own body with no such flag, and would otherwise lose its build button entirely.

**Approval triggers** — two tests, deliberately different:
- **`isApproval`** (used in `StateDesigning`, strict) — exact match on `"approve"`, `"go ahead"`, `"build it"`, `"create it"`, `"/approve"`, plus `"approve and build"`/`"approve and build it"` (and their `&` spellings), which is what the web button SENDS (trailing punctuation trimmed). Casual `"ok"`/`"yes"` while answering design questions does NOT launch a full generation run. **The phrase the button sends is a separate question from the label it shows**: this test is exact-match, so renaming the button without adding its phrase here would send text that falls through to an ordinary design turn and the button would silently do nothing. `internal/skilldesigner` carries the same list for the same reason — the two designers share one `DesignerSurface` and therefore one `BUILD_PHRASE`.
- **`isVerifyApproval`** (used in `StateVerifying`, forgiving) — also accepts `"yes"`, `"save"`, `"ok"`, `"looks good"`, `"confirm"`, `"go"`, `"do it"`, `"ship it"`, `"lgtm"`, `"perfect"`, `"great"`, the past-tense/synonym forms `"approved"`/`"accept"`/`"accepted"`, `"save agent"`/`"save the skill"`, and casual `"yep"`/`"sure"`/`"sounds good"`/`"go for it"`/`"all good"`, …, and excludes negative cues (`"don't"`, `"not yet"`, `"change"`, `"wait"`, `"instead"`). A natural confirmation saves the build instead of being read as a change request.

  **A word missing from this list does not merely fail to save — it rebuilds.** An
  unmatched reply falls to `stepVerifying`'s change-request branch, which drops the FSM
  back to `StateDesigning`; the user's *next* `approve` then matches the **designing**
  predicate and launches a second full generation run. Nothing reports this, so from the
  outside a finished agent silently starts rebuilding. `"Approved"` did exactly that in
  production — the list had `"approve"` and `"confirmed"` but not the past tense — costing
  a six-minute rebuild. Hence the tense and synonym variants are enumerated rather than
  left to luck, and `internal/skilldesigner` carries the **same** additions: the two
  designers share one `DesignerSurface`, so a word that saves an agent and rebuilds a
  skill is the kind of inconsistency nobody finds until it costs them a build.

**A settled plan and a finished build both lock the composer, and the lock is tied to the
buttons being VISIBLE — not to the FSM state, and not merely to the buttons existing.**
Accepting goes through a button rather than a guess at which words the server takes.
`Make changes` / `Request changes` set `changesRequested`, which unlocks and focuses the
composer; the flag clears on every new transcript turn (keyed on turn COUNT, not `fsmState`,
which now locks during design too and would re-lock the box under the user's cursor).

Three conditions are load-bearing, and the third was learned the hard way:

- **Design locks only at a SETTLED plan** (`planReady || planInvitesApproval`), never merely
  because an action row is rendered. On a surface that does not gate on plan-readiness — the
  skill designer passes no `gateBuildOnPlanReady`, so `buildOffered` is always true — a row
  follows every assistant turn, and locking on that closes the box for the ordinary
  back-and-forth of answering the designer's own questions.
- **`planInvitesApproval` is the fallback for plan-readiness.** The server derives `plan_ready`
  from a `[TECHNICAL SPEC]` marker a weak model frequently never emits, so the gate never
  opened and the user was left with a finished plan and no buttons at all. A plan almost always
  ends by inviting approval in so many words ("Type approve and I'll build it"), and that
  sentence is the signal. Deliberately narrow — it must not match a clarifying question, which
  is the case `gateBuildOnPlanReady` exists to protect.
- **The bar renders OUTSIDE `ChatScroll`, and that is the whole fix.** It is a sibling of the
  composer, not a child of the transcript. Three reports of "the box is closed and there are
  no buttons" were diagnosed twice as logic and patched twice as logic; it was **layout**.
  `ChatScroll` is stick-to-bottom only while the reader is within `STICK_THRESHOLD` (80px) of
  the bottom, and scrolling up during a five-minute build — to re-read the plan, or watch the
  tool calls — clears that flag. The review card then rendered off-screen: the buttons were in
  the DOM, below the fold, while the composer sat locked by actions the user could neither see
  nor reach. Outside the scroll container that is structurally impossible, and it is also why
  the bar survives the Spec tab (which replaces the transcript's subtree, not the bar) — which
  in turn is what lets the composer stay closed there rather than needing a `view !== "spec"`
  escape hatch, as an earlier attempt did.

**`deadend.test.tsx` asserts the invariant directly** — a button is visible OR the box is
usable, across every reachable state — plus the structural property that the bar is **not a
descendant of the scrollable element**. jsdom has no layout engine, so nothing can prove a
button is on screen; ancestry is the strongest available proxy and the one that would have
caught this. `Cancel` counts as an action there: a running build deliberately has no bar
(there is nothing to accept yet) and the header's Cancel is the genuine escape.

**Showing the bar and closing the box are separate decisions.** Collapsing them removed the
build button from the skill designer entirely, which passes no `gateBuildOnPlanReady` and has
no plan-ready signal of its own. The bar follows the old row's rule so every surface keeps the
button it had; only `decisionPending` (a settled plan, or a finished build) closes the box.

**The dry run renders from the LAST ASSISTANT turn, not the last turn.** Gating it on the
last transcript entry being an assistant turn meant anything landing after the dry run hid
the finished build completely — no output, no Save, no Request changes. The easiest way in
is a turn that **fails**: it leaves the user's own message last and clears `busy`, while the
build sits intact on the server. Combined with the trigger gap above, the only remaining move
was to type a word and hope, and hoping wrong rebuilt the agent.

**The Spec view has two moments, and the dry run is not a chat bubble.** `SpecPanel` renders the
`[TECHNICAL SPEC]` block **before** a build exists (it previously had nothing to show until one
finished — exactly when a user most wants to re-read what they are about to approve) and the
generated `AGENT.md` + tools after. Its meta row now parses `# MCP:` / `# MCP servers:` alongside
`# Skills:` and `# Connections:`; an agent bound to an MCP server previously showed no sign of it.
The `[TECHNICAL SPEC]` block gained **`Connections:`, `Skills:` and `MCP servers:`** lines so those
appear in the PRE-build view too — they are the part of an approved plan a reader most wants to
check, and they existed only after a build, parsed off AGENT.md, which is the one moment they have
stopped being a question. That exposed a gap: **the designer had never been shown an MCP server
name** (`DesignSystemParams` carried `Skills` and `Connections` and nothing for MCP), so the line
would have invited it to invent one. `MCPServers []MCPServerRef` + an `<available_mcp_servers>`
block close it, fed by `Flow.mcpRefs` from the existing `buildBoundMCP` — every ENABLED server,
because a design session, like a build, has no bindings yet. A sibling of `<available_connections>`
rather than part of it, for the reason `MCPToolsBlock` gives: a connector action is a curated call
against a known API, an MCP tool is whatever a server chose to advertise.
`parseTechnicalSpec` follows `parseSchedule`'s policy — render only the closed set of labels the
prompt asks for, fall back to the raw block otherwise, because a plausible-but-wrong summary of what
an agent is about to do is worse than raw text the user can judge.

**That policy's other half now lives in `web/ui/src/lib/cron.ts`, and it is shared.** `parseSchedule`
is a thin caller of `describeCron`, which turns a cron expression into a phrase or returns **null**
when it cannot prove a reading — the same rule, stated once: a plausible-but-wrong plain-language
schedule is worse than the raw cron, because the user has no way to tell it is wrong. It moved
because the agent page's `ScheduleCard` needed it too (it previously showed the bare expression), and
two copies would drift into describing one expression two ways. It is **not** a general cron parser:
it recognises an enumerated set of field shapes and refuses everything else, and each branch
re-checks its values against the field's real range before emitting a word. Three refusals are the
ones worth knowing, because each is a case where confident prose would be WRONG rather than merely
absent — a step wider than its own field (`*/90` in a 0–59 minute field runs **hourly**, not every 90
minutes); a restricted day-of-month **together with** a restricted day-of-week, which cron ORs while
every natural phrasing of it reads as an AND; and an interval bounded by another field (`*/10 9 * * *`
is not "every 10 minutes"). Weekdays are named in cron's own numbering order rather than the order
typed, so `1,6` and `6,1` cannot render as two different schedules. `ScheduleCard` describes the
field being TYPED, not the saved schedule, and shows nothing for a half-finished expression.

A **View spec** button sits
beside *Approve & build* and beside *Save*, switching the existing header toggle rather than opening
a second surface. In `StateVerifying` the last assistant turn is promoted out of the bubble stream
into `ReviewCard` — the one turn where action is required used to be visually identical to every
question above it and scrolled past. That card is presentation only (no FSM state, no endpoint, no
response field), is deliberately not sticky (it would fight `ChatScroll`), keeps `MessageMeta` so
promoting a turn does not silently cost its copy button, and renders without an action row for a
read-only mirror.

**Change requests no longer discard the build.** When the user replies in `StateVerifying` with something that isn't approval, the session returns to `StateDesigning` but **keeps** `PendingAgentMD`/`PendingTools` in memory — a misfire (e.g. `"yes"`, `"save"`, `"ok"`) no longer silently drops the generated agent. The next approve re-generates with the change context and overwrites.

**On approval:** `runGeneration()` calls the coder with the same tool set as an agent run (`WithDir(agentDir).WithAllowedTools("Bash,WebFetch,Read,Write,Edit").WithProgress(notify)`) — so the coder runs REAL end-to-end tests against live services during the build, not mock-only. `WithProgress(notify)` streams the API engine's per-tool-call milestones (`🔧 run_script(...)` / `🔧 write_file(...)` / `🔧 web_search(...)`) to the build SSE + Telegram — the same live visibility a run has (agentrunner wires `WithProgress(OnProgress)`), so a weak-model build no longer looks frozen at the static `🤖 Coder is building your agent…` string. No-op for the CLI engine (it never calls the progress sink). Secrets are injected via `WithSecretsLoader`/`WithExtraEnv` so the real API calls the agent will make at run time are actually exercised here. The one hard exception: never send real OUTBOUND messages on the user's behalf at build time (enforced by the testing-rules prompt, not by withholding credentials). Coder writes `AGENT.md` + `tools/*.py` to disk, runs scripts via Bash, fixes errors, outputs `[TEST_OUTPUT]...[/TEST_OUTPUT]`. Flow reads AGENT.md, runs guardrails, stores in `PendingAgentMD`/`PendingTools`, moves to `StateVerifying`. A missing `[TEST_OUTPUT]` no longer automatically keeps the user in `StateDesigning`: on the API backend, if the engine CONFIRMED the authored script ran (`Result.ScriptVerified`) the build advances to `StateVerifying` with the captured real output shown (see "Script-verification bridge"); it only stays in `StateDesigning` when there's no confirmed run and no clean marker.

**A create build RUNS the agent once before showing it to you** (`internal/agentdesigner/dryrun.go`). `decideBuildOutcome` only ever has executed evidence when the build authored a script AND the engine confirmed it ran — so a TIER 1 agent, which has no script at all and is the correct tier for "call an API, compare, notify" and therefore the common case, fell back to a preview of the model's own prose presented as *"here's what a test run produces"*. Nothing had run. `Flow.dryRun` borrows only the runtime prompt and the output protocol, and is deliberately **not** `agentrunner.Run`: that needs a `db.Agent` row a draft does not have, and it writes an `agent_runs` row, an inbox message and a vault reflection — none of which a build may produce. It is create-only (an edit already has a live agent the user has seen work) and best-effort (any failure leaves the message exactly as it was, because a rehearsal must never fail a build that is already on disk and past its guardrails); it runs on `genCtx` rather than the request context, so `Cancel()` still stops it and a page navigation does not; and it costs one extra full agent run per create build — `dryrun.go` records one measured at over 1.5M tokens. When nothing executed, `reviewMessage(sample, false)` says so instead of calling prose a test run.

**Where it sits in `runGeneration` is load-bearing in three directions, and the first one was got wrong once already.** It must be BELOW the `if !outcome.advance { … return }` early return: a build that is not advancing never shows what the rehearsal wrote into `decision.message` — on the presentable-but-blocked weak-backend branch `reconcileBlockedOutcome` discards it for a message of its own — so from above that return the rehearsal was paid for in full, discarded, and followed by *"I couldn't confirm the helper it wrote actually runs"*, which is this feature's own thesis defect reintroduced by its own fix. It must be ABOVE `caveatTruncatedBuild`, which PREPENDS: the dry run replaces, so running it second would wipe the caveat off a build the engine cut short. And it must ASSIGN INTO `outcome.message` — the string that is returned, appended to History and rendered at review — by swapping the review message out of it rather than overwriting the whole thing, because `reconcileBlockedOutcome`'s advance-with-a-blocker branch prepends its own caveat there and a wholesale assignment would delete an explanation the user had already been given. (`cleanupOnSuccess()` is **not** the constraint an earlier draft of this paragraph claimed: for a create build it is a no-op — the draft dir IS the pending agent and survives until finalize — and for an edit it removes the staging dir, which never dry-runs at all.)

**The build-phase marker is only half of what restrains that rehearsal, and the prompt is the other half.** `buildphase.EnvVar` is set in `dryRun`'s `WithExtraEnv` exactly as the build call sets it (one map built in one place, because `WithExtraEnv` REPLACES rather than merges), and it gates `connectors.Execute` and `mcp.Execute` and **nothing else**. At build time the rest comes from `testingRulesBlock`'s "never send real outbound messages on the user's behalf", which `ImplementationParams.capabilitySpec` injects into the IMPLEMENTATION prompts only — and a dry run uses `BuildCoderPrompt`, the RUNTIME prompt, which says the opposite ("DO the task", "RUN that script"). So a TIER 2 agent holding an SMTP or bot token in a secret, with its own `tools/send.py`, would really have sent, during a rehearsal of an agent the user has not yet approved. `dryRunSendProhibition` closes that script/Bash path, and it is a PROMPT, not a boundary: it brings the dry run to parity with the build call beside it, which has always relied on the same prompt-level protection while injecting the same secrets and granting the same `Bash` — it does not make the rehearsal safe against a model that ignores instructions. Real enforcement would mean withholding outbound-capable secrets or confining the network, and neither is built. Two further traps, both of which look harmless: `dryRunPrompt` must be handed the workspace's `BackendType`, because `coderCapabilitiesBlock` falls through to the full-CLI branch on an empty one and would tell an `api`-engine workspace it has direct shell access when it actually has function tools — that yields prose instead of execution, which `reviewMessage(sample, true)` then labels as executed, reintroducing the exact bug the dry run exists to remove, from inside its own fix. And `restoreDryRunState` puts `state.md` back, because a rehearsal writes state like any run: `saveAndFinish` promotes the draft dir wholesale, `writeAgentContent` rewrites AGENT.md and `tools/` but not `state.md`, and `cleanupTestArtifacts` does not classify it as junk — so the rehearsal's state would ship as the saved agent's memory and a change-detection agent's very first real run would see "already seen" and stay silent. It removes a file the rehearsal created and restores the BUILD's version of one the rehearsal changed or deleted; deleting unconditionally would be simpler and would destroy legitimate build output. **An omission was once claimed to be doing real work here, and it was not.** `dryRunPrompt` used to pass **no `VaultRoot`** on the grounds that `BuildCoderPrompt` gates its whole `<agent_workspace>` block on that field, so a rehearsal never told where the vault is would not write there — described, in this file and in a comment beside the call, as "the only thing keeping a rehearsal out of the user's live knowledge base". It kept nothing out. A build and a dry run were each observed replacing a real user note, inode and md5 changing mid-build. Withholding the field withholds a *description* of the vault, never *access* to it, and **three** channels grant that access regardless of what the prompt says: `hostToolSet.resolveVault` validates a resolved path against the vault **root** rather than `workDir` (both its absolute and its relative branch), so anything under the vault is writable; `save_to_kb` writes to the knowledge base by contract; and `buildScriptCommand`'s per-invocation Landlock `Spec` puts `<data_dir>/vaults/<workspaceID>` in `ReadWritePaths`, so `bash`/`run_script` reach it too. The tool descriptions advertise this outright — `write_file` says "relative to the vault root, or absolute within the vault". The model was never escaping confinement; there was none.

**Containment is `vault.WriteJournal` now, and it undoes rather than prevents.** Prevention was rejected because a rehearsal that cannot write the knowledge base cannot rehearse a knowledge-base-writing agent, which is most of them — and a build is supposed to run real end-to-end tests. So both phases journal what they write into the protected region and revert it: `Flow.beginKBRehearsal` opens a journal and returns the revert, `runGeneration` attaches one via `coder.WithKBJournal` and reverts it **before** the dry run (so the rehearsal is not handed a knowledge base its own build altered), and `dryRun` opens a second, independent one. `Record` covers the writes the host process can observe; `AroundExec` brackets the ones it cannot — per `run_script`/`bash` call on the API engine, which keeps the attribution window to seconds, and around the whole `Generate` call for a CLI coder, which has no host tools to instrument and therefore a window as wide as the build. That wide window is a real limitation: every protected-region change inside a bracket is attributed to the rehearsal, so a concurrent chat or scheduled run writing a note inside it would be reverted too. The protected region is `Guard`'s, so `agents/` is excluded and the build's own output is never touched. `save_to_kb` needed no change — `verifyBuild` already refuses it in both phases. **Consequently `dryRunPrompt` now DOES pass `VaultRoot`**, deliberately reversing the earlier decision: the omission bought no safety and cost the rehearsal the fidelity that is its entire purpose. `TestDryRunPromptCarriesTheVaultRoot` and `TestBuildAndDryRunLeaveTheUsersKnowledgeBaseAsTheyFoundIt` pin both halves, the latter reproducing the original clobber when the journal is detached.

**Create-mode draft working dir (`draft_<slug>`).** A create build runs in a readable `agentdesigner.DraftAgentDir(vaultsBase, workspaceID, agentName)` = `agents/draft_<slugifyAgentName(name)>` — named from the agent's NAME, not the opaque UUID, so a work-in-progress agent is recognizable in the KB browser. The dir is KEPT across blocked/designing/verifying builds (a failed build never removes it) so a resumed draft's next generation iterates in the same place and `recoverBuiltAgentFromDisk` can recover an interrupted build. `finalizeAgent` (on save) promotes it to the canonical `AgentDir(<uuid>)` and removes the draft dir; the nightly GC sweeps `DraftAgentDir` on draft expiry (create only — edit drafts point `AgentID` at the LIVE agent and are never swept). `DesignSession.HasSaveableBuild` drives whether "keep it as-is" (`isKeepAsIs`) is offered.

**Test-artifact cleanup.** A real end-to-end test leaves junk in the agent dir (downloaded files, run outputs, scratch probes like `_probe.py`). `cleanupTestArtifacts(agentDir)` (post-approval, in `saveAndFinish`/`updateAndFinish`) removes that junk so only shipping source remains; artifacts persist through `StateVerifying` so the user can see real test output as proof. `isTestArtifact(path, name, toolsDir)` in `toolstree.go` is the shared classifier (binary-download extensions, run-output file names/suffixes, `_`-prefixed scratch probes at the `tools/` top level, root-level scratch `.json`). `ReadToolsTree` also skips test artifacts so they never corrupt the pending-tools map or trip guardrails.

**Generation failure handling:** `[BLOCKED]` marker, `ErrUsageLimit`, and timeout all return soft user-facing strings (not Go errors) — user stays in `StateDesigning`.

**SSE progress:** `DesignSession` carries `progressCh chan string` + `cancelGenerate`. `GetProgressChan(workspaceID)` lets the web SSE handler stream milestone events to the browser. The build streams the API engine's per-tool-call `🔧 …` milestones (via `WithProgress(notify)` in `runGeneration`) alongside the fixed `⚙️ Preparing workspace…` / `🤖 Coder is building your agent…` / `🔍 Validating agent safety checks…` strings — so generation is observable tool-call by tool-call, not a frozen spinner. The skill designer (`skilldesigner.runGeneration`) wires `WithProgress(notify)` the same way for parity.

**A design CONVERSATION turn streams too, and making that possible meant deleting an overload.** Since the conversation gained read-only tools it searches the knowledge base, sizes files and fetches URLs while it answers — and none of it was visible: `callCoder` wired no `WithProgress`, and `ensureSSE` (`DesignerSurface.tsx`) was only ever called on mount recovery, on a build, or when a POST reported one already running. The user watched a silent spinner while the designer read their notes. Both halves were required; either alone changes nothing. The blocker was that **`progressCh` doubled as the "a build is running" flag** — `IsGenerating` reported `progressCh != nil`, and the web layer uses `IsGenerating` to REJECT concurrent design POSTs, so simply opening the channel for a turn would have answered every following turn with "⏳ Still building your agent" and wedged the session until the draft expired. `DesignSession.generating` is now an explicit field set in `startGeneration` and cleared in `closeProgress`, and all four readers (`IsGenerating`, `DesignSnapshot.Generating`, `startGeneration`'s already-building check, and the `*ForTest` helpers package `web` drives the SSE handler with) read it; `progressCh` is purely a transport. `Flow.beginTurnProgress` opens the channel for one turn and returns its sink plus a **conditional** close — it closes only a channel it opened, that is still the session's, and that no build has since adopted, because `startGeneration` reuses an open channel rather than making a second one and an unconditional close would cut a build's stream the instant the turn before it returned. Browser-side, `ensureSSE` gained a third attach source `"turn"`: like `"live"` it never refetches on done, but it is a distinct value because `"live"` is what `awaitingBuildResultRef` is read against. `ensureSSE` also **upgrades** rather than early-returning when a build attaches over a turn's stream — same server channel, so a plain early return would leave it labelled `"turn"`, and the build's result would never be fetched. The card is titled from the stream's kind (`Looking through your knowledge base…` vs `Building your <entity>…`) and a turn's card renders only once it has a line, since most turns read nothing and an empty card would claim work that never happened.

**Exclusive session ownership (`internal/agentdesigner/session_origin.go`).** A design session is a per-workspace singleton both the SPA and a chat adapter can reach, so `DesignSession.Origin` (`OriginWeb`|`OriginChat`) records which one created it. It is fixed at creation and **never reassigned** — the owner drives, the other surface may read. Threaded as a compile-checked parameter on all six creation entry points (`Start`, `StartDesign`, `StartEdit`, `StartEditDesign`, `ResumeDraft`, `OfferDraftResume`) plus `Step`, which takes the CALLING surface and refuses a non-owner turn **before** the FSM dispatch — a refused turn appends no history, advances nothing, and (the reported bug) starts no build. `Origin.Owns` fails OPEN on a zero origin on either side, so a session predating the field stays drivable rather than being bricked. Three consequences worth knowing:
- **Delivery is origin-routed.** `BuildCompleteFunc` carries the origin and `main.go` sends to chat ONLY when it is `OriginChat`, logging `chat_suppressed=true` otherwise. Previously the hook was registered once at wiring time, could not see which surface the user was on, and announced every finished build in chat — so a web-started build put its dry-run in Telegram and left the browser blank. A failed chat send is `slog.Warn`, not `Debug`: for a chat-owned build that message is the user's only copy.
- **Cancel is asymmetric on purpose.** The web endpoint refuses a session it does not own (the SPA adopts whatever session exists on mount, so a mirroring tab could otherwise kill a live Telegram build), while chat's `/agent cancel` stays **unconditional** — it is the escape hatch for a web-owned session whose browser is gone, which would otherwise lock chat out until the 7-day draft TTL. `nonOwnerRefusal` names the command for exactly that reason.
- **Ownership is deliberately NOT persisted** (it would need a column; this change ships no migration), so a draft resumed after a restart is owned by whoever resumed it. Correct rather than a compromise: after a restart there is no in-flight build and no surface holds a live view.

**One durable user-facing record (`roleNote`).** `History` does double duty — the user's transcript AND the coder's retry context — and on the failure path those conflicted: `runGeneration` returned `outcome.message` (which reached chat) while `recordGenerationFailure` appended a *different*, generic note (which is what the web rendered), so on a workspace with no chat platform the real reason reached nobody. Both branches now write the user-facing message as an ordinary `assistant` turn; the coder's steering note goes in under `roleNote` (exported as `RoleNote`). `dbMessagesToPrompt` maps `note`→`assistant` **and coalesces adjacent assistant turns**, so the coder's prompt shape is unchanged (without the coalesce a failure emits two consecutive assistant messages where there was one, which several providers reject or silently merge); `designHistoryDTO` drops `note` turns so the UI never shows them.

**Three independent completion signals for the web.** The SPA used to have exactly one way to learn a build finished, and it was fragile. Now: (1) `handleDesignProgress` emits `event: done` before closing, matching `run_tracker.go` — `openSSE` already registers that listener unconditionally, so the server side was the whole gap, and without it the browser could only infer completion from EventSource's transparent reconnect hitting a 404 *after* the handler's 30-second poll; (2) `onError` refetches `/design/state` instead of stopping the spinner and giving up; (3) a 5s `/design/state` poll runs while `generating`, covering a proxy that swallows the stream without ever erroring. Any one delivers the outcome. `/design/state` also carries `origin` (the SPA's read-only-mirror signal), the `IsGenerating` branch of `handleDesignChat` returns the full `designTurnResponse` (the hand-rolled body omitted `state`/`generation_failed`/`can_keep_as_is`, and the SPA coerces a missing field to false — so a mid-build message silently cleared the failure banner), and a design turn with no session returns **409 `session_ended`** with an explanation instead of the bare `400 "name is required to start a new session"` a user hit after their session was completed from the other surface.

**Build traceability.** `startGeneration` mints a `buildID` onto the session and five `slog.Info` lines share it — build start (with origin/agent/edit/backend), coder returned, decision, outcome, and finished with a duration — so `grep build_id=<id> logs/server.log` reconstructs one build end to end. A full build previously produced ZERO designer log lines, which is why the misdelivery had to be diagnosed from the database instead. `MarkGeneratingForTest`/`PushProgressForTest`/`FinishGeneratingForTest` are exported test-only helpers (same rationale as the pre-existing `MarkGeneratingForTest`: the SSE handler lives in package `web` and cannot reach the unexported channel).

**Auto-schedule:** If AGENT.md starts with `# Suggested schedule: */10 * * * *`, `parseSuggestedSchedule()` calls `db.UpsertAgentSchedule()` immediately.

**Cron is evaluated in the SERVER's local time, and the schedule prompt now says so — in the user's terms.** The SCHEDULE DECISION block named the 5-part format and stayed silent on the zone, while the `UserProfile` block handed the model the user's timezone, so it converted: an agent asked for "Monday at 8" was written `# Suggested schedule: 0 6 * * 1` and fired two hours early, on two separate builds. The rule is stated in **two** places — the SCHEDULE DECISION block in `agentArchitectureGateBlock` (which the implementation prompts carry) and the `[TECHNICAL SPEC]`'s `Schedule:` line in the design system prompt — because the plan the user approves and the file the coder writes must agree; changing one and not the other silently reopens this. Both phrase it as the user's OWN LOCAL TIME rather than the server's, which is deliberate and is not a mistake to "correct": it is the only useful instruction to give a model that has been told the user's timezone and nothing whatsoever about the host's. **A schedule now carries its own timezone, so that advice is true rather than merely usually true.** `agent_schedules.timezone` (migration 014) holds an IANA zone and `scheduler.nextRunIn` converts `from` into it before asking `schedule.Next` — the conversion is the mechanism, because `cron.Schedule.Next` derives its wall-clock fields from the location of the time it is handed, so a UTC instant reads `0 8` as 08:00 UTC whatever the schedule intended. **An EMPTY timezone means the HOST's local zone, and that is the whole safety of the change**: it is byte-identical to the pre-column behaviour, so no existing schedule moves. `profile.LoadLocation` returns **UTC** for a workspace with no profile timezone, and reusing it here — the obvious implementation — would have silently re-timed every agent on every install that never filled in a profile, a multi-hour shift arriving with no error and no log line on agents that had been correct for months. `profile.ScheduleZone` therefore returns the stored name and the matching location *together*, so the two can never disagree, and refuses to store a zone that does not load (a persisted name the scheduler silently falls back from would misdescribe what the schedule does). The column is populated at the three write sites (auto-schedule, `reconcileScheduleOnSave`, the settings API) and deliberately **not backfilled** — a backfill would re-time existing agents, which is the exact failure the empty default exists to prevent. Residual: DST transitions behave as the cron library defines them.

**Skills selection via `# Skills:` header.** `DesignSession.Skills` is `[]prompts.SkillRef` (name+description), loaded once on start as **core skills (embedded) + the user's own skills**. The designer's `<available_skills>` block lists each skill with its description and **requires** the coder to emit a `# Skills: skill-one, skill-two` header line in AGENT.md (alongside the schedule line) declaring EXACTLY the skills this agent needs — never all of them, and never omitting the line (`# Skills: none` if it needs none). `parseSkillsLine(agentMD, installed)` reads that header and is deliberately tolerant of LLM formatting drift: case-insensitive heading matching (any `#` level, optional `required/needed/uses` qualifier, `:`/`-`/`=` separator), splits on `,`/`;`/`|`/`+`/`&`/`/`/` and `/` or `/newline, strips backticks/quotes/trailing prose (`pdf (for …)` → `pdf`), and also reads a bullet/numbered list when the heading has no inline names. Names are matched case-insensitively against the installed pool (so multi-word names like "Google Workspace" survive — bare spaces are NOT separators); unknown names are dropped with a warning rather than failing. Contract: returns `nil` ONLY when no skills header is found at all (caller treats as "declared none"); a present-but-empty/`none` header returns a non-nil empty slice. On approval `saveAndFinish`/`updateAndFinish` persist the declared names to the `agent_skills` DB table (the source of truth — see "Skill attachments source of truth" below); if no header is found, `agentdesigner.SelectSkills` runs as a fallback — one text-only call that asks the model directly which skills the agent needs, parsed with the same tolerant matcher and failing CLOSED (empty + a warning) on any error, so a weak model omitting the header no longer means zero skills. An explicit `# Skills: none` is respected and does NOT trigger the fallback (`parseSkillsLine` returns nil only when the header is absent entirely — that nil-vs-empty distinction is load-bearing). On an EDIT, an explicit header still wins; hand-curation is protected upstream instead, by `loadAgentForEdit` rewriting the header from `agent_skills` before the coder sees AGENT.md (mirroring the schedule line), so the UI and the file cannot drift. The header requirement is now injected by `availableSkillsBlock` into the design prompt AND both implementation prompts — previously only the design conversation asked for it, which is why `agent_skills` was empty across the whole install. Covered by `parse_skills_test.go` + `skills_db_test.go`.

**Skill attachments source of truth (DB, not AGENT.md).** The `agent_skills` DB table — keyed by **skill name**, not `skill_id` — is the single source of truth for an agent's skills (core + user, by name). Core (embedded) skills have no `skills`-table row, so they could never be represented by the old `skill_id` FK; migration 010 rebuilt `agent_skills(agent_id, skill_name)` and backfilled existing user-skill rows by resolving `skill_id → name`. The designer (`SaveAgent`/`UpdateAgent`) writes the parsed `# Skills:` names here — AGENT.md is for the LLM only, the DB is the skill record; there is no `agent.json` cache to keep in sync, because `agent.json` is gone (see "Agent state" and "Startup migration" above). The runner (`runCoderAgent`) and the agent page (`loadAgentDetail`) read declared skills from `db.ListAgentSkillNames(agentID)` exclusively. The web Skills card renders core + user skill checkboxes by name (`AttachedSet`/`AttachedSkills` + `CoreSkills`/`AllSkills`); `handleSaveAgentSkills` accepts `skill_names` (not IDs), validates against core ∪ user names, and writes the DB only. Deleting a user skill calls `db.DeleteAgentSkillsByName` to drop dangling attachments. **One-time cutover (absorbed into the state migration):** the standalone `ReconcileSkillAttachmentsToDB` startup step is gone — its job now runs as one phase of `agentdesigner.MigrateAgentFilesToMarkdown` (see "Startup migration" above), which must reconcile `agent.json`'s legacy `Skills` field into the DB *before* deleting that file, since `agent.json` was the only place it lived. Same semantics as before: seeds the DB only when the agent has no `agent_skills` rows yet, skipping the legacy "all core skills" fallback-bloat signature.

### Prompt architecture (coder-agnostic, three-tier)

All prompts live in `internal/prompts` (single source). The designer produces **coder-agnostic** AGENT.md — it says WHAT to do, never runtime-specific tool names (so it works on a full coder like claude-code/codex OR a basic model call like OpenRouter GLM). HOW the coder acts on files is injected separately based on `BackendType`:

- **`platformContextBlock(chatApps, vaultRoot)`** — full Rookery primer (flexible ever-growing KB with USER-REORGANIZABLE vs SYSTEM-WRITTEN fixed locations, secrets store, chats, reminders, connected chat apps + commands, output protocol, schedule). Injected into design, implementation, and runtime prompts.
- **`coderCapabilitiesBlock(backendType)`** — three-way: `BackendFullCoder` (CLI) → direct tool access; `BackendToolCalling` (the `api` engine) → native function calls (`read_file`/`write_file`/`edit_file`/`list_dir`/`search_files`/`glob`/`web_search`/`web_fetch`/`run_script`) the host executes, final answer as protocol markers; `BackendBasicModel` → `[READ_FILE]`/`[WRITE_FILE]`/`[RUN_SCRIPT]` output markers. `MapCoderBackend()` maps the coder's backend type (`"api"` → tool-calling) to these. `BuildChatSystemPrompt(vaultRoot, backendType, conns, connToolNames, connectorBin)` is likewise backend-aware (tool-calling chat offers the file tools incl. `search_files`/`glob` but NOT the exec/network tools) and appends `connectedToolsBlock` when the workspace has active connections.
- **`agentPhilosophyBlock()`** — three-tier taxonomy (TIER 1 reasoning-only / TIER 2 one script / TIER 3 multi-file) with NOT-TO-DO lists; forces the coder to pick the simplest tier that solves the task (prevents writing a script for trivial reasoning work).
- **`agentArchitectureGateBlock()`** — mandatory TASK ANALYSIS → TIER DECISION → NOTIFICATION DECISION → SCHEDULE DECISION before any file is created. Supports no-notification (`[SILENT]`) and no-schedule (`none`) agents.
- **The inbox is described in `platformContextBlock`, and the point is the CONSTRAINT, not the
  feature.** Nothing in `internal/prompts` mentioned the inbox at all, so a user asking to be
  notified "in the inbox, not on Telegram" met a model that had never heard of it — it proposed a
  SILENT agent, close to the opposite of the request. The block states three facts: every
  notification is recorded in the inbox **automatically** (an agent cannot write there and must not
  look for a way); delivery is **all-or-nothing** — a `[CHAT]` block reaches the inbox *and* every
  connected chat app, `[SILENT]` reaches neither, and picking one channel is **not supported**
  (`internal/agentrunner` pairs `recordInbox` with `SendOutput` at all three delivery sites); and
  "inbox" means THIS inbox, not Gmail or Outlook, which are connectors where the equivalent is
  *sending an email*. With no chat app connected, notifying already reaches the inbox alone — often
  the real answer to the request. `TestInboxBlockPromisesNoChannelSelection` guards the fix from
  drifting into a capability claim the code does not back.
- **`ChatAppsForPlatforms()`** — central platform→`ChatAppInfo` (name + commands) mapping; callers load via `db.ListUserPlatformConnections` (no GatewayManager method needed).
- Design UX is non-technical: a jargon blocklist (FORBIDDEN: AGENT.md, Python, script, vault, cron, JSON, shell, Bash, webhook, endpoint); asks notification preference + schedule; emits a `[TECHNICAL SPEC]` for the code generator.

### Connector service layer (self-managed OAuth; replaces Composio — which is fully removed)

`internal/connectors` is the platform's own external-service integration: **self-managed OAuth** +
**native typed tools** per connected account. It is **coder-agnostic** (knows nothing about coders) —
both coder kinds converge on `connectors.Execute`. **There is no Composio anywhere in the codebase.**

- **Data files, not code.** Adding a service = a `providers/<p>.yaml` (auth config) + a
  `connectors/<p>.yaml` (curated action manifest), both `go:embed`ed. `LoadBundled()` parses them.
  **136 providers (~934 actions):** the Google family (Gmail/Drive/Sheets/Docs/Calendar/Tasks
  **+ Slides/Forms/Chat/Contacts**, and AdSense/GA4/Search Console), **YouTube**,
  the Microsoft 365 family (Outlook mail **+ Outlook Calendar/Contacts, OneDrive,
  Excel, OneNote, Microsoft To Do**, Teams),
  GitHub, Slack, OpenAI, Notion, Jira, HubSpot,
  Dropbox, Calendly, Asana, ClickUp, Airtable, Intercom, SendGrid, Monday, Salesforce,
  Shopify, Mailchimp, Zendesk, Stripe, Twilio, Trello.
  (AWS SigV4 + PostgreSQL were scoped but dropped.) Each action = name + JSON-schema params +
  `mutating` flag + a request template (method/URL/query + one body kind) + `response_extract`.
  Every provider declares a `category:` grouping it on the connections page (one of Google,
  Publishing & Media, Advertising, Productivity, Communication, Commerce, Developer, Support,
  Other; empty renders under Other). The UI list is **derived from the registry**
  (`Registry.ProviderNames()`, sorted) — the old hardcoded `availableServiceProviders` slice is
  gone, so adding a service really is two YAML files and no Go change.
- **The publisher-side Google providers discover their own identifiers.** AdSense, GA4, Search
  Console, and YouTube are read-only and alias the `google` OAuth app, and each ships a list
  action (`adsense_list_accounts`, `ga4_list_properties`, `gsc_list_sites`,
  `youtube_my_channel`) that uses the SAME scope as its reporting action. That is why none of
  them needs `connect_inputs`: the agent enumerates accounts/properties/sites and picks one,
  rather than the identifier being pinned at connect time. Two renderer features exist for
  them: `{{arg|escape}}` opt-in path escaping (a Search Console site URL sits inside a path
  segment, while AdSense's `accounts/pub-…` and GA4's `properties/…` carry REAL separators that
  a blanket escape would corrupt), and the `ga4_report`/`ga4_realtime` body builders (GA4 wants
  `metrics` as `[{"name":"…"}]`; `renderBody` can substitute an array but not restructure one).
- **Auth is declarative + reusable.** A provider is OAuth2 (default) or `auth.kind: api_key`
  (`placement: header`/`query`/`basic`, `value_prefix`, `basic_user_template` for a two-part Basic
  username like Twilio's SID). Cross-provider reuse via `auth_parent`: a child (e.g. `google_sheets`)
  reuses the parent (`google`) OAuth app/token — one consent, per-service connection rows + binding;
  `Registry.OAuthProvider(name)` resolves the parent for endpoints/creds/refresh, `ProviderByName`
  keeps the child's scopes/actions. Per-connection values feed `{{conn.<key>}}` in URL/body templates
  from four sources: `connect_inputs` (fields the paste-key form collects — Shopify shop, Zendesk
  subdomain/email), `token_extra` (fields captured from the OAuth token response — Salesforce
  `instance_url`), `key_extra` (parsed from the API key — Mailchimp datacenter), and the `post_connect`
  hook (Jira cloud id).
- **Body kinds** (mutually exclusive per action, `render.go`): `body:` nested JSON (`renderBody` —
  type-preserving, optional-key-omitting, array passthrough), `body_arg:` (the whole body is one
  object arg — Salesforce sObjects), `form:` (`application/x-www-form-urlencoded` via `renderForm`,
  bracket-notation keys + array→repeated-key — Stripe/Twilio), or a Go `body_builder` for non-JSON
  encodings (`gmail_rfc822`/`gmail_reply`/`gmail_draft`, `notion_page`, `msgraph_*`, `jira_*`).
- **`Execute(ctx, reg, store, client, conn, action, args, buildPhase)`** — the single typed choke
  point: validate args → refuse `mutating` actions when `buildPhase` (build-time guard, keyed off
  `internal/buildphase.EnvVar`) → `store.AccessToken` (refresh if near expiry) → render request →
  `applyAuth` (`auth.go`: Bearer / api-key header/query/HTTP-Basic / templated-Basic username, per
  the provider's `auth` block) + provider `static_headers` (resolved via `OAuthProvider` so aliased
  children inherit the parent's) → call (1 transient retry) → normalize into a `ConnectorError`
  taxonomy (auth/ratelimit/server/needs-reauth/bad-args/build-blocked).
- **OAuth** (`oauth.go`): `ConsentURL`/`ExchangeCode`/`Refresh`/`ExchangeLongLived`/`FetchIdentity`.
  `token_expiry` has a third mode beyond `expiring`/`never`: **`exchange`** (Meta) means there is
  NO refresh token — a short-lived token is swapped for a ~60-day one via the `fb_exchange_token`
  grant and renewed by exchanging the CURRENT access token again. `Refresh` routes there so
  `DBTokenStore`/`RunRefreshLoop` need no Meta branch, and `ExchangeLongLived` returns the new
  access token as the RefreshToken too — the store hands RefreshToken back on the next renewal,
  and for this provider that IS what you exchange, so omitting it would break the *second*
  renewal ~60 days in. `post_connect` may now also **replace the connection's access token**
  (`PostConnectResult.AccessToken`): the `meta_page_token` hook swaps the user token for the first
  managed Page's own token, because publishing to a Page requires the PAGE token. That keeps the
  credential in `encrypted_access_token` instead of plaintext `extra` — which is why the design's
  "encrypt `extra`" change was dropped as unnecessary. A connection therefore means "this Page";
  several Pages means connecting several times. Per-provider config
  covers the real quirks: `token_expiry: never` (GitHub/Notion — empty `expires_at`, never refreshed),
  `token_auth: basic` + `token_content_type: json` (Notion), `static_headers` (Notion-Version, GitHub
  Accept), `authorize_extra` (Atlassian audience/prompt, Google access_type/prompt, Notion owner),
  `post_connect: atlassian_cloudid` (resolves Jira cloud id into `service_connections.extra`, exposed
  to URL templates as `{{conn.cloudid}}`). Refresh-token **rotation** is persisted (Atlassian).
- **Tokens** are `secrets.EncryptWithSystemKey`-encrypted (headless — the background `RunRefreshLoop`
  and cron runs decrypt without a master password). `DBTokenStore` reads/refreshes/persists them.
- **Tool exposure** (`tools.go` — single source): `ToolDefs(bound)` builds the tool set (single
  account → bare `gmail_send_email`; multiple of one provider → `gmail_send_email__<slug(label)>`,
  slugged to the provider's `^[a-zA-Z0-9_-]{1,64}$`); `ResolveTool(bound, name)` reverses it.
  - **API engine** exposes them as native function tools in `hostToolSet` (`coder/connectortools.go`).
  - **CLI coders** reach the SAME `Execute` via a **loopback bridge** (`bridge.go`): a `127.0.0.1`
    HTTP listener started in `serve`; the runner registers a per-run bearer token scoped to the run's
    bound connections; the coder runs `rookery connector exec <tool> --args '<json>'` (a thin
    client subcommand) which POSTs to it. Tokens never leave the host; Landlock restricts filesystem,
    not loopback TCP, so a sandboxed coder can reach it (the `rookery` binary dir is granted
    RO+exec in the sandbox spec so the child can exec it). The bridge response is **byte-capped**
    at `maxBridgeResult` (8 KiB, mirroring `coder.maxToolResult`) via `capBridgeData` — the API
    engine always truncated and the bridge did not, and an analytics or ad-insights report is
    exactly the payload that exploited the gap. Under the cap the envelope is unchanged
    (`{"data": …}`); over it, `data` becomes a truncated STRING plus `truncated: true` and a note
    telling the model to narrow its query, because a JSON value cut in place still parses and
    reads as complete data.
- **`connect_inputs` work on the OAuth path too**, not just the paste-key form. A value that
  cannot be discovered from any API (a Google Ads developer token) is collected BEFORE consent
  and rides the **signed OAuth state** — already HMAC-signed and TTL'd, so no server-side pending
  row exists to garbage-collect when a user abandons the consent screen. Base64 keeps the JSON
  clear of the `~` field separator; the callback accepts both the 4- and 5-field state shapes,
  because a state issued before the change can still be in flight across a deploy. Required
  inputs are validated at CONNECT, not at callback — otherwise a user completes consent and is
  then told a field was missing. Two further one-field provider generalisations live alongside:
  `token_exchange_grant` (Threads uses `th_exchange_token`, not Meta's `fb_exchange_token`) and
  `client_param` (TikTok names the client id `client_key` in both the consent URL and the token
  request). Both default to today's behaviour.
- **Approval gate for public writes** (`internal/approval`, opt-in, default OFF). Three layers:
  an action-level `public_write: true` in the connector YAML marks irreversible PUBLIC
  publishing (`mutating` is too blunt — pausing an ad campaign is mutating but private and
  reversible); a binding-level `agent_connections.approval_mode` (`auto` default | `approve`)
  chooses per agent+account, so one agent can post autonomously to a personal account while
  requiring approval on a company one; and `Execute`'s `Policy{BuildPhase, Parker}` (which
  replaced the bare `buildPhase bool`) enforces it. Semantics are **park, plain**: a gated call
  is written to `pending_actions` and the coder gets a queue ticket as a SUCCESS (never an
  `error:` string — the tool loop would retry it), the run finishes, and the owner resolves it
  via `/pending` `/approve <id>` `/reject <id>` in chat or the web inbox. Park sits AFTER arg
  validation (never ask a human to approve a broken call) and BEFORE the token fetch (approval
  arrives hours later, so ARGS are stored and re-rendered against a fresh token at send time).
  `Parker.Park` returning `("", nil)` means "not gated — send now", which is how a mixed set of
  bindings is honoured. `ClaimPendingAction` is a conditional UPDATE making `status` the lock,
  so chat and the web inbox racing cannot double-publish. `ParkerFor` returns nil when an agent
  has no gated binding (ungated installs pay nothing) and fails OPEN on a DB error — failing
  closed would silently halt an autonomous agent the user never gated. Both coder kinds get the
  same parker (`Coder.WithParker`, `Bridge.RegisterGated`) so changing coder kind cannot disable
  the setting. Accepted costs of park, recorded: no chaining, no error reaction, and state drift
  if the owner rejects — mitigated only by the parked result's wording. Stale rows expire after
  7 days in the nightly GC. **Control surface:**
  `PUT /api/v1/agents/:id/connections/:connID/approval` toggles a binding;
  `GET /api/v1/approvals` + `POST /api/v1/approvals/:id/{approve,reject}` resolve from the web,
  so a workspace with no chat platform connected is not stuck until the expiry.
  **One-off chat is deliberately NOT gated** — `ParkerFor` returns nil when `agentID == ""` and
  the chat bridge registration passes no parker. Chat is a human typing a request in real time,
  so gating would hold the user against themselves; the residual gap is that they have not
  reviewed the wording the model produced. Revisit if a workspace-level default is added.
- **Agent binding** (`agent_connections` table, keyed by connection id) is the source of truth for
  run-time tool exposure — NOT the AGENT.md `# Connections:` header. THREE ways to bind: the designer
  parses a `# Connections:` header (`agentdesigner.parseConnectionsLine`, tolerant of inline OR
  bullet/comment-list form) into the table; OR **auto-bind** (`agentdesigner.AutoBindTargets`) — when
  a weak model OMITS the header, the designer binds exactly the connections the build's connector-tool
  calls actually used (the API engine tracks used connection ids on `coder.Result.UsedConnectionIDs`,
  persisted across restart/keep-as-is via `agent_drafts.pending_used_connections`) — never all, never
  clobbering an existing binding, explicit header always wins; OR the **Attach-connections card** on the agent page
  (checkboxes → `handleSaveAgentConnections` → `SetAgentConnections`), which is the reliable path when
  a weak model forgets the header. Builds expose ALL workspace connections; runs expose only bound
  ones. The build/impl AND runtime prompts inject `connectedToolsBlock` (backend-aware: native tools
  vs the `connector exec` command) so the coder knows the tools exist and is told there is **no
  Composio/SDK/service keys** in the env.

**A binding is a grant of live credentials, so `parseConnectionsLine` may only contains-match an
identity that is an EMAIL and belongs to exactly ONE connection.** Its loose match previously ran
`strings.Contains(header, identity)` over every connection's `AccountIdentity`, which broke twice
on an ordinary workspace — a wrong binding there is access, not untidiness:

- **Short identities are shared.** `test` belonged to adguard, mailchimp *and* stripe, so the header
  `# Connections: adguard/test, google_sheets/ilija 133` contained the substring `test` and granted a
  DNS-watchdog agent the owner's payment and mailing-list credentials. Stripe test-mode accounts named
  "test" are ubiquitous, so this is the common case, not a contrived one.
- **Family identities are shared by design.** Every `google_*` child carries the same address, so one
  email in a header bound Drive and Docs alongside the Sheets account actually named.

Requiring `@` keeps the case the match exists for (the bullet form a weak model writes —
`google account "personal" — me@x.com`) while refusing to treat a bare word like `test` or `personal`
as an identifier; requiring uniqueness refuses an address that cannot single anything out. Everything
excluded stays reachable through provider/label or exact-token matching, so the cost is at worst
under-binding, which a checkbox fixes. `overbind_test.go` pins both incidents by reproducing the real
workspace's shape.

**The AI connector tier (2026-08) — and why a connector cannot carry media.** Anthropic,
OpenRouter, Perplexity, Replicate, Deepgram, AssemblyAI and Hugging Face join OpenAI under a new
`AI` category (OpenAI moved there from `Developer`; splitting one group across two headings was
the alternative). These are services an agent calls with the **user's own key**, deliberately
distinct from `coder.APIProviders()`, which is how the workspace's own agent thinks.

Three auth shapes here are worth knowing because guessing them wrong yields a provider that
accepts a key and fails every call: **Anthropic** uses `x-api-key`, not `Authorization: Bearer`,
plus a mandatory `anthropic-version` static header; **Deepgram** uses `Authorization: Token …`;
and **AssemblyAI** sends the raw key with no scheme at all. All three were confirmed against the
providers' own documentation.

**ElevenLabs and Stability were scoped and dropped**, for the same reason `aws.yaml` omits S3:
`Result.Data` is a `json.RawMessage` and `extract` returns a non-JSON body unchanged, so a
response of audio or image BYTES corrupts the envelope rather than merely failing to narrow it.
Base64 is not a way out — the bridge caps a result at 8 KiB. Replicate is in precisely because a
prediction answers with **URLs** to its outputs. Hugging Face is metadata-only for the same
reason. This is now the second instance of one rule: a connector answers in JSON, so a service
whose payload is binary or XML needs framework support that does not exist yet.

**Waves 3–6 (2026-08): money, notifications, homelab, developer.** Wise, CoinGecko, Alpha
Vantage; Pushover, Pushbullet, Resend, Mailgun, Matrix; Prowlarr, Lidarr, Bazarr, Proxmox VE,
Tailscale, Plex; GitLab, Bitbucket, Linear, Sentry, npm, PyPI. Five findings from those waves are
worth carrying, because each is the sort of thing a copied YAML gets wrong:

- **The connector layer cannot put a credential in a request BODY** — only a header, a query
  parameter or Basic. Pushover documents form-encoded POSTs, so a first draft invented a dummy
  header name to route around it, which would have failed every call. A live probe settled it:
  a bogus token in the QUERY string came back `"application token is invalid"` rather than
  *missing*, proving Pushover parses credentials there. `placement: query` is verified, not
  guessed. The framework gap remains real for any provider that accepts credentials **only** in
  a body.
- **`a connector answers in JSON` has now bitten three times** — XML (S3), binary media
  (ElevenLabs, Stability), and Plex, which answers XML unless asked otherwise and therefore
  carries a static `Accept: application/json`. Treat it as a rule when scoping a provider, not
  as a surprise.
- **Bazarr is not an *arr app for API purposes.** Its endpoints live under `/api/`, not the
  `/api/v1/` Sonarr, Radarr, Prowlarr and Lidarr use, so a pasted manifest 404s on every action.
- **Proxmox's credential is the whole `user@realm!tokenid=secret` string**, behind the scheme
  `PVEAPIToken=` — not just the secret.
- **Sending email is `public_write`, not merely `mutating`** (Resend, Mailgun): it lands in
  someone else's inbox and cannot be recalled, the same standard already applied to posting on a
  social account.

`npm` and `PyPI` use `auth.kind: none`, the keyless shape Open-Meteo established. PyPI ships no
search action because PyPI withdrew its search API — an action that appeared to search and could
not is worse than its absence.

**The cloud-adjacent tier (2026-08).** Cloudflare, DigitalOcean, Vercel, Netlify, Fly.io, Hetzner
Cloud and Linode join AWS under `Cloud` — infrastructure the user RENTS, as distinct from
`Self-hosted`, which is the box under their desk. All seven are plain Bearer tokens, so all seven
are pure YAML.

The one thing worth knowing is where per-account identifiers go. `TestConnectInputsAreReferenced`
requires every declared `connect_input` to appear as `{{conn.x}}` in a template, which forces the
right design rather than merely checking it: Cloudflare's `account_id`/`zone_id`, Vercel's
`team_id` and Fly.io's `org_slug` belong to the CONNECTION (this account, this zone) and so live
in the action URLs and query strings, not as action arguments the model must supply on every call.
Vercel's `team_id` is deliberately optional — the query renderer drops an empty value, so a
personal-account token works unchanged. An earlier draft of `cloudflare_list_zones` reached for a
`/accounts/{{conn.account_id}}/../../zones` path purely to satisfy the guard; the real fix was
Cloudflare's own `account.id` query filter, which scopes the result properly as well.

**`auth.kind: sigv4` — AWS, and the one scheme that signs rather than carries.** Every other
kind puts a credential somewhere in the request; SigV4 signs the request itself, which is why
it forced two changes to `applyAuth`. It now takes the rendered **body** (AWS signs a payload
hash) and **returns an error** (a connection missing its region or service cannot be signed at
all). It is also applied **LAST** in `Execute`, after `Content-Type` and every static and
per-action header — running first is fine for a scheme that only adds a header, but a signature
must cover the headers actually sent. No provider sets `Authorization` through `static_headers`,
so the reorder clobbers nothing.

The signer is `internal/awssig`, extracted from `internal/backup` (which is now a caller) and
verified against **AWS's own published SigV4 test vectors** — the only independent check
available offline. Extracting it exposed two defects that could not bite the backup code that
carried them: the canonical query used `url.Values.Encode`, which escapes a space as `+` where
SigV4 requires `%20` (backup's only query values are snapshot names, which have no spaces), and
only three headers were ever signed, so a service demanding `X-Amz-Target` or a signed
`Content-Type` could not be reached. `X-Amz-Content-Sha256` is now set for **s3 only**: it is
required there and merely harmless elsewhere, but setting it unconditionally changes
`SignedHeaders`, which is precisely why the published vectors could never be run against the
original.

**Credential placement is the part that is easy to get wrong.** `service_connections.extra` is
**plaintext JSON** by design — the Meta page-token hook exists to move a credential *out* of it.
So the **secret access key** is the credential and is encrypted like every other; the **access
key id** and the **region** are ordinary `connect_inputs`, since the key id travels in the
`Authorization` header of every signed request and is not a secret. Service and region are
per-CONNECTION, not per-provider: one AWS connection reaches many services and every region is a
separate signing scope, so one connection means one service.

**`aws.yaml` ships Lambda and CloudWatch Logs and NOT S3, EC2 or CloudWatch metrics.** Those are
Query/REST-XML protocols with no JSON option, and `extract` returns a body that does not parse as
JSON **unchanged** into a `json.RawMessage` — so an XML response does not merely fail to extract,
it corrupts the envelope it lands in. This is recorded in the YAML itself because the obvious fix
for "S3 is missing" is to write the actions. Reaching the JSON-RPC services (which dispatch on
`X-Amz-Target`) is what motivated **per-action `headers:`** on `RequestTemplate`, distinct from
provider-wide `static_headers`: the target names the operation, so it varies per action.

**Connection re-auth alerting, and why the status flip is gated.** `DBTokenStore.refresh` used
to set `NEEDS_REAUTH` on **any** refresh error. Because `ConnectionsNearExpiry` selects
`WHERE status='ACTIVE'`, a flipped row leaves the refresh loop permanently — so a 500, a 429 or a
DNS blip silently cost the user a working connection until they reconnected it by hand. The
information needed to tell the cases apart was being discarded one layer down, where
`tokenRequest` mapped every status `>= 400` onto `KindAuth`. It now classifies (429 →
`KindRateLimit`, 5xx → `KindServer`, other 4xx → `KindAuth`) and `refresh` flips only on
`KindAuth` — the provider's own rejection of the credential. `definitiveRejection` fails OPEN on
an unclassified error: one more retry is cheap, a lost connection is not.

Only on that gated transition does `notifyReauth` fire, sending an **"⚠️ Action required"** notice
to BOTH the inbox (`source: "connection"`, a third value beside `agent_run` and `reminder`) and
chat, mirroring the approval gate's dual surface so a workspace with no chat platform is not
stuck. **Fire-once costs no schema change**: the row leaves the `status='ACTIVE'` query on the
flip, so it does not re-fire on every tick. This holds per repair cycle in PRACTICE, not
absolutely — there are two `DBTokenStore` instances carrying notifiers (`main.go` and
`web/server.go`) and the flip is check-then-write with no lock, so a background refresh racing a
web-path `AccessToken` on the same expired row can duplicate the alert. Two inbox rows, no data
loss; the same check-then-write shape recorded for the `state.md` 409 guard. Note also that
`ConnectionsNearExpiry` filters `expires_at <> '' AND encrypted_refresh_token <> ''`, so a row
without either never reaches the loop at all — its alert fires from `AccessToken` DURING an agent
run, concurrent with the failure rather than ahead of it. The inbox row is written FIRST and independently of the
chat send — a workspace with no chat platform errors on *every* send, and the inbox is precisely
that user's only surface. Not covered, deliberately: `token_expiry: never` providers (GitHub,
Notion) and `auth.kind: none` never enter the refresh loop, so a revocation there surfaces only
when a run 401s (where `agentrunner.FriendlyRunError` already reports it — hooking this notifier
there too would send two messages for one event); and `session_exchange` returns `KindNeedsReauth`
without flipping status, so a revoked Bluesky app password does not alert. Flipping it there needs
a consecutive-failure threshold, since a transient 4xx would otherwise permanently mark a healthy
connection broken.

**The everyday tier** (waves 1–4, 2026-08) opened a second axis alongside the business/SaaS
providers: services people use in their own lives. Three shapes, all data-only —
**personal cloud** (Todoist, YNAB, Raindrop.io) paste a token; **self-hosted**
(Home Assistant, Immich, Paperless-ngx) pair a token with the user's own `base_url`,
collected via `connect_inputs` with `normalize: base_url` (`NormalizeBaseURL` requires a
scheme and strips trailing slashes but **preserves a path prefix** — `/nextcloud` and a
reverse-proxied `/paperless` are mainstream) and reached because connectors deliberately
do not use the private-address dial guard; and **keyless** (Open-Meteo) needs no
credential at all via `auth.kind: none`. Google Calendar and Google Tasks ride the
existing Google OAuth app through `auth_parent` — `buildConsentURL` passes the CHILD's
own scopes to the PARENT's endpoint, so each child consents separately and adding them
did not disturb existing Gmail connections.

`auth.kind: "none"` touches five places: `applyAuth` returns early (the default branch
would send `Authorization: Bearer ` with an empty value), `DBTokenStore.AccessToken`
returns `("", nil)` before the expiry check (an unset expiry reads as *expired* and
would route the row into a refresh it cannot survive), the connect endpoint relaxes the
key requirement and rejects a duplicate, `connectAPIKeyCore` stores no ciphertext and
names the row after the provider, and the SPA renders `kind: "keyless"` as a bare
Connect button. `RunRefreshLoop` needs no change — `ConnectionsNearExpiry` already
filters on `expires_at <> '' AND encrypted_refresh_token <> ''`.

**Wave 2** added Readwise, Toggl Track, ntfy, Jellyfin, AdGuard Home, Miniflux, Firefly
III, TMDB, Wikipedia, Hacker News, Frankfurter and Strava — Strava filling the
`Health & Fitness` category that shipped empty. It brought one framework field:
`auth.basic_pass_literal` makes the CREDENTIAL the HTTP Basic username and a fixed
string the password, the inverse of `basic_user_template` (Toggl wants the token as the
user and the literal `api_token` as the password). `basic_user_template` still wins when
both are set, so Zendesk and Twilio are untouched. **Wave 3** added the homelab stack —
Sonarr, Radarr, Grafana, n8n, Gitea, Karakeep, Audiobookshelf, Changedetection.io and
Syncthing — plus Steam, Last.fm, Clockify and WakaTime.

**Fitbit was replaced by `google_health`, and Zoom removed** (2026-08). Fitbit's Web
API is decommissioned in September 2026 along with its OAuth server; Google Health
supersedes it and authenticates through the SHARED Google OAuth app, so it is an
`auth_parent: google` child rather than a provider of its own. Existing Fitbit tokens do
not carry over — every user re-consents. Zoom was pulled after its connect flow could
not be completed against a real account. `TestRemovedProvidersStayRemoved` keeps both
out, because the obvious fix for "Fitbit is missing" is to re-add the YAML, which would
ship a connector against an API that stops answering.

**The paste form now names the credential from the provider YAML.** `key_label`/
`key_hint` were written into every `api_key` provider and reached nothing: the wizard
hardcoded `"<Provider> API key"`, which was simply wrong for the providers that take no
API key — AdGuard Home reuses the web-UI login, Nextcloud wants an app password. Both
fields are now on the services DTO and rendered by the form.

**Wave 4** added Open Library, OpenStreetMap (Nominatim), Open Food Facts, Nextcloud,
Mealie, Vikunja, Gotify, Linkwarden, Portainer, Fitbit, Oura, Spotify and Trakt —
taking `Health & Fitness` from one provider to three (Fitbit was later removed; see
above). **Withings was deliberately dropped**: its token exchange posts
`action=requesttoken` rather than a standard grant, which the OAuth client cannot
express, and shipping a provider that cannot authenticate is worse than omitting it.

**Three more providers block or throttle anonymous clients**, so `static_headers` carries
an identifying `User-Agent` for Nominatim, Open Library and Open Food Facts as well as
Wikipedia. Nextcloud needs two mandatory headers of its own — `OCS-APIRequest: true`
(the OCS API rejects requests without it) and `Accept: application/json` (it returns XML
otherwise, which `extract` cannot read). Fitbit (before its later removal) and Spotify
both require HTTP Basic client auth on the token endpoint (`token_auth: basic`); body
credentials fail with `invalid_client`.

**Wikimedia blocks the default Go user-agent** with a 403 citing its robot policy, so
`wikipedia.yaml` sets a descriptive `User-Agent` in `static_headers`. Every Wikipedia
action failed until it did; found by live verification and pinned by a test, because
nothing else would catch its removal.

Providers not confirmed against their live API carry `unverified: true` in their YAML;
`TestWave1ProvidersDeclareVerificationStatus` fails if a wave-1 provider is neither
verified nor marked. Open-Meteo is verified by a `//go:build livecheck` test that calls
the real API — excluded from the normal run so CI never depends on a third party.

**`response_extract` walks DOTTED paths, and `response_filter` narrows arrays.**
`extract` originally resolved a single top-level key, so any nested path silently
returned the whole body — `$.data.children` (Reddit) and `$.data.user`/`$.data.videos`
(TikTok) had never once narrowed. The failure is invisible in the YAML and surfaces only
as a truncated blob against the bridge's 8 KiB cap, which is why the fix matters more
than it looks. `ResponseFilter{Field, PrefixArg}` is the client-side complement, for APIs
with no server-side filter: Home Assistant's `/api/states` returns every entity in the
house, so `ha_list_states`'s `entity_prefix` is honoured after extraction. A missing
filter argument yields an empty prefix and no-ops — matching nothing would return `[]`,
which reads to the model as "you have no sensors".

**Connectors deliberately do NOT use the private-address dial guard.**
`connectors.Execute` falls back to a plain `&http.Client{Timeout: 30s}`, and every
call site passes nil or an unguarded client — unlike `internal/websearch`, the coder's
`web_fetch`, and the Discord attachment fetcher, which all use
`nethttp.GuardedClient`. This is the property the **self-hosted tier** (Home Assistant,
Immich, Paperless-ngx) is built on: those services live at RFC1918 or Tailscale
addresses that the guard blocks at dial time. The guard's threat model is untrusted
content steering a fetch; a connector's host comes from vendored YAML or from a value
the single owner typed into their own install, so it does not apply here.
`connectors.TestExecuteReachesPrivateAddresses` pins this, and its failure message
says what breaks. Revisit if Rookery ever becomes multi-tenant — that test is where
the conversation should start.

**UI:** the SPA connections page (backed by the `/api/v1/services` JSON endpoints) — per-workspace
OAuth-app creds + connect per provider, with per-provider setup guidance
(`label`/`setup_url`/`setup_steps` in the provider YAML). The OAuth **callback** is the one
server-rendered redirect route that survives the SPA cutover: `GET /dashboard/connectors/services/callback/:provider`
(HMAC-signed, TTL'd `state`; path FROZEN because it's the registered external redirect URI; it finishes
with an HTTP redirect back to the SPA). `ROOKERY_PUBLIC_URL` sets the callback base (Google rejects
non-public-TLD/`http` redirect URIs — use `https://` or `http://localhost`).

**Redirect-URI reliability.** `internal/publicurl` owns the instance base URL
(`Resolve`: the `system_settings.public_url` row → `ROOKERY_PUBLIC_URL` → detection
from the request) and judges it against a provider's `redirect_policy` YAML block
(`Check`, a pure function). Only a policy marked `verified: true` may hard-block
the Connect button; an absent block is the zero `Policy`, which is fully
permissive, so rolling policies out provider by provider can never lock a user
out. Host classification hard-blocks only RFC-reserved suffixes — an ICANN
public suffix passes, and a PSL *private* entry such as `github.io` degrades to a
soft warning, because `publicsuffix.PublicSuffix` reports `icann=false` for both
it and `.lan`. The consent-time redirect URI is pinned into the signed OAuth
state (a 6th `~` field; 4- and 5-field states are still accepted for the 10-minute
TTL) so the token exchange cannot use a different string than consent did — and
a divergence is logged and proceeds, never rejected, because the user has already
granted consent by then. Provider `setup_steps` carry a `{{redirect_uri}}`
placeholder that the SPA substitutes (browser-side, rendered as copyable code
rather than a link — following our own callback with no `state` only errors);
`connectors.TestSetupStepsUsePlaceholderNotProse` bans the old "shown above"
wording. The hard block is **UI-only by design**: the policy predicts a third
party's rules rather than expressing an invariant we own, so a server-side gate
would turn a stale YAML entry into a lockout with no override.

### MCP servers (wave 1: HTTP transport, static token, tools only)

`internal/mcp` is the **escape hatch beside connectors, never their replacement** — a
distinction worth keeping, or the next contributor starts converting connectors to MCP.
A connector's ~5 curated actions are vendored YAML, testable against golden fixtures and
controllable; an MCP server advertises whatever it likes. What MCP buys is the one thing
the connector model structurally cannot have: **a user adds an integration without
waiting for a Rookery release.** Its value concentrates in capability servers with no
HTTP API to wrap, services Rookery has not vendored, and the user's own servers.

**Where servers and tools come from — two different things.** *Which servers exist:* the
owner pastes a URL; nothing is compiled into the binary and no directory is consulted.
*What tools a server has:* discovered from **that server**, via `tools/list` after
`initialize`, cached in `mcp_tools`.

Load-bearing details:

- **Slugging is mandatory, not defensive.** Exposed names are `mcp__<server-slug>__<tool>`.
  MCP tool names legally contain dots (`admin.tools.list`) and run to 128 characters,
  while a provider enforces `^[a-zA-Z0-9_-]{1,64}$` and rejects the **whole tool list**
  when one name violates it — so a single spec-compliant MCP name would otherwise take
  out every connector action the agent has. Truncation carries a hash suffix over the
  unslugged identity, and the result is persisted in `mcp_tools.tool_name` so a re-sync
  cannot rename a tool the model has already been taught mid-conversation. The namespace
  is Rookery's own slug, never `serverInfo.name` — the spec states outright that it is
  not unique across servers.
- **The build guard reads the owner's `read_only` column, never the server's
  `readOnlyHint`.** The MCP spec *requires* clients to treat annotations as untrusted;
  the hint only seeds the column at first sync, and the owner's correction is what
  `Execute` honours.
- **Sync's enable policy is asymmetric, and that asymmetry IS the control.** On the
  first sync tools arrive **enabled** (the owner is adding this server and reading its
  tool list right then; thirty tick-boxes would be friction with no security payoff). On
  every later sync a newly appeared tool arrives **disabled** — a server cannot grow a
  live tool between runs. Enabled tools are capped per server
  (`MaxEnabledToolsPerServer`) because tool-list size is a shared budget: one server
  advertising 80 tools degrades the model's selection across every *other* tool,
  connector actions included. Over the cap the UI states how many were held back —
  never a silent truncation.
- **Two error channels map opposite ways.** A tool *execution* error (`isError: true` —
  "date must be in the future") is returned as plain text **without** the `error:`
  prefix, because the spec says to hand it to the model for self-correction and that
  prefix is what the API engine's oscillation guard counts as a failing call. A protocol
  or transport failure gets the prefix. Reversing them either kills legitimate
  retry-with-fixed-args or lets a dead server spin out the turn budget.
- **The status flip is gated**, applying the `DBTokenStore.refresh` lesson on day one:
  only a definitive 401 produces `NEEDS_AUTH`; 5xx, 429 and transport failures produce
  `UNREACHABLE`, which neither alerts nor leaves the retry path. A down server's tools
  stay offered from cache with a definitive error — withholding them would make the
  agent silently lose capability, and the run would read as though it chose not to act.
- **Rookery advertises neither sampling nor elicitation.** The SDK infers capabilities
  from which handlers are set, so leaving them nil is the mechanism. A third-party
  server therefore cannot spend the owner's tokens or block a 03:00 run waiting for a
  human. Do not add those handlers casually.
- **`internal/mcp` deliberately does NOT use the `nethttp` private-address dial guard**,
  mirroring connectors and for the same recorded reason: the URL is owner-typed and
  self-hosted servers live at RFC1918/Tailscale addresses.
  `mcp.TestExecuteReachesPrivateAddresses` pins it.

**Both coder kinds converge on `mcp.Execute`.** The API engine offers native typed tools
(`coder/mcptools.go`); a CLI coder runs `rookery mcp exec <tool> --args '<json>'` against
a loopback bridge (`ROOKERY_MCP_URL`/`ROOKERY_MCP_TOKEN`, plus a scoped
`Bash(<bin> mcp exec:*)` grant since chat is otherwise file-only). **Native
`--mcp-config` passthrough was rejected**: it bypasses the build guard, the parker and
the 8 KiB cap, makes coder kind silently change the security posture, and — decisively —
requires writing the server credential to a file the sandboxed subprocess reads, when
today tokens never leave the host process. The MCP bridge is a *sibling* of the connector
bridge rather than a route on it, because `internal/connectors` must not import
`internal/mcp` to serve it.

**Binding** mirrors connections exactly: a `# MCP:` header in AGENT.md, auto-bind from
the servers a build actually called (`coder.Result.UsedMCPServerIDs` →
`agent_drafts.pending_used_mcp_servers`), and the agent page's card. `parseMCPLine`
returns nil ONLY when the header is absent (fall back to the build) and a non-nil empty
slice for an explicit `none` (honour it) — that nil-vs-empty distinction is load-bearing.
Builds see every enabled server; runs see only bound ones; chat sees every enabled server
(it is not an agent, so there is nothing to narrow by).

**UI:** `/connections` gains an **MCP servers** section — structurally where *Chat apps*
sits, not among the service categories, because those are derived from the vendored
registry while MCP servers are rows the owner created. **Test & sync is a hard gate**,
running a real `initialize` + `tools/list`: the returned tool list *is* the review step
where the owner reads the (untrusted, server-authored) descriptions before anything is
enabled. That matters because the real risk here is not our code but third-party server
conformance.

### Skill system (core + user skills)

Two pools of skills, both surfaced to the agent designer and the runner as `[]prompts.SkillRef`:

- **Core skills** — embedded in the binary (`internal/skilllibrary/skills/*/SKILL.md`, `go:embed`). Always-on for every user: no DB rows, no disk seeding, no admin gate. `LoadBundled()` enumerates metadata; `CoreSkillContent(slug)` returns the full SKILL.md (frontmatter+body) for agent-context injection when an agent declares the skill; `IsCoreSkill(slug)` is the reserved-name guard. `ParseMeta()` reads Anthropic+openclaw YAML frontmatter (name, description, version, license, category, `metadata.openclaw.requires.{bins,anyBins,env}`, `metadata.openclaw.install[]`). 22 bundled skills. File Processing: csv, pdf, docx, pptx, xlsx, markdown, image-ocr. Agent Behaviour: kb-curation, change-detection, notification-writing, agent-collaboration, resilient-runs, time-and-timezones. Web & Research: web-research. Development: git-and-github, cli-tool-installer. Productivity: email-triage, calendar-scheduling. Integrations: api-integration, ssh. Meta: skill-creator, skill-vetter. (web-search + web-scraper merged into web-research, and github-integration became git-and-github, because all three duplicated native tools or the GitHub connector.) **A core skill ships SKILL.md only — never a `scripts/` directory**: CoreSkillContent returns the embedded markdown and nothing else, and nothing materializes the embed to disk, so a shipped script would reference a file that never reaches the agent's working dir. Core skills teach through inline snippets; USER skills, which live on disk in the vault, may ship scripts. Pinned by `skilllibrary.TestCoreSkillsShipNoScripts` and the rest of `catalog_test.go` (frontmatter parses, name == directory, description carries triggers, referenced `scripts/` paths exist). (The Composio-based composio-toolkit + google-workspace skills were removed; connected services are reached via native connector tools.) **`playwright-browser` was removed in 2026-08** when the native `browser_*` tools landed: it taught a model to hand-write Playwright in Python, which is exactly what the weak models this platform runs cannot do, and a skill competing with a tool for the same job makes their choice worse. `skilllibrary.TestRemovedSkillsStayRemoved` pins it (the obvious fix for "the playwright skill is missing" is to write the file back), a sibling test bans any surviving skill from referencing it, and migration 019 sweeps the `agent_skills` rows that named it — that table is keyed by NAME with no foreign key, so a dangling row does not error, it just silently costs the agent a capability it believes it has.

**That removal stated a rule the project then failed to apply four more times, which is what the 2026-09 skill rewrite fixed.** The rule is in `removed_test.go`: *a skill and a tool competing for the same job is worse than either alone*, because the small models this platform runs pick badly between them — and the skill, being the more specific instruction, usually wins with the weaker implementation. `csv` went on teaching pandas after `kb_table_query` shipped; `docx`, `pptx`, `xlsx` and `pdf` went on teaching python-docx, markitdown, openpyxl and pdfplumber after `rookery kb convert` shipped. Zero of the 21 skills mentioned `kb_table_query`, `kb_file_map`, `search_files`, `glob`, `get_state`/`set_state`, the `browser_*` acting tools, `save_to_kb`, `connector exec` or MCP; exactly one mentioned `web_fetch`.

**The doctrine is now stated in `skill-creator` and mechanised in `doctrine_test.go`.** Rank: a native tool, else a CLI invocation, else Python. The test is keyed on the INSTALL SPEC rather than on prose, deliberately — a skill may legitimately *name* a superseded library to say "do not use it", which the rewritten skills do so the reader knows why the old advice went away, but it may not declare it as a dependency, because that is what the runtime acts on. Sibling tests assert the five document skills point at `rookery kb convert` and that `csv`/`xlsx` name `kb_table_query` and `kb_file_map`, so the frontmatter cannot be cleaned up while the body still teaches the library.

**No skill was deleted, and that is deliberate**: the five document skills keep their trigger descriptions ("read this excel"), and a skill that says *use `kb convert`* is more useful than an absent one — so unlike the playwright removal, no `agent_skills` migration is needed. `ssh` was added, which earns its place under the same rule: there is no SSH tool, so it is judgment plus a CLI, exactly what the second tier is for. Its content is mostly the three ways key handling fails silently — `$TMPDIR` rather than `/tmp` (the sandbox grants no `/tmp`, and the resulting error reads like an SSH fault), `chmod 600` before use, and `printf '%s\n'` rather than `echo` because a key without a trailing newline is rejected as malformed.
- **User skills** — created via the skill creator (below) or imported (ZIP/pasted SKILL.md), per-workspace, written to `<vault>/skills/<name>/SKILL.md` (+ `scripts/`), tracked in the `skills` table. Loaded from disk by `skillstore`.

At run time (`agentrunner.runCoderAgent`), the agent's declared skills' content is injected into the coder prompt's `<skill_instructions>` block. Core skill content comes from the embed (`skilllibrary.CoreSkillContent`); user skill content is read from disk. `resolveSkillBins` resolves the absolute path of every CLI tool a declared skill requires (`requires.bins` / `anyBins`: `$HOME/.local/bin/<bin>` then `PATH`) and `prompts.SkillEnvBlock` builds a `<skill_environment>` block telling the agent where each tool lives (or to install it via the cli-tool-installer skill) plus sandbox conventions (invoke by absolute path, use `$TMPDIR` not `/tmp`, secrets are env vars, vault root).

**Skill format.** `skills/<name>/SKILL.md` (required: YAML frontmatter + markdown body) + optional `scripts/` (deterministic code) + `references/` (on-demand docs). Only `name`+`description` are strictly required; `description` is the trigger — it must say what the skill does AND the contexts that activate it. Tool names are written BARE in the body (the runtime env block supplies the real path).

**Conversational skill creator** (`internal/skilldesigner`, driven by the SPA via the `/api/v1/skills/design` FSM endpoints and by chat platforms via `/skill`): mirrors `agentdesigner.Flow`'s shape — FSM (`StateIdle → StateAwaitingResume → StateDescribing → StateDesigning → StateVerifying → StateDone`), SSE progress, 7-day drafts (`skill_drafts` table, one per user), strict/forgiving approval triggers (same split as the agent designer). Flow:
1. Design conversation (text-only coder, `BuildSkillDesignSystemPrompt`) — focused Q&A, proposes a plan, asks for `approve`. Drafts are persisted on every turn so the conversation survives reloads/restarts (even on usage-limit).
2. Generation (`runGeneration`, `BuildSkillImplementationPrompt` with the `skill-creator` core skill body) — coder writes SKILL.md (+ `scripts/`) into a staging dir (`<vault>/skills/.staging-<name>/`, live folder touched only on approval), tests scripts, emits `[TEST_OUTPUT]`. Guardrails (`CheckEthics` + `RunToolGuardrails`) run on the actual generated content.
3. Vetting (`vetSkill`, `BuildSkillVettingPrompt` with the `skill-vetter` core skill body as the system prompt) — a second text-only coder call audits the skill for malicious behaviour (exfil of vault notes/USER.md/SOUL.md/secrets, raw-IP network calls, obfuscated payloads, sudo, destructive ops, …) and emits a structured report. `vettingBlocksSave()` parses the authoritative `Verdict:` line (a pure `❌ do not save` blocks save; an echoed `✅ safe to save | ⚠️ … | ❌ do not save` alternation does NOT — guards against a literal model echoing the option list). A blocking verdict keeps the user in `StateDesigning` and the skill is NOT saved. Covered by `flow_test.go`.
4. Approval → `SkillSaver.SaveSkill` writes SKILL.md+scripts/ to the vault, upserts the `skills` row (in-place overwrite if a skill of the same name exists; core-skill names are reserved and rejected), drops the draft + session, cleans up staging.

Nightly GC (in `serve`) sweeps expired skill drafts and their orphaned `.staging-<name>/` dirs alongside agent drafts.

### Conversational agent editing

Editing reuses the same `Flow` FSM via `DesignSession.IsEdit`. `loadAgentForEdit` reads live `AGENT.md` and reconciles its `# Suggested schedule:` line against the real `agent_schedules` row before the coder sees it.

**One chat surface.** `AgentEditPage` mounts the SAME `DesignerSurface` as creation from the first paint — there is no pre-screen. It used to render its own full-width chrome until the first reply landed and then swap in the surface, which jumped the layout to the designer's 10% gutter and showed no bubble (and no typing indicator) for the whole first coder round-trip. The only thing that pre-screen did is now a prop: **`startEndpoint`** routes the VERY FIRST message of a fresh session to `/api/v1/agents/:id/edit/start` (body `{message}` only — `startPayload` is the alternative way to open a session and is never merged in); every later message goes to `endpoints.design`, since a created edit session is indistinguishable from a create session. Two things make that work: `handleStartEditDesign` returns the FULL design-turn body (`web.designTurnResponse`, shared with `handleDesignChat`) — without `state` the stepper never leaves "Describe" and the Build button never appears, because nothing remounts into `GET /design/state` any more — and **`acceptRecoveredSession`** vetoes a recovered session that isn't this agent's edit (the design session is a per-workspace SINGLETON, so mount recovery would otherwise adopt an unrelated create conversation and offer to save the wrong agent). A vetoed session is treated as absent, SSE attach included, so another entity's build log can't stream into this page. `handleCancel` is likewise gated on a `sessionTouchedRef` — an untouched surface navigates without POSTing cancel, which would otherwise kill a stranger's in-flight build.

**Diagnose-before-fix flow** (`BuildEditImplementationPrompt` + the edit variant of `BuildDesignSystemPrompt`): the designer must DIAGNOSE the root cause in plain English to the user → CONFIRM the proposed fix → AWAIT APPROVAL, then the editor states the root cause + fix in code, applies only the targeted change, and fully re-tests, proving the original bug no longer occurs. Prevents superficial edits that don't fix the actual problem.

**Edit generation** runs in a sibling staging dir (`<agentID>-edit-staging`) — the live agent dir is never touched before approval. On approval, `updateAndFinish()` calls `AgentDesigner.UpdateAgent` → `db.UpdateAgentDescription` (UPDATE, not INSERT). `reconcileScheduleOnSave()` reuses the existing schedule row's ID to avoid duplicate rows and double-firing.

### Agent output protocol (AGENT.md)

- **`[CHAT]`** — sends a message to the user. A `[CHAT]` block runs until the next protocol marker (`[STATE]`, `[CALL]`, `[SILENT]`, a new `[CHAT]`) or end of output; **blank lines are part of the message, not a terminator** (an earlier rule ended the block at a blank line and silently dropped real content — fixed). Empty/whitespace-only `[CHAT]` blocks are dropped (never deliver a blank message).
- **`[STATE]...[/STATE]`** — JSON merged into `state.md`'s ```` ```json ```` fence (null = delete key); the heading, intro, and any `## Notes` prose are left untouched. Agents must not hand-edit the json fence directly, and should extend `## Notes` with a targeted edit rather than a full overwrite. Inline and multi-line forms accepted.
- **`[CALL: <agent-name>]`** — invoke another agent synchronously (max depth 3, cycle detection).
- **`[SILENT]`** — emitted alone as the last line by note-only/state-only agents that intentionally produce no user-facing message. Suppresses the prose-delivery fallback (see "Reliable delivery" below).

### Reliable delivery

Delivery does **not** depend solely on the coder emitting `[CHAT]` — models (especially basic ones) sometimes forget the marker and write the message as plain prose, or emit only reasoning. The runner (`runCoderAgent`, `parseCoderOutput`, `extractProseMessage` in `internal/agentrunner/runner.go`) guarantees:

1. **Empty `[CHAT]` filtered** — a blank marker never delivers an empty message.
2. **Prose fallback** — if no `[CHAT]` was parsed and `[SILENT]` was NOT emitted, the coder's prose output (protocol markers stripped) is delivered as the message, with a `no [CHAT] marker emitted; delivered prose as fallback` warning recorded.
3. **`[SILENT]`** — when present, the prose fallback is suppressed so silent agents aren't noisified by stray prose.
4. **Visible failure** — if a run succeeds but produces nothing deliverable and didn't signal `[SILENT]`, the user receives `⚠️ <agent> ran but produced no notification — see the run log.` instead of a silent success.
5. **A coder that returned NOTHING is a failed run, not a quiet one** (`coderProducedNothing`). Zero bytes of raw output means nothing was fetched, nothing decided and no state written — the agent's whole job was skipped — so the run is recorded `exit -1` and the message names that instead of the "produced no notification" wording, which describes an agent that ran and chose not to speak. Judged on RAW output, the only place the difference survives: parsing an empty string and parsing a marker-less paragraph both yield zero chat lines. A `[SILENT]` run is never "nothing" — the marker is the decision we asked for.
6. **Tool-call scaffolding is never delivered** (`coder.LooksLikeToolScaffolding`,
   `agentrunner.deliverableProse`). A model with no structured tool channel will
   sometimes express a pending call as raw text, and the prose fallback — built to
   rescue a forgotten `[CHAT]` — forwarded DeepSeek's `｜DSML｜` markup to a real
   user's phone. The check is keyed on the tools the run OFFERED, never on a
   provider dialect: our own tool name inside a markup construct is decisive, with
   markup density as a backstop. The name must match as a WHOLE TOKEN — every API
   run offers a tool literally named `glob`, so a bare substring test suppressed any
   real message containing "global" or "globe" beside a bracketed aside. The trigger was our own grace turn, which strips
   `req.Tools` while the model still has work queued. That grace turn is now
   best-effort — its reply is used only if it passes this same check — and
   `exhaustionSummary` composes the real message from run facts instead.
   One property is pinned by a test rather than left to luck
   (`TestExhaustionSummaryIsNeverSuppressedAsScaffolding`): `exhaustionSummary`
   lists the offered tool names, and `deliverableProse` runs the scaffolding check
   over exactly that text. It passes only because rule 1 also demands a markup
   token and the summary carries none — a coincidence in today's implementation,
   not a guarantee. Were it to regress, the engine's own account of a failed run
   would suppress itself and the user would get silence.

**`isSilentMarker` is lenient about DECORATION and strict about CONTEXT, and the asymmetry is deliberate.** The check was `trimmed == "[SILENT]"`, an exact line compare, so every ordinary way a model decorates a token missed it — `**[SILENT]**`, `` `[SILENT]` ``, `[silent]`, `[SILENT].`, `[/SILENT]`, a bare `SILENT` — and a missed marker is not a no-op: rule 4 then fires, so a correctly-behaving agent with nothing to report notified its owner anyway, on every run, forever. (Observed in production: twice a day from an agent built precisely to stay quiet.) The two failure modes are not symmetric, which is why the match still refuses a marker mentioned inside a sentence: a missed marker is noise the user can see, while a marker matched inside prose silently swallows a real message and nothing says so. A bare `silent` line is accepted because models write it and the blast radius is bounded — `silent` only suppresses the fallback and the warning, so a run with real `[CHAT]` content still delivers. `extractProseMessage` strips through the same predicate, or a fallback delivery would post the literal marker text.

**Runs log one `agentrunner: run finished` line.** Agent runs previously logged nothing on the happy path, so a run that produced no output left no trace anywhere but an empty "Raw output" section in its own note — the reason a real empty-run report had to be diagnosed out of the database. It carries `exit`, `raw_chunks`, `chat_lines`, `silent`, `produced_nothing`, `warnings` and `total_tokens`, mirroring the `build_id` tracing the designer already had.

Delivery reaches both paths: `SendOutput` (durable — web → `gateway.SendToUser`, scheduler → chat platform) and `OnProgress` (live SSE). Parser behavior is covered by `runner_test.go`, `silent_test.go` and `emptyrun_test.go`.

### Run transcript and the silent flag

**Run history could only ever replay what the user had already read.** Tool-call
milestones (`toolMilestone`) went to the progress sink, which feeds the live SSE
stream and nothing else, so they were gone the moment the stream closed. The
coder's raw per-turn responses reached the vault run note but never the database:
`FinishAgentRun` stores `finalOutput` — the `[CHAT]` lines — as `stdout`. So the
one view you open before editing a misbehaving agent showed its conclusions and
nothing about how it reached them, and an agent reporting "no change" looked
identical whether it had checked and found nothing or never checked at all.

`agent_runs.transcript` (migration 016) holds the fix: an ordered JSON list
interleaving progress milestones with coder turns. `internal/agentrunner/transcript.go`
owns the format. Four things are load-bearing:

- **It is collected in the RUNNER, not at the web layer.** That is the only layer
  both triggers pass through — the scheduler wires `SendOutput` and **no
  `OnProgress` at all** — so a collector attached where `startManualRun` builds
  its sink would leave cron runs, the ones nobody watched and therefore the ones
  most in need of a record, with nothing captured. `transcriptCollector.wrap`
  returns a non-nil sink even when handed nil for exactly that case, and forwards
  as well as records so turning the transcript on cannot cost the live view.
- **One list, not two.** Milestones and coder turns are appended as they happen
  rather than reunited by timestamp at the end, because the question the record
  answers is *what did it do, in what order* — and two lists merged afterwards is
  the same answer with a way to get it wrong.
- **It is distinct from `toolTrace`.** That is a per-call summary (name, turn,
  bytes, error) built for one `slog` line, carrying neither arguments nor ordering
  against the model's own replies. Both are kept, and `SummarizeToolTrace` is
  appended as the transcript's closing `summary` event rather than given a column
  of its own.
- **Capped at `maxTranscriptBytes` (64 KiB), dropping OLDEST with a marker.** Runs
  are kept indefinitely. The tail is kept because the end of a run is where it
  went wrong; a single event over the whole budget is clipped rather than dropped,
  since returning nothing would be the same failure this exists to remove.

**`agent_runs.silent` is a column, not an inference and not a transcript field.**
`rctx.silentSignaled` was known at the end of every run and discarded, leaving a
`[SILENT]` run and a broken one identically shaped — exit 0, empty `stdout` — so
the interface rendered the same empty row for both. Inferring it from
`exit==0 && stdout=="" && stderr==""` is the tempting zero-migration version and
reconstructs a fact the code already had, which is the shape of defect this
codebase keeps recording. It is a column rather than a field inside the JSON
because the run LIST renders a chip for it, and a per-row transcript fetch just to
decide whether to draw a chip would defeat the split below. **Not backfilled**: a
historical run reads `silent = 0`, meaning "not known to be silent", which is the
truth about a row written before the flag existed.

**`rctx.outcome()` builds the `db.RunOutcome` for all three exit paths** (coder
error, produced-nothing, success). Centralised because each writes its own row and
a path that forgot the new fields would produce a run with no record and no sign
anything was missing. `db.RunOutcome` is a struct rather than more positional
parameters — the list was already seven long and `transcript`/`stderr` are both
strings whose meanings swap without the compiler noticing.

**List and detail are deliberately split.** `db.ListAgentRuns` selects `silent` and
**not** `transcript`; `db.GetAgentRun` selects both and backs
`GET /api/v1/agents/:id/runs/:runID`, fetched only when a row is expanded. The
agent-detail response already lists every recent run, so carrying each transcript
would pay on every page load for a panel that is collapsed by default. The endpoint
is scoped through the AGENT, not just the run's `workspace_id`: the run id comes
from the URL, and resolving it without confirming it belongs to this workspace's
agent would let a guessed id read another tenant's run. `transcript` marshals as
`[]` and never `null`, asserted on raw bytes — a TypeScript default substitutes
only for `undefined`.

The vault run note gains a `## Tool calls` section from the same collector
(`progressLines`), since that note is the durable copy an agent can read.

### Live run progress is retained, not piped

**`agentRunState` holds `lines []string` and readers follow by index.** It used to
hold a `chan string`, which is consume-once and single-reader: leaving the agent
page closed the SSE stream and discarded every line already delivered, so
returning to a running agent showed an empty activity card. Two tabs on one run
also stole each other's lines. A per-subscriber channel is the obvious fan-out and
has to either block the run on a slow reader or drop — and dropping is how a live
view silently disagrees with the record it is supposed to be showing, so readers
follow `lines` by absolute index and wait on a `notify` channel that is closed and
replaced on every append (closing IS the broadcast, so a reader that grabbed the
channel before the append still wakes).

**The stream opens with a named `meta` event carrying `elapsed_ms`.** `RunPanel`
stamped `startedAt = Date.now()` on attach, which measures how long the TAB has
been watching — the reason the timer restarted at zero on every revisit. A
duration rather than a start timestamp, because the browser anchors it against its
own clock and an absolute time would be wrong by however much the two disagree; on
a self-hosted LAN install that is potentially minutes.

Retention is capped at `maxRetainedLines` (2000), dropping oldest, with `dropped`
tracking the absolute offset so a truncated reader fast-forwards rather than
blocking. The existing 90s eviction still bounds how long a finished run stays
attachable; after that the transcript is the record. Cron runs remain
**not** live-streamable — the scheduler has no handle on the web server's tracker
— which is why the transcript matters for them.

### Secret injection

Secrets stored encrypted in `secrets` table. Three sources of `MasterPw` at runtime:
- **Scheduled runs** — `scheduler.go` decrypts stored `EncryptedMasterPassword` (encrypted with `systemKey`).
- **Manual runs** — `handleRunAgent()` does the same. No password field on the run form.
- **Agent generation** — `Flow.WithSecretsLoader()` decrypts and injects via `WithExtraEnv(secrets)` so agents can make real API calls during validation. Same loader is wired on the skill creator (`skilldesigner.Flow.WithSecretsLoader`).

### Coder tool isolation

`internal/coder/coder.go` modifiers (all return a shallow copy):

- **`WithNoTools()`** — `--allowedTools ""`. Used for design conversation turns.
- **`WithAllowedTools(tools)`** — Required whenever `--setting-sources ""` is active or subprocess blocks on permission prompts. Agent generation: `"Bash,WebFetch,Read,Write,Edit"` (matches the run set, so builds do real end-to-end tests against live services); skill generation: `"Bash,Write,Edit,Read"`; runs: `"Bash,WebFetch,Read,Write,Edit"`.
- **`WithDir(dir)`** — overrides subprocess CWD. Used by generation AND every agent run — without it the agent writes to the shared per-workspace home and contaminates other agents.
- **`WithExtraEnv(env)`** — merges additional env vars. System overrides always take precedence.
- **`WithKBJournal(j)`** — journals whatever this coder writes into the user's knowledge base so the caller can undo it. Set **only** for a rehearsal (an agent build, the create-build dry run); nil everywhere else, so chat and real runs — which write the knowledge base on purpose — are unaffected by construction. Not confinement: the writes happen and are reverted, because a rehearsal that cannot write cannot rehearse a KB-writing agent. The API engine journals each host-tool write and brackets each script call; a CLI coder has no host tools, so `Generate` brackets its whole call.
- **`WithBackendType(t)`** — forces `"claude"`, `"generic"`, `"api"`, or `""` (auto-detect by binary name).
- **`WithAPIConfig(provider, model, baseURL, apiKeySecretName)`** — switches the coder to the in-process API engine (`coder_kind=="api"`). Once set, `Generate`/`Chat`/`Ping` dispatch to the tool-calling loop instead of a subprocess. **`WithSecretsLookup(f)`** attaches the lazy provider-key resolver (the API engine fetches its own key by secret name at run time, so every call site authenticates regardless of env injection); **`WithVault(v)`** attaches the vault for the host file tools; **`WithProgress(f)`** streams per-tool-call milestones to the run AND build SSE (agent + skill designer builds wire it too); **`IsAPI()`** reports the kind. `Ping(ctx, workspaceID)` now takes a workspace id (needed to resolve the API key).

**`CoderBackend`** (`internal/coder/backend.go`): one struct per coder — `claudeBackend` (JSON, `--setting-sources ""`), `opencodeBackend` (`run <prompt> --format json`, NDJSON events, XDG isolation — VERIFIED end-to-end on a real host incl. the success path: `ollama-cloud/glm-5.2` → reply; the reply text is nested in `part.text` for `part.type=="text"` events, not top-level `text` — parsed accordingly), and authored-unverified `codexBackend` (`exec --json`, `CODEX_HOME`), `geminiBackend` (`-p --output-format json --yolo`, `~/.gemini`), `cursorBackend` (`-p --output-format json --trust`); `genericCLIBackend` is the last-resort fallback. Each backend declares its own `configEnv` (per-workspace config-dir env vars) + `seedFiles` (operator auth seeded in; sessions/history are not). `coder.BackendForBin` maps a chosen binary to its backend type; `Coder.Smoke` is the fail-loud end-to-end check surfaced in coder settings. The API engine is not a `CoderBackend` — it bypasses the subprocess path entirely.

**Critical:** `--setting-sources ""` + no `--allowedTools` = subprocess hangs indefinitely (CLI engine only).

**Coder detection off Linux.** `DetectInstalled` takes a `detectHost` (GOOS, home,
`LookPath`, `Stat`, `Getenv`) rather than calling the OS directly, because there is
no macOS or Windows runner here and every bug it had was platform-specific.
`exec.LookPath` already honours `PATHEXT` on Windows, so a coder **on PATH** always
resolved; the fallback search is what was broken, in three separate ways. It looked
only in `~/.local/bin` — missing Homebrew's `/opt/homebrew/bin` (Apple silicon) and
`/usr/local/bin` (Intel), which matters because **a launchd-started process inherits
a minimal PATH containing neither**, so detection could fail for someone whose
terminal finds the binary without any trouble. It missed `%APPDATA%\npm` and
`%LOCALAPPDATA%\Programs` on Windows. And it gated every candidate on
`fi.Mode()&0o111 != 0`, a bit **Go never sets on Windows** (mode is synthesized from
file attributes), so the fallback there could not match anything at all — compounded
by statting the bare name when npm installs these coders as `claude.cmd` shims.
`coderSearchDirs` supplies the per-platform list and `binCandidates` expands
PATHEXT-shaped names on Windows only. The executable-bit test still applies on POSIX,
where it is real. `detect_platform_test.go` describes all three platforms against a
fake filesystem; the claim is that the logic is right and pinned, **not** that it was
run on a Mac.

**OpenCode requires an explicit model (multi-provider, no built-in default).** Unlike Claude (whose default model is tied to its login), OpenCode talks to many providers and has NO default model of its own. When none is specified it targets a hardcoded default provider (**OpenRouter**) and returns `coder error: User not found. (status 401)` if that provider isn't authed — the failure looks like broken auth but is really a missing model. The model comes from the workspace's `CoderModel` field, passed as `opencode run … -m <provider/model>` (e.g. `ollama-cloud/glm-5.2`); `opencodeBackend.buildArgs` adds `-m` only when `CoderModel` is set, and **the coder settings form's `#coder_local` section now has a Model input** that says so inline. Codex, Gemini and Cursor take the same field via `-m`/`--model`. The requirement is stated as help text, never enforced as validation: a hard rule would be wrong the moment OpenCode ships a default of its own, and would block anyone relying on a host-level one. **The per-workspace sandbox redirects `XDG_CONFIG_HOME` to an empty dir, so OpenCode does NOT inherit the operator's `~/.config/opencode/opencode.json`** (its default model, `plugin` list, and `mcp` servers such as `oh-my-openagent` / `codebase-memory-mcp`) — only the seeded `~/.local/share/opencode/auth.json`. Consequences: (a) setting a default model in the host `opencode.json` does NOT reach workspaces — the model must come from `CoderModel`; (b) host plugins/MCP intentionally do not run inside the sandbox. Re-authing a provider (`opencode auth login`) does not change the default-model selection, so it alone never fixes the 401.

### API coder engine (`coder_kind == "api"`)

A workspace can run its coder as a **direct LLM provider API** instead of a host CLI binary. `Coder.runAPI`/`runToolLoop` (`internal/coder/api_engine.go`) drive an in-process loop: `Complete → execute host tools against the vault → feed results back → Complete`, until the model emits a final answer (no tool calls), the turn budget is spent, or the deadline passes. The model's final text carries the same `[CHAT]`/`[STATE]`/`[SILENT]` protocol markers, so the runner's parser is unchanged.

- **Host tools** (`hosttools.go`, `hostToolSet`): `read_file`/`write_file`/`edit_file`/`list_dir` are vault-path-safe (relative to workDir/vault root, escapes rejected). Two **always-on read-only discovery tools** (NOT exec-gated — safe in chat, closing the API-chat gap with the CLI chat's `Grep`/`Glob`): `search_files(query)` exposes the existing `vault.Searcher` (ripgrep + pure-Go fallback, case-insensitive fixed-string, 5 matches/file, skips `.kb`) so "find the note where I mentioned the dentist" is a TIER-1 lookup instead of a `read_file` walk; `glob(pattern)` finds files by name/pattern (`*`/`?`/`**`) across the vault via `compileGlob`→anchored regexp, skipping dotfiles + `.kb`; an **absolute-within-vault** path passed as the pattern is relativized first (mirror `resolveVault`) so a weak model that types the full vault path still matches, and an absolute path outside the vault is rejected. Both search the **whole vault root** (not workDir) and return a non-`error:` empty-result notice on no matches (so they never trip the oscillation guard). `kb_file_map(path)` describes ONE file before it is read (kind, size, shape) and `kb_table_query(...)` filters/groups/aggregates a markdown table's rows — both added by PR #247, both **read-only pure functions of one vault file** and therefore also outside the exec gate. **Four** tools are gated behind `includeExecTools` (agent builds+runs only — workDir ≠ vault root; excluded from chat): `run_script` (`python3`) and `bash` both run sandboxed via Landlock (`buildScriptCommand`) with the agent's secrets in env (provider key stripped), reporting stdout+stderr on failure, plus `get_state`/`set_state` (statetools.go), which only mean anything where workDir is a real agent's own dir. **`web_fetch` and `web_search` are NOT exec-gated** — this documentation claimed for a long time that they were and that chat could not reach them, while the code declared them above the gate and two tests (`hosttools_web_test.go`, `searchkey_wiring_test.go`) pinned them as available in chat. They are read-only, cannot carry secrets and cannot reach private address space, which is the reason they sit outside it. `web_fetch(url)` is an HTTP(S) client in the **host process** (no sandbox — it adds no capability agents lack via run_script/bash) that returns text (HTML reduced to readable text via a stdlib stripper), **retries transient 429/5xx/network internally** so a blip never trips the loop-guard, and **cannot carry secrets** (authenticated calls use run_script/bash); `web_search(query)` is the discovery complement — a keyless DuckDuckGo HTML scrape (`ddgHTMLEndpoint`, browser `User-Agent`) returning numbered title/url/snippet entries (real URL decoded from the `uddg` redirect param via `parseDDGResults`/`decodeDDGRedirect`, HTML stripped), with the same transient-retry contract as `web_fetch` and a 200-but-no-results page yielding `"(no search results)"` (non-error) so the model falls back to `web_fetch` without tripping the guard. `ddgBaseURL` (empty→production) lets tests point at an httptest server. All results are byte-capped and never empty (an empty tool result breaks strict serializers). This closes the CLI-vs-API capability gap: a simple public fetch/find is now TIER 1 via `web_fetch`/`web_search`/`search_files`/`glob` (see the network-split + file-discovery tier guidance in `prompts.agentArchitectureGateBlock`), matching a CLI coder. **Caveat:** an arbitrary `bash` string is sandboxed but NOT AST-scanned the way an authored `tools/*.py` is at build.
- **Turn budgets are spent by UNPRODUCTIVE turns only** (`internal/coder/turnbudget.go`):
  base `maxAPITurns` (30) for runs/chat, `maxBuildAPITurns` (50) for builds, a
  `maxHardTurns` (150) ceiling never extended by anything, and a
  `maxUnproductiveStreak` (6) that stops a stuck model far sooner than any base
  budget would. A turn is productive when it executed at least one tool call that
  succeeded and was not a short-circuited repeat. The fixed cap this replaced could
  not tell a runaway loop from legitimately long work — they are identical by turn
  count, and an agent that genuinely needed more turns hit the cap, had its tools
  stripped by the grace turn, and emitted a pending tool call as raw text.
  **The 150 ceiling is not reachable in practice today**: nothing trims
  `req.Messages`, so a 128k-context model exceeds its window around turn 45-50 and
  the provider errors first. History compaction is the prerequisite.
  Budget exhaustion no longer produces `ErrMaxTurns`: `graceTurnOnBudgetExhausted`
  always returns a `Result`, falling back to `exhaustionSummary` when the model's
  wrap-up is empty or fails the scaffolding check. The five `errors.Is(err,
  coder.ErrMaxTurns)` branches (agentdesigner ×2, agentrunner, gateway,
  skilldesigner) are therefore vestigial — kept rather than removed because deleting
  them spans five packages for no behavioural gain, and each site's replacement
  behaviour was reviewed: the gateway branch was already unreachable, and an
  exhausted run now records `exit 0` with the summary delivered as its message.

  **A truncated build must not read as a finished one.** Because the grace turn now
  always returns a non-nil `Result`, an exhausted BUILD whose script had already
  verified gets `thinProof=false`, so the weak-backend gate never fires and
  `parseBlockedOutput` finds no marker in the deterministic summary — leaving the
  confident "Here's what a test run produces…" with no sign the build ran out of turns.
  `Result.StopReason` ("", `budget`, `unproductive`, `hard-ceiling`, `empty`, `truncated`)
  carries that fact
  out of the engine, and BOTH designers caveat off it rather than off the model
  remembering to emit `[BLOCKED]` (`agentdesigner.caveatTruncatedBuild`, and its
  two-arg mirror in `skilldesigner` — a skill build sets `buildphase.Generation`, so
  it shares the budget and had the identical defect). A caveat that depends on a failing model to announce
  its own failure is the same defect this whole change set exists to remove.
- **Build-time script verification** (weak-model hardening, build only): the engine refuses to "finish" a build while the model authored a helper script that never once returned real output — `verifyFinishNudge` drives it to run/inspect/fix (bounded by `maxVerifyNudges`), or report the failure in plain language. Plus a loop-guard (`recentFails` ring + `consecutiveFails`) that short-circuits repeated/oscillating failing calls.
- **Script-verification bridge → `coder.Result`.** The engine tracks per authored `tools/*.py` whether it RAN with real stdout (`hostToolSet.producedOutput`) and captures that stdout (`lastVerifiedOutput`, secret-redacted via `redactSecrets`). `runToolLoop` surfaces this ground truth on `Result.ScriptVerified` / `Result.ScriptOutput` (+ `Result.ScriptRan` = an authored script was executed at least once, for observability). The agent designer's `decideBuildOutcome(workDir, resultText, backendType, scriptVerified, scriptOutput)` **trusts the engine** instead of re-deriving verification from a `[TEST_OUTPUT]` marker the weak model often forgets: an engine-confirmed run advances to review showing the real captured output as the sample, and the weak-backend gate (`BackendToolCalling && hasAuthoredScript && thinProof && !scriptVerified`) only fires when the engine did NOT confirm a run — fixing the false "I couldn't confirm the helper it wrote actually runs." When that gate DOES fire, the `agentdesigner: build not presentable` slog carries `script_ran` to discriminate "ran but produced nothing" (broken/outbound-blocked) from "never ran". Fields are zero for CLI coders and runs/chat. **On a CREATE build this is no longer the last word on the review sample**: `dryRun` runs the finished agent once afterwards and overwrites the sample with that output when it produces one (see "A create build RUNS the agent once before showing it to you"), so a TIER 1 agent — which authors no script and can therefore never set `ScriptVerified` — still shows something that ran. Covered by `build_outcome_test.go` + `api_engine_test.go`.
- **Design conversations vs one-off chat** (`Chat` split by `noTools`): `chatAPI` (text-only single completion, real alternating user/assistant turns so the model doesn't re-ask its opening question) vs `chatToolsAPI` (adds the always-on host tools, minus the exec-gated `run_script`/`bash`/`get_state`/`set_state`, for on-demand KB read/write — parity with the CLI chat's set).

- **The design conversation runs a READ-ONLY tool profile** (`Coder.WithReadOnlyTools`,
  `hostToolSet.readOnly`). It was the only coder surface in the product offered NO tools
  while chat, builds and runs all had the always-on eleven — so the designer proposed
  plans it had no way to check. Most concretely: `agentArchitectureGateBlock` makes the
  design's `[TECHNICAL SPEC]` Tier a **ceiling** the build may only lower, and the
  designer was choosing it with no view of how much data the agent would face.
  It is offered the always-on set **minus `write_file`/`edit_file`/`save_to_kb`** — eight
  tools — and never the exec-gated four. Five things are load-bearing:
  **it is an additive flag, not a third value of `noTools`** (that field is read at ten
  sites governing build, run, chat, ping, kickoff selection and the exec gate; every one
  is byte-identical for callers that don't set the new flag);
  **it is enforced twice** — `tools()` stops declaring the three and the dispatch switch
  refuses them by name, because a model can call a tool it was never offered and the
  switch dispatches by name (the guard sits before the switch and shares one
  `mutatingToolNames` map with the filter, so a new mutating tool is covered without
  anyone remembering);
  **`includeExecTools` gains `&& !c.readOnlyTools`**, so read-only never means shell by
  construction rather than as a side effect of the workDir-vs-vault-root comparison;
  **`effectiveAllowedTools` makes the CLI hang impossible** — `claudeBackend.buildArgs`
  emits no `--allowedTools` flag when `noTools` is false and the grant is empty, which
  alongside `--setting-sources ""` hangs the subprocess forever, so a read-only caller
  that forgets `WithAllowedTools` falls back to `DesignAllowedTools("")`;
  and **`DesignAllowedTools` scopes the kb grants per subcommand** rather than reusing
  `ChatAllowedTools`' blanket `Bash(<bin> kb:*)`, which would reach `kb convert` — and
  convert writes a note into the vault.
  Both designers call it (they share one front end and drift otherwise), with
  `WithDir(vaultRoot)` because a CLI coder's run dir otherwise defaults to the
  per-workspace claude-home where credentials live. A workspace with no vault stays
  text-only. A design turn also gets a tighter budget (`maxDesignAPITurns` = 8, vs 30 for
  a run) because it is a **blocking POST with no SSE** — SSE covers generation only — so
  every extra turn is time the user waits with no progress output.
  **The skill vetter deliberately keeps `WithNoTools`**: it audits generated skill content
  for exfiltration of vault notes, `USER.md`, `SOUL.md` and secrets, and an auditor holding
  file and network tools gives the audited content a way to act. A test pins the carve-out.
  Not wired: the KB bridge, so a CLI-engine design conversation gets
  `Read,Glob,Grep,WebFetch,WebSearch` and no `kb map`/`kb table` — matching what a CLI
  **build** has today, which lacks the bridge for the same reason.
- **Providers** (`internal/llm`): `openai`, `openrouter`, `anthropic`, `generic` (any OpenAI-compatible endpoint; base URL required). Not probed — always available in the settings picker via `coder.APIProviders()`.

### Usage-limit / rate-limit detection

`coder.ErrUsageLimit` — CLI: non-zero exit with empty stdout+stderr; API: provider 402 (credits/quota exhausted, `ErrQuotaExhausted`). `coder.ErrRateLimited` — API transient 429 that didn't clear within the retry budget (distinct so the message says "try again in a moment", not "out of quota"). `coder.ErrAPIAuth` (bad/missing key) is a config error, not a usage limit; `coder.ErrMaxTurns` is now vestigial — budget exhaustion returns a `Result` carrying `exhaustionSummary` rather than an error (see the turn-budget bullet above). `agentrunner.FriendlyRunError` converts each to a user-facing message sent via `input.SendOutput` on every run failure. Also handled softly during generation and design conversation turns. API token usage is accumulated across the loop (`coder.Usage`) and persisted per run.

### The browser (JavaScript-rendered pages, and acting on them)

`web_fetch` is an HTTP client, so a single-page app returns ~400 bytes of markup
with no words in it, and the keyless search cascade is regularly served a JS
challenge that is indistinguishable from genuine no-results. `internal/browser`
closes both, via `github.com/mxschmitt/playwright-go` (note the module path: it
still declares `mxschmitt`, so requiring it under `playwright-community` fails
to resolve).

**Availability is optional and degrades with a warning.** The runtime is Node +
the playwright driver (134 MB on disk) plus Chromium (389 MB, from a 115 MiB
download), so it is installed by `rookery browser install`, reported as its own
`/healthz` boolean, and **deliberately not an `onboard.HostTool`** — that type
probes a binary on `PATH`, this is a cache directory, and the "four host tools"
count is asserted across four delivery surfaces. A missing runtime yields
`ErrBrowserUnavailable` naming the fix and the tools are simply not offered:
advertising a tool the host cannot execute is worse than omitting it, because
the model spends turns on it and reports a platform fault to the user.

**`rookery onboard` offers it, and says NOTHING when it is already installed**
(`cmd/rookery/onboard_browser.go`). The silence is the part worth keeping: this
is a several-hundred-megabyte opt-in most installs will never have, and setup
output is only read while it stays short enough to read — so an owner who
already has it gets no line about it, unlike `stepHostTools`, which reports "all
present". The step works identically on all three platforms because the install
is a Go call into the Playwright driver, which fetches the right Node build and
Chromium for the host — no package manager involved, unlike the host-tool step.
Only Linux needs shared libraries afterwards, which is why
`browser.SystemDepsHint` returns `""` off Linux rather than the caller branching
on `GOOS`; an unrecognised Linux package manager falls back to naming the
libraries. `--yes` installs it, consistent with the host tools and the service.
**Both installers offer it too**, so setup is not the first place an owner hears
about it — being offered during `onboard` what the installer should already have
handled was the reported complaint. The scripts do not resolve it themselves and
`packaging/browser_test.go` fails if they start to: Playwright's runtime is
version-MATCHED to the binary (the cache directory is named after the Playwright
version compiled in), so a version hardcoded in shell would silently stop
matching at the next dependency bump, and the symptom would be "installed and
Rookery cannot see it" — the exact class of bug this work removes.

**`browser.Resolve` decides what is launched, and it is why an owner with Chrome
is no longer asked to download one.** Rookery used to launch exactly one thing,
Playwright's managed Chromium, so a machine with Chrome or Edge already on it
was offered a several-hundred-megabyte download for a capability it had. The
order is: a managed Chromium, then a system **Chrome or Edge driven through
Playwright's `Channel`**, then a managed Firefox. Four things are load-bearing:

- **The driver is a FLOOR, not an alternative.** Playwright drives even a system
  Chrome through its own Node driver, so `playwright.Run` fails without it
  whatever browsers exist. Detection therefore removes the ~115 MiB Chromium
  build from the download, never the ~70 MB driver, and `browser.InstallSize`
  says which of the two the owner is actually being offered. `Resolve` reports a
  missing driver as a missing DRIVER, because "no browser" would send someone
  looking in the wrong place.
- **`Probe().OK` now means a browser can actually be launched.** It used to ask
  `hasChromium`, which matched a *directory* name — so a half-extracted cache
  reported the browser present while `ChromiumExecutable` returned `""` and every
  render failed. Resolution requires a real executable.
- **`ChromiumExecutable` falls back to a system Chrome**, because
  `internal/export` shells out to a Chromium **binary** rather than going through
  Playwright. Without that fallback, the change that stopped downloading Chromium
  would have left PDF export reporting "unavailable" on a host rendering pages
  perfectly well — reintroducing the exact invisible-working-renderer bug that
  function was written to fix. **The fallback cannot cover a host whose only
  browser is a managed Firefox**, which resolves and renders while PDF export
  stays unavailable: PDF needs a Chromium binary and there is none. Recorded as a
  gap rather than fixed, because it requires having installed Playwright's
  Firefox deliberately, and `rookery browser install` resolves it.
- **Each engine carries its OWN loopback hardening, and WebKit is refused by
  name.** The browser is routed through a guarded proxy, and every engine needs a
  different setting to stop it bypassing that proxy for localhost, where this
  install's own connector, KB and MCP bridges listen: an argument for Chromium
  (`--proxy-bypass-list=<-loopback>`), a preference for Firefox
  (`network.proxy.allow_hijacking_localhost`). WebKit has no equivalent that
  could be verified here, and the only test asserting the *behaviour* rather than
  the flag sits behind the `browser` build tag that CI does not run — so a
  WebKit-only cache is reported as unusable **with a reason** rather than either
  launched unprotected or silently read as "no browser installed".
`Channel` is preferred over `ExecutablePath` throughout, because playwright-go
documents the first and warns "use at your own risk" about the second.

**`/healthz` reports `tools.browser` and deliberately does NOT warn about it.**
The other four host tools are *expected* to be present — `install.sh` offers
them, the container ships them, both package formats recommend them — so an
absence is a deviation worth reporting. A warning that fires on virtually every
default install is how a warnings list stops being read, and it would devalue
the four that mean something. `TestNoWarningsWhenHealthy` catches a regression
here; the comment in `Warnings()` says why the entry is absent, because adding
one looks like an obvious omission being fixed.

**Chromium runs in a SANDBOXED HELPER PROCESS, and that was verified before it
was designed around.** `playwright-go` drives the Node driver from whatever
process calls it, so the naive implementation puts a browser rendering untrusted
third-party content in the same address space as the database, the system key and
every decrypted secret — which is precisely why the MCP section defers stdio
transport. It would also have been a **regression**: the `playwright-browser`
core skill this replaces already ran Chromium inside the coder's own Landlock
sandbox. So `__browser-host` is spawned through `sandbox.Wrap`, and Chromium was
measured launching, running JS and producing an aria snapshot under
`landlock.V5.BestEffort()` at ABI 8. Its grants are each load-bearing: **RW** on
the scratch profile dir, `~/.cache/ms-playwright`, `~/.cache/ms-playwright-go`
and `/dev/shm`; **RO** on `SystemReadOnlyPaths()` **plus the directory of the
running binary** — without that last one the helper cannot exec itself and dies
with a bare `permission denied`, which is the first thing a reimplementation
hits. Process separation alone would buy nothing (same uid, same files); the
Landlock confinement is what makes the split worth its complexity.

**The address guard is a CONNECT proxy, not URL inspection.** Chromium resolves
DNS itself, so `net.Dialer.Control` cannot reach it, and inspecting the URL up
front misses the two cases that matter — a public hostname resolving into
private space, and a redirect hop. The helper owns a loopback proxy whose dial
decision **is** `nethttp.DenyPrivateAddr`, so every request, redirect and
subresource passes one policy with no second copy of the blocklist. Measured: a
real page returned ~8k characters of body text while a `goto` at a loopback URL
standing in for the connector bridge was refused at the proxy.
`--proxy-bypass-list=<-loopback>` is set explicitly **even though it was measured
to be redundant** (Playwright already routes loopback through the proxy) —
relying on an undocumented default for a security property is how it regresses,
so the flag is set and the test asserts the BEHAVIOUR, not the flag.
`ROOKERY_BROWSER_ALLOW_PRIVATE=1` is the documented escape for reading a
self-hosted dashboard, and it logs a warning at startup.

**Routing is stated from both ends, because that is what makes a separate tool
work.** `browser_read`'s description says to try `web_fetch` first; `web_fetch`
returns a notice naming `browser_read` when an HTML response yields almost no
words. The prompt half alone is a rule stated thousands of tokens before the
failure; the result half arrives exactly when the model is stuck. Silent
escalation inside `web_fetch` was rejected — it would make a fetch cost seconds
and spawn Chromium invisibly, and the tool's own description would stop being
true. The threshold counts **words, not bytes**: an SPA shell is often several KB
of `<script>` tags, so a byte test would call it a full page.

**Search:** `websearch.BrowserProvider` registers LAST in the cascade and only
when the browser is available. Every engine above it is one HTTP request; this is
seconds plus a browser, so it runs only once the cheap engines have all returned
nothing — which for this cascade is exactly the signal that they were served a
challenge. `Label()` renders it "DuckDuckGo (browser)" so the provenance line
tells the user a browser was needed.

**Acting is gated by ONE predicate, `browser.CheckAct`,** shared by the API
engine and the CLI bridge so changing coder kind cannot change what an agent may
do. There is exactly ONE browser permission, and the reason there is only one is
a measured finding rather than a simplification.

**The lower tier — a grant for clicking and typing AT ALL — was removed after it
was shown to gate nothing.** An agent asked to log into a site and report back
did it with `bash` and `curl`: eleven calls, not one browser tool touched, and
the same result whether the grant was on or off. The switch withheld one ROUTE to
an action the agent could already perform another way, so it cost the owner a
decision about a task they had just described in words and bought no safety. It
also implied a guarantee that was not there. Note the residual, which is real: an
agent with `bash` can still POST to a payment endpoint, and no browser permission
covers that — closing it means withholding outbound-capable secrets or confining
the network, neither of which is built.

What remains is `agents.browser_irreversible` (migration 020): may this agent do
something that cannot be undone. Two layers still apply in order:

- **The build-phase rule now PERMITS ordinary interaction and refuses only the
  irreversible step**, telling the model to describe what it would have done. The
  earlier rule refused every mutating call at build, which meant a rehearsal
  could not log in — and a rehearsal that cannot log in proves nothing about the
  agent anybody doubts. The cost is stated rather than hidden: a rehearsal now
  genuinely fills forms and clicks through pages.
- **`browser_irreversible`** is the owner's grant, and it outranks nothing: a
  build refuses the irreversible step even when it is set, because a rehearsal
  happens before the agent has been approved at all.

**Consent is collected during DESIGN, not discovered during a run.** The
`[TECHNICAL SPEC]` carries an `Irreversible actions:` line, `SpecDeclaresIrreversible`
reads it, and `DesignSnapshot.PlanDestructive` (derived, like `PlanReady`, from the
last assistant turn) drives the build bar: the button reads **"Allow and build"**
above a one-line warning. Approving records `DestructiveApproved` on the session,
and `persistIrreversible` then turns the permission ON at save, so the agent works
on its first run instead of stopping halfway for consent already given. The owner
withdraws it on the agent's page.

That ordering is the point. The first implementation only had the run-time
refusal, so the owner met a stopped agent and a refusal buried in a run log,
having already approved something whose shape they were never shown. The consent
moment belongs where the user is reading the plan.

**A declaration alone never GRANTS.** `persistIrreversible` shows the permission
when either source says so — the build's `# Irreversible actions:` header or the
approval — but grants only on the approval, because a header the model wrote
about itself is not consent.

**`agents.browser_needs_irreversible` is a FINDING, not a permission**, and it is
what decides whether the owner is shown anything. It is set by the designer's
`# Irreversible actions:` header at build (`ParseIrreversibleLine`, the same
shape as `# Skills:`) and by any run that is REFUSED such an action
(`coder.Result.BrowserWantedIrreversible` → `db.MarkAgentNeedsIrreversible`, the
same shape as connector auto-bind). Either way the permission appears on the
agent's page, above the schedule, with an explanation — rather than the owner
meeting a stopped agent and a refusal buried in a run log. It is **additive and
never cleared automatically**: both sources are fallible, so letting either clear
it would retract a warning the owner may already have acted on. A missing header
reads as "no", because defaulting the other way puts a payment warning on every
agent whose model forgot a line, and a warning that appears everywhere is one
nobody reads.

**The irreversibility judgement is now the whole guard, which changed what it has
to do.** As a second layer a miss cost nothing the lower tier was not already
gating; as the only layer a miss is a real click on a real payment button. So it
judges the PAGE as well as the control:

- a control name matching `irreversibleHints` — which now carries Macedonian,
  German, French, Spanish, Italian and Nordic terms, because an English-only list
  left the guard silently absent on exactly the sites this platform's own owner
  uses. `matchesHint` splits on `unicode.IsLetter`, not `a-z`; the previous
  splitter discarded every Cyrillic word, so adding the terms without fixing it
  would have changed nothing.
- **any mutating action on a page that reads as checkout/payment/deletion**,
  whatever the control is called. This closes the two holes a name-only test
  leaves: a button with no accessible name, and `browser_press("Enter")` on a
  focused form, which has no control to judge at all and was completely ungated.
- **Only the URL's PATH is matched, never the host.** Matching the whole URL
  looks equivalent and is not: a company at `billing-portal.example.com` would
  have every action on every page treated as a payment. A guard that fires while
  merely browsing trains the owner to switch it on permanently, which is worse
  than not having it. Caught by its own test, not in review.

The grant is **per agent, not per site** — a domain allowlist was considered and
rejected because a real flow redirects across hosts (an identity provider, a
payment processor), so the list would either break ordinary logins or be widened
until it meant nothing.

**Element addressing is Playwright's aria "ai" snapshot**, which carries
`[ref=e2]` handles its `aria-ref=` selector engine resolves — so the model says
`click(ref=e7)` instead of composing a CSS selector, which is the single property
deciding whether a weak model can drive a page at all. The raw snapshot measured
**53,592 characters for one news homepage** against an 8 KiB result cap, so it is
filtered to interactive roles and **says how many rows it withheld**; a silent
truncation reads as "this page has nine controls" and the model concludes the
button it needs does not exist. The parser is tested against a **real captured
page** (`testdata/github-login.snapshot`) — a fixture invented to match the
parser would only prove it agrees with itself.

**A ref is re-resolved against the LIVE page before any mutating call.** The
irreversibility check is only as good as the name it judges, and a name from the
model's previous listing may describe a control the page has since re-rendered —
which is exactly the case where "Next" has become "Pay now". A ref that cannot be
identified is **refused**, not passed through with an empty name: an empty name
matches no hint, so continuing would quietly demote an unidentifiable click to
the lower grant, failing open on the one check that guards payments.

**Secrets are typed, never seen.** The model writes `${SECRET_NAME}` and the host
substitutes at fill time. `browser.ResolveSecretValue` deliberately does NOT
reuse `secrets.Service.Proxy` despite sharing the syntax: `Proxy` leaves an
unresolvable placeholder AS-IS, which here would type the literal string
`${CARD_NUMBER}` into a payment field, so this **fails closed** and names the
missing secret (never its value — an error string is the one part of a tool
result that routinely reaches a log). Four echo channels are redacted on the way
back: page text, a field's rendered value, the final URL (a GET form puts it in
the query string) and Playwright's own error messages. **Screenshots are never
returned to the model** — redaction cannot touch pixels — and that is a permanent
non-goal, not a deferral.

**Refusals are not shaped like failures.** A refusal returns WITHOUT the `error:`
prefix, because the API engine's oscillation guard counts that prefix as a
failing call worth short-circuiting, whereas a refusal is a settled outcome the
model must report to the user. Same distinction `internal/mcp` draws between a
tool's own error and a protocol failure.

**Chat gets reading and never acting**, on the always-on side of
`includeExecTools`. Reading a rendered page carries no more authority than
`web_fetch`, which is already always-on; acting from chat would mean clicking
"Pay" with no approval gate at all (`ParkerFor` returns nil when `agentID == ""`),
holding the user against themselves.

**The designer is told the browser exists, and that was missed once with real
consequences.** `browserToolsBlock` reaches chat, the build prompt and the runtime
prompt; `BuildDesignSystemPrompt` was the one surface without it. So the designer
had no idea agents can click, and told a user outright that "this platform's
agents can't click buttons", refused to build the agent four times across a
conversation, and recommended Selenium instead — confidently, on the FIRST
surface anyone meets. A capability the designer does not know about does not
exist as far as users are concerned, however well it works underneath.

**The designer probes a site before the plan is approved.** Both designers are
`WithNoTools` and cannot investigate, so — exactly as `vault.BuildKBContext`
supplies retrieval to a designer with no search tool — `Flow.loadFeasibility`
renders any URL the user mentions and injects a `<site_feasibility>` block. A
captcha or Cloudflare wall cannot be worked around, so finding it during the
conversation replaces a six-minute build that was never going to succeed. A login
wall is reported as a DIFFERENT answer: that one the owner can fix by storing
credentials, and conflating the two would talk them out of a buildable agent.
Bounded to three sites per session and cached per session (a design turn is a
blocking POST with no SSE, so every probe is spinner time); the cache is
deliberately **not** persisted with the draft, since a conversation resumed days
later should look again and a stale "blocked" is worse than no hint.

**A step that needs the USER — a bank-app push — is a WAIT, not an approval.**
Nothing comes back to the agent: the bank tells the merchant, and the page
changes on its own, so the agent only has to notice. `Manager.WaitFor` polls in
`waitSegment` (20s) slices up to `MaxWaitFor` (15 min) instead of blocking one
long call, and the segmenting is load-bearing three times over: a single call
past the manager's 3-minute HTTP timeout fails at the TRANSPORT and the error
path calls `m.stop()`, destroying the page and its login; `lastUsed` is stamped
when a call STARTS, so a long call lets the helper's own idle reaper close the
context underneath it; and a timed-out wait returns an `error:` result, which
`noteCall` records as a failed call against `maxUnproductiveStreak` (6), so
polling by hand would end the run. An unmet wait therefore returns a NON-error
result that also tells the model not to simply wait again.

**`browser_wait`'s `notify` exists because [CHAT] cannot reach the user in
time.** `[CHAT]` is delivered durably only when a run ENDS, and the scheduler
wires `SendOutput` with **no `OnProgress` at all** — so an agent that stopped at
a payment step to ask for a push approval could not tell anyone until after the
wait it was asking about. On a 03:00 cron run the message reached nobody, ten
minutes late. `Coder.WithNotifier` is deliberately distinct from `WithProgress`
for that reason: progress feeds a live view that a scheduled run has no
subscriber for. The runner wires it to `SendOutput` plus an inbox record, since a
chat message scrolls away and the owner may only look hours later. A surface with
no notifier makes the tool SAY the message went nowhere, because a model told its
message was delivered will not repeat it at the end of the run.

**Not built, deliberately:** cross-run login persistence. A run holds one browser
context for its duration, so login-then-act works within a run; persisting
`StorageState` between runs would create a new credential store at rest (cookies
are bearer tokens) needing its own encryption, invalidation and backup policy,
to buy an optimisation. The cost is stated rather than hidden: logging in every
run is more fragile and likelier to trip 2FA and fraud heuristics. Also not
built: per-click human approval — `internal/approval`'s "park, plain" semantics
finish the run, so by the time an owner approves, the browser context and the
half-filled form are gone; holding a live context across a human wait is the
feature that would make it possible.

### Guardrails

`internal/agentdesigner/guardrails.go`:
- `CheckEthics(code, "")` — blocklist (rm -rf, drop table, bitcoin wallet, etc.). Used on AGENT.md.
- `RunFullGuardrails(code, profile)` / `RunToolGuardrails(filename, code, profile)` — ethics + AST, where
  `profile` is `ProfileAgentTool` or `ProfileSkillScript`. Both ban `eval`, `exec`, `compile`,
  `__import__`, `os.system`, `os.popen`, the `os.exec*`/`os.spawn*` family, `socket.socket`, and any
  `shell=` keyword that is not provably `False`. They differ on one axis: `ProfileAgentTool` (an agent's
  `tools/*.py`) bans `subprocess.*` outright, while `ProfileSkillScript` (a user skill's `scripts/`)
  ALLOWS list-form `subprocess` so a skill can drive an installed CLI tool — and additionally rejects any
  `**` spread into a `subprocess.*` call, since the checker cannot prove `shell` is absent from it.
  The AST check is a best-effort filter, NOT a security boundary (an aliased `import subprocess as sp`
  defeats the receiver-keyed rules); Landlock and the skill-vetter audit are the actual enforcement.
  `skilldesigner.guardrailsForGeneratedFile` routes `.md` files to the doc-ethics profile so a reference
  doc describing a destructive command isn't blocked as though it executed one.

### Per-workspace coder isolation

- `CLAUDE_CONFIG_DIR` → `<data_dir>/claude-homes/<workspaceID>/.claude/`
- `HOME` → `<data_dir>/claude-homes/<workspaceID>/`
- `--setting-sources ""` — suppresses `settings.json` and all `CLAUDE.md` traversal.
- `.credentials.json` copied from operator's `~/.claude/` on every invocation.

**`ANTHROPIC_CONFIG_DIR` does NOT work** — only `CLAUDE_CONFIG_DIR` redirects config.

### Coder filesystem confinement (Landlock)

`internal/sandbox` adds preventive filesystem confinement via Linux Landlock LSM. No external deps, no setuid, no namespaces.

**Mechanism:** `coder.buildCommand()` wraps the real command as `rookery __sandbox-exec <base64-spec>`. The helper applies `landlock.V5.BestEffort().RestrictPaths(...)` then `syscall.Exec`s the real command. Inherited by all children (`claude`→`bash`→`python`).

**Allowed:** RW: per-workspace HOME + agent workdir. RO: system paths, coder binary dir, the `rookery` binary dir (so a confined CLI coder can exec `rookery connector exec`), the workspace's vault root. Denied: SQLite DB, config.yaml, other workspaces' vaults.

`config.SandboxConfig.Enabled` (default true; `ROOKERY_SANDBOX=0` disables). With Landlock unavailable, the sandbox is not applied and nothing physically prevents writes outside the vault — agents/chat run trusted within the user's own vault.

### Database

SQLite via `modernc.org/sqlite` (CGo-free). WAL mode + foreign keys set on open. Migrations in alphabetical order from `migrations/`.

The base schema was consolidated into `migrations/001_initial_schema.up.sql` during the workspace
refactor (the old incremental migrations were collapsed; data was wiped and re-created fresh);
incremental migrations resume from there — `002_coder_api` adds `workspaces.coder_base_url`, and
`003_agent_runs_usage` adds `agent_runs.{prompt,completion,total}_tokens` for the API coder; `005_connectors` adds the self-managed-OAuth tables; `006_connection_extra` adds `service_connections.extra` (JSON); `007_draft_used_connections` adds `agent_drafts.pending_used_connections` (persists build-used connections for auto-bind); `016_agent_run_transcript` adds `agent_runs.{transcript,silent}` (see "Run transcript and the silent flag" below); `017_agent_runs_cost` adds `agent_runs.{cached_tokens,cache_reported,cost_usd,cost_reported}` — a token count is not a price (the same 200k tokens cost different amounts on different models, and a cached prefix bills at a fraction), so "what is this agent costing me?" could not be answered from run history at all. Each figure carries a `_reported` flag rather than using 0 as a sentinel: a provider reporting zero and a provider reporting nothing are opposite findings, and a CLI coder reports neither — rendering its runs as `$0.00` would read as free rather than as unmeasured.

**`db.ListAgents` is name-ordered and must stay that way; the agents PAGE is ordered in the
handler.** The list page shows the newest agent first — the one you just built is the one you came
back to look at — but that ordering lives in `apiListAgents`, because `ListAgents` has five other
callers that want it alphabetical: the chat context builder, the gateway's `/run` listing, the
dashboard, the KB and global search. Moving the `ORDER BY` into the shared query is the tidier-looking
fix and silently re-orders all five, so `TestListAgentsQueryStaysNameOrdered` pins the query
alongside the test for the page — the page's own test passes either way, which is exactly why it
cannot be the only one. `apiAgent.LastRunAt` comes from `db.LastRunTimes`, one aggregate over
`agent_runs` rather than a lookup per row (this is the hottest page in the product), and reads
`started_at` rather than `finished_at`: a run in flight has no `finished_at`, and an agent running
right now has certainly run. It is sent as an explicit `null` and never omitted, for the reason
`flattenRequires` and `plan_ready` record — asserted on raw response bytes, since decoding erases
the distinction.

**`015_orphaned_agent_rows` is a one-time sweep, not a change to the delete path.** Foreign keys
were enforced per-CONNECTION until the DSN-pragma fix (#214, 2026-08-17), so `DELETE FROM agents`
cascaded only when the pool happened to hand it a connection with `foreign_keys` on. On the
reporting install that left **61 of 92 `agent_runs`** orphaned — rendering as blank rows in Home's
recent activity, since `RecentAgentRunsWithNames` `LEFT JOIN`s and `COALESCE`s a missing name to
`""` — plus 13 `agent_skills`, 7 `agent_connections` (bindings granting live credentials to agents
that no longer exist) and 3 `agent_schedules`. Every orphan predates the fix and
`TestDeletingAnAgentNowCascades` pins that the cascade fires today. The migration applies the
policy each table's own foreign key already declares: **CASCADE tables lose the orphan; SET NULL
tables keep the row and lose only the dangling id.** `inbox_messages` is deliberately in the
second group — it carries a denormalized `agent_name` whose schema comment reads "survives agent
delete", it renders correctly, and it is the owner's notification history rather than a projection
of the agent; but its dangling id made Home deep-link to a deleted agent's page. The down
migration is empty and says why. The join stays a `LEFT JOIN` (an inner join would silently HIDE
such runs, trading a visible bug for an invisible one) and the DTO falls back to a legible label.
Tables: `owner` (single row), `workspaces` (replaces `users`; carries `about` + inlined coder
config: `coder_kind`/`coder_bin`/`coder_timeout_s`/`coder_backend_type` + the now-active API-coder
fields `coder_provider`/`coder_model`/`coder_api_key_secret`/`coder_base_url`), `platform_connections`,
`platform_identities`, `agents`, `agent_schedules`, `agent_runs`, `secrets`, `chats`, `reminders`,
`workspace_permissions` (was `user_permissions`), `mcp_servers`, `workspace_settings` (was
`user_settings`), `system_settings` (owner/system-level, not tenant-scoped, no FK),
`audit_logs` (records active `workspace_id`; owner is the implicit actor), `schema_migrations`,
`chat_messages` (FK `chat_id`→`chats`), `skills`, `agent_skills` (keyed by `(agent_id, skill_name)`),
`service_provider_configs`/`service_connections`/`agent_connections` (self-managed-OAuth connectors — all secret columns encrypted under the system key),
`agent_drafts`/`skill_drafts` (one row per workspace; 7-day TTL). Every tenant table keys off
`workspace_id`. There is **no** `coders` table — coder config is inlined on `workspaces`.

### Web UI routes

**The server-rendered template UI has been deleted (big-bang cutover).** There are now exactly
**two** HTTP surfaces: the embedded **React SPA** at `/` and the **`/api/v1` JSON API**. All the old
`/dashboard/*` and `/admin/*` HTML routes, the `TemplateRenderer`/`setupTemplates`/`parseTemplates`
machinery, the `web/templates/` + `web/static/` directories, and their `templates_dir`/`static_dir`
config plus the two environment overrides that fed them are gone. The SPA talks to the JSON API for
everything.

#### Design system (tokens first, not per-page)

**The type scale, radii and colours are remapped in `index.css`'s `@theme inline`, never at call
sites.** Tailwind v4 resolves every `text-*` utility from a `--text-*` token, so raising the scale in
one file grew all ~405 existing `text-xs`/`text-sm` uses at once. Two traps: **each `--text-X` must
be set together with its `--text-X--line-height` partner** (size alone leaves line-height pinned to
the old metric, making text *cramped* rather than more readable), and **raising `body { font-size }`
does not do this** — `text-sm` is an absolute rem value, so the body rule only reaches elements
carrying no `text-*` class. `density.test.ts` fails the build on any `text-[<n>px]` literal, because
a hardcoded pixel size is immune to the token remap and stays small forever.

**`contrast.test.ts` computes WCAG ratios out of the stylesheet itself**, in both themes, for
`--foreground`/`--muted`/`--muted-2` and for `--ok`/`--warn`/`--danger` against `--background`,
`--chrome` **and their own `-soft` fill** (the tightest constraint). This exists because an earlier
review measured `--ok` at 3.68:1 on `--ok-soft` and darkened it — a manual finding that nothing
stopped the next palette edit from undoing. Changing a colour token means running this.

**`button { cursor: pointer }` is restored in an `@layer base` rule.** Tailwind v4's Preflight
dropped it to match the browser, and a `<button>`'s browser default is `cursor: default` — so 54 raw
buttons across the app hovered as if inert. It read worst in the KB pane, where `FileTree`'s rows opt
into `cursor-pointer` explicitly while search results did not, which is how "KB search finds the
pages but they are not clickable" was reported. The clicks always worked; the affordance never did.
Fixed once in base so new buttons inherit it.

**The UI font is vendored at `internal/fonts/InterVariable.woff2` — one copy, two consumers.** It is
its own Go package because `go:embed` cannot reach outside its own directory, and both the Go export
path and the SPA (via the `@fonts` Vite alias) need the same bytes; a second checked-in copy would
drift silently. A CDN `@import` is not an option — Rookery ships as a single binary for offline/LAN
installs. It is declared in **three** places: `index.css` (`--font-sans`), `pages/kb/editor.css`
(explicitly, so a future body-style change cannot silently drop the KB editor back to a system font),
and `internal/export/html.go`, which **base64-inlines it as a `data:` URI** rather than naming it —
`ToPDF` shells out to a headless renderer on the *server*, which will not have Inter installed, so a
named font would silently fall back while still reporting success. DOCX can only *name* the font
(embedding in OOXML is out of scope), which is recorded in `docx.go` as a stated limitation.

**`lib/entityIcons.tsx` is the single icon map**, read by the rail, `PageTitle`, the command
palette's kind labels and the settings nav. Rules: lucide only, `currentColor` always (never coloured
except `text-danger`/`-warn`/`-ok`), `size-4` inline / `size-5` in a page title. The one exception is
`components/brand/ProviderLogo.tsx`, which keeps brand colour — a monochrome Slack mark is harder to
recognise than a coloured one. Before this map, `SettingsPage` held **emoji strings** for its section
nav while every other surface used lucide, which is the whole reason settings looked "coloured and
everything else grey"; a test fails the build if emoji return.

**Brand logos are generated by `scripts/vendor-brand-logos.sh`; never hand-edit
`web/ui/src/assets/logos/`.** A hand-edit is silently lost on the next run, and the run rewrites the
whole manifest, so check `git status` after and revert incidental upstream churn (simple-icons
redraws marks — Threads has already changed once). Four properties worth knowing:

- **`inline_class_styles` runs before the `<style>` strip, and is not optional.** The strip itself is
  required (these files are inlined with `dangerouslySetInnerHTML`), but Illustrator and Inkscape
  export marks as `class="st2"` plus a stylesheet — so stripping it left every classed element at the
  SVG default `fill: black`. That silently shipped **six** broken marks: llama.cpp was a solid black
  square (its background `<rect>` is `.st2`), frankfurter a black blob, Google Ads and Google
  Analytics black silhouettes, Gotify a black blob, and Open Library had lost three stroke paths.
  Every one passed the existing tests. The inliner handles selector LISTS (`.p, .s {…}` — reading
  only the last class leaves the other black) and skips `@media` blocks (frankfurter ships a
  `prefers-color-scheme: dark` rule that would invert it, and the tile is always white).
  `TestBrandLogoAssetsCarryNoDanglingClasses` fails on any surviving `class=`.
- **A mark can pass every structural test and render invisibly.** `ProviderLogo` draws on a WHITE
  tile, so lobehub's `-color` variant for Kimi — a white mark on a transparent field, meant for
  Moonshot's own blue container — showed an empty square with a speck. Prefer the monochrome variant
  when a brand offers one; the tile pins `#18181b` for `currentColor`.
  `TestBrandLogoMarksAreVisibleOnTheTile`
  catches only the TOTAL case, and says so: deciding the partial case needs rendered AREA, which the
  source cannot give you (Hacker News draws its full-canvas background as the 18-byte path
  `m4 4h188v188h-188z`, so any length heuristic ranks a correct mark below a broken one). Partial
  cases are pinned per brand instead.
- **Removing a provider means removing its manifest line too.** Fitbit and Zoom were deleted on
  purpose, but their lines survived and a re-vendor silently recreated both logos.
- **Size is paid on render**, since every logo is inlined. LocalAI's genuine square vector is a
  41-path illustration that alone grew the `ProviderLogo` chunk from 286 KB to 376 KB, so it takes
  the `UPSTREAM_PNG_LARGE` path — the publisher's own 1024px raster downscaled to 128px (~17 KB),
  still 3-4× the rendered tile.

**Buttons**: `default` = primary, `outline` = secondary, `ghost` = tertiary/inline, `destructive` =
removes data. Every *action* button carries a leading icon, with two deliberate carve-outs because
the blanket rule reads worse: dialog footer **pairs** (Cancel/Save) and the `link` variant stay
text-only. A destructive *confirm* keeps its icon — there the icon is the warning.

**`PageContainer`** (`mx-auto w-full max-w-[1600px] px-8 py-6`) is the one page wrapper; it replaced
four independent hardcoded widths that centred content and left ~900px empty on a 1920px display.
`mx-auto` only bites past the cap, so a 1440px viewport is genuinely fluid. It keeps `px`/`py`
separate rather than a `p-*` shorthand **on purpose**: tailwind-merge treats `p` and `px` as
different groups, so `cn("p-8","px-[7%]")` keeps BOTH and lets stylesheet order pick the winner —
the same trap CLAUDE.md records for `ChatScroll`. The KB editor relies on overriding it (`px-[7%]`,
applied to **both** the WYSIWYG container and the raw textarea; changing only one makes switching
modes jump sideways). Forms still cap their own field column (~640px) — a 1500px text input is worse,
not better.

**`PageTitle`** owns only the heading *group* (icon + `<h1>` + optional subtitle), not the whole
header row: pages already have their own search boxes and actions, so scoping it to the part that was
inconsistent made it adoptable at all 16 sites without restructuring them.

**`DialogContent`'s width cap must stay unprefixed, and its grid must stay
`grid-cols-1`** (`dialog.test.tsx` pins both). The base used to end
`sm:max-w-lg`, a different tailwind-merge conflict group from a caller's
`max-w-2xl` — so both survived the merge and the responsive one won at ≥640px,
pinning **every dialog in the app** to 512px regardless of what it asked for.
The same merge ate `max-w-[calc(100%-2rem)]`, so the small-viewport inset now
lives on `w-`, not `max-w-`. Separately, with the implicit `auto` grid track a
grid item's automatic minimum size is content-based (CSS Grid §6.6), so one wide
non-wrapping child — the KB icon picker's category tab strip, ~880px of
max-content, whose own `overflow-x: auto` does **not** zero its min-content
contribution — stretched the track and every sibling with it straight through
the side of the modal. `grid-cols-1` emits `repeat(1, minmax(0, 1fr))`, whose
`0` min sizing function contains it; no `min-w-0` is needed. This is the third
recorded instance of the tailwind-merge group trap (see `ChatScroll` and
`PageContainer` below).

**The side slide-over is `w-[clamp(400px,33vw,720px)]`, set in BOTH `sheet.tsx` and `AppShell`** —
the width used to live in two places and had already drifted (`sm:max-w-sm` vs `sm:max-w-md`). A test
asserts they agree. `AppShell`'s `p-0 gap-0` on the content well stays: panel content owns its inner
padding, and a shell-level `p-4` would double chrome for a full-height embed like the global chat.

**The shell root is `h-screen overflow-hidden` and the KB editor pane is `overscroll-contain`.**
The shell is a fixed-height frame in which every scrolling region is explicit, and both halves are
load-bearing. Without them, a long KB note propagated its scrollable overflow to the initial
containing block (`documentElement.scrollHeight` 2359 against a `clientHeight` of 900), so wheeling
once more with the editor pane already at its bottom chained out to the document and dragged the
icon rail and context pane off the top — measured `documentElement.scrollTop` 0 → 1459, rail `top`
0 → −1459. It read as "scrolling past the note into blank background", because that is the page
behind the shell. `overscroll-contain` stops the chaining; `overflow-hidden` is what makes the
document unscrollable at all (setting `scrollTop` directly still moved the rail otherwise).
**jsdom has no layout engine and no scrolling**, so the vitest suites can only assert the two
declarations are present — `scripts/verify-kb-layout.py` drives a real browser and asserts the
behaviour, and is the only thing that can. Two plausible-sounding theories were disproved by that
harness and are recorded in the spec: the blank band below a short note is NOT inert (clicking it
already places a caret, which is what `min-height: 60vh` is for), and `scrollIntoView` does NOT
chain to ancestors here.

**The KB slash menu measures itself before placing, and re-measures on a `ResizeObserver`.**
`placeMenu` (`pages/kb/SlashMenu.tsx`) is a pure function — caret rect, menu size and viewport in,
`{left, top, maxHeight}` out — precisely because jsdom returns zeros for every rect, so a test
driving the real popup can prove it OPENS but never WHERE it lands. That gap is how the original
shipped with `top = caret.bottom + 4` and no bounds check: with the caret on the last line of a long
note the 442px popup rendered 410px below the fold. The observer is equally load-bearing —
`ReactRenderer` has not laid the list out when `onStart` appends the element, so the first measure
reads height 0, zero fits below any caret, and the placement is wrong exactly when it matters. All
listeners read the LATEST suggestion props, since `@tiptap/suggestion` hands out a fresh
`clientRect` closure per update and they fire outside those calls.

**Owner settings is five separate sections, not one stacked page** (`owner-workspaces`,
`owner-instance-url`, `owner-system`, `owner-backup`, `owner-audit`), under an `OWNER` group in the
settings pane nav; `?section=owner` redirects to the first. Each mounts `OwnerGate` **independently**,
which costs nothing extra: the gate's probe is a react-query on the shared `["admin","overview"]`
key, so five mounts share one request, and one unlock covers all five because **the server owns the
verification stamp** — the component is the affordance, not the protection. A test asserts every
owner slug renders gated, so a missed wrap fails the build rather than quietly exposing an
install-level section.

**Emoji are generated, not curated.** `scripts/gen-emoji.mjs` turns a vendored Unicode data file into
`pages/kb/emojiData.generated.ts` (1906 emoji, 9 standard groups, keyword search) with **zero runtime
dependencies** — emoji-mart was the escape hatch the old curated file itself named, but a ~200 KB
runtime dep with its own styling to theme is a poor trade for data. The generated file is
**committed** so the release build never runs the generator, and `emojiData.test.ts` re-runs it and
compares, so a stale commit fails CI instead of shipping an old set.

**Workspace presets are 36 inline SVGs** (`lib/workspaceIcons.tsx`) — eight renderings of the
Rookery mark in the brand's hues, then 28 gradient-plus-motif tiles, all legible at the 20px the
rail actually renders. `web/api_settings.go`'s `workspaceIcons` validator must list the same slugs,
and `TestWorkspaceIconSlugsMatchTheSPA` parses the TSX to assert it: a preset added only to the SPA
400s on save, one added only to Go has no artwork, and neither failure is visible in either file
alone. **Custom upload is deliberately not built** — it is the one item needing a multipart
endpoint, an `iolimit` cap, MIME sniffing (SVG is an XSS vector), vault storage and a two-shape icon
field.

`DEFAULT_WORKSPACE_ICON` (`"rookery"`) is what an **unset** icon renders — it used to be the
workspace name's initial on a solid square. An **unknown** slug still falls back to that monogram,
and the distinction is deliberate: an unknown value means a workspace configured by a NEWER build,
where rendering the default would silently present it as the user's choice. The 28 motif slugs and
motifs are frozen (a rename orphans every workspace that stored it); the 2026-08 palette pass
re-derived only their gradients onto one ember-compatible recipe, and kept hue SEPARATION on purpose
— telling workspaces apart at 20px is the only job these have, so pulling them all toward orange
would defeat it.

**The Rookery mark lives in `components/brand/RookeryMark.tsx`, drawn inline.** `RookeryMark` strokes
in `currentColor`; `RookeryTile` is the tile form, whose glyph is painted the explicit brand cream
because a tile supplies its own background and an inherited foreground would vanish into the fill
(its gradient `id` is a prop for the same reason `WorkspaceAvatar` derives one — two on screen with
the same id make the second reference the first's gradient); `RookeryLogo` is the mark-plus-wordmark
lockup, whose tight gap is load-bearing, since mark and word read as one logo only while they sit
closer to each other than to anything else. It is a component and not an `<img>` because **an image
cannot inherit `currentColor`** — that is exactly how the documentation site's mark ended up painting
black and disappearing on the dark theme. `public/favicon.svg` stays a separate file (a browser tab
needs a real one) and is the one copy that cannot be generated from the component. Sign-in and
workspace selection carry the mark because they are the only two screens outside the app shell,
where the rail's branding is absent.

**Shell primitives** (`web/ui/src/components/shell/`): every page renders inside `AppShell` —
an icon rail + list panel + a `ContextPane` slot. The context pane is user-resizable —
`usePaneWidth`/`PaneResizeHandle` (`usePaneWidth.tsx`) persist a 200–560px width to `localStorage`
(`sa.paneWidth`; a corrupt or out-of-range stored value falls back to the 256px default rather than
being clamped), draggable via pointer events or fully keyboard-operable (`role="separator"`, arrow
keys step 16px, Home/End jump to the extremes, double-click resets). `ContextPaneHeader`/
`ContextSection` (`ContextPaneParts.tsx`) are the shared title/section primitives all five context
panes (Home, Chats, Connections, KB, Settings) are built from, so heading case/padding/the header's
bottom border don't drift per-page. `ToastProvider`/`ToastHost` (`Toast.tsx`) is the app's toast
system and its one `aria-live="polite"` region — mounted once regardless of whether a toast is
showing, so screen readers don't miss the first announcement; a toast carries an optional action
(e.g. "Undo") and auto-dismisses after 5s. `useDeferredDelete` (`lib/useDeferredDelete.ts`) builds
the inbox's and reminders' delete-with-undo on top of it: clicking delete hides the row immediately
and shows an Undo toast, but the real DELETE call is deferred 5s — it fires only on expiry (or on
`beforeunload`/route-away, which flush every pending delete so none is silently dropped), never on
click; Undo cancels the timer so the call is never made at all. No soft-delete schema needed — the
"delete" is a pending client-side timer, committed or cancelled.

Home's inbox (`pages/home/HomePage.tsx`) groups notifications under calendar-day headers
(Today/Yesterday/`Weekday, D Mon`, bucketed in local time), flags a failed agent run with a "Failed"
status badge, marks unread rows with a left accent bar, and deep-links each card to its source agent;
deleting a message or a reminder goes through the deferred-delete/undo flow above.

**Chat message chrome.** Every `ChatMessageBubble` (`components/chat/Bubbles.tsx`) renders a
`MessageMeta` footer — a `Day HH:MM` timestamp plus a copy-to-clipboard button. The footer is
**always mounted** and revealed purely by opacity (`opacity-0 group-hover:opacity-100
focus-within:opacity-100`); mounting it on hover would insert a node under the cursor and cancel an
in-progress drag-select, so `select-none` is scoped to the footer row and never applied to the
message body. **Designer turns are timestamped too**, so the two chat surfaces read identically: both
`Flow`s stamp `db.ChatMessage.CreatedAt` on every history append, and `web.designHistoryDTO` (the one
mapper behind the agent resume/state and skill resume handlers) emits it as a `created_at`
RFC3339Nano **string** — a `time.Time` DTO field would defeat `omitempty` (a no-op on structs) and
stamp pre-timestamp drafts year 1. Turns appended client-side (the optimistic user bubble, each
assistant reply, the resume message) are stamped in the browser via `nowStamp()`, because the design
endpoints return prose, not a transcript row. `createdAt` stays optional: an old draft's turn simply
omits the field and the footer degrades to the copy button alone. The **timezone**
reaches the footer as CONTEXT (`lib/timezone.tsx`: `TimeZoneProvider` at the app root,
`useTimeZone()` at the leaf) rather than a `useSession()` call inside the bubble — the bubble is
mounted in places with no `QueryClientProvider` above it, where `useQuery` would throw; an undefined
context degrades to browser-local. `formatMessageTime` (`lib/utils.ts`) wraps `Intl` in a try/catch
because `profile.Timezone` is free text (`""`/`"CEST"`/`"UTC+2"` all throw `RangeError`), and a throw
during render would blank the whole conversation. Opening a chat from `ChatsPage` **auto-resumes it
once per open** if it is stopped (the chip is presentational — `handleChatMessage` never checks
`chat.active`) and focuses the composer; the decision is latched in a ref on the FIRST detail load of
the mount, before the active check, so a later manual Stop sticks.

**Copying a message works without a secure context.** `navigator.clipboard` exists ONLY in a secure
context (https, or localhost) — and the normal way to reach a self-hosted install is plain HTTP on
the LAN (`http://<host>:8080`), where it is `undefined` and reading `.writeText` off it throws. So
`MessageMeta.copy()` guards with `navigator.clipboard?.writeText` and falls back to
`document.execCommand("copy")` via an off-screen (NOT `display:none` — a hidden node is unselectable
and copies nothing) textarea, restoring the user's own selection afterwards. When both paths fail the
button shows a "Copy failed" state: the earlier silent no-op is precisely why the broken button went
unnoticed.

**Chat gutters (the 10% column).** On the full-page chat surfaces — `ChatsPage` and both designers via
`DesignerSurface` — the messages and the composer share one column inset 10% on each side
(`ChatScroll className="px-[10%]"` + `<Composer gutter>`). The ~448px slide-over panel opts out
(`ChatWindow compact`). No rule is drawn above the composer: the design is deliberately unframed.
Two traps live here:
- `ChatScroll`'s base padding is `px-4 py-4`, **not** the `p-4` shorthand. tailwind-merge treats `p`
  and `px` as different groups, so `cn("p-4", "px-[10%]")` keeps BOTH classes and leaves the winner to
  the generated stylesheet's ordering — the composer would inset while the bubbles did not. Two `px-*`
  classes are one group, where the last provably wins.
- A page-level composer registers as the docked bottom bar (`components/shell/dockedComposer.tsx`)
  so `AppShell` lifts the floating action buttons above it; otherwise they sit on top of the Send
  button. The 10% gutter alone only clears them above ~1100px viewport width. That context lives in
  its own module purely to break an import cycle (`Composer → AppShell → GlobalChatButton →
  ChatWindow → Composer`). The registration is COUNTED, not a boolean: on a route change the incoming
  composer mounts before the outgoing one unmounts.

**Session `timezone`.** `GET /api/v1/auth/session` carries the active workspace's profile timezone
(`""` when unset or no workspace entered). It lives here rather than on `/api/v1/settings` because
the SPA already loads and caches the session once, while the settings endpoint re-probes the host
filesystem for installed coders on every call.

```
/                        # embedded React SPA (index.html); every unmatched deep path falls through
/*                       #   to the SPA catch-all (client-side routing). 503 if built without `make ui`.
/app, /app/*             # 301 → the same path with /app stripped (legacy; SPA moved from /app to /)
/dashboard/connectors/services/callback/:provider   # GET: OAuth callback — the ONE non-SPA, non-API
                         #   route. Registered standalone (guarded requireOwner → requireActiveWorkspace
                         #   → requireSetupComplete). FROZEN: this exact path is the redirect URI
                         #   registered in external OAuth apps, so it must never change. Finishes with an
                         #   HTTP redirect back to the SPA connections page (/connections?...), not JSON.
```

#### `/api/v1` (JSON API — the SPA's only backend)

The JSON API is the whole application surface (spec §12). The authoritative, exhaustive route
inventory is the `want` table in `web/api_parity_test.go` (`TestAPIParityInventory`) — a merge gate
asserting every planned route is registered via `s.echo.Routes()`; consult it directly rather than
duplicating the full list here. Route groups:

- **auth** — session, login, logout, change-password
- **workspaces + admin** — list/create/enter/leave/delete workspaces, permissions, admin overview/audit/settings
- **agents + design** — CRUD, run + run-progress SSE, per-run transcript detail, schedule, agent-md, skills, connections, and the full conversational design FSM (design/cancel/resume/dismiss/progress/state, edit/start)
- **skills** — CRUD, core-skill read, and the conversational skill-design FSM (design/cancel/resume/dismiss/progress)
- **secrets** — list/create/delete
- **connectors** — chat-platform connections (Telegram/Discord/Slack): list/create/delete/test
- **services** — self-managed-OAuth service connections: list, per-provider creds/connect/apikey, delete
- **chats** — CRUD, messages (**202 + turn_id**, not the reply), turn-progress SSE, resume/stop
- **reminders + inbox** — reminders CRUD + poll; inbox list/poll/read/read-all/delete
- **kb** — tree, note read/write/new/delete/rename, search, raw, resolve, selection assist (AI actions)
- **settings + setup** — profile/workspace/coder/master-password settings, coder test, setup wizard
- **search** — global search

The embedded SPA is served at `/` (see above); `/app` + `/app/*` 301-redirect to their `/app`-stripped
equivalents. Serving/redirect wiring lives in `web/spa.go` (`setupSPARoutes`), not the JSON API group.

**A slice field on a DTO must never marshal to `null`.** A Go nil slice becomes JSON `null`, and a
TypeScript default parameter (`requires = []`) substitutes only for `undefined` — never for `null`.
`flattenRequires` (`web/api_skills.go`) returned `var out []string`, so every core skill declaring no
tooling served `"requires":null` and `requires.length` threw, unmounting the whole route with
"Unexpected Application Error". It reproduced on every built-in skill and on no user skill, because
those happened to declare requirements — which is exactly the shape that makes it look like a
frontend bug. Initialise with `[]T{}` on the server AND normalise with `?? []` at the consumer; a
test asserting on the RAW response bytes is the one that catches it, since decoding into `[]string`
erases the distinction.

### Owner vs. workspace separation

The two-level session (`owner_id` logged in + `active_workspace_id` entered) is unchanged; only the
guard mechanism moved to the JSON API now that the template routes are gone.

- **Owner-scoped** endpoints (`/api/v1/admin/*`, workspace management) are guarded by `requireOwnerAPI`
  (session `owner_id` → `c.Set("owner")`, 401 JSON if absent).
- **Workspace-scoped** endpoints (agents, secrets, connectors, chats, reminders, KB) add
  `requireActiveWorkspaceAPI` (session `active_workspace_id` → `c.Set("workspace")`, 403 `no_workspace` JSON if none)
  + `requireSetupCompleteAPI`. Handlers read `c.Get("workspace").(*db.Workspace)`.
- The template middlewares `requireOwner` / `requireActiveWorkspace` / `requireSetupComplete`
  (redirect variants, in `web/server.go`) still exist but now guard **only** the standalone OAuth
  callback route (the one browser-facing, non-API endpoint that needs workspace context).
- Entering/switching + leaving are JSON-API actions (`POST /api/v1/workspaces/:id/enter` /
  `.../leave`). `verifyWorkspaceMasterPassword` (shared core in `web/handlers_admin.go`) decrypts the
  workspace's `encrypted_master_password` with the system key and compares to the typed one (an access
  gate — the stored form must remain so the scheduler can decrypt for headless cron runs). Re-prompts
  on every switch.

### Per-workspace coder

**`coder_timeout_s = 0` means "follow the server default", and a form that cannot express
zero will destroy that.** The default is **30 minutes** (`config.defaults()` and
`coder.DefaultTimeout`, pinned equal by tests in both packages — two fallbacks reached by
different construction paths must not disagree). The settings form used to initialise its
field to a hardcoded `120`, render a stored `0` as `120`, and post the field on **every**
save — so merely opening coder settings and pressing Save converted a workspace from
following the default to a hard two-minute cap, and the setup wizard, which reuses the same
component, wrote that cap into every workspace ever created. Two minutes is long enough to
look deliberate and short enough to cut an agent build off mid-repair, which is why it went
unnoticed: the symptom is a *failed build*, not a visibly wrong setting. The field is now a
**string** whose empty value means unset, renders the effective default as its placeholder
(`default_timeout_s`, served alongside `timeout_s` so the SPA never has to invent a number),
and is **hidden entirely in the wizard** (`hideTimeout`). Migration 013 clears exactly
`coder_timeout_s = 120` — the fingerprint of the bug, never a value the interface let anyone
choose — and its down migration is deliberately empty.

**A timed-out build is retried once, and only below `coder.RetryTimeoutBelow` (10 min).**
A build is the longest thing the coder does, so under a small timeout it is cut off
mid-repair often enough that one retry converts a routine failure into a success; at the
30-minute default the reasoning inverts — a timeout there means something is genuinely
wrong, and a second 30 minutes occupies the coder and delays the report for nothing. The
retry lives in `agentdesigner.runGeneration`, branches on the typed `coder.ErrTimeout`
(whose message is byte-identical to the old `fmt.Errorf` text, because other sites still
substring-match `"timed out"`), and is **not** attempted when `genCtx` is already cancelled
— `Cancel()` cancels that context, and a cancelled build must stay cancelled rather than
quietly starting again. Nothing between the browser and the coder imposes an earlier
deadline: the build is detached onto `context.Background()` and the server sets no write
timeout, so the configured value is the one that decides.

Each workspace inlines its own coder config on the `workspaces` row (`coder_kind` `local`/`api`,
`coder_bin`, `coder_timeout_s`, `coder_backend_type`, and for `api`:
`coder_provider`/`coder_model`/`coder_api_key_secret`/`coder_base_url`). `coder.ForWorkspace(w, …)`
builds a `*coder.Coder` from it — a **local** CLI coder or the **api** engine — falling back to the
system defaults when unset; `coder.DetectInstalled()` probes PATH **and the
platform's usual install directories** for supported binaries (claude/claude-code,
opencode, codex, gemini, cursor) — see "Coder detection off Linux" below — and
`coder.APIProviders()` returns a curated catalog of **39** named providers (33
hosted, 6 local) in two
tiers. **Hosted** covers the frontier labs (OpenAI, Anthropic, Gemini, xAI,
Mistral, DeepSeek, Moonshot, Z.AI, MiniMax, Cohere), the routers (OpenRouter,
OpenCode Zen/Go, Perplexity, **Vercel AI Gateway**), the enterprise clouds
(**AWS Bedrock**, **Alibaba Cloud/Qwen**, **NVIDIA NIM**) and
the open-weight inference clouds (Groq, Ollama Cloud, Together, Fireworks,
Cerebras, SambaNova, Nebius, DeepInfra, Baseten, Novita, Hyperbolic, Venice,
plus the Hugging Face and GitHub Models
aggregators). **Local** covers self-hosted OpenAI-compatible servers — Ollama,
LM Studio, llama.cpp, vLLM, LocalAI and Jan — which need no API key
(`RequiresKey: false`, enforced as an **iff** against `Group == GroupLocal` by
`TestAPIProviders_KeylessIsLocalTier`, so a hosted provider cannot forget its
key requirement and a local one cannot demand a key it does not need).
`coder.PlanKeySecret` stores `placeholderLocalKey` for that tier, because
`llm.New` rejects an empty key. A "Custom (OpenAI-compatible)" escape hatch
remains **last** in the list (`TestAPIProviders_CustomIsGenericAndLast`).

Base URLs are single-sourced in `internal/llm.DefaultBaseURL(name)`, are not
duplicated in the catalog, and are **always resolved, never templated**:
`llm.New` assigns the value straight into the HTTP client with no validation,
so a `{region}` placeholder would satisfy every other test and then fail at
request time with an opaque DNS error. Bedrock therefore ships `us-east-1` (on
the `bedrock-mantle` endpoint AWS recommends — the one that takes a Bedrock API
key as a plain bearer token, with no SigV4 signing, which is the only reason
Bedrock is a drop-in) and region variation goes through the per-workspace
override. `TestAPIProviders_BaseURLsAreDialable` pins this.

The coder form accepts an inline API key pasted directly into a settings field,
which `coder.PlanKeySecret` transparently stores as an encrypted
`CODER_KEY_<PROVIDER>` secret. The **base-URL override is prefilled** with the
selected provider's default rather than left blank, auto-expands for the local
tier, and shows the effective URL on the collapsed Advanced toggle — the
capability always existed and always persisted, but was undiscoverable behind a
generic placeholder, so a non-default Ollama port could not be configured in
practice. An unmodified prefill still posts an empty `base_url`, so a workspace
keeps following the registry default rather than freezing on today's URL.

**Azure OpenAI and Google Vertex AI are deliberately absent** — see
`docs/superpowers/specs/2026-08-04-llm-provider-expansion-design.md`. Azure uses
an `api-key` header, a deployment name in the path and a mandatory
`api-version` query parameter; Vertex mints short-lived OAuth tokens from a
service account, which `llm.Config.APIKey` (a plain string `llm.New` rejects
when empty) cannot express. Each needs its own provider implementation rather
than a catalog row. The web `coderForWorkspace(id)` and the runner's injected coder
factory (`Runner.WithCoderFactory`, wired in `main.go`) both use `ForWorkspace` — as do the agent
designer, skill creator, and Telegram chat (via the `coderFor(workspaceID)` factory in `main.go`) —
so scheduled + manual runs, generation, and chat all honor the workspace's coder.

**Both kinds are fully implemented.** The `api` kind resolves its provider API key lazily via
`WithSecretsLookup` (a closure in `main.go`/`web.Server.secretsLookup` that decrypts the workspace
master password and reads the named secret, same path the scheduler uses) — so it authenticates on
every call site regardless of whether that path injects secrets via env. Settings/setup save
provider/model/base-url/api-key-secret through `db.UpdateWorkspaceCoder`.

### Natural language reminders

`internal/reminder/timeparser.go` — `ParseNaturalTime(text, now, loc)` parses expressions like `"in 10 minutes"`, `"tomorrow at 3pm"`, `"next Tuesday at noon"`. Both web UI and Telegram use `profile.LoadLocation(db, workspaceID)` so reminders fire in the workspace's timezone.

### Missed runs and the laptop case

Rookery is meant to run all day and is mostly installed on a laptop, so "the host
was off" is the normal case, not the exception. Three guarantees, each with a
distinct mechanism:

- **Overdue work is caught up, once.** Both loops call `tick()` immediately on
  start, and both due predicates are past-or-present (`next_run_at <= now`,
  `remind_at <= now`), so a machine opened after three days delivers every unsent
  reminder and runs every overdue agent within seconds. Missed slots **collapse**:
  `fire` reschedules from `firedAt` via `cron.Next`, so an hourly agent that was
  off for three days runs once, not 72 times. The same policy `internal/backup`
  documents. This does **not** drift the cron phase — the parser is built
  `Minute|Hour|Dom|Month|Dow` with no descriptor support, so every expression is an
  absolute wall-clock grid and `Next(from)` lands on a real slot whatever `from` is.
  A reminder more than 2h late relabels itself "⏰ Delayed reminder", which is the
  clearest evidence the catch-up is intended rather than incidental.
- **A run killed mid-flight is retried exactly once.** `fire` advances
  `next_run_at` *before* the run executes (that ordering is what stops a queued
  schedule double-firing, so it cannot simply be swapped), which means a run that
  dies at 09:02 has already spent its slot and no later tick will ever pick it up —
  the case a laptop hits most, since closing the lid mid-run is likelier than being
  off across a whole slot. `db.ReconcileStaleRuns` therefore returns the interrupted
  **cron** runs alongside the count it already reported, and the scheduler retries
  them before its first poll. **`retryTrigger` ("cron-retry") IS the once-only
  guard** — reconcile reports only `trigger='cron'`, so a retry that is itself
  interrupted is never retried again. Collapsing that value back to `"cron"` looks
  like a tidy-up and creates an agent that retries forever, once per boot, taking
  the server down with it each time. The retry mirrors `ListDueSchedules`' `a.active
  = 1` join: an agent paused or deleted in the meantime stays stopped.
  `finished_at IS NULL` is the discriminator, captured inside reconcile's own
  transaction, because `exit_code=-1` means both "interrupted" and "failed honestly"
  and the UPDATE destroys the only signal that separates them.
- **The catch-up is capped** at `maxConcurrentRuns` (3). Opening a laptop with five
  overdue agents otherwise launches five coder subprocesses at once on the machine
  the user has just opened. Capping delays the backlog, never drops it; the cap is
  scheduler-local, since a manual or chat run is a human waiting and never arrives
  in a herd. Cancellation is checked on **both** sides of the semaphore acquire: a
  bare two-case `select` leaves both cases ready once the last in-flight run frees a
  slot, and `select` then chooses uniformly at random, so roughly half a queued
  backlog starts anyway during shutdown. Measured, not theorised.

### SQLite pragmas belong in the DSN

`busy_timeout` and `foreign_keys` are per-**connection** settings and
`database/sql` is a connection **pool**, so an `Exec("PRAGMA …")` after `Open`
configures whichever single connection the pool happened to hand out and leaves
every other one at its defaults. This was never "the database has foreign keys
on" — it was "one connection does". `journal_mode` is the exception that hid it:
WAL is persisted in the file itself, so it stuck regardless and the arrangement
looked like it worked.

The cost was concrete. WAL permits many readers but exactly one writer, and
SQLite's default for the second writer is to return `SQLITE_BUSY` **immediately**
rather than wait. Rookery writes from several goroutines by design (the scheduler
firing overdue agents, a run recording its result, the connector refresh loop, the
web API) and those call sites generally log the error and carry on — so the
scheduler's `UpdateScheduleRunTimes` could fail, the run would proceed anyway, and
the schedule stayed due for the next poll to run the same agent a second time.
`fire` now treats a failed claim as "skip this run, the next poll will retry":
late beats twice. `TestPragmasApplyToEveryPooledConnection` pins the pool-wide
property, because a pragma set on one connection is invisible in every way except
the intermittent failure it causes.

---

## Backup and restore

One **owner-level** snapshot covers the entire install — the database plus every
workspace's vault — in a single passphrase-encrypted `.rkb` file. Configured in
owner settings (`BackupSection`), scheduled daily/weekly, restorable via CLI or
from the UI.

**The system key is why this design looks the way it does.** `secrets.SystemKey`
encrypts `workspaces.encrypted_master_password`, every `service_connections`
OAuth token, and every `platform_connections` bot token. It used to be derived
from the **hostname** whenever `ROOKERY_SYSTEM_KEY` was unset, so a naive file-copy
backup restored on new hardware produced an install that booted, looked healthy,
and had silently lost every scheduled agent and every connector. Three
consequences, all load-bearing:

- **The key travels inside the encrypted snapshot** (`Manifest.SystemKey`), which
  is what makes cross-machine restore one step. It is also why the envelope needs
  a passphrase — and why the passphrase is the one thing an owner must not lose.
- **`secrets.SystemKey(dataDir, hasWorkspaces)` pins the key to
  `<data_dir>/system.key`.** Resolution order is `ROOKERY_SYSTEM_KEY` → the file →
  derive-and-persist (hostname-derived when the install already has workspaces,
  so an upgrade keeps its exact key; random for a fresh install). `SystemKeyFromEnv`
  survives only as the legacy path the migration test compares against — **every
  call site must use `SystemKey`**, or it will diverge from the restored key with
  no visible symptom.
- **`ApplyPendingRestore` moves the OLD `system.key` into `.pre-restore-<ts>/`
  together with the database and vaults**, and writes the new key only after that
  succeeds. Leaving it behind would make the rollback copy undecryptable the
  instant a restore landed. Only the newest `.pre-restore-*` is kept.

**`session.key` is the system key's sibling, and is deliberately NOT in the
snapshot.** `secrets.SessionKey(dataDir, configured)` resolves the cookie-signing
key by the same order — configured value → `<data_dir>/session.key` → generate and
persist at 0600 — because the fallback it replaced was the literal
`"change-me-in-production-32bytes!!"` compiled into a published binary, so every
install that never set `ROOKERY_SESSION_KEY` signed its sessions with a key anyone
could read out of the repository. Unlike the system key it encrypts nothing at
rest, so losing it costs one sign-in rather than the whole install; leaving it out
of the `.rkb` means a restore onto new hardware does not also transplant live
session cookies. An empty `data_dir` (tests) yields an ephemeral key rather than a
shared constant.

**Restore only ever runs against a dead install.** `serve` calls
`ApplyPendingRestore` at the very top — *before* the database is opened or
migrated — then holds an exclusive `flock` on `<data_dir>/rookery.pid` for
its whole lifetime. The offline CLI takes the same lock and refuses when the
server holds it. The settings button does not swap anything itself: it stages,
writes a `.restore-pending` marker, and shuts the server down, so the swap
happens on the next boot through the identical code path. **The CLI does not
defer** — `cmd/rookery`'s `restore` action calls `StageRestore` *and*
`ApplyPendingRestore` in the same command and prints `restore complete`, so
starting the server afterwards is how you use the restored install, not how the
restore happens. Conflating the two is how someone concludes a finished restore
failed, which is why the documentation now states both paths side by side.
`rookery backup cancel-restore` abandons a staged restore that would otherwise
fire weeks later.

**The local destination is not configurable, and the removal is the fix rather
than a simplification.** Snapshots go to `backup.DefaultLocalDir(dataDir)` =
`<data_dir>/backups`, computed in one place and used by the CLI, the scheduler
and the web API alike. Owner settings used to offer a free-text folder, but the
packaged unit runs with `ProtectSystem=strict` and `ReadWritePaths=<data_dir>`
and the container mounts one volume, so every other path failed at 03:00 with a
permission error rather than at save time with an explanation. `Config`
therefore has no `Local` field: an install that stored one drops it silently,
because `encoding/json` ignores unknown keys — that is the entire migration, and
`TestLoadConfigIgnoresALegacyLocalDirectory` pins it, because the obvious future
"fix" for a dropped field is to add it back. `local_dir` survives on the API as
an **output**, the resolved path the settings page displays. The CLI keeps
`--dir`, since it runs as the operator rather than as the confined unit, and
`restore` must accept a path to a downloaded snapshot (`openSnapshot` tries
`os.Open` before falling back to a name lookup).

**Snapshot contents.** `db/rookery.db` (via `VACUUM INTO` — copying the
live file is torn, the WAL is multi-megabyte) plus `vaults/**`. Excluded:
`claude-homes/` (regenerable; `.credentials.json` is re-copied per invocation),
`config.yaml`, staging/work dirs. The vault walker is a **raw `filepath.WalkDir`,
never `vault.List`** — those hide dotfiles, which would silently drop `.kb/`
(db-export sidecars, `links.json`) from every snapshot.

Details worth knowing before changing this code:

- `readArchive` **drains to the end of the gzip stream** before returning. tar
  stops at its own end-of-archive marker, which sits before the gzip trailer, so
  without the drain the CRC32 is never checked and tail damage goes undetected.
- Snapshot names have one-second granularity, so `freeSnapshotName` probes the
  destination and advances by whole seconds — two runs in the same second
  otherwise resolved to one name and the second silently overwrote the first.
- The envelope is **framed** (1 MiB AES-256-GCM frames, frame index + final flag
  authenticated as AAD) rather than one-shot: that bounds memory and makes
  reordering and truncation detectable. A first frame that fails to authenticate
  is reported as `ErrBadPassphrase` — a wrong passphrase and a corrupted frame 0
  are genuinely indistinguishable.
- `Prune` and both destinations filter on `IsSnapshotName`, so a bucket or folder
  shared with other data never has a foreign file listed, downloaded or deleted.
- `POST /api/v1/backup/restore` is **exempt from the shared 25 MiB `iolimit`
  cap** — a real snapshot exceeds it as soon as a workspace has attachments.
- The eight `/api/v1/backup/*` routes sit on the **owner** group with no
  `requireActiveWorkspace`: backup covers every workspace, so it must be
  configurable before one exists.
- No new dependencies: SigV4 is stdlib HMAC/SHA-256, and the CLI suppresses
  terminal echo per-platform rather than pulling in `golang.org/x/term` —
  `cmd/rookery/echo_unix.go` shells out to `stty`, `echo_windows.go` clears
  `ENABLE_ECHO_INPUT` via `golang.org/x/sys/windows` (already a **direct**
  requirement, so it costs nothing; `syscall` exports `GetConsoleMode` on
  Windows but not its setter, which only the cross-compile step revealed).
  **The Windows half is why the split exists**: there is no `stty` there, so
  the old `LookPath` guard returned a no-op and every `rookery backup` prompt
  printed the passphrase as it was typed — no error, no warning, just the
  characters on screen, while `reference/cli.md` asserted the opposite.
  `ENABLE_LINE_INPUT` is deliberately left set: clearing it too would switch the
  console to character-at-a-time input and `readPassphrase`'s
  `ReadString('\n')` would block forever. Every failure path returns the same
  no-op restore the `stty` implementation returns, so the worst outcome is the
  behaviour it replaces — a visible passphrase, never a lost one, which matters
  because this is the one credential a backup cannot recover.
  Untested on a real Windows host; the cross-compile gate is what checks it.

**Not built** (deliberate): per-workspace restore, incremental/deduplicated
backup, and the Google Drive / Dropbox / GitHub destinations — adding one is a
new `Destination` implementation plus a settings form. GitHub was considered and
rejected: a daily encrypted blob committed to git grows history without bound and
cannot be pruned.

## Known gaps

- **The API engine's exec tools are Linux/macOS only, and nothing says so.**
  `runBash` shells `bash -c` and `runScript` shells `python3`, both hardcoded
  with no per-platform resolution — unlike `coder.DetectInstalled`, which was
  given a `detectHost` precisely because these assumptions break off Linux. On
  Windows a default install has neither: `bash` exists only with WSL or Git Bash
  on `PATH`, and `python3` is normally `python.exe` (the bare name resolves only
  via the Store alias or the launcher). So an agent that writes a helper script
  or reaches for `curl` — which is what a weak model does by default for an HTTP
  login, observed on a real run — fails on Windows and works on macOS. The
  cross-compile gate proves these link, never that they run. Noticed while
  answering "what will that use on macOS and Windows?", which nothing in the
  documentation could answer.
  Worth knowing alongside it: **the browser path IS cross-platform** (Chromium
  runs on all three), so for web work the native `browser_*` tools are the
  portable route and a shelled-out `curl` is not. And `/tmp` is not in the
  sandbox's granted paths — an agent reaching for `/tmp/cookies.txt` is denied
  and has to be steered to `$TMPDIR`, which cost several wasted tool calls on
  that same run.
- **Thin e2e coverage.** CI has two end-to-end gates: the container smoke test (`pr.yml` → `Container smoke test`) starts the real image and asserts `/healthz`, the SPA root and the session endpoint answer, and the package smoke test (`pr.yml` → `Package smoke test`) installs the built deb/rpm and extracts the tar.gz, running `owner bootstrap` + `serve` + `healthcheck` on each. Everything above that — coder subprocess round-trips (real edit → test → approve), agent runs, connector calls — is still exercised manually. Unit tests cover logic boundaries.
- **Codex, Gemini CLI and Cursor are authored-but-unverified.** All five CLI coders are detected (`knownCoders`) and all five now receive a model, but only `claude` and `opencode` have been exercised end-to-end on a real host. The other three backends were authored from their published flags and have never completed a `Coder.Smoke` round trip; closing this needs a host with the binaries installed and accounts behind them. (The *previous* gap here — no Model field for a local coder, which made `CoderModel` unsettable through the UI and actively wiped it on every save, blocking OpenCode out of the box — was fixed in 2026-08: `#coder_local` has a Model input, both save paths persist it, and `selectBackend` passes it to codex and gemini as well as opencode and cursor.)
- **Discord adapter** — implemented (DM-only); live WS round-trip is operator-verified. **Slack adapter** — implemented (DM-only, Socket Mode); live loop operator-verified. Note: Slack's Socket Mode inbound loop does not auto-restart after a *fatal* reconnect failure (reconnect exhaustion) — outbound still works, but inbound DMs stop until the connector is re-saved or the server restarts; a per-adapter supervisor is a future framework enhancement. Mattermost/Matrix adapters — not yet implemented (framework ready: adapter registry + `CredSpec` + render subsystem all support a new platform via `init()` registration alone; Mattermost should be a hand-rolled thin REST+WS client, NOT the heavy official SDK; Matrix E2EE needs `-tags goolm` to stay CGo-free). The connectors UI (SPA `/connections` → Chat apps tab, backed by `/api/v1/connectors`) is `CredSpec`-driven — a new platform's connect card is data, not hand-written markup. **Design stance:** all adapters use an **outbound** connection (bot dials out; zero inbound port) — a deliberate security property for self-hosted/home installs (works behind NAT, home firewall can drop-by-default, no forgeable public endpoint). **Webhook-based platforms** (WhatsApp/Viber/LINE/Teams/Messenger/Google Chat) are deferred OUT of the home-install core; if built, they must be tunnel/relay-first (outbound), never a raw open port. Future outbound-only candidates: Zulip (event-queue long-poll), XMPP. See `docs/superpowers/specs/2026-07-15-multi-platform-chat-adapters-design.md`.
- **Skill editing + import via chat** — `/skill` covers list/create/cancel, but there is no `/skill edit` (the skill designer has no edit mode at all, unlike `agentdesigner.StartEdit`) and no skill import (ZIP / pasted SKILL.md) over chat, which needs per-adapter file-upload handling. The remaining half of the skill parity gap.
- **MCP servers** — wave 1 shipped: HTTP transport, static bearer/header auth, tools
  only. Deliberately deferred, each for a reason recorded in
  `docs/superpowers/specs/2026-08-11-mcp-server-integration-design.md`: **stdio**
  (spawned by the host process, which holds the DB and system key and is unsandboxed —
  strictly more privileged than the coder's own Landlock-confined `bash`, so it needs
  its own sandboxing story rather than a transport switch); **MCP OAuth** (RFC 9728
  protected-resource-metadata discovery, RFC 8414/OIDC auth-server discovery, client
  registration, PKCE, RFC 8707 resource indicators, RFC 9207 `iss` validation — it
  shares nothing with the connector OAuth path, which assumes a hand-registered
  per-provider app; until then a server requiring OAuth gets a named error rather than
  an opaque 401); **resources and prompts** primitives; the **public registry**
  browse picker (`registry.modelcontextprotocol.io` — additive, but a browsable list
  changes the trust story the gating rests on); and `notifications/tools/list_changed`
  push (needs a held subscription stream; TTL-aware polling plus manual sync covers
  this wave). No live third-party server has been exercised end-to-end in CI — the
  tests drive a real in-process MCP server, which proves the protocol path but not any
  particular vendor's conformance.
- **Custom workspace image upload** — the 36 presets are inline SVG on purpose (no endpoint, no
  storage, no MIME validation, crisp at any size). Uploading a custom image is the one requested UI
  item deliberately deferred: it needs a multipart endpoint, a 25 MiB `iolimit` cap, MIME sniffing
  (SVG is an XSS vector needing sanitising or rasterising), a vault storage location with backup
  implications, and relaxing `web/api_settings.go`'s slug validator into a two-shape field
  (`preset:<slug>` vs `upload:<id>`). Bundling it into a visual-polish change would have put a
  security review on that change's critical path.
- **Connector provider configs (non-Google) unverified against live APIs** — google/github/notion verified end-to-end against real accounts; outlook/jira were hand-authored (rendering unit-tested only). Verify each against live docs before relying on it. A dev harness for this lives at `cmd/livecheck` (tracked; run `go run ./cmd/livecheck <provider> <action> '<json-args>'` against real stored tokens).
- **Connector native tools for CLI coders** — CLI coders reach connector actions via the `rookery connector exec` command (loopback bridge), not as native function tools in their own loop; true native parity for MCP-capable coders (claude-code) would be an MCP transport over the same `connectors.Execute` (not built).
- **Build-time connector testing exposes ALL workspace connections** (the agent hasn't declared bindings yet); a real run exposes only the agent's bound connections (`agent_connections`).
- **CLI-chat connector permission is a scoped Bash grant** — a CLI chat coder is otherwise file-only; when connectors are wired it gets `Bash(<bin> connector exec:*)` (only that command). Relies on the coder CLI honoring command-scoped Bash permissions (claude-code does); a coder that doesn't would need a wider grant.
