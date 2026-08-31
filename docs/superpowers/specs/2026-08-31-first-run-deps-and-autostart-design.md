# First run: dependency detection, dependency installation, and autostart

Date: 2026-08-31
Status: approved, implementation in progress

## The report

A first install on Windows produced three complaints, all from the same
half-hour:

1. The installer offered the host tools; `rookery onboard` then offered the
   same tools **again**, plus the browser — "asking for reinstall just to
   realize that they are installed".
2. The installer had installed ripgrep, Poppler and Tesseract in that same
   session, and onboard still did not see them.
3. Nothing registered a service, so Rookery did not come back after a reboot.

The three are not independent. (2) is why (1) happens, and (3) is a gap the
installer was never asked to close. Underneath all three is one structural
problem: **the installer and onboard each carry their own idea of what is
installed, and neither is the same idea the running server uses.**

## What is actually wrong

### Defect 1 — the two sides probe different binaries

`internal/onboard/hosttools.go` probes `python3`. `install.ps1` probes
`python` and installs `Python.Python.3.13`, whose python.org distribution ships
`python.exe` and `py.exe`. So on Windows the installer installs Python, reports
success, and onboard then reports `python3` missing — **forever**, on every
subsequent run, because installing it again changes nothing.

This one needs no Windows host to confirm; it is visible in the two files.

### Defect 2 — detection is PATH-only, and on Windows these tools are not on PATH

`onboard.Missing` resolves exclusively through `exec.LookPath`. That is the
whole probe. On Windows:

- `UB-Mannheim.TesseractOCR` installs into `C:\Program Files\Tesseract-OCR`
  and does not put itself on PATH.
- `oschwartz10612.Poppler` is a portable package; winget's shim lands in
  `%LOCALAPPDATA%\Microsoft\WinGet\Links`, which a process that resolved its
  environment before the install cannot see.

So the tools are present and onboard cannot find them — exactly the report.

**The important half is that finding them is not sufficient.** Everything that
actually consumes these tools also goes through `exec.LookPath`:
`internal/health` (`have()`), `internal/convert` (pdftotext, tesseract),
`internal/vault/search.go` (rg), `internal/agentdesigner/guardrails.go`
(python3), and `internal/coder/hosttools.go`, which additionally uses the
resolved path to grant the interpreter's directory read+execute inside the
Landlock sandbox. A fix that taught only `onboard` to look harder would report
"all present" while OCR, PDF extraction and the AST guardrail stayed broken —
**a quieter bug than the one being fixed, and in the more dangerous direction**.
Today's failure is at least loud.

### Defect 3 — there is no autostart on Windows

`onboard.ServiceFor("windows")` returns `Managed: false` with the note that
registration "is not built yet", and suggests NSSM. Nothing in either installer
addresses it. A laptop reboot therefore leaves the scheduler dead, which is
invisible until an agent silently misses a run.

### Defect 4 — the browser is offered even when a browser exists

`internal/browser` only ever launches Playwright's own managed Chromium
(`host.go`, `pw.Chromium.Launch(...)`, with neither `Channel` nor
`ExecutablePath` set), so `Probe()` asks only whether that managed build is
present. An owner with Chrome or Edge already installed is offered a ~200 MB
download for a capability their machine can already provide.

## Design

### A. One resolver, and it is the only one

`internal/onboard` gains `Resolve` — the single answer to "where is this tool,
if anywhere" — modelled directly on `coder.DetectInstalled`'s `detectHost`,
which exists for the same reason and solved the same problem for coder
binaries.

```go
type Host struct {
    GOOS     string
    Home     string
    LookPath func(string) (string, error)
    Stat     func(string) (fs.FileInfo, error)
    Getenv   func(string) string
    Verify   func(path string) bool   // execution check; see below
}

func Resolve(h Host, t HostTool) (path string, ok bool)
func Missing(look LookPath) []HostTool   // kept, now implemented over Resolve
```

Resolution order per tool: PATH first (`LookPath` already applies `PATHEXT` on
Windows), then a platform search list, then per-tool directory hints.

`HostTool` gains two fields:

- **`Bins []string`** — candidate names in preference order. `python3` becomes
  `{"python3", "python", "py"}` on Windows and `{"python3"}` elsewhere. `Bin`
  stays the canonical identifier: it is what `/healthz` reports and what every
  package-name map is keyed by, so nothing downstream moves.
- **`Dirs`** — per-tool, per-platform directory hints for the installs that
  land off PATH (`C:\Program Files\Tesseract-OCR`, the winget `Links` and
  `Packages` directories, `%LOCALAPPDATA%\Programs\Python\Python3*`).

Two Windows-specific rules, both load-bearing:

- **No executable-bit test on Windows.** Go synthesises file mode from file
  attributes there and never sets `0o111`, so a mode check matches nothing.
  `coder.binCandidates` records this exact trap; this code must not reintroduce
  it.
- **`python3` is verified by EXECUTION, not by existence.** Stock Windows ships
  an App Execution Alias stub at
  `%LOCALAPPDATA%\Microsoft\WindowsApps\python3.exe` that resolves through
  `LookPath` and opens the Microsoft Store instead of running Python. Existence
  alone would turn "offers an installed tool" into "reports a missing tool as
  present" — again the worse direction. `Verify` runs the candidate with
  `--version` under a short timeout and requires exit 0 with `Python` in the
  output. It applies to `python3` alone, because it is the only tool with a
  known decoy, and an unnecessary subprocess per tool per probe is not free.

### B. The resolver is made true for the whole process by augmenting PATH

Rather than convert eight call sites in six packages to the new resolver — and
leave the ninth, written later, silently wrong — `main.go` resolves the host
tools once at startup and **appends any directory that contributed a tool to
the process `PATH`**.

Appended rather than prepended: these directories are only ever derived from
tools PATH could not already resolve, so there is nothing to shadow, and
appending leaves a deliberate operator override in front of anything inferred
here.

Every existing `exec.LookPath` consumer then becomes correct with no edit, the
sandbox grant in `internal/coder/hosttools.go` resolves the same interpreter
the guardrail will run, and `/healthz` and `onboard` cannot disagree, because
by construction they are asking the same question of the same environment.

Three constraints:

- It **skips the two internal helper commands** (`__sandbox-exec` and the
  browser host). Those re-exec a command they were handed; changing the
  environment underneath them is not this hook's business.
- It is **additive and logged**. Directories are appended to the existing PATH,
  never replacing it, and the augmentation is reported at startup alongside the
  other capability lines. Silent environment mutation is how this becomes
  someone else's long afternoon.
- On Linux it changes nothing in practice (these tools live in `/usr/bin`,
  already on PATH and already inside the sandbox's read-only set), which is
  what makes the change safe to ship: the platform it affects is the one that
  is broken.

### C. The browser becomes a dependency Rookery detects rather than owns

Requirement, as given: treat the browser as an external dependency, do not
offer it when the owner already has one, and do not restrict the check to
Chrome/Chromium — detect Playwright's other browsers too.

`internal/browser` gains `Resolve() Choice`:

```go
type Choice struct {
    Engine     string // "chromium" | "firefox" | "webkit"
    Channel    string // "chrome" | "msedge" when driving a system install
    Source     string // "managed" | "channel"
    OK         bool
    Reason     string
}
```

Resolution order:

1. **Playwright-managed builds already in the cache** — chromium, then
   firefox, then webkit. This is what satisfies "detect other Playwright
   browsers": an owner who already has Playwright's Firefox gets it used
   instead of being asked to download Chromium.
2. **A system Chromium-channel browser** — `chrome`, then `msedge`, driven
   through Playwright's `Channel` option with no browser download at all.
3. Nothing — and only then is an install offered.

`host.go` launches the resolved engine instead of hardcoding
`pw.Chromium.Launch`.

**One thing must be stated plainly rather than discovered later: detecting a
system Chrome removes the ~115 MiB Chromium download, not the whole ~200 MB.**
Playwright drives a system browser through its own Node driver — `host.go`
cannot launch anything until `driver/package/cli.js` exists — so the driver
(~70 MB) is required whatever is found. `Channel` and `ExecutablePath` replace
the browser build, never the driver. The installer prompt must therefore say
what it is actually about to download, and `Install` takes the resolved choice
so it can fetch the driver alone when a system browser will be used.

`Channel` is preferred over `ExecutablePath` wherever both would work, because
playwright-go's own documentation warns that `ExecutablePath` is unsupported
("use at your own risk") while `Channel` explicitly names `chrome` and
`msedge`.

Four constraints found while mapping this, each of which would otherwise have
been discovered as a regression:

- **`Probe()` today checks for a *directory*, not a binary.** `hasChromium`
  looks for a `chromium-*` directory under the browsers cache, so `Probe().OK`
  is already compatible with `ChromiumExecutable()` returning `""`. The
  resolution layer must resolve an executable — `pdfengine.go`'s
  newest-revision-first walk is the function to build on, not `hasChromium`.
- **PDF export needs a Chromium-family binary PATH, not merely a working
  browser.** `internal/export/pdf.go` shells out directly to
  `browser.ChromiumExecutable()`, outside Playwright. So resolving to Firefox
  would leave rendering working and silently break PDF export. Chromium-family
  therefore stays the preferred engine, and a resolved system Chrome must
  surface its path and not only its channel name.
- **The loopback-proxy guard is engine-specific, and it is a security property.**
  `host.go` routes the browser through a local proxy and forces loopback through
  it too, because Chromium bypasses the proxy for localhost by default and this
  install's own connector, KB and MCP bridges — and their bearer tokens — listen
  there. The Chromium flag has no Firefox equivalent; Firefox needs the
  `network.proxy.allow_hijacking_localhost` preference set through
  `FirefoxUserPrefs`. Each engine carries its own hardening explicitly, and an
  engine whose hardening cannot be applied is **refused with a named reason
  rather than launched unprotected**. The only test that asserts this behaviour
  (`TestBrowserCannotReachLoopbackWhenGuarded`) sits behind the `browser` build
  tag and does not run in CI, so per-engine verification is a release gate for
  Firefox and WebKit, not a footnote.
- **A system browser writes outside the sandbox's writable set.** The browser
  host's Landlock spec grants RW on scratch and the Playwright caches only; a
  system Chrome wants a profile and crash directory, so one must be pointed at
  scratch and granted, or the launch fails under the default `Sandboxed=true`.

Non-goal: driving a system Firefox or Safari. Playwright's Firefox and WebKit
are patched builds and its own documentation does not support substituting the
system ones, so those two engines are managed-only.

### D. Autostart: a logon Scheduled Task, registered by Go

Chosen mechanism: a **Windows Task Scheduler task triggered at logon**,
running as the current user with `InteractiveToken`.

The constraint that decides this is not tidiness, it is: *works for a standard
non-admin user, with no stored credentials, and can reach a data directory
under the user's own profile.* That rules out an SCM service, which needs
administrator rights to install and then runs as a different principal —
reintroducing precisely the problem the Linux side avoids by using a systemd
**user** unit. It also rules out depending on `S4U`, which needs a batch-logon
right a standard user may not hold.

Accepted cost, recorded rather than engineered around: a logon-triggered task
running an interactive console application shows a console window. It is
cosmetic. The alternatives that hide it all trade a visible window for a
credential prompt or a privilege requirement, which are worse.

`ServiceFor("windows")` becomes `Managed: true`, and the generated task XML is
the tested artifact — exactly as `UnitFileFor` is for systemd. That is the
level of verification available here; see "What is not verified".

### E. `rookery service` — asked by the installer, performed in Go

A new top-level command with `install`, `uninstall` and `status`.

This is the split the requirement asks for, and the boundary is drawn where the
knowledge is: **the installers install the external dependencies themselves**
(they are ordinary OS packages, and both scripts already do it through winget,
apt, dnf, zypper, pacman and brew), while **autostart is Rookery's own
configuration and is registered by Rookery**. Writing task XML and systemd
units into two shell dialects — one of which cannot even be syntax-checked on
the development host — would put the platform knowledge in the two places least
able to test it, and would leave out everyone who installed from an rpm, a deb
or a tarball and never ran a script.

`onboard`'s service step delegates to the same code, so the two cannot drift.

### F. Both installers offer everything, and onboard goes quiet

Installers: host tools (already), then the browser, then autostart. Each is
offered and, on consent, done. `--yes` / `ROOKERY_YES=1` consents to all three;
a non-interactive session prints the commands instead of hanging.

`onboard` becomes idempotent and quiet. A step whose work is already done says
one line and moves on; it never re-offers. Combined with (A) and (B), the
reported "asking for reinstall just to realize they are installed" cannot
happen, because onboard is asking the question the installer already answered
and getting the same answer.

## What is not verified

The Windows half is authored against published behaviour and is **not exercised
on a real Windows host**. There is none here, and `install.ps1` is not even
syntax-checked — a gap this repository already records for `swap_windows.go`
and `echo_windows.go`. What checks it: the cross-compile gate, unit tests over
the generated task XML and the resolver (both driven against a described host
rather than the real one, the same technique `coder/detect_platform_test.go`
uses), and `packaging/scripts_test.go` over the installer text.

This is stated in the pull request bodies too. A change whose entire subject is
a platform we cannot run should not present itself as verified.

## Testing

- `onboard.Resolve` against a described Windows host: tool off PATH in
  `Program Files`; the winget `Links` shim; `python` present but not `python3`;
  the `WindowsApps` decoy rejected by `Verify`; no executable-bit dependence.
- **The agreement property**, on Linux, where it can genuinely run: place `rg`
  in a directory outside PATH, run the augmentation, and assert `onboard` and
  `health.Detect` return the same answer. This is the invariant the whole of
  (A)+(B) exists to establish, and it is the one test that would catch the
  quieter regression.
- A child process inherits the augmented PATH.
- `browser.Resolve` ordering across managed builds and channels, and that an
  engine without applicable proxy hardening is refused rather than launched.
- Generated Scheduled Task XML: correct binary path, logon trigger, current
  user, and no stored credential.
- `packaging/scripts_test.go` extended for the browser and service steps; its
  existing assertions (no bare `exit`, `throw` present, `checksum mismatch`,
  `rookery onboard`, the archive naming, the literal `python3`) all still hold.

## Documentation obligations

- `reference/cli.md` in `rookery-web` needs a `## service` section —
  `check_cli_coverage` reads the root command list and fails without it. `##
  browser` is **already** missing and already failing; fixed in the same pass.
- `installation/windows.md` — `check_windows_winget_ids` compares the page's
  winget ids against `install.ps1`'s own table **bidirectionally**, so any id
  the script gains needs a matching line on the page.
- `README.md` and `CLAUDE.md` — the platform table's Windows row ("SCM (not yet
  shipped)") and the "Windows service registration remains deferred" sentence
  both become false.
- `internal/onboard/service_test.go`'s `TestOnlyLinuxIsManaged` is deliberately
  rewritten, not worked around.

## Delivery

Three pull requests, each branched from `main` rather than stacked, because a
stacked PR runs zero checks in this repository. They are sequenced because the
second and third both touch the installer scripts.

1. **Host tools** — the resolver, PATH augmentation, the Python spelling
   alignment across `install.ps1` and `internal/onboard`, and setup
   re-resolving after an install instead of advising a new terminal. This is
   the whole of defects 1 and 2, and it ships first because it is the reported
   complaint and it needs nothing from the other two.
2. **Browser** — the resolution layer above, and the installers' browser step.
   Larger than it looks: it changes the only launch site in the codebase, and
   the engines beyond Chromium cannot be verified by anything CI runs.
3. **Autostart** — `rookery service`, the Windows logon task, `ServiceFor`, and
   the installers' autostart prompt.

Splitting (1) out is deliberate rather than tidy. Bundling it with the browser
work would put a change we can fully verify on Linux behind one we cannot
verify at all.
