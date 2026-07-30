# Identity source of truth: workspace and user context

**Date:** 2026-07-30
**Status:** approved

## Problem

A freshly set-up workspace tells the LLM almost nothing about itself, and what it
does tell it comes from a field the settings UI does not present as authoritative.
Three defects collaborate:

1. **`EnsureScaffold` writes placeholders, not values.**
   `internal/vault/vault.go:203-220` creates `memory/USER.md` and `memory/SOUL.md`
   containing only an H1 and an HTML comment. It never reads the workspace name,
   the workspace "about" text, or any profile field collected by the setup wizard.

2. **`memory.isEffectivelyEmpty` then discards exactly that shape.**
   `internal/memory/memory.go:316` classifies a body of headings plus HTML
   comments as empty, so both scaffolded files are excluded from
   `memory.ContextString()`. The `memory/` folder therefore contributes *nothing*
   to any prompt until the user hand-writes into it, and `profile.ContextString()`'s
   `[User profile]` block silently becomes the de-facto source of truth.

3. **`workspaces.about` reaches no prompt at all.**
   The comment at `internal/db/models.go:22` reads
   `// "what is this workspace about" — injected into LLM context`. It is false.
   The field flows only into `web/api_auth.go` (session DTO) and
   `web/api_settings.go` (settings form). Zero prompt-construction site reads it.
   The one piece of context that says what the workspace is *for* never reaches
   the model.

Two further defects share the same root cause — a description of the knowledge
base duplicated across files that then drifted:

4. **The scaffolded README names files that do not exist.**
   `readmeTemplate` (`internal/vault/readme_template.go`) documents `USER.md`,
   `SOUL.md`, and `GENERAL.md`. `GENERAL.md` is created lazily and only if the
   user runs `/memory add`, so a new workspace's README describes a file that is
   not there. `platformContextBlock` (`internal/prompts/prompts.go:288-292`)
   hardcodes the same list, so the same staleness exists in the agent prompt.

5. **Chat has no product identity.**
   `platformContextBlock` opens with "running inside Rookery" but is agent-only.
   `BuildChatSystemPrompt` opens with "a helpful assistant… an Obsidian-style
   vault of markdown notes". Asked "what is the name of this platform?", the model
   has no answer in context and *infers it from the filesystem path*, then recites
   the absolute host path back to the user. It also describes itself in terms of
   an unrelated third-party product ("Obsidian-style"), and the CLI branch of that
   prompt lists a `reminders/` folder that does not exist — reminders live only in
   the database and are never reflected into the knowledge base.

Additionally, discovered while verifying the above: **neither chat nor agent runs
are told the current date, time, or timezone.** The only `time.Now()` in
`internal/prompts` is in the reminder parser (`prompts.go:1990`). An assistant
that cannot say what day it is cannot reason about anything scheduled.

## Goals

- Exactly one editable location per identity fact, and that location is the one
  the prompt reads.
- Setup values reach the model. A workspace that has completed setup describes
  itself and its owner without further action.
- Existing installs are repaired, not just new ones.
- Chat states what platform it is and what it can and cannot do, without naming
  an unrelated product and without quoting host filesystem paths at the user.

## Non-goals

- Renaming the Go package `internal/vault`, its types, or filesystem paths. The
  word "vault" disappears from prompt text and UI copy only. Renaming the package
  is a large diff with no user-visible payoff and is explicitly out of scope.
- Any change to how reminders are stored or surfaced.
- Multi-user identity. A workspace is the tenant; there is no separate "user"
  record inside one, which is why workspace and personal identity share one file.

## Data model

| Fact | Authoritative location | Editable where |
|---|---|---|
| Workspace name | `workspaces.name` | Settings (structured — it is a UI label) |
| What the workspace is for | `memory/ABOUT.md` | Knowledge base editor |
| Who the owner is (name, email, where they are) | `memory/ABOUT.md` | Knowledge base editor |
| Background / free notes | `memory/ABOUT.md` | Knowledge base editor |
| Tone, language | `memory/STYLE.md` | Knowledge base editor |
| Timezone | `profile_timezone` (DB) | Settings |
| Display name | `display_name` (DB) | Settings |

Rationale for the split: **the database keeps what code reads; markdown is
authoritative for what gets injected.**

- `profile_timezone` must stay structured. `profile.LoadLocation` backs natural-
  language reminder parsing at `internal/gateway/router.go:764,811,863` and the
  session DTO at `web/api_auth.go:66`. You cannot parse "next Tuesday at 3pm"
  against a timezone mentioned in a markdown paragraph.
- `display_name` must stay structured. `web/api_settings.go:147` falls back to it
  for the UI greeting.
- `profile_tone`, `profile_language`, `profile_location`, `profile_email`, and
  `profile_notes` have no programmatic consumer — they are read only by
  `profile.ContextString()`, which this design deletes. They move into markdown.
- `workspaces.about` is kept in the schema and still written once, at setup, to
  seed `ABOUT.md`. It becomes read-only in Settings under the label
  **About Workspace**. Keeping the column costs nothing and preserves the value
  as the seed source for the startup backfill on existing installs.

### `memory/` after setup

```
memory/
  ABOUT.md    # "About This Workspace"
  STYLE.md    # "Communication Style"
```

Two files, both written with real values at setup, both documented in the README
because both exist. `GENERAL.md` remains created-on-demand by `/memory add`, and
the README says so rather than listing it as though it were scaffolded.

`ABOUT.md` shape:

```markdown
# About This Workspace

## Workspace
This workspace is called **Personal**. It is for: keeping my notes, research
and daily journal in one place.

## Who I am
- Name: Peer
- Email: peer@example.com
- Based in: Skopje, North Macedonia

## Background
<the setup wizard's Notes field, verbatim>
```

`STYLE.md` shape:

```markdown
# Communication Style

- Reply in **English**.
- Tone: **short & concise** — lead with the answer, skip preamble, use bullets
  for multi-part answers.
```

Sections whose source value is empty are omitted rather than emitted as an empty
heading — an empty `## Background` would be classified as content by
`isEffectivelyEmpty` and defeat the backfill guard on a later boot.

The tone rendering expands the curated select's short label into a sentence of
guidance, because "short & concise" alone is a label, not an instruction. The
mapping from each `TONE_OPTIONS` value to its expansion lives in Go beside the
renderer, not in the frontend.

## Components

### `internal/memory` — identity rendering and migration (new)

`memory` already owns everything under `memory/`, so identity rendering belongs
here. It stays dependency-free: the caller assembles the values, `memory` renders
and writes them.

```go
// Identity is the full set of values the identity files are rendered from.
// Every field is optional; an empty field omits its line or section.
type Identity struct {
    WorkspaceName  string
    WorkspaceAbout string
    DisplayName    string
    Email          string
    Location       string
    Notes          string
    Tone           string
    Language       string
}

func RenderAbout(id Identity) string
func RenderStyle(id Identity) string

// SeedIdentity writes ABOUT.md and STYLE.md, each only when that file is absent
// or effectively empty. A file with real content is never touched.
func (s *Store) SeedIdentity(workspaceID string, id Identity) error

// MigrateIdentityFiles renames USER.md→ABOUT.md and SOUL.md→STYLE.md, then
// calls SeedIdentity. Idempotent.
func (s *Store) MigrateIdentityFiles(workspaceID string, id Identity) error
```

`RenderAbout` returns `""` when every field it would render is empty, so a
workspace that skipped both the basics text and the profile step does not get a
heading-only file written over it.

`SeedIdentity`'s emptiness test reuses the existing `isEffectivelyEmpty`, which
is exactly the predicate that decides whether a file reaches a prompt. Using the
same function for "is this worth injecting" and "is this safe to overwrite"
guarantees the two can never disagree.

### `internal/vault` — stop writing placeholders

`EnsureScaffold` no longer writes `memory/USER.md` or `memory/SOUL.md`. It keeps
creating the directory tree and the README. Reason: `EnsureScaffold` is called
lazily from `web/api_kb.go:351,565`; if it wrote a placeholder, a knowledge-base
visit would create an empty file that the next boot's backfill then has to
rewrite, giving two writers for one file. The identity writer owns those two
files exclusively.

`readmeTemplate` is rewritten to describe `ABOUT.md`/`STYLE.md`, to say
`GENERAL.md` appears once you use `/memory`, and to use "knowledge base"
throughout. **The current template is appended to `legacyREADMEs`** as part of
this change. That list holds every home note ever shipped, and
`EnsureScaffold`'s upgrade path only replaces a README that byte-matches an
entry; omitting the outgoing template leaves every existing install on the old
text permanently, which is precisely the failure the mechanism exists to
prevent.

### Call sites

- **Workspace creation** (`auth.CreateWorkspace` callers in
  `web/api_workspaces.go`) seeds identity from the name and about text supplied.
- **Setup completion** (step 7, `apiPostSetup` → `MarkWorkspaceSetupComplete`)
  seeds identity from the full set of collected values. Step 7 rather than step 1
  or 4 because either of those may be skipped, and step 7 is the single point
  every path passes through.
- **Startup** — `MigrateIdentityFiles` runs in the existing per-workspace loop in
  `cmd/rookery/main.go:209-241`, after `MigrateLegacyLayout` (which may have just
  created the `memory/` directory) and alongside `MigrateToStructuredFiles`.

### `internal/profile` — runtime context replaces the identity block

`ContextString()` is deleted. `RuntimeContextString()` replaces it, emitting only
what markdown cannot hold without going stale:

```
[Current context]
- Current date and time: Thursday, 30 July 2026, 14:32 (Europe/Skopje)
- Timezone: Europe/Skopje
```

The timezone appears here rather than in `ABOUT.md` deliberately: it is editable
in Settings, so a copy rendered into markdown at setup would go stale the moment
the user changed it. Deriving it per-prompt from the authoritative DB value
cannot drift.

All four injection sites switch from `profile.ContextString()` to
`profile.RuntimeContextString()`: `chat.BuildUserContext`,
`agentrunner.runCoderAgent`, `agentdesigner.Flow`, `skilldesigner.Flow`.

### `internal/prompts` — shared product identity

New `productIdentityBlock(p ProductIdentityParams) string`, consumed by both
`BuildChatSystemPrompt` and `platformContextBlock`, so the product description
exists once. Content:

- Names the platform (Rookery) and what it offers: a knowledge base of markdown
  notes, scheduled agents, skills, reminders, and connected accounts.
- States which surface the model currently is (chat or agent) and what that
  surface can do — for chat: read/search/create/edit knowledge-base notes, web
  search and fetch, act on connected accounts.
- States what the surface cannot do — for chat: run scripts, delete or rename
  notes, create agents or skills, set reminders — and to say so plainly when
  asked rather than improvising.
- Instructs the model not to quote the knowledge base's absolute filesystem path
  back to the user; refer to notes by their path within the knowledge base.

Explicitly absent: the words "self-hosted" (irrelevant to the user and to the
model's behaviour) and "Obsidian" / "vault" (an unrelated product, and a term the
user never sees in the UI).

Three text fixes ride along:

- "Obsidian-style vault" → "knowledge base" in both branches of
  `BuildChatSystemPrompt` and in `platformContextBlock`.
- `platformContextBlock`'s `USER.md`/`SOUL.md`/`GENERAL.md` list → the new file
  names, matching the README.
- The `reminders/` folder is removed from the CLI branch of
  `BuildChatSystemPrompt`. Reminders are DB-only and never reflected.

### Settings and setup UI

- **Settings → Workspace**: `About` becomes read-only, relabelled
  **About Workspace**, with a line pointing at `memory/ABOUT.md` in the knowledge
  base as the place to change it. The name field stays editable.
- **Settings → Profile**: keeps Display name and Timezone. Tone, Language,
  Location, Email, and Notes inputs are removed, replaced by a line stating that
  they live in `memory/ABOUT.md` and `memory/STYLE.md`, with a link to the
  knowledge base.
- **Setup wizard**: unchanged in what it collects. The curated Tone/Language
  selects are good onboarding UX and now actually feed something. Only the
  destination changes.
- `apiSaveSettings` / `apiSaveProfile` stop writing the five prose keys. The
  existing DB rows are left in place, orphaned and unread — deleting them buys
  nothing and forecloses the backfill on an install that upgrades later.
- `PUT /api/v1/settings`'s workspace branch calls
  `db.UpdateWorkspaceMeta(id, name, about)`, which takes both columns. It must now
  pass the workspace's **existing** `About` rather than the request's, or a name
  change would blank the seed value that the startup backfill depends on. The
  request's `about` field is ignored, not rejected — an older SPA build still
  sending it must not start failing.

## Data flow

```
SETUP (once)                      STARTUP (every boot)
  name, about, tone,                per workspace:
  language, location,                 MigrateIdentityFiles
  email, notes                          ├─ rename USER.md → ABOUT.md
      │                                 ├─ rename SOUL.md → STYLE.md
      ▼                                 └─ SeedIdentity (empty files only)
  SeedIdentity                              │
      │                                     │
      ▼                                     ▼
  memory/ABOUT.md ◄──── user edits ────► memory/STYLE.md
      │                (KB editor)            │
      └──────────────┬────────────────────────┘
                     ▼
          memory.ContextString()
                     │
   DB ──────────────►├──► every prompt: chat, agent run,
   (timezone)        │    agent design, skill design
   profile.RuntimeContextString()
```

## Migration

Two phases per workspace, both idempotent, in `MigrateIdentityFiles`:

**Phase 1 — rename.** `USER.md` → `ABOUT.md`, `SOUL.md` → `STYLE.md`, via
`os.Rename`. If the target already exists, log loudly and touch neither file.
A rename rather than a copy: `memory.ContextString` globs `*.md` and sections by
filename, so leaving the old file behind would inject two identity documents and
recreate the exact duplication this design removes.

**Phase 2 — backfill.** Render from the DB and write, but **only when the target
is effectively empty** by `isEffectivelyEmpty`. That is precisely today's state
on every existing install: the scaffolded placeholder, or (as observed on the
live install) an H1 with the comment stripped by a pass through the KB editor.
A file with any real content is never written to, so no user's writing is lost.

`profile_notes` lands in `ABOUT.md`'s Background section during backfill. The DB
key is then left orphaned rather than deleted — it is no longer read, and keeping
it means an install that is restored from an older snapshot still has the value
available to a later backfill.

Second and subsequent boots are no-ops: the files exist and are non-empty.

## Error handling

- Every migration step logs and continues on failure. A workspace whose rename
  fails keeps both files and is reported; it must never abort `serve`, since the
  server is otherwise fully functional without it.
- `SeedIdentity` at setup completion is best-effort: a write failure logs but
  does not fail the setup step. The startup backfill will catch it on the next
  boot, and failing setup over a memory file would strand the user with a
  half-configured workspace.
- `RenderAbout`/`RenderStyle` returning `""` is a normal outcome (nothing to say),
  not an error. `SeedIdentity` skips writing an empty render.
- `profile.RuntimeContextString` degrades to UTC when the stored timezone is
  unset or unparseable, matching `LoadLocation`'s existing behaviour. It never
  returns an error.

## Testing

**`internal/memory`**
- `RenderAbout`/`RenderStyle` golden tests: all fields set; each field empty in
  turn; every field empty → `""`.
- Empty-source sections are omitted entirely, and the rendered output of a
  partially-filled `Identity` is NOT `isEffectivelyEmpty` (otherwise the backfill
  guard would rewrite it every boot).
- `SeedIdentity`: absent file → written; effectively-empty file → overwritten;
  file with real content → byte-identical after the call.
- `MigrateIdentityFiles`: renames both files; skips and logs when the target
  exists; backfills an empty renamed file; leaves a non-empty renamed file alone;
  a second call changes nothing.
- Round-trip: after migration, `ContextString` contains the workspace about text
  and the tone guidance.

**`internal/profile`**
- `RuntimeContextString` includes the timezone and a formatted local time; falls
  back to UTC for `""` and for `"CEST"` (free-text values that `LoadLocation`
  already has to survive).
- `ContextString` no longer exists — compile-time enforcement, no test needed.

**`internal/prompts`**
- The chat prompt (both backend branches) and `platformContextBlock` contain none
  of: `Obsidian`, `self-hosted`, `USER.md`, `SOUL.md`, `reminders/`.
- Both contain the product name and the "do not quote the absolute path"
  instruction.
- The chat prompt states at least one thing the surface cannot do.
- `productIdentityBlock` output appears in both consumers (guards against a
  future edit updating one and not the other).

**`internal/vault`**
- The current `readmeTemplate` is present in `legacyREADMEs`. This is the test
  that stops the next README revision from stranding existing installs.
- `EnsureScaffold` creates no file under `memory/`.
- `isPristineREADME` still accepts every historical template.

**`web`**
- `GET /api/v1/settings` still returns the workspace about text (read-only
  display), and `PUT` no longer persists the five removed prose keys.
- Setup step 7 seeds both identity files.

**`web/ui`**
- Settings Workspace section renders About Workspace as read-only text with a
  knowledge-base link.
- Settings Profile section renders neither a Tone nor a Notes input.

## Accepted costs

- **Tone and language are no longer curated selects after setup.** Changing tone
  post-setup means editing a markdown file rather than picking from a list. This
  is the direct consequence of markdown being authoritative, and it is the point:
  one editable location per fact. The setup wizard still uses the selects, so the
  onboarding path is unchanged.
- **The five orphaned `profile_*` DB rows persist.** Harmless and unread; the
  alternative is a migration whose only effect is deleting data that a later
  backfill might want.
- **A user who has written real content into `USER.md` gets the rename but no
  workspace section.** Correct by construction — we will not edit a file the user
  has written in — but it means the workspace "about" text does not reach the
  prompt for that install until they add it themselves. The README and the
  Settings pointer both tell them where it goes.
