# Windows Install Lock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Windows Rookery server that dies in any way no longer prevents the next `serve` from starting.

**Architecture:** Replace the existence-of-file lock in `internal/backup/lock_windows.go` with a `LockFileEx` byte-range lock on a file that is held open, which Windows releases when the process dies (parity with `flock` on Unix). A cross-platform test kills a lock-holding child process to prove it. A new `windows-latest` PR job proves it end to end against the real binary.

**Tech Stack:** Go, `golang.org/x/sys/windows` v0.49.0 (already a direct dependency), GitHub Actions `windows-latest`, PowerShell.

**Spec:** `docs/superpowers/specs/2026-11-23-windows-install-lock-design.md`

## Global Constraints

- No new module dependencies.
- The lock file path stays `<data_dir>/rookery.pid` (`LockPath`). It still records the pid for human triage.
- `Release` does not delete the file (Unix parity).
- `errors.Is(err, backup.ErrServerRunning)` stays true for a held lock on both platforms. `backup restore` keeps its wording.
- Run Go with `GOTOOLCHAIN=auto` (host Go is older than `go.mod`).
- Fixtures, tests and docs must not contain any real host, user or install details.

## Review Focus

1. **A leftover `rookery.pid` from an old build** (exists, nobody holds it) must be acquired, not refused. This is the upgrade path for every install already broken. Pinned in Task 1 (`TestLockAcquiresOverLeftoverFile`).
2. **A holder killed hard** (TerminateProcess / SIGKILL, no deferred code runs) must free the lock. Pinned in Task 1 (`TestLockFreedWhenHolderIsKilled`).
3. **A live second server** must still be refused, so a `backup restore` cannot run under a live one. Pinned by the existing `TestLockRefusesSecondHolder`, and by Task 3's concurrent-start step on Windows.
4. **Reading the pid file while it is locked**: the lock range must not cover the content, or `Get-Content rookery.pid` (triage) fails with a sharing violation. Pinned in Task 1 (`TestLockFileReadableWhileHeld`).
5. **The error a user sees from `serve`** must name the cause (another server, the lock file, the pid), not "stop it before restoring". Pinned in Task 2.

---

### Task 1: OS-released lock on Windows, proven by killing a holder

**Files:**
- Modify: `internal/backup/lock_windows.go`
- Create: `internal/backup/lock_test.go` (cross-platform: no build tag)
- Modify: `internal/backup/restore_test.go`: move `TestLockRefusesSecondHolder` into `lock_test.go` unchanged.

**Interfaces:**
- Produces (unchanged signatures): `AcquireLock(dataDir string) (*Lock, error)`, `(*Lock).Release() error`, `LockPath(dataDir string) string`, `ErrServerRunning`.
- Produces: `ReadLockPID(dataDir string) (int, error)` in a new untagged `internal/backup/lockpid.go`. It parses the first line of `LockPath` as an int, for Task 2's message.

- [ ] **Step 1: Write the failing tests** in `lock_test.go`:
  - A `TestMain` helper mode: when env `ROOKERY_LOCK_HELPER_DIR` is set, the test binary calls `AcquireLock(dir)`, prints `locked\n` to stdout (or `refused: <err>\n` and exits 3), then blocks reading stdin until EOF. Otherwise it runs `m.Run()`.
  - `TestLockFreedWhenHolderIsKilled`: start `os.Args[0]` with the helper env (stdin pipe kept open). Wait for the line `locked`. Assert `AcquireLock(dir)` returns `ErrServerRunning`. Call `cmd.Process.Kill()` and `cmd.Wait()`. Assert `AcquireLock(dir)` now succeeds, then `Release`.
  - `TestLockAcquiresOverLeftoverFile`: `os.WriteFile(LockPath(dir), []byte("99999\n"), 0o600)`, then assert `AcquireLock(dir)` succeeds and `ReadLockPID(dir) == os.Getpid()`.
  - `TestLockFileReadableWhileHeld`: after acquiring, `os.ReadFile(LockPath(dir))` succeeds and parses to `os.Getpid()`.
  - `TestLockReleaseKeepsFile`: after `Release`, the file still exists.

- [ ] **Step 2: Verify they fail.** Run on Linux: `GOTOOLCHAIN=auto go test ./internal/backup/ -run 'Lock' -count=1`. Expected: compile failure on `ReadLockPID`; after a stub, Linux passes. Windows failure on the old code is demonstrated in CI (Task 3 Step 4). Also `GOOS=windows go vet ./internal/backup/`.

- [ ] **Step 3: Implement.**
  - `lock_windows.go`: `os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)`, then `windows.LockFileEx(windows.Handle(f.Fd()), LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{Offset: 0, OffsetHigh: 0x7fffffff})`, a 1-byte lock far past any content. `ERROR_LOCK_VIOLATION` (or `ERROR_IO_PENDING`) → close and return `ErrServerRunning`; any other error is wrapped. Then truncate, seek and write the pid, as `lock.go` does. `Release`: `UnlockFileEx` with the same range, then `Close`, with no removal. Rewrite the file comment to say why (mirror `lock.go`).
  - `lockpid.go`: `ReadLockPID`.
  - Unix `Release` is unchanged.

- [ ] **Step 4: Verify.** Run `GOTOOLCHAIN=auto go test ./internal/backup/ -count=1`, then `GOOS=windows GOARCH=amd64 GOTOOLCHAIN=auto go vet ./internal/backup/ ./cmd/rookery/` and `make ci-fmt`. Expected: PASS / clean.

- [ ] **Step 5: Commit** `fix(backup): release the Windows install lock when the server dies`.

### Task 2: `serve` says what is holding the lock

**Files:**
- Modify: `cmd/rookery/main.go` (the `backup.AcquireLock` call in `serveCmd`)
- Create: `cmd/rookery/serve_lock.go` with `serveLockError(dataDir string, err error) error`
- Test: `cmd/rookery/serve_lock_test.go`

**Interfaces:** Consumes `backup.ErrServerRunning`, `backup.LockPath`, `backup.ReadLockPID`.

- [ ] **Step 1: Failing test** `TestServeLockErrorNamesTheOtherServer`. With a lock file holding `4242`, `serveLockError(dir, backup.ErrServerRunning)` must satisfy `errors.Is(_, backup.ErrServerRunning)`. Its text must contain `another Rookery server is already running`, `4242` and `backup.LockPath(dir)`, and must not contain `restoring`. A non-lock error passes through unchanged.
- [ ] **Step 2: Run it**: `GOTOOLCHAIN=auto go test ./cmd/rookery/ -run TestServeLockError -count=1`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** The message is `another Rookery server is already running with this data directory (pid N, lock file PATH); stop it before starting a new one`. The `pid N, ` part is dropped when the file is unreadable. The returned error must satisfy `errors.Is(err, ErrServerRunning)` without printing its text. Use it in `serveCmd`.
- [ ] **Step 4: Run** the test and `make ci-vet`. Expected: PASS.
- [ ] **Step 5: Commit** `fix(serve): name the running server when the install lock is held`.

### Task 3: Windows smoke test in CI, shown red on the old code

**Files:**
- Create: `scripts/smoke-windows.ps1`
- Modify: `.github/workflows/pr.yml` (new job `windows-smoke`, name `Windows smoke test`)

- [ ] **Step 1: Write `scripts/smoke-windows.ps1`** (param `-Exe`). It uses `ROOKERY_DATA_DIR` = a fresh temp dir and `ROOKERY_HOST=127.0.0.1`, `ROOKERY_PORT=8899`. With `$ErrorActionPreference='Stop'` and `throw` on failure:
  1. `& $Exe owner bootstrap -u smoke -p <random>`
  2. `Start-Process $Exe serve` (output redirected to two log files); poll `http://127.0.0.1:8899/healthz` for up to 60s, expecting 200.
  3. Start a second `serve`; it must exit non-zero within 30s and its stderr must contain `already running`.
  4. `Stop-Process -Id <first> -Force`; wait for exit.
  5. Start `serve` again; `/healthz` must answer 200 within 60s. **This is the regression check.**
  6. Stop it, and on any failure print both log files.

- [ ] **Step 2: Add the job** to `pr.yml`: `runs-on: windows-latest`, `timeout-minutes: 20`, checkout + setup-go (`go-version-file: go.mod`, cache). Steps:
  - `go build -o rookery.exe ./cmd/rookery` (CGO 0);
  - `go test ./internal/backup/ -run Lock -count=1`;
  - `pwsh scripts/smoke-windows.ps1 -Exe ./rookery.exe`.

  Then try the logon task: `rookery service install` followed by `schtasks /Query`. Keep it only if it registers on a hosted runner; otherwise leave a comment saying it was tried and why it is absent.

- [ ] **Step 3: Prove it catches the bug.** On a throwaway branch `ci/windows-smoke-proof`, cut from this branch with the old `lock_windows.go` restored, run the job (push it, or `workflow_dispatch` if the job is given that trigger). Expected: the backup lock test and smoke step 5 both FAIL. Record the run URL for the PR description, then delete the branch.

- [ ] **Step 4: Verify on this branch**: the PR run's `Windows smoke test` is green.

- [ ] **Step 5: Commit** `ci: smoke-test serve restart after a hard kill on Windows`.

### Task 4: Documentation

**Files:**
- Modify: `CLAUDE.md`: in the CI/CD section, "seven jobs" becomes eight, with a `Windows smoke test` bullet saying what it covers and why (the stale lock); the stacked-PR, "four of the seven" and gate-count lines are adjusted to match; a sentence goes under "Windows autostart" saying the lock is OS-released and why.
- Run the `docs-sync` skill. Update any `~/rookery-web` page stating the gate count or the Windows lock behaviour, in its own branch and PR.

- [ ] **Step 1:** Edit, then run `make ci-docs`. Expected: PASS.
- [ ] **Step 2: Commit** `docs: record the Windows install lock and its CI gate`.
