# Backup destination and documentation — remove a folder field the service cannot honour

**Date:** 2026-08-11
**Status:** approved

## Problem

**1. The backup folder field offers something the service physically cannot do.**
Owner settings renders a free-text "Backup folder" input (placeholder
`/mnt/backups`) that is stored as `backup.config`'s `local.dir` and handed
straight to `NewLocalDestination`. But the packaged unit
(`packaging/systemd/rookery.service`) sets:

```
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%h/.rookery
```

and `internal/onboard/service.go` generates the same unit against the running
binary. Under `ProtectSystem=strict` the entire filesystem is read-only except
the listed paths, so the service can write **only inside its data directory**.
The container is the same story from a different direction: `/data` is the one
volume. Every other path a user can type into that field fails at run time as an
opaque `create backup dir: permission denied`, at 03:00, on a schedule nobody is
watching.

The CLI already defaults to `<data_dir>/backups` (`--dir`, `backup_cmd.go`), so
a data-dir-relative destination is the existing convention. The UI is the odd
one out, and it is the surface that most users configure backups from.

**2. The documentation does not tell anyone to do the one thing that matters.**
`docs/concepts/backup-and-restore.md` explains what a snapshot is and why a file
copy loses every credential — both correct and load-bearing — but it is filed
under Concepts, reads as an explainer, and never says: turn automatic backups
on, download each snapshot, keep the copy somewhere other than this machine, and
here is exactly how to recover onto a new install. Backup is the only feature
where not reading the page costs the user everything they have.

## Scope

**In:** removing the local-folder configuration from the UI and the stored
config, a one-shot in-app warning when backups are not configured, and rewriting
and relocating the documentation page.

**Out:** relaxing the systemd unit (the hardening is correct — the field is the
error), a folder picker of any shape, new backup destinations, per-workspace
backup.

**Stated assumption:** no shipped install has a working custom folder to
protect. The project is pre-1.0 with no public release, and the hardened unit
means most such configurations were already failing. This is what makes the
silent strip below acceptable.

## Design

### 1. The local destination is always `<data_dir>/backups`

`internal/backup/settings.go`:

- Delete the `LocalConfig` type and the `Config.Local` field.
- Add `DefaultLocalDir(dataDir string) string` returning
  `filepath.Join(dataDir, "backups")` — the single source for that path.
- `Validate()`'s `DestLocal` case loses its directory check: a local
  destination is now always valid.
- `BuildDestination` takes the data directory:
  `BuildDestination(dataDir string, systemKey []byte) (Destination, error)`.
  Both callers already hold one — `Scheduler` is constructed with
  `cfg.Data.Dir` (`schedule.go:118`) and the web server reads `s.cfg.Data.Dir`
  (`web/api_backup.go:168`).

**The migration is the absence of one.** `encoding/json` ignores unknown keys,
so an existing `backup.config` row carrying `"local":{"dir":"/mnt/backups"}`
unmarshals cleanly into the new `Config` and the value is dropped. No migration
code, no startup pass, no log line. Snapshots already written to the old path
remain on disk; they simply stop appearing in the list. A test pins this
contract, because the obvious future "fix" for a dropped field is to add it
back.

`LocalDestination` and `NewLocalDestination` are unchanged, and
`cmd/rookery/backup_cmd.go` keeps its `--dir` flag. The CLI runs as the operator
in their own shell, not as the confined unit, so `rookery backup now --dir
/media/usb` genuinely works and is the manual off-host path; `restore
<file|snapshot-name>` must accept an arbitrary path or recovery from a
downloaded snapshot is impossible. `localDestFor` stops open-coding the join and
calls `backup.DefaultLocalDir`.

### 2. API: `local_dir` becomes an output, not an input

`web/api_backup.go`:

- The save request body drops `local.dir`. A client that still sends it is not
  rejected; the field is simply not read.
- The config response keeps `local_dir`, with a changed meaning: it is now
  **the resolved absolute path, always populated**, rather than a configured
  value that may be empty. It exists so the UI can state where snapshots land.

No route changes, so `TestAPIParityInventory` is untouched.

### 3. UI: the input becomes a statement

`web/ui/src/pages/settings/BackupSection.tsx` — where the folder input was,
render the resolved `local_dir` as static text with the reason a copy belongs
elsewhere:

```
Local folder
  Snapshots are written to
    /home/rookie/.rookery/backups
  A backup on the same disk as the install is not a backup.
  Download each one and keep it somewhere else.
```

`FormState.localDir` and its `set` call go away. A short line by the snapshot
list points at the existing Download button.

### 4. A one-shot warning when backups are not configured

A dismissible banner at the top of the **owner-workspaces** section — the
de-facto owner landing, since `?section=owner` redirects there:

```
⚠ Backups are not enabled
   This install has no snapshot of its database or knowledge bases.
   [ Set up backups ]      [ Dismiss ]
```

- **Data source:** the existing `useBackupConfig()` hook from `@/lib/backup`.
  Sharing the query key means the banner and `BackupSection` make one request
  between them, and owner-workspaces is already owner-gated so there is no 401
  path to handle.
- **Shown when** `passphrase_set` is false or `enabled` is false.
- **Dismissal is permanent.** A `localStorage` flag (`sa.backupWarningDismissed`,
  matching the existing `sa.paneWidth` convention) is set on dismiss and checked
  forever after. It is never cleared — not when backups are enabled, and not if
  they are later disabled again. An owner who has said "not now" once has
  answered; asking again is nagging, and a warning that returns after being
  dismissed teaches people to ignore banners.
- "Set up backups" navigates to the backup section.

### 5. Documentation

**Move.** `src/content/docs/docs/concepts/backup-and-restore.md` →
`src/content/docs/docs/operations/backup-and-restore.md`, second in the
Operations group:

```
Operations
  Configuration
  Backup and restore
  Health and troubleshooting
```

The sidebar entry moves in `astro.config.mjs` (out of Concepts, into
Operations); the `data-icon: backup` attribute travels with it.

**Redirect.** The old URL is public, so `astro.config.mjs` gains a redirect from
`/docs/concepts/backup-and-restore` to `/docs/operations/backup-and-restore/`.

**Inbound links** — four, all updated to the new path:

| File | Line |
|---|---|
| `docs/concepts/knowledge-base.md` | 121 |
| `docs/reference/cli.md` | 94 |
| `docs/installation/windows.md` | 69 |
| `docs/reference/api.md` | 62 |

`reference/api.md` lists the backup routes as a path table only — it does not
document the config payload, so `local_dir`'s change of meaning needs no entry
there. The link on line 62 is the whole edit.

**Rewrite as a runbook**, keeping the existing "why a plain file copy is not
enough" explainer intact — it is the reason the whole feature exists:

1. **What a snapshot contains** — the database plus every workspace's knowledge
   base, in one passphrase-encrypted file; and why copying the data folder to
   new hardware yields an install that starts, looks healthy, and has silently
   lost every scheduled agent and every connection.
2. **Turn on automatic backups** — owner settings → Backup: set a passphrase,
   choose daily or weekly and an hour, set how many to keep. Missed runs
   collapse rather than piling up.
3. **Where snapshots go** — `<data_dir>/backups`. Not configurable, and the
   page says why: the service unit permits writes only under the data
   directory, so a path elsewhere would fail at run time rather than at save
   time. S3 or any S3-compatible bucket is the off-host destination.
4. **Keep a copy off this machine** — download each snapshot from owner
   settings and store it somewhere that is not this disk. A backup that dies
   with the machine it protects is not a backup. This is the section the whole
   page exists for.
5. **Restore onto a new machine** — the full CLI sequence, written against what
   `cmd/rookery/backup_cmd.go`'s `restore` action actually does:

   ```bash
   # 1. Install Rookery. Same version as the snapshot, or newer — a snapshot
   #    from a newer build is refused, naming the version to upgrade to.
   # 2. Do not start the server. There is no owner to bootstrap and no
   #    database to create: the snapshot brings both.
   rookery backup restore ~/Downloads/rookery-2026-08-11T03-00-00.rkb
   # Passphrase:  (prompted, echo off; --passphrase-stdin to script it)
   # restore complete; the previous data is in .pre-restore-* under the data dir
   # 3. Start the server and sign in with the owner password from the old install.
   ```

   Four points the page must get right, each verified against the code rather
   than inferred:

   - **The CLI restore applies immediately.** It calls `StageRestore` and then
     `ApplyPendingRestore` in the same command. Starting the server afterwards
     is how you *use* the restored install, not how the restore happens. This
     differs from the UI restore (`POST /api/v1/backup/restore`), which stages,
     writes a `.restore-pending` marker and shuts the server down so the swap
     happens on the next boot — the page should say which is which, or someone
     who used the button will think it failed.
   - **No prior install state is required.** The restore action never opens the
     database and never calls `systemKeyFor`, so a fresh data directory with no
     `owner bootstrap` is exactly the expected starting point.
   - **The argument may be a file path or a snapshot name.** `openSnapshot`
     tries `os.Open(arg)` first and falls back to looking the name up in the
     local backup directory — so a snapshot downloaded from the UI restores by
     path with no copying into place.
   - **The server must be stopped.** `AcquireLock` takes the same flock the
     running server holds for its lifetime, and the command refuses rather than
     racing it.

   Worth a note on the page: if `ROOKERY_SYSTEM_KEY` is set to a value other
   than the one inside the snapshot, the restore is refused with an explanatory
   error — `docs/operations/configuration.md` already documents this, so the
   two pages should agree.
6. **The passphrase is the one thing you cannot lose** — it is what the system
   key travels inside.

**Also updated:** `docs/operations/configuration.md`'s data-directory listing,
whose `backups/  local backups, if configured` line (line 37) loses its
conditional — the folder is now where local snapshots always go — and
`CLAUDE.md`'s Backup and restore section (the
local destination is fixed at `<data_dir>/backups`, the UI has no folder field,
and the systemd hardening is why). The `docs-sync` skill and
`make docs-sync-check` run before the PR. `README.md` has no backup mention
today and needs none.

## Testing

**Go unit tests**

- `Validate()` accepts an enabled local config that carries no directory.
- `BuildDestination(dataDir, key)` resolves a local destination to
  `<data_dir>/backups`.
- `DefaultLocalDir` and `localDestFor`'s no-flag path agree.
- A stored config JSON carrying a legacy `"local":{"dir":...}` parses without
  error and the value is not honoured — the silent-strip contract.
- API: a save payload with no directory round-trips; the config response's
  `local_dir` is the resolved absolute path.

**Frontend (vitest)**

- `BackupSection` renders the resolved path and no folder input.
- The banner shows when the config reports unconfigured, and hides when enabled.
- Dismissing sets the `localStorage` flag and the banner stays hidden on
  remount — including when the config subsequently reports backups disabled.

**Website** — `npm run build` in `~/rookery-web`, and a check that no `src/`
reference to the old slug survives. Starlight fails the build on a stale
`slug:` in the sidebar, so the file move half-checks itself.

**Implementation note:** this work happens in a git worktree, so
`make docs-sync-check` must be run with `ROOKERY_WEB_DIR` pointing at the
website checkout. Without it the resolver falls through to a sibling of the
worktree, finds nothing, and **skips the website assertions silently** — which
reads as a pass.

## Rejected alternatives

**Relax the systemd unit.** `ProtectSystem=strict` with a narrow
`ReadWritePaths` is a correct property of a service that runs LLM-driven agents;
widening it so a settings field can work inverts the trade.

**Keep the field, validate writability on save.** Turns a run-time failure into
a save-time error, which is better, but leaves a configuration axis that fails
for nearly every value on the two supported install shapes, and the useful
answer to the error is still "use the default".

**A subfolder relative to the data directory.** Works, but buys nothing: one
install has one backup destination, and naming it `snapshots` instead of
`backups` is not a feature.

**Honour an already-configured directory as a legacy override.** Requires a
read-only UI affordance, a switch-to-default action, and a second code path
through `BuildDestination`, all to protect installs the stated assumption says
do not exist.
