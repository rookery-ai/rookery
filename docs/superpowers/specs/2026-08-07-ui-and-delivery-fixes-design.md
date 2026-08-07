# Agent identity in chat, uploads consolidation, owner-settings polish, and a collapsible toggle

**Date:** 2026-08-07
**Status:** Approved design

Eleven reported defects across four subsystems: chat-platform delivery (agent identity,
silent-run completion), the vault's upload folders (`files/`, `assets/` → one `uploads/`),
the React SPA's icon/wizard/owner-settings surfaces, and the knowledge-base toggle list,
which has never actually collapsed.

Ten are self-contained. **Item 3 is not** — it renames an on-disk vault directory whose
path is embedded twice in every imported note, so it carries a migration and is sequenced
as its own phase with its own tests.

---

## Phase A — chat delivery

### 1. Agent output carries no identity

#### Diagnosis

`GatewayManager.SendToUser(workspaceID, text)` (`internal/gateway/gateway.go:278`) is
agent-blind *by signature* — it is the shared sink for reminders, the scheduler and the
approval service alike. Nothing on the delivery path names the agent, so two agents
notifying the same user are indistinguishable.

`runCoderAgent` has three `SendOutput` call sites (`internal/agentrunner/runner.go:456`,
`:483`, `:493`) with `agent` in scope, which looks like the obvious choke point. **It is
the wrong one.** `runner.go:653` reuses `SendOutput` as a *collector* for child-agent
recursion:

```go
SendOutput: func(msg string) { childChat = append(childChat, msg) },
```

That collection is fed to `prompts.BuildChildAgentFollowUpPrompt` — i.e. into the parent
agent's **LLM prompt**. Prefixing inside the runner would inject `🤖 **name**` into model
input, not just chat. The same objection applies to `recordInbox` (inbox rows already
carry `AgentName`, `internal/db/models.go:168`) and to `OnProgress` (the web SSE view is
already scoped to one agent's page).

There is **no icon field on `db.Agent`** (`models.go:61-69`). `db.Workspace.Icon` exists,
but adding a per-agent equivalent means a migration, an API field and a picker UI.

#### Fix

Prefix at the three sites where `SendOutput` *is* the real chat sender, leaving the
collector, the inbox and the SSE stream untouched:

| Path | File |
|---|---|
| chat `/run` | `cmd/rookery/main.go:473` |
| cron | `internal/scheduler/scheduler.go:121` |
| web "Run Now" | `web/run_tracker.go:66` |

A single exported helper owns the format so the three sites cannot drift:

```go
// internal/gateway/identity.go
func AgentPrefixed(agentName, text string) string
```

producing neutral CommonMark — `🤖 **<name>**\n\n<text>` — composed **upstream** of
`render.For(platform)`. The router emits neutral CommonMark and each adapter renders on
send (Telegram goldmark→MarkdownV2, Discord passthrough, Slack mrkdwn); emitting
platform-specific markup here would break Telegram escaping.

**Assumption, stated because the request is ambiguous:** `🤖` is a fixed emoji for every
agent. The user asked for `<Agent_smiley> <Agent name>`; the discriminator they need is
the *name*, and the emoji is decoration. A per-agent icon is a schema change well beyond
this batch and is explicitly not built here.

`runner.go:491`'s warning already embeds the name (`"⚠️ %s ran but produced no
notification"`) and would read `🤖 **weather** … ⚠️ weather ran but…`. Drop the name from
that string; the inbox copy loses nothing because `InboxMessage.AgentName` is a column.

#### Test

`internal/gateway/identity_test.go` — the prefix is applied once, is valid CommonMark, and
does not double-prefix. A runner test asserts the child-agent collector receives
**unprefixed** text (the regression that matters most, since it is invisible in chat).

---

### 2. A silent run leaves "Running agent …" dangling

#### Diagnosis

`internal/gateway/router.go:565` posts `Running agent **%s**...` then calls `onAgentRun`.
When the coder emits `[SILENT]`, `runCoderAgent`'s switch (`runner.go:480-497`) has **no
matching case** — nothing is sent, nothing recorded, nothing streamed, and `Run` returns
`nil`. The comment at `runner.go:528` confirms it: *"Silent ([SILENT]) runs never reach a
SendOutput site, so they post nothing."* The user is left on "Running agent…" forever.

All three adapters **do** support editing (`telegram.go:184`, `discord.go:449`,
`slack.go:431`), and `updatePlaceholder` (`gateway.go:487`) already *creates* a placeholder
when none exists — behaviour pinned for a slash command by
`placeholder_test.go:163`. CLAUDE.md's "mandatory delete" describes adapter capability,
not a policy, and is not a blocker.

The reason the message cannot be edited *today* is plumbing, in three parts: `/`-prefixed
messages never get a placeholder (`gateway.go:442`), `send` discards `m.Send`'s result
(`gateway.go:517`), and `sendProgress` is wired only to `handleText` (`router.go:190`),
never to `handleRun` (`router.go:178`).

#### Fix

Route `sendProgress` into `handleRun`, emit the running notice through it (creating the
placeholder), and wrap `send` to record whether anything was delivered:

- run produced output → `send` edits "Running agent…" into the result (one message, not two)
- run was silent → edit the placeholder to `✅ **<name>** finished — no notification.`
- run failed → the runner already delivered `FriendlyRunError` via `SendOutput`; **return
  `nil`** so `gateway.go:586` does not append `An error occurred: …`

That last clause fixes a real adjacent bug found during investigation: a failed `/run`
currently posts its error **twice**, and it is literally what follows "Running agent…"
today.

**Scope, deliberately narrow:** this applies only to a *chat-initiated* run, where a
"Running…" message is orphaned. Scheduled runs are untouched — `[SILENT]` suppression is
the entire point of a quiet cron agent, and making those start talking would be a
regression, not a fix.

**Accepted degradation:** `updatePlaceholder` no-ops for a gateway that is not a
`TypingGateway`. All three shipped adapters implement it; only test fakes do not, and
those are updated.

#### Test

`internal/gateway/` — a silent run edits the notice to a completion line; a failed run
posts exactly one message; a producing run does not leave a stray "Running agent…".

---

## Phase B — one uploads folder

This phase is sequenced alone. It renames an on-disk directory and rewrites note content.

### 3. `files/` → `uploads/`

#### Diagnosis

`vault.FilesDir = "files"` (`internal/vault/import.go:17`) is the single source of truth
and has exactly one writer (`import.go:172`). Every ingest door funnels through
`vault.ImportFile`, so "all uploads, whether from chat app or web UI" is **one call site,
not five**:

| Door | Entry | Original lands |
|---|---|---|
| Web KB upload | `web/api_kb.go:1155` | `files/` |
| Web chat attachment | same endpoint | `files/` |
| Telegram / Discord / Slack | `internal/gateway/router.go:259` | `files/` |
| Coder bridge `convert` | `internal/vault/bridge.go:173` | `files/` |
| `save_to_kb` (incl. URL fetch) | `internal/coder/hosttools.go:854` | `files/` |
| KB **asset** upload (editor images) | `web/api_kb.go:180` | `assets/` — **bypasses `ImportFile`** |

`files/` is absent from `EnsureScaffold`, so it is created lazily and a migration must
tolerate "never existed".

The rename is not just a directory move. `renderImportedNote` (`import.go:224-255`)
embeds the original's path **twice** in every imported note — `original_file: "files/…"`
in frontmatter (`:236`) and a `[x.pdf](files/x.pdf)` body link (`:249`). Renaming without
rewriting orphans both in every existing note.

#### Fix

`FilesDir = "uploads"`, plus `MigrateFilesToUploads` in `internal/vault/migrate.go`,
modelled on `MigrateSessionsToChats` (`migrate.go:172`) — the existing precedent that does
*both* halves, dir rename and content rewrite — including its `drainInto` collision
handler, which never clobbers.

The content rewrite is **scoped to the two emitted patterns**, not a blind global replace:
`original_file: "files/` and `](files/`. A blind replace would corrupt a user's prose.

Redirect the asset upload (`api_kb.go:180`) to `uploads/` as well, so editor images are
"uploads from the web UI" too. `assetName` already appends 4 random bytes
(`api_kb.go:116`), so it cannot collide with `ImportFile`'s `uniquePath` originals.

`kbExcludedDirs` (`internal/vault/kbcontext.go:53`) uses the constant and follows
automatically.

#### Test

`internal/vault/migrate_test.go` — rename happens; `original_file:` and the body link are
rewritten; a note containing the literal word "files/" in prose is **untouched**; the
migration is idempotent and a no-op when `files/` never existed; a pre-existing `uploads/`
drains without clobbering.

---

### 4. `assets/` is confusing and should not be displayed

#### Diagnosis

One writer creates root `assets/` — the asset endpoint (`api_kb.go:180`), a bare string
literal with no constant. It is **not** in `EnsureScaffold`; `WriteNote`'s `MkdirAll`
creates it lazily.

Hiding it is safe: notes store the portable path `![alt](assets/foo.png)`, which resolves
through `/kb/raw` → `vault.Resolve`, never through the tree listing. The image picker uses
`vault.ListImageFiles`, an independent walk. So neither breaks.

Skills have their own nested `skills/<id>/assets/` (`internal/skillstore/skillstore.go:138`).
**Any hide must be root-level only** or it hides those too.

#### Fix

Filter root-level `assets` in the two places a folder surfaces:

- `apiKBTree`'s node loop (`web/api_kb.go:566`), following the existing root-only idiom
  `isRoot := rel == ""` (`api_kb.go:582`)
- `apiKBFolders` (`api_kb.go:347`), so it also stops being a move/create destination —
  otherwise it is half-hidden

**Existing `assets/` files are deliberately left in place and not migrated.** Every
existing note references them as `![](assets/foo.png)`; those links keep resolving. New
uploads go to `uploads/` (item 3), so the folder becomes inert legacy. Rewriting image
references across all user notes is the one change in this batch with real corruption
risk and no user-visible upside — the folder is hidden either way.

Hiding also closes a latent hazard: `assets` is `System: true` (`api_kb.go:446`) but absent
from both `protectedTopDirs` and the SPA's `PROTECTED_TOP_DIRS`, so a user can currently
rename or delete it and orphan every image link.

#### Test

Go: root `assets` is absent from the tree and the folder list; `skills/x/assets` is
**present**; `uploads` is present.

---

### 5. "Uploads" with a capital U

#### Diagnosis

`kbSystemFolderLabels` (`web/handlers_kb.go:39`) capitalises notes/memory/skills/agents/chats
for display while on-disk names stay lowercase — presentation only, applied at root
(`handlers_kb.go:59`). `files` was never in the map, which is exactly why it rendered
lowercase.

#### Fix

Add `"uploads": "Uploads"`. No case-rename migration: the on-disk name stays lowercase
`uploads`, matching every other system folder.

`kbSystemDirs` is deliberately **not** changed — adding `uploads` there would set
`System: true` and drag-lock the folder, a behaviour change nobody asked for.

#### Test

A handler test asserts the root node for `uploads` carries `DisplayName: "Uploads"` while
`Name`/`Path` stay lowercase.

---

## Phase C — SPA fixes

### 6. Agents/Chats/Skills are dimmer than Memory/Notes

#### Diagnosis

`web/ui/src/pages/kb/FileTree.tsx:675` applies `isEffectivelySystem(node) && "text-muted-2"`.
The predicate (`:53`) is `node.system && node.name !== "memory"`; the server marks
`agents, chats, memory, skills, assets` as system (`api_kb.go:444`) and `notes` was never
in that set. So Agents/Chats/Skills render muted while Memory and Notes do not. Lucide
glyphs inherit `currentColor`, so icon and label mute together.

#### Fix

Delete **only** the `text-muted-2` clause at `:675`.

The predicate itself must not change: it also gates drag-reorder (`:509`), move-into
(`:512`) and reorder persistence (`:844`) for DB-backed folders. Rename/Delete protection
is a *separate* predicate (`isProtectedPath`), so the colour fix cannot expose destructive
actions. `FolderPage.tsx:41` already hard-codes `system: false`, so the folder *page* is
unmuted today — the tree is the only mismatch.

#### Test

`tree.test.tsx` — no root folder row carries `text-muted-2`; dragging onto `agents/` is
still refused.

---

### 7. Two icons on one button

#### Diagnosis

`components/ui/button.tsx` has **no** auto-icon behaviour — it is stock shadcn cva. Both
duplicates are two literal icons authored into one button: a lucide element plus an emoji
baked into the label string.

- `DesignerSurface.tsx:721` — `<Hammer />` + `labels.buildButton` = `"🔨 Build it"`
- `DesignerSurface.tsx:734` — `<Save />` + `"✅ Save agent"` (same bug, unreported)
- `SetupWizard.tsx:171` — `<ArrowRight />` + `"Continue →"`

Labels come from three constant files: `AgentNewPage.tsx:33`, `AgentEditPage.tsx:21`,
`SkillNewPage.tsx:30`.

The lucide element is correct: `button.tsx:8` states *"Every ACTION button carries a
leading lucide icon"*, and `entityIcons.tsx` records *"lucide only"* — its header describes
this exact bug class from the settings-page emoji cleanup.

#### Fix

Strip the emoji/text glyph from the label strings, keeping the lucide element. Also clean
the milder same-class cases carrying a stray `→`: `SetupWizard.tsx:255`, `:418`, and
`ServiceWizard.tsx:377`, `:568`.

`SetupWizard.tsx:95`'s `BackBar` uses a raw `<button>`, so neither `gap-2` nor the
`[&_svg]:size-4` rule applies and `<ArrowLeft />` renders at lucide's default 24px beside
13px text. Convert it to `Button variant="link"`.

**Blast radius:** the lucide `<svg>` contributes nothing to the accessible name, so
stripping the emoji changes it from `"🔨 Build it"` to `"Build it"` and breaks
`edit.test.tsx:111` and `designer.test.tsx:71`. Both are updated.

#### Test

A test asserts no button label string in the SPA begins with an emoji or contains a
trailing `→`, so the class cannot return.

---

### 8. The setup stepper wraps to two lines

#### Diagnosis

The wizard is **not** inside `PageContainer` — it is a standalone card,
`SetupWizard.tsx:821`: `max-w-xl` (576px) with `p-8`, leaving **512px** of content.

Measured chrome for five steps: circles 5×20 = 100, inner gaps 54, four connectors
(`w-4` + `mx-1`) 96, list gaps 16 — **266px**, leaving ~246px for 41 label characters at
`text-xs`. `index.css:165` raises `--text-xs` from 12px to 13px, widening labels a further
~8%. This is a genuine overflow with margin, not a hairline, so the wrap is reliable; the
`flex-wrap` on the `<ol>` is what permits it.

Because the connector renders *inside* the preceding `<li>`, the first row ends with a
dangling line.

#### Fix

Widen the card to `max-w-2xl` (672px → 608px content) and shorten `"Master password"` to
`"Password"`. Together that takes the text budget from ~246px to ~342px for ~34
characters — comfortable margin rather than a fix that is one label away from regressing.
The user explicitly sanctioned widening.

#### Test

`SetupWizard.test.tsx` asserts the stepper container is not `flex-wrap`-dependent — i.e.
the labels+chrome budget is asserted numerically, since jsdom cannot measure layout.

---

### 9. Both "Congratulations" buttons go home

#### Diagnosis

**The buttons are correct.** `DoneScreen` (`SetupWizard.tsx:710`) passes `/agents/new` and
`/kb`, both real routes (`router.tsx:108`, `:110`).

The redirect comes from the only `<Navigate to="/" replace />` in the chain —
`RequireSetupWorkspace`, `router.tsx:82`:

```jsx
if (!session.workspace?.needs_setup) return <Navigate to="/" replace />;
```

`finish()` (`SetupWizard.tsx:805`) creates exactly the window that trips it:

```js
await api.post("/api/v1/setup", { step: 7 });
await qc.invalidateQueries({ queryKey: ["session"] });   // resolves AFTER refetch
nav(target);                                             // too late
```

`needs_setup` is already `false` while `/setup` is still the matched route, so the guard
fires before `nav(target)` runs. This is target-independent — which is precisely why
*both* buttons land on home.

#### Fix

Navigate before invalidating (or do not await the invalidation).

#### Test

The existing tests (`SetupWizard.test.tsx:463`, `:482`) assert the right destinations and
still pass against the bug, for two independent reasons: the harness mounts `SetupWizard`
directly, so `RequireSetupWorkspace` is absent from the tree, and `SESSION_FIXTURE`
hardcodes `needs_setup: true` so nothing ever flips. The regression test must mount the
real `router` with a fixture that flips `needs_setup` to `false` after `POST {step:7}`.

---

## Phase D — owner settings

### 10. Workspaces, System status and Backup

#### Diagnosis

**Workspaces.** Buttons are not conditionally hidden and the list is never empty
(`web/api_auth.go:54` always populates `session.workspaces`). The complaint is not literal
absence — it is that nothing *reads* as a button. Every control in the section is
`variant="outline"` at `size="sm"`: a white box (`--background: #ffffff`) with a `#dcd8d2`
hairline on a white page at 13px. It is the only owner section with no primary
(`variant="default"`, ember) button at all. Audit has the same problem — its one button is
outline *and* conditional.

Separately, one action is genuinely missing: **Enter** exists in `pages/Workspaces.tsx:255`
and `WorkspaceMenu.tsx:43` but not in the settings section. Rename/permissions are
correctly absent — there is no such endpoint, and `OwnerSections.test.tsx:212` pins that.

**System status.** It renders two `<dl>` entries. When the sandbox is off, "off" is grey
`text-muted-2` while "ready" is green `text-ok` — so Landlock is the only thing that
registers. This is not a UI-only fix: `GET /api/v1/admin/settings`
(`web/api_workspaces.go:247`) returns *only* `sandbox_on` and `landlock_ready`.
`/healthz` already computes version, commit, Landlock ABI, coder mode and the four host
tools (`internal/health/health.go:35`), and `Warnings()` (`:71`) carries ready-made prose
— including *"python3 not found — the agent-tool AST guardrail is INACTIVE"*, the one
genuinely security-relevant omission.

**Backup — the sharpest finding: dead CSS classes.** `--color-line` and `--color-warning`
**do not exist** (the real tokens are `--color-border` and `--color-warn`). BackupSection
is the only file in the SPA using them, and they were confirmed absent from the built
stylesheet. Consequences: the four `<select>`s render with **no border** (Tailwind v4
Preflight sets `border: 0 solid`, so `border` alone gives width 0), the snapshot list gets
no dividers, and the **pending-restore banner** — the most alarming state in the app —
renders as transparent plain body text (`:208`, `:571`).

Beyond that: four raw `<button>`/`<a>` elements doing action-button work, two with no icon
at all and one opening a destructive flow (`:504-523`); bare unstyled checkboxes; `<label>`
instead of the `Label` primitive ~12 times; a hand-rolled destructive confirm where every
other confirm in the app is a `Dialog`; and the icon+`h2` header block repeated **four
times** verbatim.

**Typography, all three sections.** No `<pre>` and no hardcoded pixel sizes exist
(`density.test.ts` forbids the latter). The real defect is that owner sections use a third
heading pattern and `text-xs` (13px) body copy where every workspace-scoped settings
section uses `text-sm` (15px) — `--text-xs: 0.8125rem`, `--text-sm: 0.9375rem`
(`index.css:166`). Owner pages literally render two points smaller than every other
settings page.

#### Fix

- Adopt `PageTitle` (these are full pages now) and `text-sm` body copy across all five
  owner sections; the hand-rolled `OwnerIcon` becomes redundant since `PageTitle` reads the
  same `entityIcons` map.
- Workspaces: add **Enter**, and make the primary action `variant="default"`.
- System status: widen `apiAdminSettings` to carry the `health.Report`, and render version,
  commit, Landlock ABI, coder mode, host tools **and `Warnings()`**.
- Backup: replace the dead classes with real tokens, adopt `Button`/`Label`/`Card`/`Dialog`,
  give every action button a leading lucide icon, and collapse the four duplicated headers
  into one `PageTitle`.
- Fix `CoderSection.tsx:379`'s "Test" button, which violates the same icon rule.

#### Test

A test asserts no SPA source references `-line` or `-warning` colour classes, so the dead
tokens cannot come back. `contrast.test.ts` is re-run because owner sections change colour
usage; `density.test.ts` guards the typography.

---

## Phase E — the toggle list

### 11. A toggle cannot be collapsed

#### Diagnosis

There is **no NodeView on the toggle at all** — `addNodeView` appears exactly once in the
SPA (`kbImage.ts:117`). The toggle is pure `renderHTML`, and `renderHTML` deliberately
never emits `open` (`nodes/toggle.ts:102-111`), because tiptap-markdown's HTML fallback
wrote `open=""` back into the saved note, force-expanding it forever.

So `open` is **neither a doc attribute nor durable DOM state**:

1. Not an attribute — the node has no `addAttributes` at all.
2. Not durable — if the browser sets `open` natively, ProseMirror's `DOMObserver` sees an
   `attributes` mutation, the base `ignoreMutation` returns `false` (the node *has* a
   `contentDOM`), the node is marked dirty, and the `<details>` is re-rendered from doc
   state — i.e. **without `open`**. This wipe is gated on `view.editable`.

ProseMirror does **not** swallow the click: `handleSingleClick` bails (the note's
`handleClickOn`/`handleClick` return false for non-wikilink targets, and `toggleSummary`
is not an atom), and PM registers no `click` handler at all — its `preventDefault` fires on
`mouseup`, which does not cancel `<summary>`'s click-activation behaviour.

The CSS rule `.tiptap details > *:not(summary) { display: block }` (`editor.css:170`) is a
**secondary suspect and probably a no-op**: browsers hide a closed `<details>` body via the
rendering ancestor (UA shadow slot / `::details-content`), not by styling light-DOM
children, so `display: block` on a child cannot un-hide it. The file itself admits the rule
is unverified.

#### Fix

Add a NodeView holding `open` as **editor-only DOM state**:

- `dom` is a real `<details>`; `contentDOM` is the same element, so the `<summary>` stays a
  direct child. This is load-bearing: the serializer stringifies the summary's DOM
  (`state.render(node.firstChild, …)`), so any invented wrapper markup would break every
  fidelity test.
- `ignoreMutation: (m) => m.type === "attributes"` — without it, PM's redraw wipes `open`.
- A click handler toggles `open` only when the click lands in the **arrow region**
  (`e.clientX - summary.getBoundingClientRect().left < ARROW_HIT_PX`), with the native
  marker hidden and our own arrow drawn via `summary::before`. Toggling on the whole
  summary would make it impossible to click into the title to edit it.
- `if (!editor.isEditable) return` — the established guard from `kbImage.ts:129`, since a
  read-only note still mounts NodeViews.

`renderHTML` and the markdown serializer are **not touched**, so fidelity risk is zero and
no transaction is dispatched (no spurious autosave).

**Stated limitation:** open/closed is *not persisted*. The markdown format has nowhere to
put it, and making it a real attribute is exactly the bug the current code was written to
avoid. Toggles open by default and collapse within the session.

The slash-menu body becomes a bulleted list (`slashItems.ts:54` → `setToggle`,
`toggle.ts:117`), which is schema-legal since `content` is `toggleSummary block+`.

`editor.test.ts:407` pins the *current* empty-paragraph body as a fidelity fixed point, and
no fixture existed for a bullet body — a form that is not a fixed point makes the first
save open the note **read-only**. This was the batch's biggest unknown, so it was settled
empirically before the design was fixed, by running candidate bodies through the real
`checkFidelity`:

| Body | Fidelity |
|---|---|
| empty paragraph (today) | pass |
| `-` (bare dash, empty item) | pass |
| `- First item` | pass |
| `- One` / `- Two` | pass |
| `- One` then a paragraph | pass |

All five round-trip, so the bullet default is safe and needs no fallback. The fixtures are
committed as permanent tests.

#### Test

Fidelity fixtures for the new body form. `slash.test.ts:120` is extended to assert a
`bulletList` is inserted.

**jsdom cannot verify the actual bug.** It has no layout engine and no `<details>`
semantics, and `editor.css:163` already records that nothing in the vitest suite can assert
collapse. Verification therefore extends `scripts/verify-kb-layout.py` — the existing
Playwright harness, whose docstring states it is the only way these behaviours are
observable — with checks that clicking the arrow hides the body and clicking again reveals
it. Playwright and Chromium are confirmed working on this host.

---

## Verification

`make ci` mirrors the PR gate (gofmt, vet, `-race`, six-way cross-compile, tsc/oxlint/
vitest/vite build) and is run before the PR. It will **not** catch: the toggle actually
collapsing, the wizard buttons actually navigating, the stepper fitting on one line, or the
chat prefix rendering on Telegram. Those are verified against a running server on a
non-default port, and the final report states plainly which items were verified how.

## Not built (deliberate)

- **A per-agent icon.** Needs a schema migration, an API field and a picker; `🤖` is fixed.
- **Migrating existing `assets/` content.** Rewriting image references across all user
  notes is the only corruption-risk change here and buys nothing once the folder is hidden.
- **Persisting a toggle's open/closed state.** The markdown format cannot express it.
- **Gating scheduled silent runs.** `[SILENT]` suppression on cron is intended behaviour.
