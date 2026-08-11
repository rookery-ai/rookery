# CLI coder detection on macOS and Windows — design

**Date:** 2026-08-10
**Unit:** F of the [onboarding, brand and platform batch](2026-08-10-onboarding-brand-and-platform-batch-design.md)

The question was whether `coder.DetectInstalled` works off Linux. Reading it, the
answer is: partly, and the part that fails does so silently.

## What was actually wrong

`exec.LookPath` honours `PATHEXT` on Windows, so a coder **on `PATH`** always
resolved correctly. The bug was entirely in the fallback search, and it had three
independent faults.

**It looked in one directory.** `extraDirs` was `~/.local/bin` alone. That misses
Homebrew — `/opt/homebrew/bin` on Apple silicon, `/usr/local/bin` on Intel — and
misses `%APPDATA%\npm` and `%LOCALAPPDATA%\Programs` on Windows, which is where
npm and most installers actually put these binaries.

**It demanded an executable bit that Windows does not have.** The candidate test
was `fi.Mode()&0o111 != 0`. Go synthesizes mode bits on Windows from file
attributes and never sets `0111`, so on that platform the condition rejected
every candidate. The fallback search could not find anything at all, ever.

**It statted the bare name.** `exec.LookPath` applies `PATHEXT` when searching
`PATH`; a direct `os.Stat` does not. npm installs these coders as `claude.cmd`
shims, so even a correct directory with the executable-bit test removed would
still have found nothing.

There is a fourth problem that is not a code fault but shapes the fix: **a
launchd-started process on macOS inherits a minimal `PATH`** that excludes
`/opt/homebrew/bin`. So a user whose terminal finds `claude` without any trouble
can still have Rookery report no coder installed, and the obvious diagnostic —
running `which claude` — actively misleads them.

## The fix

`DetectInstalled` now delegates to `detectInstalled(detectHost)`, where
`detectHost` carries `GOOS`, `Home`, `LookPath`, `Stat` and `Getenv`. Everything
platform-specific reads from that struct rather than calling the OS directly.

Two small pure functions carry the platform knowledge:

- **`coderSearchDirs(h)`** returns the directories probed after `PATH`, per
  platform. Linux and macOS get `~/.local/bin`, `~/.npm-global/bin` and `~/bin`;
  macOS adds both Homebrew prefixes; Windows gets `%APPDATA%\npm` and
  `%LOCALAPPDATA%\Programs`.
- **`binCandidates(goos, bin)`** expands a bare name into the file names to stat.
  On Windows that is `.exe`, `.cmd`, `.bat`, `.ps1` and the bare name; everywhere
  else it is the bare name alone, so a Linux host does not start looking for
  `claude.exe`.

The executable-bit test is kept on POSIX, where it is meaningful — a
non-executable file genuinely is not a coder — and dropped on Windows, where
existence plus a `PATHEXT`-shaped name is the correct test.

`PATH` still wins, and its answer is used as-is.

## Why the host is a parameter

There is no macOS or Windows runner available here, and every one of these bugs
was platform-specific. A test that can only describe the machine it runs on could
not have caught any of them, and cannot prove the fix. Injecting the host is what
makes "a Windows file with no executable bit, in `%APPDATA%\npm`, named
`claude.cmd`" something a Linux CI job can assert about.

This mirrors `internal/onboard`'s injectable `LookPath`, for the same reason.

## What is and is not claimed

**Claimed:** the logic is correct for all three platforms and is pinned by tests
that describe each one.

**Not claimed:** that this ran on a Mac or a Windows box. It did not. Adding
`macos-latest` and `windows-latest` runners to CI would convert the first claim
into the second; that is a separate decision about runner minutes and PR-gate
latency, and is deliberately not taken here.

## Testing

`internal/coder/detect_platform_test.go`, against a fake filesystem and a
`LookPath` that resolves nothing — because `PATH` already worked and the fallback
is what was broken:

- Homebrew is found on macOS, at both prefixes;
- an npm `claude.cmd` shim is found on Windows, with the right backend type;
- a Windows file with mode `0666` — the exact condition the old test could not
  pass — is accepted;
- a POSIX file with mode `0644` is still rejected;
- a directory named like a coder is rejected;
- every platform searches at least one directory beyond `PATH`, so detection
  cannot silently regress to `PATH`-only;
- an empty environment (the launchd/service case, with no `HOME` and no
  `APPDATA`) produces no bogus paths and does not panic;
- `PATH` resolution takes precedence over the fallback;
- `binCandidates` expands only on Windows;
- two candidate names resolving to one coder report it once.

## Adjacent, not fixed here

The settings UI still has no Model field for a **local** CLI coder, which is what
blocks OpenCode out of the box (it 401s on its default provider without an
explicit model). That is a separate known gap and is untouched by this change.
