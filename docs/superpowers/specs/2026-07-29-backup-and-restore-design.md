# Backup and Restore — Design

**Date:** 2026-07-29
**Status:** Approved (approach A)

## Problem

A Simple Agents install holds data that exists nowhere else: the knowledge base
(vaults), agents and their state, skills, secrets, connector tokens, chat
history, reminders. Today there is no way to snapshot it. A dead SSD ends the
install.

The owner must be able to take a single recoverable snapshot of **everything**,
on a schedule, to a remote destination — and restore it after a disaster,
including onto a **different machine**.

## The constraint that shapes everything: the system key

`secrets.SystemKeyFromEnv()` reads `SA_SYSTEM_KEY`, and **when unset derives the
key from the hostname**. Most installs (including the reference one) have it
unset. That key encrypts:

| Data | Key | Survives restore onto a new host? |
|---|---|---|
| `secrets` table | Argon2id(workspace master password, `workspaces.secrets_salt`) | ✅ salt is in the DB; needs only the master password |
| `workspaces.encrypted_master_password` | system key | ❌ — the scheduler needs it for headless cron runs |
| `service_connections` OAuth tokens | system key | ❌ — every connector needs re-auth |
| `platform_connections` bot tokens + `encrypted_config` | system key | ❌ — Telegram/Discord/Slack go dark |

So a naive file-copy backup restored on new hardware produces an install that
boots, looks healthy, and has **silently lost every scheduled agent and every
connector**. That is worse than an obvious failure.

**Decision: the 32-byte system key travels inside the snapshot**, which is why
the snapshot is passphrase-encrypted. The blob already contains every secret
that key protects, so carrying the key adds near-zero marginal exposure while
collapsing all three tiers to "restore and it works".

The passphrase therefore becomes the one thing the owner must not lose. It is
shown once at setup with an explicit warning, and stored encrypted under the
system key so scheduled runs can use it — the same accepted pattern as
`encrypted_master_password`.

### Corollary: pin the system key to a file

Hostname-derivation is a latent footgun independent of backup — renaming the
host destroys every secret. Restore also needs somewhere to *put* the recovered
key, since it cannot set an environment variable for a future process.

`secrets.SystemKey(dataDir)` replaces `SystemKeyFromEnv()`:

1. `SA_SYSTEM_KEY` if set (unchanged, still wins).
2. Else `<data_dir>/system.key` (0600) if present.
3. Else **derive and persist**: if the DB already has `workspaces` rows, derive
   from the hostname exactly as today and write it to `system.key` — this
   preserves every existing install's key byte-for-byte while pinning it against
   future hostname changes. If there are no workspaces (fresh install), generate
   32 random bytes and write those instead.

Restore writes the manifest's key to `<data_dir>/system.key`. Existing installs
are unaffected on upgrade; they simply gain a stable key file.

**`SA_SYSTEM_KEY` outranks the restored key**, which is a trap: an install with
that variable set would ignore the key written by restore and fail to decrypt
everything it just recovered. So restore compares the two, and when
`SA_SYSTEM_KEY` is set and differs from the manifest's key it **refuses**,
telling the operator to unset the variable or set it to the snapshot's key. It
does not silently proceed and it does not silently overwrite the environment's
intent.

## Scope

**In:** one snapshot covering all workspaces; local filesystem and S3-compatible
destinations; daily/weekly schedule with retention; manual snapshot; restore via
offline CLI and via settings; verify.

**Out** (explicitly, so the plan does not drift): per-workspace restore;
incremental or deduplicated backup; Google Drive, Dropbox and GitHub
destinations; backing up `claude-homes/` or `config.yaml`; re-encrypting
existing snapshots when the passphrase changes.

On GitHub specifically — it was considered and rejected as a destination. A
daily encrypted binary blob committed to git grows history without bound and
cannot be pruned. If ever added it must target release assets, not commits.

## Architecture

A new `internal/backup` package owns the whole feature. It depends on `db`,
`secrets`, `buildinfo` and `config`, and nothing depends on it except
`cmd/simple-agents` and `web`.

| File | Responsibility |
|---|---|
| `backup.go` | `Snapshot()` / `Restore()` orchestration, `Options`, errors |
| `manifest.go` | `Manifest` type, checksums, compatibility gate |
| `archive.go` | tar+gzip writer/reader over the snapshot file set |
| `crypto.go` | chunked streaming AEAD envelope |
| `destination.go` | `Destination` interface + registry + snapshot naming |
| `dest_local.go` | filesystem destination |
| `dest_s3.go` | S3-compatible destination |
| `sigv4.go` | minimal AWS SigV4 request signer |
| `settings.go` | owner config load/save via `system_settings` |
| `schedule.go` | the backup scheduler goroutine + next-run computation |
| `restore.go` | staging, the pending-restore marker, `ApplyPendingRestore` |

`internal/config.BackupConfig` (the inert `enabled`/`target`/`dest` struct and
its yaml key) is **deleted** — verified unreferenced outside its own definition.
Leaving a second dead config surface next to the real one is exactly the failure
this project has a rule against.

## Snapshot format

### Contents

```
manifest.json
db/simple-agents.db          ← VACUUM INTO output
vaults/<workspaceID>/...     ← every workspace's full vault tree
```

The vault tree is walked with a raw `filepath.WalkDir` over the vault root and an
explicit exclude list — **not** with `vault.List` or its siblings. Those skip
every dotfile by design, which would silently omit `.kb/` (the db-export
sidecars and `links.json`) from every snapshot, and the omission would only
surface after a restore came back with no link index. The archive walker wants
the literal tree; the KB browser's helpers exist to hide things from humans.

The DB is produced with `VACUUM INTO '<tmp>'` — a single statement that yields a
consistent, checkpointed copy. Copying the live `.db` file is not acceptable:
the reference install carries a 4 MB WAL, so a plain copy is a torn snapshot.
`VACUUM INTO` also folds the WAL in, so no `-wal`/`-shm` files are archived.

Excluded and why:

- `claude-homes/` — 567 MB of coder caches. CLAUDE.md already declares it
  never-backed-up, and `.credentials.json` is re-copied from the operator's
  `~/.claude` on every invocation, so nothing is lost.
- `config.yaml` — install-specific paths, ports and binaries. Its `session_key`
  matters only in that losing it logs browsers out.
- `.restore-staging/`, `.pre-restore-*/`, `*.log`.

Reference sizing: DB 676 KB + vaults 11 MB ≈ **12 MB per snapshot**. This is the
number that makes incremental backup pointless and justifies one whole artifact
per run.

### manifest.json

```json
{
  "format_version": 1,
  "created_at": "2026-07-29T03:00:00Z",
  "app_version": "v0.3.1",
  "app_commit": "abc1234",
  "schema_version": "011_pending_actions",
  "system_key": "<64 hex chars>",
  "workspace_count": 7,
  "total_bytes": 12345678,
  "files": [
    { "path": "db/simple-agents.db", "size": 675840, "sha256": "…" }
  ]
}
```

`schema_version` is the highest applied row in `schema_migrations`. **Restore
refuses when the snapshot's schema version is newer than the running binary
knows about**, with "upgrade to <app_version> first". A half-applied restore is
the failure mode that destroys the data you were protecting, so this gate is
mandatory rather than advisory. An older snapshot is fine — migrations run
forward normally after the swap.

### Encryption envelope

Order is tar → gzip → encrypt. Compressing after encryption would be useless.
gzip is stdlib; no new dependency (the project has no zstd dep and does not need
one for 12 MB of markdown).

```
magic       "SABACKUP"   8 bytes
version     u8 = 1
kdf         u8 = 1 (argon2id)
time        u32 = 3
memory_kib  u32 = 65536
threads     u8  = 4
salt        16 bytes (random per snapshot)
--- then repeating frames ---
frame: [u32 ciphertext_len][nonce 12][ciphertext‖tag]
```

Key = Argon2id(passphrase, salt) with the parameters above — the same constants
`internal/secrets` already uses. Each frame is AES-256-GCM over at most 1 MiB of
plaintext, with **AAD = header bytes ‖ u64 frame index ‖ u8 final-flag**.

Framing rather than one-shot GCM buys three things: bounded memory if a vault
grows to gigabytes of attachments, detection of reordered frames (index in AAD),
and detection of truncation (the final flag — a snapshot cut short by a failed
upload must not decrypt cleanly into a partial archive). Authenticating the
header as AAD makes parameter tampering detectable.

### Naming

`simple-agents-YYYYMMDD-HHMMSS.sab`, UTC. Timestamps in this form sort
lexically, which is what retention relies on.

## Destinations

```go
type Destination interface {
    Name() string
    Put(ctx context.Context, name string, r io.Reader, size int64) error
    Get(ctx context.Context, name string) (io.ReadCloser, error)
    List(ctx context.Context) ([]Entry, error)
    Delete(ctx context.Context, name string) error
}

type Entry struct {
    Name    string
    Size    int64
    ModTime time.Time
}
```

The encrypted archive is staged to a temp file before upload. That yields a
known `size` (S3 needs `Content-Length`), makes a failed upload retryable
without regenerating the snapshot, and keeps both destinations free of
multipart/chunked-upload logic. At 12 MB the temp file costs nothing.

**Local** — a directory. `Put` writes `<name>.tmp` then renames, so a listing
never shows a half-written snapshot.

**S3-compatible** — endpoint, region, bucket, prefix, access key, secret key and
a path-style toggle (MinIO and some R2 setups need path-style; AWS uses
virtual-host). Operations are `PUT`, `GET`, `DELETE` and `GET ?list-type=2`,
each signed with SigV4.

SigV4 is implemented directly against stdlib `crypto/hmac` + `crypto/sha256` —
roughly 150 lines for these four verbs. Pulling in `aws-sdk-go-v2` for four
requests would add a large dependency tree to a project that deliberately keeps
them few, and the connectors layer already declined SigV4 for the same reason.

Adding Drive/Dropbox/GitHub later is a new file implementing `Destination` plus
a settings form — no change to the engine.

## Configuration and scheduling

Owner-level, stored as JSON under the `system_settings` key `backup.config`.
Secret fields are encrypted with `secrets.EncryptWithSystemKey` — never stored
plain.

```json
{
  "enabled": true,
  "destination": "s3",
  "schedule": "daily",
  "hour": 3,
  "weekday": 0,
  "retention": 7,
  "encrypted_passphrase": "…",
  "local": { "dir": "/mnt/backups" },
  "s3": {
    "endpoint": "", "region": "us-east-1", "bucket": "…",
    "prefix": "simple-agents/", "access_key": "…",
    "encrypted_secret_key": "…", "path_style": false
  },
  "last_run_at": "…", "last_status": "ok",
  "last_error": "", "last_size": 12345678, "next_run_at": "…"
}
```

`schedule` is `daily` (default) or `weekly`; `hour` is 0–23 in **server local
time**; `weekday` applies only to weekly. There is no owner-level timezone in
the schema — workspaces have profiles, the owner does not — so server local time
is the honest choice, and the UI labels it as such.

A dedicated goroutine started in `serve` ticks every minute and fires when
`now >= next_run_at`. It is deliberately **not** folded into
`internal/scheduler`, whose `agent_schedules` rows are foreign-keyed to a
workspace; backup is owner-level and has no workspace.

**Missed runs collapse.** If the server was down across several scheduled times,
boot finds `next_run_at` in the past, runs **once**, and reschedules forward. It
never replays every missed slot.

**Retention** keeps the newest N snapshots (default 7) after each successful
upload, and only ever deletes objects matching
`simple-agents-\d{8}-\d{6}\.sab`. A foreign file sharing the bucket or directory
is never touched.

## Restore

Both entry points drive **one engine that only runs when nothing is live**. That
is what makes shipping both paths cost barely more than shipping the CLI alone.

### Liveness interlock

`serve` holds an exclusive `flock` on `<data_dir>/simple-agents.pid` for its
whole lifetime. Restore takes the same lock; if it cannot, it refuses. A flock is
used rather than a PID file comparison because it is released automatically by
the kernel on crash, so a stale file can never wedge recovery.

Only `restore` needs the lock. `list`, `verify` and `now` are safe against a
running server — `verify` is read-only, and `now` reads the live DB through
`VACUUM INTO`, which is concurrency-safe by design.

### Offline CLI

```bash
simple-agents backup restore <file|snapshot-name> [--from-destination]
simple-agents backup verify  <file|snapshot-name>
simple-agents backup list
simple-agents backup now
simple-agents backup cancel-restore
```

The passphrase is read from the terminal (or `--passphrase-stdin` for
automation), never from a flag — flags land in shell history and `ps`.

`restore` then: acquires the lock → decrypts → verifies every checksum against
the manifest → checks the schema-version gate → stages into
`<data_dir>/.restore-staging/` → moves the current `simple-agents.db*` and
`vaults/` into `<data_dir>/.pre-restore-<ts>/` → moves the staged tree into
place → writes `system.key` → removes staging.

Nothing live is touched until decryption and verification have both fully
succeeded, so a wrong passphrase or a corrupt archive fails with the install
untouched. The previous data is moved aside rather than deleted; only the most
recent `.pre-restore-*` is retained, and it is the operator's to remove.

**`system.key` is part of the rolled-back unit.** It moves into
`.pre-restore-<ts>/` alongside the DB and vaults, and the manifest's key is
written only once that move has succeeded. Without this the safety net is an
illusion: the restored key would overwrite the old one, and the pre-restore copy
— whose master passwords and connector tokens were encrypted under the *old*
key — would become permanently undecryptable the instant the restore landed. An
operator who restored the wrong snapshot and tried to roll back would hit
exactly the silent-loss failure this design exists to prevent.

Staging lives **inside `<data_dir>`** deliberately: the swap is then a `rename`
within one filesystem, which is atomic and cannot half-complete. Staging to
`/tmp` would degrade the swap into a cross-device copy that can fail partway
with the live tree already moved aside.

### From settings

The owner picks a snapshot (from the destination, or uploads a `.sab`), types
the passphrase, and confirms by typing `RESTORE`. The server decrypts, verifies,
stages, writes a `.restore-pending` marker, and shuts down.

`backup.ApplyPendingRestore(dataDir)` runs at the very top of `serve` — **before
the DB is opened and before migrations** — sees the marker, performs the same
swap as the CLI, clears the marker, and lets boot continue.

Under systemd or a container the server comes straight back and the restore is
seamless. Started as a bare binary it will not restart itself, so the UI says so
plainly: *"The server will stop to apply the restore. If you started it manually,
start it again — the restore is applied on the next launch."*

**A pending restore is cancellable.** The marker records when it was staged;
`ApplyPendingRestore` logs that timestamp loudly before swapping, and
`simple-agents backup cancel-restore` removes the marker and the staging dir.
Otherwise an abandoned restore lies in wait and fires whenever the server next
starts — possibly weeks later, over data the owner has since changed.

**The upload door is exempt from the shared 25 MiB cap.** `internal/iolimit`
applies one 25 MiB bound at every ingest door, but a legitimate snapshot exceeds
that as soon as a workspace has KB attachments. `POST /api/v1/backup/restore`
streams the uploaded `.sab` straight to a temp file under its own much larger
bound. Left unstated this would be implemented with `ReadCapped` by convention,
breaking restore for precisely the installs with the most to lose.

## Web surface

Owner-scoped, guarded by `requireOwnerAPI` (backup is not workspace-scoped, so
`requireActiveWorkspaceAPI` is deliberately absent):

```
GET    /api/v1/backup/config
PUT    /api/v1/backup/config
POST   /api/v1/backup/run
GET    /api/v1/backup/snapshots
GET    /api/v1/backup/snapshots/:name/download
DELETE /api/v1/backup/snapshots/:name
POST   /api/v1/backup/verify
POST   /api/v1/backup/restore
```

Every route must be added to the `want` table in `web/api_parity_test.go`, which
is a merge gate — a route not listed there fails CI.

The UI is a `BackupSection` in `web/ui/src/pages/settings/OwnerSections.tsx`,
alongside the existing Workspaces / System status / Audit log sections:
destination picker with per-destination fields, passphrase setup, schedule
controls, retention, last-run status, a snapshot list with download/verify/
restore/delete, and a **Back up now** button.

The passphrase is displayed once on creation with an unmissable warning that it
is the only way to recover, and is never echoed back by the API afterwards.
Changing it does not re-encrypt existing snapshots — they remain decryptable
with the passphrase in force when they were written, and the UI states this.

Every backup, restore, config change and delete writes an `audit_logs` row.
`workspace_id` is a nullable FK (`ON DELETE SET NULL`), so owner-level events
leave it empty.

## Error handling

| Failure | Behaviour |
|---|---|
| Destination unreachable | one retry, then record `last_status=error` + message; no infinite retry |
| Snapshot fails mid-run | temp file discarded; previous snapshots untouched; error surfaced in settings, audit log and server log |
| Disk full while staging a restore | fails during staging, before anything live is moved |
| Wrong passphrase | fails at the first frame, before staging |
| Corrupt/truncated archive | AEAD tag or final-flag check fails; restore aborts |
| Checksum mismatch | restore aborts naming the offending file |
| Snapshot schema newer than binary | restore refuses: "upgrade to `<app_version>` first" |
| No passphrase configured but schedule enabled | scheduler refuses to run and surfaces a settings error rather than writing an unencrypted snapshot |

There is no owner-level inbox (the inbox is workspace-scoped), so failures
surface in the settings banner, the audit log and the server log — not as a
notification.

## Testing

Unit tests, no network, following the project's existing style. Per the
live-instance-safety rule every test runs against a temp data dir; none touches
the operator's install.

- **crypto** — round-trip; wrong passphrase; a flipped ciphertext byte; a
  flipped header byte (AAD); a dropped final frame (truncation); reordered
  frames.
- **manifest** — checksum verification; the schema-version gate accepts older
  and refuses newer.
- **archive** — tar/gzip round-trip preserving tree shape; exclusion of
  `claude-homes`/`config.yaml`/staging dirs; and an explicit assertion that
  `.kb/` **is** included, which is the regression the dotfile-skipping helpers
  would otherwise cause.
- **local destination** — put/get/list/delete against a temp dir; `.tmp` never
  appears in listings.
- **S3 destination** — against `httptest`, asserting the SigV4 `Authorization`
  header is present and well-formed, and that path-style vs virtual-host produce
  the correct URLs.
- **sigv4** — against AWS's published deterministic test vectors.
- **retention** — keeps N newest; never deletes a non-matching filename.
- **schedule** — next-run computation for daily and weekly, missed-run collapse,
  and a DST boundary.
- **restore** — staging + `ApplyPendingRestore` on a temp data dir; verifies the
  pre-restore copy exists, that it contains the **old** `system.key`, and that
  the new key is written only afterwards; that a differing `SA_SYSTEM_KEY`
  causes a refusal; and that `cancel-restore` clears both marker and staging.
- **system key** — an existing install with workspaces keeps its hostname-derived
  key and gains a key file; a fresh install gets a random one; `SA_SYSTEM_KEY`
  still wins.
- **api parity** — the eight new routes registered.

End-to-end (snapshot → S3 → restore onto a second machine) is manual, consistent
with the project's known e2e gap.

## Build order

The feature is one coherent unit but should land in phases, each independently
testable and each leaving the tree green:

1. **Key pinning** — `secrets.SystemKey(dataDir)` + migration behaviour. Ships
   alone; fixes the hostname footgun regardless of the rest.
2. **Engine** — crypto envelope, manifest, archive, `Snapshot()`/`Restore()`,
   local destination, CLI subcommands. Fully usable at this point via CLI.
3. **Scheduling** — owner config in `system_settings`, the ticker, retention.
4. **S3 destination** — SigV4 plus the destination.
5. **Web surface** — the eight API routes, parity-test entries, and the settings
   `BackupSection`.

## Changes made during implementation

Recorded here because they diverge from the design as written above, and were
found by building and testing it rather than by reasoning:

- **`readArchive` drains the gzip stream before returning.** tar stops at its own
  end-of-archive marker, which sits *before* the gzip trailer, so the CRC32 was
  never verified and damage to the tail of a snapshot went undetected. Per-file
  SHA-256 still caught damage to file contents; this covers the rest.
- **Snapshot names are collision-checked (`freeSnapshotName`).** Names have
  one-second granularity, so two runs inside the same second — a double-clicked
  "Back up now", or a manual run racing the scheduler — resolved to one name and
  the second silently overwrote the first. Observed in a smoke test.
- **Terminal echo is suppressed with `stty`, not `golang.org/x/term`.** That
  module is only a graph entry in this repo, so using it would have added a real
  dependency, against the no-new-deps constraint.
- **The API derives the binary's schema version from the database**
  (`LatestSchemaVersion`) rather than re-reading the migrations directory: a
  running server has already applied every migration it ships, so the newest
  applied row *is* the binary's version, and no new `Server` field is needed.
- **`useSnapshots` gates on `passphrase_set`, not `enabled`.** An owner who
  configured a destination but left automatic runs off can still use "Back up
  now", and hiding their snapshots would make those backups look lost.

## Accepted costs

- Restore requires a restart. Automatic under systemd/container, manual for a
  bare binary.
- A lost passphrase means an unrecoverable backup. Mitigated by show-once
  warnings and by `verify`, not eliminated.
- The snapshot is a single artifact containing the system key — anyone holding
  both the file and the passphrase holds the whole install. This is inherent to
  one-step recovery and is the reason for the passphrase.
- No point-in-time consistency between the DB and vaults: `VACUUM INTO` and the
  vault walk are not one transaction, so an agent writing a note mid-snapshot
  could land in one and not the other. At daily cadence on a single-owner
  install this is not worth a global write freeze.
