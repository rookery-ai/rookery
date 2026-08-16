# Multi-platform documentation and CLI parity — design

The documentation site tells every new user to run `rookery owner bootstrap`
followed by `rookery serve`. That was correct until `rookery onboard` shipped,
and it has been wrong since: `onboard` resolves both keys, creates the owner,
offers the host tools, reports the coder situation and installs the systemd unit
with lingering enabled. A user following the published path gets an install with
no service, no host tools, and no idea that either was on offer.

The Windows page compounds it. Its PowerShell is not merely out of date — three
of its commands are wrong in ways that produce a working-looking install with
missing pieces, and one documented guarantee is false on Windows outright.

This change corrects the published commands on every platform, adds the two CLI
commands the reference never learned about, fixes the one place where the
product genuinely behaves worse on Windows than the documentation claims, and
extends the documentation gate so the same drift cannot recur silently.

## What is actually wrong

Findings are grouped by whether the source of truth is the product or the prose.
Everything in the first group is a documentation defect; everything in the
second is a product defect the documentation then described accurately or not
at all.

### Documentation defects

| # | Where | Defect |
|---|---|---|
| 1 | `installation/{windows,macos}.md`, `getting-started/first-15-minutes.md` | Happy path is `owner bootstrap` + `serve`, skipping `onboard` entirely. |
| 2 | `installation/linux-server.md` | Never mentions `onboard` at all, and hand-rolls the steps it performs. |
| 3 | `reference/cli.md` | No `upgrade`, no `uninstall`. Both shipped in v0.2.0. |
| 4 | `installation/binary.md` | "upgrading is: stop, replace the binary, start" — `rookery upgrade` does this. |
| 5 | `operations/configuration.md` | Documents `ROOKERY_CLAUDE_BIN`, retired in favour of `ROOKERY_CODER_BIN`. |
| 6 | `installation/windows.md` | Offers `Python.Python.3.12`; the installer offers `Python.Python.3.13`. |
| 7 | `installation/windows.md` | "Poppler has no winget package" — `install.ps1` installs `oschwartz10612.Poppler`, which ships `pdftotext.exe`. |
| 8 | `installation/windows.md` | `winget install <id>` without `-e --id`, so the id is matched as a search term. |
| 9 | `installation/windows.md` | `curl http://localhost:8080/healthz` — on Windows `curl` is an alias for `Invoke-WebRequest`, which returns a response object, not the body. |
| 10 | every page | Environment variables are shown only in POSIX `VAR=value` form, which sets nothing in PowerShell. |
| 11 | `reference/cli.md` | "prompts for the passphrase with terminal echo suppressed" — false on Windows (see defect A). |
| 12 | `operations/backup-and-restore.md` | No mention that piping a passphrase in Windows PowerShell 5.1 mangles non-ASCII. |
| 13 | `installation/windows.md` | No way to install a specific version: `irm … \| iex` cannot take parameters. |

### Product defects

**A. The backup passphrase is echoed in plain view on Windows.**
`readPassphrase` suppresses terminal echo by shelling out to `stty`, and falls
back to a no-op when `stty` is absent. There is no `stty` on Windows, so every
`rookery backup now`, `verify` and `restore` prompts for the passphrase and
prints it as it is typed. It is not a crash and not a warning; the only signal
is the characters on screen. The documentation asserts the opposite.

**B. `install.ps1`'s failure path kills the user's PowerShell session.**
The script is advertised as `irm … | iex`, which executes it in the *caller's*
session rather than a child scope. Its error path calls `exit 1`, and `exit` in
that context terminates the host session — so a checksum mismatch or a missing
asset closes the window, taking the error message with it. The one case where
the message matters most is the one case it cannot be read.

**C. Both installers lead their 404 message with a cause that no longer
exists.** The message names a private repository first. The repository is
public and v0.2.0 is released, so that is now the one thing a 404 cannot mean,
and it points a user away from the real causes: a missing asset for their
platform, or a proxy. CLAUDE.md records this as knowingly-stale and pinned by
`packaging/scripts_test.go`, which is why it is corrected here as a change to
the installers *and* their test rather than as a prose edit.

**D. `install.ps1`'s own docstring advertises `rookery.sh`.** The domain is
`rookery.cloud`. The comment in `packaging/scripts_test.go` repeats it.

**E. `rookery upgrade` cannot work on Windows at all.** Found while verifying
the sentence "stop the server first" before publishing it. `replaceBinary`
finishes with `os.Rename(tmp, target)`, which is correct on POSIX — the unlink
drops the directory entry, not the open file — and impossible on Windows, where
a running image is held with a share mode denying delete. Stopping the server
does not help: `upgrade` replaces the binary it is *itself* executing, so the
upgrade process is the lock. `uninstall` has the same defect in `os.Remove(self)`,
and its failure message advises retrying "with the privileges that installed
it", which is the wrong diagnosis there. Separately, `upgrade` closes by printing
`systemctl --user restart rookery.service` on all three platforms, naming a
command that does not exist on two of them.

This is scope growth, taken deliberately: the documentation half of this change
advertises both commands on Windows, and publishing that while they cannot work
there would be the same defect class the rest of this change exists to remove.

## Approach

Three principles decide most of the individual edits.

**Prefer a subcommand over shell plumbing.** Wherever the binary already does
the job, the documentation says so rather than teaching three dialects of the
same thing. `rookery healthcheck` replaces `curl -s …/healthz` as the primary
instruction on every platform, because the Go binary behaves identically
everywhere and the shell equivalents do not. The raw endpoint stays documented
for the case that actually needs it — reading it from another machine.

**One happy path, then the platform differences.** Every installation page
leads with install → `rookery onboard` → open the browser. What follows is only
what genuinely differs: the service story, the host-tool package manager, the
data directory, and the confinement caveat. This replaces the current shape,
where each page re-derives the whole setup and drifts independently.

**Document Windows as a first-class platform, including where it is worse.**
The two places Windows is genuinely weaker — no filesystem confinement, no
service registration — are already documented and stay. Defect A adds a third
that was undocumented, and it gets fixed in code rather than written down,
because a backup passphrase displayed on screen is not a caveat a user can work
around once they know about it.

### Why the Windows echo fix ships here rather than as a separate change

It is twenty lines, needs no new dependency, and fails safe. The fix reads the
console mode, clears `ENABLE_ECHO_INPUT` while leaving `ENABLE_LINE_INPUT` set
so the existing `ReadString('\n')` still works, and restores the original mode
afterwards. Every failure path returns the same no-op restore function the
`stty` path already returns, so the worst outcome is exactly today's behaviour.

It calls `golang.org/x/sys/windows`, not `syscall`. The first attempt used
`syscall`, on the assumption that a package exporting `GetConsoleMode` also
exports its setter; it does not, and only the cross-compile step said so.
`x/sys` is already a **direct** requirement of this module, so reaching for it
adds nothing to the module graph and is preferable to hand-rolling the kernel32
call through a `LazyDLL`.

The existing `disableEcho` splits into two build-tagged files — `echo_unix.go`
(`//go:build !windows`, the current `stty` implementation moved verbatim) and
`echo_windows.go`. `readPassphrase` is untouched. The change is compile-checked
on Windows by the `Cross-compile` PR gate, which builds all six GOOS/GOARCH
pairs; that is a real check, and it is the only one available here — there is
no Windows host, and this is recorded rather than papered over.

### Why the gate is extended

`check_cli()` already asserts that `reference/cli.md` documents nothing the
source does not declare. It does not assert the converse, which is precisely
why `upgrade` and `uninstall` shipped undocumented: adding a command breaks no
check. Likewise `serve` is a real command, so the pages telling users to run it
in place of `onboard` passed every gate for as long as the drift existed.

Three assertions are added to `scripts/check-docs-sync.py`:

1. **Every top-level command in `cmd/rookery` has a `## ` section in
   `cli.md`.** Exempting only the sandbox helper, which is hidden by design.
   The command list is derived from source, so the next subcommand fails this
   until it is documented.
2. **Every installation page names `rookery onboard`.** Stated as a positive
   requirement rather than a ban on `owner bootstrap`, because that command is
   legitimately documented elsewhere on those pages as the non-interactive
   alternative.
3. **The Windows page's winget ids match the ones `install.ps1` offers.** Read
   out of the installer, not out of a second list, so the two cannot drift the
   way defects 6 and 7 did.

These run only when the website checkout is resolvable, matching every existing
website assertion — a gate that fails when it cannot see the second repository
is a gate that gets deleted.

## Scope

**In:** the nine documentation pages named above; `install.ps1` and
`install.sh`; `cmd/rookery/backup_cmd.go` split into `echo_unix.go` +
`echo_windows.go`; the `swapBinary`/`removeSelf` split into `swap_unix.go` +
`swap_windows.go` and `upgrade`'s service-restart line;
`packaging/scripts_test.go`; `scripts/check-docs-sync.py`; the CLAUDE.md and
README passages the above make wrong.

**Deploy order is part of the change, not an afterthought.** Three pages now
state that the passphrase prompt hides what you type, and two describe the
`.old` move-aside — all of which describe code that does not exist in v0.2.0.
The website deploys on merge to its own `main`, while the product needs
merge → release PR → merge → tag. So the product must be **released** before the
website PR merges, or rookery.cloud documents behaviour no released binary has,
about the one credential a backup cannot recover.

**Out:**

- **launchd and Windows service registration.** Both remain unbuilt. The
  documentation says so; building them is a Tier-2 feature, not a doc fix.
- **A Homebrew tap and a winget manifest for Rookery itself.** Same reason.
- **Console input *encoding* on Windows.** The echo fix does not change how
  bytes are decoded. A non-ASCII passphrase typed at a Windows console is a
  separate question from a non-ASCII passphrase piped through PowerShell 5.1,
  and the latter is documented rather than fixed because it is a property of
  the shell, not of Rookery.

## Verification

`make ci` covers the Go and gate changes; `GOOS=windows go build ./...` is what
proves `echo_windows.go` compiles, and runs inside `ci-cross`.
`make docs-sync-check` with `ROOKERY_WEB_DIR` pointed at the website worktree
proves the three new assertions pass against the rewritten pages — and, run
before the pages are rewritten, that they fail. Asserting the failure first is
the point: a gate that has never been observed to fail has not been shown to
gate anything.

Neither PowerShell file can be executed here, and `install.ps1` cannot even be
syntax-checked — there is no PowerShell on the development host. Every
PowerShell change in this design is therefore derived from documented
semantics, and the two non-obvious ones were confirmed against upstream sources
rather than recalled: `[scriptblock]::Create()` as the only way to pass
arguments to a piped installer, and `$OutputEncoding` defaulting to ASCII in
Windows PowerShell 5.1 versus UTF-8 in 7. This is the same gap
`packaging/scripts_test.go` already exists to narrow, and it stays a gap.
