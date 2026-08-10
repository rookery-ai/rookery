# Onboarding, brand and platform batch — umbrella design

**Date:** 2026-08-10
**Status:** approved, decomposed into seven shipping units

Nine reported items arrived as one request. They share no code, and two of them
live in a different repository, so this document records the decomposition, the
premises that turned out to be wrong, and the decisions each unit inherits.
Every unit gets its own spec and its own pull request.

## Corrected premises

Three items were reported against an understanding of the code that does not
match the code. Designing to the report would have built the wrong thing.

**The system key is already persisted, and backups already carry it.**
`secrets.SystemKey(dataDir, hasWorkspaces)` (`internal/secrets/service.go:272`)
resolves `ROOKERY_SYSTEM_KEY` → `<data_dir>/system.key` → derive-and-persist,
writing the file at `0600`. `internal/backup` puts the same key inside the
encrypted snapshot as `Manifest.SystemKey`, which is what makes cross-machine
restore a single step. Nothing is regenerated on restart and nothing is lost.

Onboarding therefore must **not** print the key and ask the operator to write it
down. A "save this hex" prompt for a file that already exists on disk teaches the
opposite of the truth — that the file is disposable — and creates a second copy
of the install's most sensitive secret in whatever the operator pastes it into.
What onboarding owes the operator is the location of the file, the fact that
`.rkb` snapshots contain it, and the fact that the snapshot passphrase is the one
thing that cannot be recovered.

**The session key is the real defect.** `web/server.go:86` falls back to the
literal `"change-me-in-production-32bytes!!"` whenever `ROOKERY_SESSION_KEY` is
unset. That string is in the public source, so every install that has not set the
variable signs its session cookies with a key an attacker can read. This was not
reported and is the most serious item in the batch. It ships first, alone.

**The rpm shipped a Debian package name.** `.goreleaser.yaml`'s single `nfpms`
entry lists `recommends: [python3, ripgrep, poppler-utils, tesseract-ocr]` for
both formats. Fedora has no `tesseract-ocr`; the package is `tesseract`. `dnf`
drops an unresolvable weak dependency without saying so, which is exactly why a
Fedora install produced `rookery` and `ripgrep` and no OCR. The fix is per-format
`overrides:`, and the assertion belongs in `scripts/smoke-package.sh` so it
cannot regress silently a second time.

## The seven units

| Unit | Scope | Repository |
|---|---|---|
| 0 | Persist the session key | product |
| A | Rookery mark as a workspace image; brand-harmonised presets; mark on login and workspace selection | product |
| B | Website logo: themes correctly, sits closer to the wordmark | rookery-web |
| C | `install.sh` / `install.ps1`; per-format package dependencies | product |
| D | `rookery onboard` | product |
| E | KB table row and column editing | product |
| F | CLI coder detection on macOS and Windows | product |

Unit 0 ships first because it is a security fix. Unit C ships next because it is
fully independent and its rpm half is already diagnosed. A and B are related by
artwork but cannot share a pull request across repositories; B follows the
`docs-sync` cross-repository procedure.

## Decisions inherited by the units

**Unit 0 — persist, do not fail closed.** A generated session key is written to
`<data_dir>/session.key` by the same resolution order `system.key` uses. Failing
to start when the variable is unset would be safer still, but it breaks every
existing install's next restart for a problem the operator did not create. The
one accepted cost of persisting instead is that everyone is signed out once, when
the hardcoded key stops being honoured.

**Unit A — harmonise, do not renumber.** All 30 preset motifs and slugs stay, so
no stored `workspaces.icon` value is invalidated; only the gradient stops change,
re-derived from the ember palette. Eight new presets render the Rookery mark, one
per brand hue, and the amber one becomes what a workspace shows when `icon` is
unset — today that case renders the workspace name's initial on a solid square.
`TestWorkspaceIconSlugsMatchTheSPA` already pins the SPA catalogue against
`web/api_settings.go`'s validator; both move together.

**Unit B — override the component, do not swap the file.** Starlight's
`logo: { src }` renders an `<img>`, and an `<img>` cannot inherit `currentColor`
— which is why a mark stroked in `currentColor` paints black and vanishes on the
dark theme. Starlight's supported `logo: { light, dark }` pair would fix the
colour and nothing else. A `SiteTitle` override inlines the SVG, restoring
`currentColor` and putting the wordmark gap under our own CSS in the same place.
The repository already overrides `PageTitle`, so the pattern is established.

**Unit C — the script installs, `onboard` configures.** `install.sh` and
`install.ps1` fetch the release archive, verify its checksum, place the binary on
`PATH`, probe for `python3`, `ripgrep`, `pdftotext` and `tesseract`, offer to
install whichever are missing, and then hand off by printing `rookery onboard`.
They do not bootstrap an owner or start a service. Keeping configuration in Go
means it is written once and tested, rather than twice in bash and PowerShell,
and it serves the operator who installed from an rpm, a deb or a tarball and
never ran a script at all.

Host tools are **offered, not installed silently**. A script piped from the
network into a shell should not `sudo` four packages without asking. All four
tools are installable on every target: `dnf`/`apt`/`pacman` on Linux, Homebrew on
macOS, and on Windows `winget` covers all four — `Python.Python.3.13`,
`BurntSushi.ripgrep.MSVC`, `oschwartz10612.Poppler` (which ships `pdftotext`) and
`UB-Mannheim.TesseractOCR`. Windows needs one caveat written into the output: a
freshly installed tool may not be on `PATH` until a new shell.

**`curl | sh` cannot work while the repository is private**, because release
assets require an authenticated request. Writing the scripts does not make the
website's advertised command true. The scripts are written and published now, and
the website keeps a note until the repository is public; that is a release
decision, not a design one, and nothing in the scripts has to change when it
flips.

**Unit D — interactive and acting.** `rookery onboard` detects the platform and
performs the setup: bootstraps the owner, ensures the session and system keys
exist and explains where they live, installs missing host tools, creates the
first workspace, configures a coder, and prints the URL. `--non-interactive`
covers scripted installs. On Linux it installs and enables the systemd user unit
and `enable-linger`, both of which already ship in the deb and rpm. On macOS and
Windows it prints how to run the server in the foreground and states plainly that
service registration is not yet built — the honest report, rather than a
half-implemented launchd plist.

**Unit E — direct manipulation.** The slash command opens a size picker; the
inserted table carries hover handles on each row and column with insert and
delete affordances. TipTap already implements every command involved
(`addRowAfter`, `deleteColumn`, and the rest), so this is a control surface, not
a capability. The binding constraint is serialization: `pipeSafeTable.ts` and
`generatorFidelity.test.ts` exist because a round-trip mismatch makes
`checkFidelity` open the note read-only, so every operation must produce the
canonical form. The size picker reuses `SlashMenu.tsx`'s pure `placeMenu` rather
than adding a third floating-UI placement path — that function is pure precisely
because jsdom cannot measure, and a second implementation would be untestable.

**Unit F — fix the gaps, pin them with tests.** Detection cannot be verified from
this host, so the unit claims what it can prove. `exec.LookPath` honours
`PATHEXT` on Windows, so a coder on `PATH` already resolves. The fallback is
where it breaks: `extraDirs` is `~/.local/bin` alone, which misses Homebrew's
`/opt/homebrew/bin` and `/usr/local/bin` and Windows' `%APPDATA%\npm`; and the
`fi.Mode()&0o111 != 0` executable-bit test can never pass on Windows, because Go
synthesizes mode bits from file attributes. macOS adds a third failure: a
launchd-started process inherits a minimal `PATH` that excludes
`/opt/homebrew/bin`, so detection can fail for someone whose terminal finds the
binary without trouble. The fix makes the search path platform-aware and the
lookup injectable, and covers all three operating systems with table-driven tests
that run on Linux in CI. The claim is that the logic is right and pinned, not
that it ran on a Mac.

## Not in scope

Custom workspace image upload stays deferred for the reasons already recorded in
`CLAUDE.md`. launchd and Windows SCM service registration stay Tier 2. Making
the repository public is a release decision this batch prepares for but does not
take.
