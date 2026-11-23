# Windows install lock survives a dead server — design

## Intent

On Windows, Rookery stops starting after the first time its server dies
uncleanly. A shutdown or restart, Ctrl+C, closing the console window or a
crash each leave `<data_dir>/rookery.pid` behind, and every later `serve` exits
at once with `ErrServerRunning`. The autostart task's whole purpose is to bring
Rookery back after a power-off, and it fails on exactly that case. A real
installation on a fresh laptop hit this: install finished, nothing answered on
`localhost:8899`.

**Cause.** `internal/backup/lock_windows.go` treats the *existence* of the file
as the lock (`O_CREATE|O_EXCL`), and only `Lock.Release` removes it. `serve` has
no signal handling, so `Release` never runs on any stop. Linux is unaffected
because `lock.go` uses `flock`, which the kernel drops when the holder dies; its
own comment names this exact failure. The task's console closes as soon as
`serve` exits, so the user never sees the error, and `RestartOnFailure` retries
three times into the same wall.

## Goals

- A Windows server that dies **any** way does not stop the next start.
- Two live servers still cannot run against one data directory. The lock's
  original job, refusing a `backup restore` under a live server, is kept.
- Existing broken installs heal on upgrade, with no manual file deletion.
- A test that fails on today's code and passes on the fix, on a real Windows
  machine, on every pull request.

## Non-goals

- Graceful shutdown or signal handling in `serve`. That is a separate
  improvement, and it is not needed for correctness once the lock cannot go
  stale.
- Starting at boot rather than at sign-in. The logon task is deliberate (no
  administrator rights needed).
- Exercising `install.ps1` itself in CI.

## Anti-goals

- A "is the pid in the file still alive?" heuristic. Windows reuses pids, so a
  stale file naming a reused pid would still wedge the install, and a guess
  wrong in the other direction would let two servers run.
- A test that only starts the server once. That passes on today's broken code.

## Approaches considered

1. **OS byte-range lock via `LockFileEx` (chosen).** Open the file without
   `O_EXCL`, keep it open, and take
   `LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY`. Windows releases the lock
   when the process dies, however it dies. This mirrors `flock` exactly.
   `golang.org/x/sys/windows` is already a direct dependency, so nothing new is
   added. An old stale file is simply opened and locked, which is what heals
   existing installs.
2. **Pid-liveness check on the existing file.** Rejected under the anti-goals:
   pid reuse makes it unreliable in both directions.
3. **Delete the file at startup.** Rejected: this removes the protection
   against two servers and against restoring under a live server.

## Design

- `lock_windows.go`: `OpenFile(O_CREATE|O_RDWR)`, then `windows.LockFileEx` on
  a range far past the content, so the pid text stays readable by other
  processes while locked. On `ERROR_LOCK_VIOLATION` return `ErrServerRunning`.
  Otherwise truncate and write the pid, as the Unix side does. `Release`
  unlocks and closes, and does **not** delete the file (Unix parity: the lock,
  not the file, is what matters).
- `ErrServerRunning`'s message today talks about restoring, which misleads when
  `serve` hits it. `serve` wraps it to say that another Rookery server is
  already using this data directory, and names the lock file and the pid it
  records. `backup restore` keeps its existing wording.

## Verification ("fixed for real")

1. **Cross-platform unit test** in `internal/backup` that starts a child process
   (the test binary re-invoked) which takes the lock, then:
   - asserts a second acquire from the parent fails while the child is alive;
   - **kills the child hard** and asserts the next acquire succeeds;
   - asserts that acquiring over a leftover file from an old build succeeds.

   It runs on Linux in the existing job and on Windows in the new one.
2. **New PR job, `Windows smoke test`** on `windows-latest`:
   - build `rookery.exe` and run `owner bootstrap`;
   - start `serve` and wait for `/healthz`;
   - `Stop-Process -Force`, start `serve` again, and assert `/healthz` answers
     again;
   - run `go test ./internal/backup/` on Windows.

   Before merging, it must be shown failing against the old `lock_windows.go`
   (run once on a throwaway commit) so the job is known to catch the bug.
3. Docs: the "seven jobs" count in `CLAUDE.md` becomes eight, with a line on
   what this gate covers. Run docs-sync for the website pages that describe the
   CI gates or Windows autostart.

## Left to the builder

- Exact byte range and offset for the lock.
- Whether the smoke job also exercises `rookery service install` plus
  `schtasks /Run`. Hosted runners may not support an interactive-token logon
  task. Include it if it works there; otherwise note it in the job.
