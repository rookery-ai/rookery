# Documentation and website sync

**Date:** 2026-08-10
**Status:** implemented, merged
**Repositories:** `ilijad1/rookery` (product), `ilijad1/rookery-web` (website)

## The problem

Documentation about Rookery lives in four places, and nothing keeps them
honest:

| Surface | Repository |
|---|---|
| `README.md` | product |
| `CLAUDE.md` | product |
| Documentation site (24 pages) | website |
| Landing page | website |

Every one of them is written by hand, and every one of them can be wrong
without anything failing. They already are. Measured on 2026-08-10, against the
source in the same commit:

| Claim | Where | Actual |
|---|---|---|
| "reach 45 external services" | `README.md:6` | 91 |
| "45 providers, ~272 curated actions" | `README.md:45` | 91 providers, 471 actions |
| Zoom listed among the providers | `CLAUDE.md:260` | removed; no `zoom.yaml` exists |
| `./bin/rookery db migrate` documented | `CLAUDE.md` | no `db` or `migrate` subcommand is registered |
| "100+ services" | `rookery-web/src/pages/index.astro:380` | 91 |
| 124 brand logos | product `web/ui/src/assets/logos/` | website carries 126 |

The last of those is the sharpest illustration: the website correctly states
that there is no separate migration command, while `CLAUDE.md` gives the command
line for one. Two surfaces contradict each other outright, and the surface that
is wrong is the one an agent reads first.

The two numeric errors point in opposite directions — the README understates the
product by half, the landing page overstates it — which is the signature of
hand-maintained prose rather than a single bad edit. The website's own README
records this class of failure happening once before, and it happened again
anyway.

The website repository already states the correct rule: *"Every factual claim in
the documentation is verified against the product's source at the time of
writing."* That rule has no mechanism behind it. This design supplies one.

## Approach

Four layers, ordered from cheapest to most reliable. Each does what it is
actually good at; none of them tries to do the others' work.

### 1. `docs-sync` skill (user level)

`~/.claude/skills/docs-sync/SKILL.md`.

Owns the procedure. Invoked at finish time, when the work is done and the
context is still loaded: diff against `main`, run the changed paths through the
trigger map, update the product's own documentation, then update the website in
its own worktree and open a pull request there.

It lives at user level rather than in the repository because `rookery/.gitignore`
ignores `.claude/` outright, and that line stays. User-level skills load
regardless of working directory, so this one is available inside the git
worktrees where the work actually happens. The cost is that it is not versioned
with the code and does not reach anyone else who clones the repository —
acceptable for a single-owner project, and the reason the *checkable* half of
this design lives in the repository instead.

### 2. Path-guarded hook

`~/.claude/hooks/rookery-docs-sync-gate`, registered as a `PostToolUse` hook in
`~/.claude/settings.json` matching `Edit|Write|MultiEdit`.

The matcher selects on tool name, so the script does the real filtering: it
reads the tool input, and exits silently unless the edited path is under the
product repository *and* matches a trigger path. When it does match, it emits
one line naming the affected website pages.

Silence is the default, and any failure is silent too — the same discipline as
the existing `cbm-code-discovery-gate` hook, which never blocks a call and exits
0 on every error path.

The hook is **best-effort, and its silence proves nothing**. An edit made
through `Bash` — a `sed` invocation, a Python one-liner — never matches an
`Edit|Write` matcher and produces no reminder. The hook exists to catch the
common path cheaply, not to be the gate; that is layer 3's job, and it is the
reason layer 3 is the one wired into `make ci`.

A blanket `Stop` hook was rejected. It would fire on every Go edit in the
repository, become noise within a week, and be disabled — which is worse than
not having it, because the disabling is invisible.

### 3. `scripts/check-docs-sync.py`

Tracked in the product repository, exposed as `make docs-sync-check` and wired
into `make ci`. This is the only layer that cannot be forgotten, and it is
deliberately the one that lives in version control: it is a documentation
accuracy check, not agent tooling. It shipped as Python rather than the
originally-planned Bash: set arithmetic and frontmatter handling are where a
checker like this dies in Bash.

It asserts seven things.

**Claims table.** An explicit list of `(file, regex with one capture group,
derived value)`. Each entry pins one number in prose to the source it describes.
On mismatch it prints the file, the line, the claimed value and the real one.
This is the check that would have caught 45-against-91.

Initial entries:

| File | Claim | Derived from |
|---|---|---|
| `README.md` | external services | `internal/connectors/providers/*.yaml` |
| `README.md` | providers, curated actions | providers YAML, `- name:` in `connectors/*.yaml` |
| `README.md` | bundled skills | `internal/skilllibrary/skills/*/` |
| `CLAUDE.md` | providers, actions (two sites) | as above |
| `rookery-web` `index.astro` | services | providers YAML |
| `rookery-web` `concepts/skills.md` | built in | skills directory |

The scan reads frontmatter as well as body. The skills claim lives in the
`description:` field of `concepts/skills.md`, not in its prose, and a body-only
scan would silently match nothing and pass.

**Provider name coverage.** `reference/connected-services.md` states no count —
it enumerates services by name in prose sections, which is a better page for it.
So the assertion there is coverage, not arithmetic: every provider in
`internal/connectors/providers/` is named somewhere on the page. What shipped
for the removal half is narrower than "no name on the page lacks a provider":
a hand-maintained `REMOVED_PROVIDERS` set (`{"Zoom", "Fitbit"}`) is checked
against prose in `CLAUDE.md`, `README.md`, and the services page, with an
exemption for a sentence that itself narrates the removal (so stating "Zoom
was removed" doesn't trip the same check it satisfies). This is how Zoom
survived in `CLAUDE.md` after its YAML was deleted. The cost is real: the set
is hand-maintained, so the next provider removal is uncovered unless someone
remembers to add its name to it.

**Inflated approximations.** Any claim of the form `N+` where `N` exceeds the
real count fails. "100+ services" against 91 is false in the direction that
matters, and a regex pinned to an exact number would not catch it.

**Environment variables.** Enumerate `ROOKERY_*` from Go source, subtract an
explicit internal-only allowlist, and assert every survivor is documented in the
website's `operations/configuration.md`.

The allowlist is required, not a convenience: source declares 14 variables and
the documentation lists 9, and the 5-variable difference is correct. Those five
— `ROOKERY_BUILD_PHASE`, `ROOKERY_CONNECTOR_URL`, `ROOKERY_CONNECTOR_TOKEN`,
`ROOKERY_KB_URL`, `ROOKERY_KB_TOKEN` — are injected by the runtime into coder
subprocesses and are not set by an operator. A naive diff would report five
false positives on its first run and be switched off. The allowlist is a file
with a one-line reason per entry, so adding a genuinely user-facing variable
cannot be waved through by appending to it thoughtlessly.

**README environment table.** `check_readme_env_table` asserts `README.md`'s
own configuration table lists every public `ROOKERY_*` variable. It was added
after the table shipped with 8 rows where 9 were needed, omitting
`ROOKERY_CLAUDE_BIN` — a missing row, which the count-based assertions above
cannot catch because they check documented names against source, not a
specific table's completeness.

**CLI commands.** Every subcommand registered in `cmd/rookery` has a heading in
the website's `reference/cli.md`.

**Brand logos.** Every connector provider has an SVG in
`rookery-web/src/assets/logos/`. Set equality is not asserted: the website
legitimately carries logos the product does not, for the model providers shown
on the landing page.

**Skip semantics.** Website assertions run only when the website repository is
present. When it is absent the check prints `SKIP: rookery-web not found` and
passes. `make ci` runs in an environment that has no second checkout, and a gate
that depends on a repository it cannot see is a gate that gets removed.

### 4. `CLAUDE.md` pointer

A short section, pointer-shaped, naming the trigger map's existence and the
skill that implements it. It stays short on purpose: `CLAUDE.md` is already
dense, and a long new section is a skimmed section.

This placement is load-bearing. The superpowers plugin is installed at a
version-pinned path (`superpowers/6.2.0/`); an edit there is silently discarded
on the next upgrade. `using-superpowers` states that `CLAUDE.md` takes
precedence over skills, so a rule stated here outranks the plugin's workflow
skills without modifying any of them.

## The trigger map

The load-bearing artifact. "Update the documentation too" is unactionable; this
is a lookup.

| Change in the product | Update in the website |
|---|---|
| `internal/connectors/{providers,connectors}/*.yaml` added or removed | `reference/connected-services.md`, service count in `index.astro`, `LogoWall.astro`, `src/assets/logos/<provider>.svg` |
| `coder.APIProviders()` | `getting-started/choosing-a-model.md`, `concepts/models.md`, `ModelChips.tsx` |
| A `ROOKERY_*` variable added, renamed or given a new default | `operations/configuration.md` |
| A subcommand added or changed in `cmd/rookery` | `reference/cli.md` |
| `internal/skilllibrary/skills/*` | `concepts/skills.md` |
| A chat adapter added in `internal/gateway` | `concepts/notifications.md` |
| A backup `Destination` added | `concepts/backup-and-restore.md` |
| `.goreleaser.yaml`, `packaging/`, `Dockerfile` | `installation/*.md` |
| `/api/v1` routes added or removed | `reference/api.md` |
| A user-visible SPA feature | the matching `concepts/*.md` |
| A new website page | the sidebar in `astro.config.mjs` — navigation is hand-maintained |

Every row also updates the product's own `README.md` and `CLAUDE.md` where they
make a claim the change invalidates. The product side is not an afterthought:
it holds the oldest error found, and `CLAUDE.md` carries a provider list that
still names a provider deleted two releases ago.

## Workflow

On finishing a piece of work, the skill:

1. Diffs the branch against `main` and maps the changed paths.
2. Stops if nothing maps, reporting "no documentation-facing change". Most
   commits are in this class and must cost nothing.
3. Updates `README.md` and `CLAUDE.md` on the product branch.
4. Adds a git worktree inside the website repository and works there — never in
   the checkout directly. The website repository is the user's own working copy
   and may hold uncommitted edits; a worktree makes it impossible to sweep them
   into a sync commit.
5. Runs `make docs-sync-check` with both repositories present. A failing check
   blocks the pull request.
6. Pushes and opens a pull request in the website repository, with a
   Conventional Commit title matching that repository's existing convention,
   linking to the product pull request.
7. Reports both pull request URLs.

Neither repository's `main` is ever committed to directly.

## Release-time sweep

The website pins no version. It uses `<version>` placeholders and links to the
releases page, which is a deliberate and correct choice — there is no version
string to keep in sync.

The useful form of a release trigger is therefore not a per-change rule but a
sweep: when release-please cuts a release, walk the generated changelog for
user-visible entries and confirm each has a home in the documentation. This
catches the case the per-change map cannot — a feature that shipped without ever
touching a mapped path.

## New page: `reference/api.md`

The `/api/v1` row has no target today; `reference/` holds only `cli.md` and
`connected-services.md`. The page is generated from the `want` table in
`web/api_parity_test.go`, which is already the authoritative route inventory and
already a merge gate, so it cannot silently fall behind the server.

It ships with an explicit caveat that the API is the SPA's own backend and may
change between releases. Publishing a route list implies a stability promise;
the caveat is what withholds it. Without the caveat this page should not be
published at all.

## One-time reconciliation

The mechanism proves itself on its first run by fixing what already drifted:

- `README.md:6` — 45 external services becomes 91.
- `README.md:45` — 45 providers and ~272 actions becomes 91 and ~471.
- `CLAUDE.md:260` — Zoom leaves the provider list.
- `CLAUDE.md` — the `db migrate` command is removed, and the automatic-on-open
  behaviour stated instead, matching `reference/cli.md`.
- `index.astro:380` — "100+ services" becomes a claim that is true.

The logo difference needs no reconciliation: the website's two extra files are
`claude.svg` and `cursor.svg`, coder marks shown on the landing page that have no
connector provider behind them. This is why the logo assertion is coverage
rather than set equality.

**Reconciliation lands before the gate.** The claims table asserts `README.md`'s
provider count, and `README.md` is wrong today. Wiring `make docs-sync-check`
into `make ci` first would turn the next unrelated pull request red for
pre-existing drift, and the cheapest way out of that is to weaken the assertion —
which is how a gate dies in its first week. Reconciliation commit first, `make
ci` wiring second.

## What this does not do

- **Generate prose.** The check verifies facts; sentences stay hand-written. A
  generated documentation site would solve drift by making the documentation
  worse.
- **Copy `InterVariable.woff2`.** The font's cross-repository obligation is an
  asset concern, tracked separately from documentation sync.
- **Touch the brand identity specification.** It stays in the product
  repository by an existing decision, because it documents code that ships from
  there.
- **Modify the superpowers plugin.** Its install path is version-pinned and any
  edit is lost on upgrade.
- **Verify prose accuracy.** Nothing here can tell whether a paragraph describes
  the feature correctly. It can only tell whether the numbers, variable names,
  command names and logo files agree with the source. That is the class of error
  that has actually occurred.
