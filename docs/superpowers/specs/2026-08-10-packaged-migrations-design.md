# Embedded migrations: every native artifact can open its own database

**Date:** 2026-08-10
**Status:** Approved, ready for implementation

## The bug

Installing the RPM and running the documented first command fails:

```
$ rookery owner bootstrap -u owner -p '<pw>'
open db: read migrations dir: open migrations: no such file or directory
```

`cmd/rookery/main.go`'s `resolveDir("migrations")` probes two locations: beside
the executable (`/usr/bin/migrations`), then the bare relative path
(`./migrations`, i.e. the process CWD). The RPM and DEB install exactly three
files — `/usr/bin/rookery`, `/usr/share/rookery/rookery.service`,
`/usr/share/doc/rookery/README.md`. No SQL ships, so both probes miss and
`db.Open` → `migrate` → `os.ReadDir` fails.

The reported RPM failure is one symptom of a wider defect:

- **All six tar.gz/zip archives are broken too.** `.goreleaser.yaml`'s
  `archives.files` lists only `packaging/README.md` and the systemd unit.
- **The packaged service fails identically**, not just the CLI. The unit sets
  `ExecStart=/usr/bin/rookery serve` with no `WorkingDirectory=`, so a systemd
  *user* unit runs with CWD `$HOME` and the fallback resolves to `~/migrations`.
- **Only the container works**, because `Dockerfile:74` does
  `COPY migrations /usr/bin/migrations` — a workaround for this exact probe,
  which CLAUDE.md currently documents as deliberate.

Native binaries are the project's stated primary artifact. None of them has ever
been able to open its own database.

### Why it survived

Nothing in CI has ever installed a package and run it. This is the same shape as
the defect CLAUDE.md already records about cross-compilation — *"the guard that
keeps `GOOS=windows` compiling; it was broken for the repo's entire history
precisely because nothing ever built it."* The `Container smoke test` job covers
the one artifact that happened to work, and it is the reason that artifact works.

### Scope: this is one bug, not the first of several

Verified during design. `resolveDir` has exactly four call sites and all four
pass `"migrations"`. Every other runtime asset is already embedded: the SPA
(`web/ui/embed.go`), core skills (`internal/skilllibrary`), connector YAML
(`internal/connectors/registry.go`), the Inter font (`internal/fonts`).
`config.Load` swallows `os.IsNotExist` and falls back to defaults, so no config
file needs to ship either. The remaining `os.Executable()` calls locate the
**rookery binary itself** for sandbox re-exec and `connector exec`; that path
always exists. Deleting `resolveDir` closes the class.

## Approach

**Embed the SQL into the binary.** Considered and rejected:

- *Ship `migrations/` in the packages and archives.* A tar.gz user extracts and
  runs from an arbitrary directory, so any disk-relative scheme has to get lucky
  about CWD. Worse, satisfying the exe-relative probe under `bindir: /usr/bin`
  would require installing SQL to `/usr/bin/migrations`, which is wrong per FHS,
  while the correct `/usr/share/rookery/migrations` is precisely where
  `resolveDir` does not look.
- *Embed as default with an on-disk override.* Leaves the container reading disk
  while native binaries read the embed — two paths that drift silently. That
  divergence is what produced this bug.

Embedding is also strictly more portable than what it replaces. `embed.FS` is
resolved at compile time into the binary's data section, and its virtual paths
are always slash-separated rather than `filepath`-dependent — so the exe-relative
probe (on Windows, `C:\Program Files\Rookery\migrations`) and the
`filepath.Join(dir, name)` read are deleted rather than ported. And because
`//go:embed` with no matches is a **compile** error, "no migrations found" moves
from a first-run runtime failure to a build failure caught by the existing
six-pair cross-compile job.

## Design

### 1. The embed package

New `migrations/embed.go`: package `migrations`, `//go:embed *.sql`, exporting
`var FS embed.FS`. The directory stays where it is, so `.goreleaser.yaml`, the
test tree layout and git history are undisturbed.

Embed `*.sql`, **not** `*.up.sql`. The `.down.sql` files are never executed
today, but the narrower pattern would silently drop them if a down-runner is ever
wired. Cost is roughly 2 KB.

### 2. `db.Open` signature

```go
func Open(path string) (*DB, error)
```

Migrations always run, from the embedded FS. This deliberately inverts today's
`""` sentinel, which meant *skip migrations entirely* and is relied on only by
`cmd/livecheck/main.go` — a dev harness opening a real data dir, where running
idempotent migrations is correct anyway.

`migrate` switches `os.ReadDir`/`os.ReadFile` to `fs.ReadDir`/`fs.ReadFile`.
Both yield the same sorted `.up.sql` names, so `schema_migrations` rows already
recorded on existing installs still match and nothing re-applies. Upgrades are
unaffected.

No `OpenWithMigrations(path, fs.FS)` escape hatch is added. Nothing needs one,
and adding it would recreate the two-path drift this change exists to remove.

### 3. Call-site cleanup

- Delete `resolveDir` and its three `cmd/rookery/main.go` uses (lines 120, 781,
  808). Migrations was its only consumer once the template/static dirs were
  removed.
- Delete `Dockerfile:74`'s `COPY migrations /usr/bin/migrations`.
- Point `cmd/rookery/backup_cmd.go`'s `binarySchemaVersion()` at the embedded FS.
  It must still return `011_pending_actions.up.sql`, so existing `.rkb` snapshot
  schema-version comparisons keep working; assert this rather than assume it.

### 4. Tests

The ~29 `db.Open(path, "../../migrations")` call sites drop their second
argument. Two helpers that exist only to locate a directory the binary now
carries are deleted outright:

- `internal/coder/searchkey_wiring_test.go`'s `findWiringMigrations`
- `internal/secrets`'s `findMigrations` (declared in `secrets_test.go`, used by
  `service_test.go`)

This is a net simplification, not churn absorbed for the fix's sake.

A unit test asserting the embed holds the expected file count does not prove the
artifact works, so it is not the acceptance criterion — see Verification.

### 5. CI package smoke test

New `pr.yml` job, sibling to `Container smoke test`, on `ubuntu-latest`:

1. goreleaser snapshot build.
2. **RPM** — install in a `fedora:latest` container and run
   `rookery owner bootstrap` from `/`. This is the reported failure; `dpkg -i` on
   Ubuntu would not reproduce it.
3. **DEB** — `sudo dpkg -i`, same bootstrap from `/`.
4. **tar.gz** — extract to a scratch dir, `cd` somewhere unrelated, run from
   there. This is the case the exe-relative probe used to paper over.
5. For each: `serve`, then assert `/healthz`, the SPA root and the session
   endpoint answer.

**Stated limitation:** darwin and windows remain compile-verified only, as today.
A three-OS matrix was considered and declined on CI cost (macOS bills at 10×,
Windows at 2×). The embed makes those targets structurally correct — there is no
longer a filesystem layout for them to get wrong — but nothing executes them.
Revisit when installers ship, which CLAUDE.md defers until the repository is
public.

### 6. Docs

CLAUDE.md's Container section currently states that `migrations/` is copied
beside the binary "because `resolveDir()` looks exe-relative before
CWD-relative". That becomes false and must be rewritten to record the embed and
why. `packaging/README.md` needs no change. The systemd unit needs no
`WorkingDirectory=` once CWD stops mattering.

## Verification

The acceptance test is the artifact, not the unit test. Before claiming done:

1. Build a goreleaser snapshot.
2. Install the RPM on the Fedora host and run the exact reported command,
   `rookery owner bootstrap -u owner -p '<pw>'`.
3. `rookery serve`, then `curl /healthz`.
4. Extract one tar.gz to a scratch directory and run it from a different CWD.

`make ci` must pass, and the new package job must pass in the PR gate.
