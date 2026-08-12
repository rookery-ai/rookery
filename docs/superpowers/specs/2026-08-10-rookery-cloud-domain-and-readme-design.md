# Domain migration to `rookery.cloud` and README restructure

**Date:** 2026-08-10
**Status:** design, awaiting approval
**Supersedes:** the domain decision in
`docs/superpowers/specs/2026-07-29-rookery-rename-design.md` §Domain

## Problem

Two things, related only by the fact that both land in the same files.

**The domain is gone.** `rookery.sh` was the documented project domain. Someone
else registered it, and it can no longer be acquired. `rookery.cloud` has been
bought and is the replacement. Thirty-four references across two repositories
still name the old domain, and five of them are not documentation — they are
live outbound identifiers sent to third parties on every request.

**The README no longer represents the product.** The website
(`/home/user/rookery-web`) went through a full design pass and reads well: a
hero, a replayed designer transcript, and ten feature sections in a considered
voice. The README is a thin bullet list written before any of that existed. The
two describe the same product and share no structure, no ordering and no
language.

## Decisions taken

Four decisions were put to the owner before this spec was written. All four are
recorded here because three of them cost something, and the reasoning should
survive the change.

| Decision | Chosen | Cost accepted |
|---|---|---|
| Sequencing against the parallel docs-sync session | **Wait for its PR to land** | A wait, in exchange for never hand-merging count fixes that the rewrite would immediately overwrite |
| Image production | **Hand-authored SVG only** | No real application screenshots — this host has no rasterizer and no headless browser |
| Historical spec/plan documents | **Rewrite them too, no exceptions** | The 2026-07-29 rename design will read as though `.cloud` were chosen for reasons that were actually about `.sh`; accepted deliberately so `grep -r 'rookery\.sh'` returns empty |
| README shape | **Site voice + reference tail** | The longest of the three candidates |

The history decision was made against a recommendation to leave the record
intact. It is the owner's call and is implemented as chosen. This spec is the
supersession record: the reasoning in the 2026-07-29 design about weighing
`.com`/`.dev`/`.ai`/`.io`/`.app`/`.org`/`.net` was real and applied to `.sh`.
`.cloud` was not chosen on those grounds — it was chosen because `.sh` became
unavailable after the fact.

## Scope

Three parts, shipping as **three** pull requests. Part A has no dependency on
the parallel session and ships first, split across the two repositories it
touches; Parts B and C wait.

- **Part A — domain rename.** 34 references, 18 files, both repositories.
- **Part B — README restructure.** `README.md` in the product repository.
- **Part C — SVG assets.** A hero banner and one architecture diagram.

## Part A — domain rename

### Inventory

Measured on 2026-08-10, excluding `.git`, `node_modules`, `dist`, `.astro`,
build output and sibling worktrees.

**Product repository — 15 references, 9 files**

| File | Lines | Kind |
|---|---|---|
| `internal/connectors/providers/wikipedia.yaml` | 10 | **live** — outbound `User-Agent` |
| `internal/connectors/providers/openstreetmap.yaml` | 9 | **live** — outbound `User-Agent` |
| `internal/connectors/providers/openlibrary.yaml` | 9 | **live** — outbound `User-Agent` |
| `internal/connectors/providers/openfoodfacts.yaml` | 7 | **live** — outbound `User-Agent` |
| `internal/llm/openai.go` | 61 | **live** — OpenRouter `HTTP-Referer` |
| `README.md` | 66 | docs (swapped in PR 1; the line is later rewritten by Part B) |
| `CLAUDE.md` | 149 | docs |
| `docs/superpowers/plans/2026-07-29-rookery-rename.md` | 991, 1260, 1312, 1347 | historical |
| `docs/superpowers/specs/2026-07-29-rookery-rename-design.md` | 58, 63, 106, 267 | historical |

**Website repository — 19 references, 9 files**

| File | Lines | Kind |
|---|---|---|
| `astro.config.mjs` | 13 | **live** — `site:`, drives canonical URLs and the sitemap |
| `src/components/InstallBlock.tsx` | 21, 24, 32, 35 | **live** — the install commands visitors copy |
| `src/components/Transcript.tsx` | 25 | demo prose |
| `src/content/docs/docs/getting-started/first-15-minutes.md` | 13, 19, 76 | docs |
| `src/content/docs/docs/installation/linux-server.md` | 13 | docs |
| `src/content/docs/docs/installation/macos.md` | 8 | docs |
| `src/content/docs/docs/installation/windows.md` | 10 | docs |
| `README.md` | 3, 119 | docs |
| `docs/website-design-spec.md` | 14, 36, 120, 121, 326 | historical |

### The live identifiers are the part that matters

Four connector manifests and one Go source file send `rookery.sh` to third
parties on every request. Wikimedia, Nominatim, Open Library and Open Food Facts
all block or throttle clients that do not identify themselves, and Wikimedia's
policy expects the URL in a `User-Agent` to be **contactable**. Once `rookery.sh`
belongs to somebody else, these headers point at a stranger's site. That is the
one part of this rename with consequences outside our own documentation.

The existing tests do not pin the domain. Both
`connectors.TestPolicyBoundProvidersSendAUserAgent` (`wave4_test.go`) and
`connectors.TestWikipediaSendsAUserAgent` (`wave2_test.go`) assert only
`strings.Contains(ua, "Rookery")`, so the swap is safe and stays covered.

### Method

A literal `rookery.sh` → `rookery.cloud` substitution. No occurrence needs
rewording: every one is either a bare domain, a URL, or the phrase "the project
domain is". `CLAUDE.md:149`'s surrounding rationale — that OAuth providers reject
redirect URIs on non-public hostnames, so a `.lan` address fails validation — is
about public TLDs generally and holds for `.cloud` unchanged. It is a domain
swap, not a rewrite of the reasoning.

Two consequences to verify rather than assume:

- `astro.config.mjs`'s `site:` field feeds canonical `<link>` tags and the
  generated sitemap. Changing it is required, not cosmetic.
- `.cloud` is an ICANN public suffix, so `internal/publicurl`'s host
  classification treats it exactly as it treated `.sh`. No `redirect_policy`
  YAML changes.

## Part B — README restructure

### Baseline

The rewrite starts from the README **as it exists after the parallel session's
PR merges**, not from today's `main`. That PR corrects two stale counts
(45 → 91 providers, ~272 → ~471 actions). Starting from `main` would mean
re-deriving those corrections by hand and then discarding them.

### Ground truth

Measured from source on 2026-08-10, independently of both the old README and
`CLAUDE.md`. Both were wrong: the README understated providers by half.

| Claim | Value | Source of truth |
|---|---|---|
| Connector providers | **91** | `ls internal/connectors/providers/*.yaml` |
| Curated actions | **471** | `grep -h '^  - name:' internal/connectors/connectors/*.yaml` |
| Bundled core skills | **22** | `ls -d internal/skilllibrary/skills/*/` |
| User-facing CLI commands | **7** | `serve`, `owner`, `backup`, `connector`, `kb`, `healthcheck`, `version` |
| Public `ROOKERY_` variables | **9** | 14 in source, less the 5 subprocess-injected — see gate 4 |

There is no `rookery db migrate`. Migrations apply automatically when the
database opens.

### Structure

Website section order and voice, then the engineering tail the site does not
carry. Section headlines are taken from the site so the two read as one product.

```
[ hero-banner.svg ]
[ badges: license · release · CI · GHCR ]

# Rookery
> Your knowledge grew hands.
Self-hosted AI agents that run on your own machine, around the clock.

## Quickstart            install, bootstrap, serve, first workspace
## What it's like        the replayed designer transcript
## Workspaces            One machine. Sealed, separate worlds.
## Knowledge base        Everything you know, as plain markdown on your own disk.
## Agents                Describe it. Don't configure it.
## Skills                Things your agents already know how to do.
## Connections           91 services. No middleman holding your keys.
## Chat                  Ask what you know. Then have it act.
## Notifications         You find out the moment it happens.
## Models                Your machine. Your model.
## Secrets               Credentials that stay yours.
## Scheduling            Every weekday at eight. And again at ten.

[ architecture.svg ]

## Configuration         the nine public ROOKERY_ variables
## Platform support      the existing four-row table
## Health                /healthz, and why a python3 warning is not cosmetic
## Documentation         → rookery.cloud/docs
## Contributing          branch, Conventional Commits, `make ci`
## License               Apache-2.0
```

This is a **restructure, not a rewrite from zero**. Quickstart, Configuration,
Platform support, Health and License already exist and are accurate; they are
kept and re-ordered. The change replaces the six-bullet "What it does" list with
the site's ten sections.

### The transcript carries an obligation

`Transcript.tsx` is marked, in its own source, as placeholder content that must
be replaced with a **verbatim capture from a real designer build** before launch,
with redaction the only permitted edit — on the grounds that a scripted demo on a
page arguing its claims are checkable would be a demo that lies.

The README's "What it's like" section inherits that obligation exactly. It is
reproduced with an HTML comment recording the same constraint, and both copies
must be replaced from the **same** capture. Replacing one and not the other
would put two different "real" transcripts in front of the same reader.

### Voice rules

- Prose says **Rookery**, capitalised, because it is a proper noun. This is a
  style rule and nothing more. An earlier draft of this spec justified it by the
  `check_cli` gate below; that justification is withdrawn — the gate's prose
  over-match is a defect its author is fixing, and prose should not be bent
  around a checker. Lowercase `rookery` mid-sentence would also be acceptable.
- No `N+` counts. Say 91, not "100+". The site's two "100+ services" claims are
  being corrected to 91 by the parallel session for the same reason.
- Every claim is measured, never copied forward from the old README or from
  `CLAUDE.md`.

## Part C — SVG assets

No rasterizer and no headless browser are installed on this host (`chromium`,
`rsvg-convert`, `inkscape`, `magick`, `convert` all absent), so application
screenshots and SVG→PNG rendering are not available. Assets are hand-authored
SVG, which GitHub renders natively in a README via `<img>`, and which stays
diff-able, tiny and crisp at any size.

Both files live in `docs/assets/` in the product repository.

**`hero-banner.svg`** — the wordmark and `mark.svg` glyph on the brand ground,
with the tagline. Fixed viewBox, roughly 1280×320.

**`architecture.svg`** — the one diagram that earns its place: chat platforms and
the browser arriving at the Rookery binary, which holds workspaces, and from
there the vault on disk, SQLite, the sandboxed coder, and the connector layer
reaching outward. It shows the thing the prose cannot, which is that everything
is inside one process on one machine.

Both use the website's own tokens from `src/styles/brand.css`, so the README and
the site are visibly the same product:

| Token | Value | Role |
|---|---|---|
| `--bone` | `#ece5db` | page ground |
| `--paper` | `#f8f4ee` | raised surfaces |
| `--bark` | `#211d1a` | ink — warm near-black, never pure black |
| `--ember` | `#a94c1c` | accent, one per view |
| `--dusk` | `#46405a` | cool counterweight |
| `--stone` | `#6f6760` | muted text |
| `--line` | `#d9cfc2` | hairlines |

Two constraints on the SVG itself:

- **Live `<text>` with a system font stack, styled by presentation attributes.**
  GitHub does not load remote fonts, so naming Inter would silently fall back to
  whatever the viewer happens to have. Converting the wordmark to outlines is the
  usual answer and is **not available here**: that needs a rasterizer or font
  tooling this host does not have, and hand-authoring eight glyphs as béziers
  with no way to render and check them would be worse than the problem. So the
  files use live text with
  `ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, Helvetica, Arial, sans-serif`
  — no remote dependency, legible everywhere, at the cost of not being Inter
  exactly. Styling is by attribute rather than a `<style>` block, which GitHub's
  SVG sanitiser may strip. If outlining tooling is installed later, converting
  the wordmark is a clean follow-on.
- **The palette is light-committed, matching the site's own decision** to have no
  dark mode on the landing page. Both files paint an explicit `--bone` background
  rather than relying on transparency, so they do not invert or wash out against
  GitHub's dark theme.

## The docs-sync checker — four gates, not one

The parallel session is building `scripts/check-docs-sync.py`, wired into
`make ci`. It gates the README in **four** independent ways. The parallel session
flagged the first; the other three were found by reading the checker and are
equally capable of turning the PR red.

**1 — `CLAIMS` regexes.** Four patterns pinned to exact README wording:

```python
r"reach (\d+) external services"
r"\*\*Connectors\*\* — (\d+) providers"
r"providers, ~(\d+) curated actions"
r"reusable capability documents, (\d+) bundled"
```

The restructure rewords all four sentences, so all four break. **Fix:** update
the `CLAIMS` list in `scripts/check-docs-sync.py` in the same commit, re-pinning
each regex to the new phrasing. Silently deleting the entries is not acceptable —
they exist because the README was wrong by half.

**2 — `check_inflated`.** `INFLATED_NOUNS = (?:services|supported)` rejects any
`N+` count against those nouns. The README must say 91 services, never "100+".

**3 — `check_cli`.** For each of `CLAUDE.md` and `README.md`, every match of
`rookery ([a-z][a-z-]+)` must be a `Name:` string declared in `cmd/rookery`.

As first written this over-matched: the regex cannot tell an invocation from a
sentence, so `rookery reads your vault` would fail on `reads`. It passes today
only because no such sentence exists in either file — and a README rewritten in
the site's voice is precisely a file full of them. Reported to the checker's
author, who agreed it is a defect rather than a hazard to route around and is
**restricting the scan to code contexts** (backtick spans and fenced blocks)
before wiring the gate into `make ci`. That preserves the real detection — the
`rookery db migrate` case that motivated the check lived inside a ```bash fence —
while making prose structurally incapable of false-firing.

**Verify before relying on it.** That fix is a commitment, not yet a landed
commit. At implementation time, confirm the narrowed scan is present in
`scripts/check-docs-sync.py`; if it is not, run `make docs-sync-check` against
the drafted README and treat any prose hit as the checker's bug to fix, not the
README's wording to change. `rookery.cloud` is safe either way — the regex
requires a literal space.

**4 — `check_env`.** Asserts the **website's**
`src/content/docs/docs/operations/configuration.md` documents every public
`ROOKERY_` variable, where "public" is source-derived minus
`scripts/docs-sync-internal-env.txt`. The README is not gated by this one, but
its Configuration table should agree with the website's.

**Fourteen `ROOKERY_` names appear in source: five internal, nine public.** The
internal five — `ROOKERY_BUILD_PHASE`, `ROOKERY_CONNECTOR_URL`/`_TOKEN`,
`ROOKERY_KB_URL`/`_TOKEN` — are *written into* a subprocess environment by the
host and are never operator-set. The distinguishing test is the direction of the
call: an internal variable is assigned into `extraEnv` (e.g.
`agentrunner/runner.go:419`), while a public one is read with `os.Getenv` in
`internal/config`.

By that test **`ROOKERY_CLAUDE_BIN` is public**, not internal — it is read at
`internal/config/config.go:133`, structurally identically to `ROOKERY_HOST`, and
overrides the coder binary path. An earlier draft of this spec listed it as
internal; that was wrong.

This matters for Part B: the README's current Configuration table has **eight**
rows and omits `ROOKERY_CLAUDE_BIN` entirely. The restructured table carries all
**nine**.

## Sequencing

**PR 1a — domain rename (product repository).** Independent of the parallel
session. Ships immediately: the five live identifiers are the urgent part. It
does swap `README.md:66`, even though Part B later rewrites that sentence away —
PR 2 waits on an external merge, and leaving a stale domain in the README until
then to save one line of churn is the wrong trade.

**PR 1b — domain rename (website repository).** Independent of PR 1a. It
touches `astro.config.mjs`, which the parallel session will also edit (a
sidebar entry, a different line) — a trivial conflict, expected rather than
discovered.

**PR 2 — README restructure and assets.** Branches after the parallel session's
PR merges. Carries the README, both SVGs, and the `CLAIMS` update, together —
the rewrite and the checker have to land in one commit or `make ci` is red
between them.

Two repositories means two of the three pull requests belong to Part A (1a and
1b); those two are independent of each other and ship immediately, and neither
blocks PR 2.

## Coordination

The parallel session (`sync documentation with rookery-web`) owns, until its PR
merges: `src/pages/index.astro` lines 380 and 395, `reference/connected-services.md`,
a new `reference/api.md`, and the `astro.config.mjs` sidebar entry. It has
confirmed it is not making domain changes and has no further README work planned.
Ownership of `README.md` and of the rename is ours.

## Verification

- No live or reader-facing surface names the old domain — code, config, shipped
  documentation, published repository metadata — with this plan and this spec
  themselves exempt, since naming the old domain is what makes their sentences
  mean anything. Verified with two checks: a literal `grep -rn 'rookery\.sh'`
  (excluding `.git`, `node_modules`, `dist`, `.astro`, sibling worktrees, and
  the two 2026-08-10 migration documents) returning nothing, and a
  `grep -n '\.sh\b'` sweep over `docs/` and the brand assets — filtered to
  ignore genuine shell-script names — returning nothing beyond those. This is
  the owner's stated bar, and it is why the historical documents are in scope.
- `go test ./... -count=1` passes, in particular the two `User-Agent` tests.
- `make ci` passes, including `make docs-sync-check` once the parallel session's
  PR has landed.
- `npm run build` succeeds in the website repository and the generated sitemap
  carries `rookery.cloud`.
- Every count in the README is re-measured against source at implementation
  time, not copied from this spec.
- The Configuration table lists exactly the nine public `ROOKERY_` variables,
  cross-checked against the website's `operations/configuration.md` so the two
  cannot drift.
- `check_cli`'s narrowed, code-context-only scan is confirmed present before the
  README's prose is judged against it.
- Both SVGs render in GitHub's light and dark themes without a font dependency.

## Out of scope

- **`install.sh` / `install.ps1` do not exist yet.** The website already
  publishes both commands and the README will too. Writing the installers, and
  serving them from the domain, is release-engineering work — deferred, per the
  original website design spec, until the repository is public.
- **Registering or configuring DNS for `rookery.cloud`.** Owner-side.
- **The website's own visual design.** It is good; this changes strings in it,
  not layout.
- **Application screenshots.** Blocked on tooling this host does not have. If
  the README should carry real captures later, that is a follow-on needing a
  headless browser install.
- **`ROOKERY_PUBLIC_URL` defaults in code.** The variable has no default and
  gains none; the domain appears in documentation as an example only.
