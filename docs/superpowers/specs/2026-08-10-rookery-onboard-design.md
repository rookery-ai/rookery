# `rookery onboard` — design

**Date:** 2026-08-10
**Unit:** D of the [onboarding, brand and platform batch](2026-08-10-onboarding-brand-and-platform-batch-design.md)

Installing the binary is the easy half. The half that loses people is everything
after it: which command creates the owner, where the data lives, which of the two
keys matters if the machine dies, and how to make the server come back after a
reboot. `rookery onboard` is that half, and it is one command.

## Why this ships with unit C rather than after it

The umbrella spec listed the installers and `onboard` as separate units. They are
not separable in practice: `install.sh` and `install.ps1` end by printing
`rookery onboard`, and `make docs-sync-check` refuses documentation that invokes
a subcommand no source file declares — correctly, since a README telling a new
user to run a command that does not exist is worse than no README. Shipping C
alone would mean either installers that hand off to nothing, or documentation the
project's own gate rejects. The handoff is the design; it goes in one PR.

## Interactive and acting, not a printed guide

`onboard` performs the setup rather than describing it. A guide is a second thing
to read, and the operator has already read the install docs to get this far.

Two flags cover the cases where acting is wrong:

- `--non-interactive` never prompts and never acts on a prompt. Every step that
  would have asked instead prints what to run, and the closing summary lists what
  was left undone.
- `--yes` consents to everything, for scripted installs.

The distinction that matters is between *skipped* and *silent*. Each skipped step
appends to a `todo` slice that the closing summary replays. A setup that quietly
skipped three things and then printed "Done" has told the operator nothing, and
the missing pieces surface later as unexplained behaviour — an agent that never
fires, OCR that never runs.

A prompt with no terminal behind it is a hang, which is strictly worse than a
skipped step, so `ask` returns false when stdin is not a character device.

## The steps

**Keys.** Both keys are resolved so they exist on disk before anything encrypts
anything with them, and then the step explains which one matters. This is mostly
education, deliberately.

The two are routinely confused, and the confusion is expensive in exactly one
direction. The **system key** (`<data_dir>/system.key`) encrypts connector OAuth
tokens, stored workspace master passwords and bot tokens; losing it means that
data is unrecoverable even with an intact database. The **session key**
(`<data_dir>/session.key`) signs browser cookies and nothing else; losing it costs
one sign-in.

`onboard` explicitly tells the operator **not** to copy either down. Both are
already files, and `rookery backup now` puts the system key inside the encrypted
snapshot — which is what makes moving to new hardware a single step. Printing a
key and asking someone to save it would create a second copy of the install's
most sensitive secret in whatever they paste it into, and would teach the
opposite of the truth: that the file on disk is disposable. What the operator
genuinely cannot recover is the **snapshot passphrase**, so that is what the step
tells them to put in a password manager.

**Owner account.** Explains the two-level model — one owner logs in, then enters
a workspace with that workspace's own master password — before asking for
credentials. An existing owner is reported, along with `owner reset-password`,
rather than treated as an error. The password is read twice, without echo, reusing
`backup_cmd.go`'s existing `disableEcho` rather than adding a second copy of the
same `stty` dance.

**Host tools.** Probes the four, prints *what stops working* rather than just the
name, and offers the install command for the detected package manager. `python3`
is marked `Critical` because its absence is a security property rather than a
convenience: without it the agent-tool AST guardrail self-skips and generated tool
scripts run unchecked.

**Coder.** Reports; does not configure. Choosing a coder means choosing a
provider, a model and an API key, and it is a **per-workspace** decision — there
is no workspace at this point in setup, and inventing one to hold the answer
would put the decision in the wrong place. So the step names any CLI coders on
PATH, notes that the `api` kind needs no binary at all, and points at Settings. A
slim build says so instead.

**Service.** On Linux, writes a systemd user unit, enables it, and runs
`loginctl enable-linger`. Lingering is not optional on a headless box: without it
a user unit stops when the last session closes, so the machine reboots and the
scheduler never comes back — a failure that is invisible until an agent silently
misses its schedule.

The unit is **generated against the running binary**, not copied from the
packaged one. `/usr/share/rookery/rookery.service` hardcodes
`ExecStart=/usr/bin/rookery`, which is right for a deb or rpm and wrong for
everyone who installed via `install.sh` into `~/.local/bin` — copying it there
would enable a service that starts nothing. The packaged file is used verbatim
only when the running binary really is `/usr/bin/rookery`.

macOS and Windows get the honest report: launchd and Windows service registration
are Tier 2 and not built, so the step says so and prints how to run the server in
the foreground. Writing a plist that half works would be worse — a half-working
service is harder to diagnose than none.

## Package boundary

The platform knowledge lives in `internal/onboard`, not in the CLI file, and its
`LookPath` is injectable. Two reasons, both load-bearing:

- The package-name mapping is exactly what shipped wrong in the rpm. It deserves
  a table test, and a table test needs to describe hosts we are not running on.
- `install.sh` and `install.ps1` need the same knowledge. Keeping it in Go means
  one mapping, tested, rather than three copies drifting in three languages.

`InstallCommands` returns a slice rather than one string because winget takes one
package per invocation while every other manager batches — returning a slice
keeps that difference from being special-cased by every caller.

## Testing

Table-driven, running on Linux in CI, over an injected lookup:

- `Missing` reports only absent tools, and nothing when all four resolve;
- `python3` and only `python3` is `Critical`;
- every manager names every tool — a missing entry silently drops that tool from
  the install command, the same failure mode as the rpm's dropped weak dependency;
- the specific divergences are pinned: Fedora's `tesseract` against Debian's
  `tesseract-ocr`, openSUSE's `poppler-tools`, Arch's `poppler` and `python`,
  winget's `oschwartz10612.Poppler`;
- winget emits one command per package and always passes
  `--accept-source-agreements`, or it blocks on a prompt nothing can answer;
- Homebrew is never invoked under `sudo` — it refuses, and forcing it leaves
  root-owned files in the prefix that break later installs;
- `apt-get` is what is probed, not `apt`, or a Debian host reports no package
  manager at all;
- only Linux reports `Managed`, and every other platform carries a `Note` and a
  foreground command;
- the generated unit targets the given binary and data dir, keeps the data dir in
  `ReadWritePaths` (under `ProtectSystem=strict` the server would otherwise start
  and then fail on its first write), and carries an `[Install]` section without
  which `systemctl --user enable` does nothing.

The interactive flow itself is exercised by running `onboard --non-interactive`
against a temporary data directory, and by creating an owner with explicit
credentials — both verified during development.
