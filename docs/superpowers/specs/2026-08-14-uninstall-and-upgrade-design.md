# `rookery uninstall` and `rookery upgrade`

**Status:** design, 2026-08-14

Rookery can be installed four ways — `install.sh`, `install.ps1`, a `.deb`/`.rpm`,
or an extracted archive — and can be removed by none of them. `install.sh`
already accepts `ROOKERY_VERSION` to pin a tag, so re-running it *is* a
version-pinned upgrade; nothing says so, and nothing removes what it wrote.

## Why subcommands rather than two more shell scripts

The obvious shape is `uninstall.sh` + `upgrade.sh` beside the installers. Three
things argue against it, and the third is decisive:

- **The project's own rule.** `CLAUDE.md` states that configuration lives in Go,
  not in two shell dialects. Uninstall is not a download loop; it decides what a
  package manager owns, what a service manager owns, and what is unrecoverable
  user data. That is exactly the knowledge `internal/onboard` already holds in
  Go and would have to be reimplemented, twice, in `sh` and PowerShell.
- **`install.ps1` cannot be syntax-checked on the development host** — a gap
  `CLAUDE.md` records rather than hides. Two more PowerShell files double an
  already-unverifiable surface.
- **Uninstall must not run unverified.** Deleting the wrong directory is the one
  failure in this project with no recovery path (`system.key` is unrecoverable
  by design). A Go subcommand is testable against a fake filesystem the way
  `internal/onboard`'s package mapping already is; a shell script on a host we
  cannot run is not.

The installers keep their single job — fetch, verify, place, hand off to
`onboard`. Everything after first install is a subcommand.

## `rookery uninstall`

```
rookery uninstall [--purge] [--yes] [--dry-run]
```

Removes, in order, only what it can prove Rookery owns:

1. **The service.** `systemctl --user disable --now rookery.service`, then the
   generated unit at `onboard.SystemdUnitPath(home)`. Linger is **left enabled**
   — it is a user-level setting that may have been on before Rookery and may be
   serving something else. Reported, not changed.
2. **The binary**, but only when this install placed it. See the package guard
   below.
3. **The data directory** — only under `--purge`.

### The package guard is the load-bearing part

`rm /usr/bin/rookery` under a deb or rpm install leaves the package database
claiming a file that is gone: a later `dnf reinstall` or `apt reinstall` is then
the only repair, and nothing explains why. So before removing any binary,
uninstall asks the system who owns it — `rpm -qf` / `dpkg -S` against the
resolved path of the running executable — and when a package owns it, **refuses
and prints the correct command** (`sudo dnf remove rookery`,
`sudo apt remove rookery`). The service and unit are still removed, because the
package never installed those into the user's home.

The same check is why `--dry-run` exists: it prints the exact plan, including
the ownership verdict, without touching anything.

### `--purge` and the sentence that has to be right

`--purge` removes the resolved `cfg.Data.Dir`: the database, every workspace
vault, `system.key`, `session.key`, `claude-homes/` and local backups. It is
**opt-in, never the default**, and without `--yes` it requires the user to type
the data directory's path back — not `y`, the path — because the whole risk is
someone purging a directory they did not realise was the live one.

The confirmation names what cannot be recovered, in these terms: `system.key`
encrypts every stored master password, connector token and bot token, and is not
derivable from anything else, so a copy of the database taken beforehand is
useless without it. This is the same fact the config data-dir warning had to
state, and it is stated the same way for the same reason.

Without `--purge`, uninstall says plainly that the data directory was kept and
where it is, so a reinstall finds it.

## `rookery upgrade`

```
rookery upgrade [--version vX.Y.Z] [--check] [--yes]
```

Resolves the target (`--version`, else the latest non-draft release), compares
it with the running `version`, downloads the goreleaser archive for the detected
platform, verifies it against the release's `checksums.txt`, and replaces the
binary **atomically** — write to a temporary file beside the target, `chmod`,
then rename — so an interrupted upgrade can never leave a half-written
executable on `PATH`.

Three behaviours worth pinning:

- **It refuses under a package manager**, for the same reason and by the same
  check as uninstall, naming `dnf upgrade` / `apt upgrade` instead.
- **It restarts the service only if it was running**, and reports the version
  the restarted service reports — not the version it intended to install. An
  upgrade that silently left the old process serving is the failure mode worth
  spending a health check on.
- **`--check` exits non-zero when an upgrade is available**, so it is usable
  from a cron line without parsing output.

Downgrades are allowed with an explicit `--version` and a warning: migrations
are forward-only and a database opened by a newer build may carry schema an
older binary does not understand. The warning says exactly that.

## Shared plumbing

Release resolution, platform detection and checksum verification exist three
times once this lands (`install.sh`, `install.ps1`, `upgrade`). The Go copy
becomes the reference implementation in a new `internal/release` package —
`Resolve`, `ArchiveName`, `Verify` — and `packaging/scripts_test.go` continues
to pin that the two shell installers build the same archive name, since they
cannot import it.

`internal/onboard` gains the package-ownership probe (`OwnedByPackage`, with an
injectable command runner for the same reason its `LookPath` is injectable: the
host that shipped the wrong rpm dependency is one we cannot run).

## Not built

- **Windows service registration** stays deferred, so `uninstall` on Windows
  removes the binary and reports that there was no service to remove.
- **A self-updating background check.** An install that phones home on a timer
  is a different product decision from a command the operator runs.
- **Rollback of a failed upgrade.** The atomic rename makes the failure mode
  "old binary still in place", which is the useful half of rollback; keeping the
  previous binary around to restore is inventory for a case the rename already
  covers.
