# Install scripts and package dependencies — design

**Date:** 2026-08-10
**Unit:** C of the [onboarding, brand and platform batch](2026-08-10-onboarding-brand-and-platform-batch-design.md)

Two reported problems share a root: nothing in the repository had ever checked
that a user could get a *working* Rookery onto a machine.

The website advertises `curl -fsSL https://rookery.sh/install.sh | sh` on the
landing page, the macOS page, the Windows page and "your first 15 minutes".
Neither `install.sh` nor `install.ps1` existed. Separately, an rpm install on
Fedora produced `rookery` and `ripgrep` and no OCR, with no error to explain it.

## The rpm defect

`.goreleaser.yaml` declared one `recommends` list for both package formats:

```yaml
recommends: [python3, ripgrep, poppler-utils, tesseract-ocr]
```

`tesseract-ocr` is the Debian name. Fedora's package is `tesseract`. A weak
dependency naming a package that does not exist is dropped by `dnf` **in
silence** — the install succeeds, the tool is absent, and nothing anywhere says
why. That is a worse failure than a hard dependency error, because there is no
thread to pull on.

The fix is a per-format `overrides:` block giving the rpm Fedora's spelling. It
is three lines. The reason it survived this long is that nothing asserted it, so
the assertion matters more than the fix.

`scripts/smoke-package.sh` now checks both halves inside the same containers it
already runs:

- the **declared** names, read back off the built artifact with
  `rpm -qp --recommends` and `dpkg-deb -f … Recommends`, so an override that
  fails to apply is caught as well as one that was never written;
- **resolvability**, by looking each name up in the distribution's own metadata.
  This is the half that catches a plausible-looking name for a package nobody
  publishes — which is precisely the shape of the original bug.

Reading the names off the artifact rather than out of `.goreleaser.yaml` is
deliberate: a test that parses the config it is meant to be checking only proves
the YAML says what the YAML says.

## The installers

**One job: put a verified binary on `PATH` and hand off.** The scripts do not
bootstrap an owner, create a workspace or start a service — `rookery onboard`
(unit D) does that. Keeping configuration in Go means writing it once, testing
it, and having it serve the operator who installed from an rpm, a deb or a
tarball and never ran a script at all. Writing it in the scripts would mean two
implementations, in shell and PowerShell, neither of which CI can meaningfully
exercise.

`install.sh` is POSIX `sh`, not bash: `curl | sh` gets whatever `/bin/sh` is, and
both the macOS default and a minimal Debian image have to run it. It detects
`linux`/`darwin` and `amd64`/`arm64`, resolves the latest release tag (or honours
`ROOKERY_VERSION`), downloads the goreleaser archive, verifies it against the
release's `checksums.txt`, and installs to the first writable directory among
`$HOME/.local/bin` and `/usr/local/bin` — then says so loudly if that directory
is not actually on `PATH`, because a binary the shell will not look for is
indistinguishable from one that failed to install and harder to diagnose.

`install.ps1` does the same on Windows, installing to
`%LOCALAPPDATA%\Programs\Rookery` and appending it to the **user** `PATH`. It
never requires administrator rights, and it says plainly that other open
terminals will not see the change.

Both refuse to install on a checksum mismatch rather than warning. The checksum
catches a truncated download and a tampered mirror; it is not a substitute for
the cosign signature, which is what proves the release is ours. Running
`rookery version` immediately after install is the end-to-end check that the
bytes actually execute — it catches an archive for the wrong platform, which a
checksum cannot.

### Host tools are offered, never installed silently

A script piped from the network into a shell must not `sudo` four packages on its
own initiative. Both installers probe for `python3`, `rg`, `pdftotext` and
`tesseract`, print **what degrades without each one** rather than just naming
them, and ask. `ROOKERY_YES=1` answers yes for scripted installs; with no
terminal to ask on, the installer prints the exact command and continues rather
than hanging on a prompt nobody can answer.

`python3` gets the sharpest description on purpose: without it the agent-tool AST
guardrail in `internal/agentdesigner` self-skips, so generated tool scripts run
unchecked. That is a security property, not a convenience.

Package names differ per manager, which is the same class of mistake the rpm
made, so `pkg_name()` maps each tool per manager — `pdftotext` alone is
`poppler-utils` on dnf and apt, `poppler-tools` on zypper, and `poppler` on
pacman and Homebrew.

**Windows reaches full coverage through winget**, with no Chocolatey or Scoop
dependency: `Python.Python.3.13`, `BurntSushi.ripgrep.MSVC`,
`oschwartz10612.Poppler` (which ships `pdftotext.exe`) and
`UB-Mannheim.TesseractOCR`. Poppler is the one whose id is not guessable. The
installer warns that a freshly installed tool appears on `PATH` only in a new
terminal — otherwise the next report is "winget said it installed but Rookery
cannot see it".

## The private-repository problem

`curl | sh` **cannot work while `ilijad1/rookery` is private**. Release assets
require an authenticated request, so an anonymous download returns `404 Not
Found` — not `401` — and the script would fail looking like a network fault.

This is a release decision, not a design one, and nothing in the scripts changes
when it flips. What the scripts owe the user meanwhile is an honest error.
`download_failed` names the private-repository case first, because while that is
true it is overwhelmingly the actual cause, and a 404 gives the user nothing to
work with on their own.

## Testing

Neither script can be executed in CI: there is no Windows host, and running the
shell installer for real would download a release and modify `PATH`. So the tests
assert the things that break silently, in `packaging/scripts_test.go`:

- both files exist and are non-empty — the assertion whose absence let the
  website advertise them for the repository's whole life;
- both build the goreleaser archive name (`rookery_<version>_<os>_<arch>`) and
  strip the leading `v` from the tag, since a naming change would 404 on a
  release that published perfectly well;
- all four host tools appear on all four delivery surfaces — `Dockerfile`,
  `.goreleaser.yaml`, and both installers;
- the rpm override names `tesseract` and no longer names `tesseract-ocr`, while
  the deb list keeps Debian's spelling;
- the four winget ids are present;
- both scripts hand off to `rookery onboard`;
- both refuse a checksum mismatch.

`install.sh` is additionally syntax-checked with `sh -n`. `install.ps1` is not
syntax-checked, because no PowerShell exists on the development host — that is a
real gap and is recorded as one rather than papered over.

## Distribution

The canonical copies live at the repository root. `rookery.sh/install.sh` and
`rookery.sh/install.ps1` redirect to the raw GitHub URLs rather than serving
copies: `curl -fsSL` and `irm` both follow redirects, and one file cannot drift
from itself. The alternative — vendoring copies into the website's `public/` —
would recreate exactly the duplication that the brand-logo manifest already has
to be defended against.
