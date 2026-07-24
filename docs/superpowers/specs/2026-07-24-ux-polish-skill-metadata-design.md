# UX polish + flat skill metadata — design

Date: 2026-07-24
Status: approved (user authorized full autonomous execution)

Seven independent fixes batched into one branch. They share no state; each is
verifiable on its own. Nothing here needs a schema migration.

---

## 1. Skill frontmatter: `metadata.openclaw.*` → `metadata.*`

### Problem

A skill declares its runtime requirements under a vendor-namespaced key:

```yaml
metadata:
  openclaw:
    requires:
      anyBins: [pdftotext, pandoc]
    install:
      - kind: pip
        package: pdfplumber
```

`openclaw` names a foreign platform. Skills created by this platform's skill
designer should declare requirements directly under `metadata`, with no vendor
segment:

```yaml
metadata:
  requires:
    anyBins: [pdftotext, pandoc]
  install:
    - kind: pip
      package: pdfplumber
```

`install` is a **sibling** of `requires`, not nested inside it.

### Approach: parse both, emit only the flat form

`skilllibrary.ParseMeta` is the single parse site for both core (embedded) and
user (on-disk) skills. Skills imported from ClawHub — a real, supported path —
carry the legacy nesting, and a hard cutover would silently drop their
`requires`/`install` (the runner would then fail to resolve tool paths, and
`SkillEnvBlock` would tell an agent a tool is missing when it is installed).

So: the `frontmatter` struct grows flat `Requires`/`Install` fields alongside
the existing `Openclaw` sub-struct. `ParseMeta` prefers the flat form per
field, falling back to the legacy one when the flat field is absent. Precedence
is **per field**, not all-or-nothing, so a half-migrated file still yields
everything it declares.

Everything the platform *emits* or *ships* uses the flat form only.

### Touch points

| File | Change |
|---|---|
| `internal/skilllibrary/library.go` | flat fields on `frontmatter`; per-field merge in `ParseMeta`; doc comments |
| `internal/skilllibrary/skills/{csv,docx,image-ocr,markdown,pdf,pptx,playwright-browser,xlsx,skill-creator}/SKILL.md` | frontmatter converted to flat form (9 files) |
| `internal/skilllibrary/skills/skill-creator/SKILL.md` | the YAML example it teaches the generator |
| `internal/skilllibrary/skills/skill-vetter/SKILL.md` | audit reference `metadata.openclaw.install[].url` → `metadata.install[].url` |
| `internal/prompts/prompts.go` | `BuildSkillDesignSystemPrompt` `<skill_format>` block and `BuildSkillImplementationPrompt` step 1 both show/name the flat form |
| `internal/agentrunner/runner.go` | comment referencing `metadata.openclaw.requires` |

### Tests

`internal/skilllibrary/catalog_test.go` gains:
- flat form parses (`requires.bins`/`anyBins`/`env` + sibling `install`);
- legacy `openclaw` form still parses (backward compatibility);
- flat wins when both are present;
- no shipped core SKILL.md contains the string `openclaw:` (pins the migration).

The existing catalog assertions (frontmatter parses, name == directory,
referenced scripts exist, core skills ship no scripts) must keep passing —
they exercise the same converted files.

---

## 2. Reminders: strikethrough, cap-at-3, home card

### Current state

`RemindersSection` in the Home context pane renders every reminder as an
identical row. `db.ListReminders` has no `WHERE sent=…` filter and
`toAPIReminder` already carries `Sent`, so the API delivers fired reminders
today — the UI just ignores the flag. **No backend change.**

### Changes

**Ordering + strikethrough.** A pure exported `splitReminders(list)` returns
`{ pending, done }`: unsent sorted by `remind_at` ascending, then sent
(most-recent first). Rendering order is pending-then-done. A done row renders
its message with `line-through text-muted-2` and a check icon in place of the
bell; its timestamp is prefixed "Done". Pure and exported so ordering is
unit-testable without rendering.

**Cap at 3.** The pane renders at most the first 3 rows of the combined list.
When more exist, a `View all reminders (N)` button opens a modal
(`RemindersDialog`) listing all of them with the same row component and the
same delete-with-undo. Modal, not a route: there is no `/reminders` route today
and the global search already maps reminder hits to `/`; adding a route would
mean registering it, adding a rail entry, and a parity-test entry for a list of
typically <10 items. The modal mirrors `TemplateGallery`'s shape.

**Home screen card.** A `RemindersCard`, styled exactly like `NextUpCard` /
`NeedsAttentionCard`, joins the two-column grid on the main content area. It
lists up to 4 upcoming (unsent) reminders with relative time; empty state
"No reminders set."; done reminders are not shown there (the pane is where you
manage them). The grid becomes 3 cards — `lg:grid-cols-2` keeps them wrapping
sanely, so no layout rework.

`ReminderRow` is extracted so pane, modal, and card share one presentation.

---

## 3. Selected/hover contrast in the designer templates and the command palette

### Problem

Four places paint a selected/hovered row with the **full accent fill**:

- `pages/agents/TemplateGallery.tsx:96` — `hover:bg-accent/40`
- `pages/agents/AgentNewPage.tsx:185` — selected card `bg-accent`, hover `bg-accent/40`
- `components/ui/command.tsx:150` — `data-[selected=true]:bg-accent data-[selected=true]:text-accent-foreground`
- `pages/kb/SlashMenu.tsx:105` — `bg-accent text-accent-foreground` / `hover:bg-accent/60`

`--accent` is `#2d5a74` (dark blue) in light mode and `#6fa2bd` (light blue) in
dark. Filling a row with it while the row's **children** keep their own colors
is what breaks: the palette's path/snippet spans stay `text-muted-2`, its icons
stay `text-muted-foreground`, and a template card's title stays `text-foreground`.
Only the top-level text flips. `bg-accent/40` is worse — a 40% blue wash under
unchanged foreground text in *both* themes.

### Approach: soft neutral selected surface

Replace the accent fill with `bg-accent-soft` (a tint designed to sit under
normal foreground text: `#e7f0f5` light / `#24313a` dark) and keep
`text-foreground`. Descendant muted text and icons then stay legible without
having to flip every child, and the change is symmetric across themes by
construction. A `ring-1 ring-accent` (selected) / left accent bar carries the
"this one is chosen" signal that the fill used to carry.

Applied to all four sites plus the muted sub-text spans inside
`CommandPalette.tsx`, which no longer need to flip.

`components/ui/command.tsx` is a vendored shadcn primitive; the edit is a
one-line class change on `CommandItem`, kept minimal so a future re-vendor is a
readable conflict.

---

## 4. Command palette "New note" does nothing

`CommandPalette.tsx:167` navigates to `/kb`, which opens the knowledge base
but creates nothing. The KB's real new-note affordance is `NewEntryDialog`,
opened from local state in `KBPage`'s context-pane header.

Fix: the action navigates to `/kb?new=note`; `KBPage` opens `NewEntryDialog`
when that param is present and strips it on open (so a back-navigation or
reload doesn't reopen the dialog). A query param, not a router state object,
because it survives a reload and is trivially assertable in a test.

---

## 5. Global chat modal has no "new chat"

`GlobalChatPanel` resolves the most-recently-updated active chat and shows it —
correct default, but there is no way to start a fresh one without leaving for
the full page.

Fix: a header row inside the panel with a **New chat** button. It calls
`useCreateChat` and pins the returned chat id in panel state; the panel renders
the pinned id when set, otherwise falls back to most-recent as today. Pinning
(rather than relying on "most recent" to update) makes the switch immediate and
immune to list-refetch timing. The existing module-level `createChatInFlight`
guard covers the auto-create-on-empty path only; the explicit button is a user
gesture and is guarded by the mutation's own `isPending`.

The footer's "Open full page ↗" link follows the currently-shown chat.

---

## 6. "Chat about this file" in the knowledge base

A button in `NoteHeader` (markdown notes) and `FileViewer` (code/binary files)
opens the same slide-over the global chat button uses, on a **new** chat,
with the composer prefilled:

```
About my note `notes/trip.md` — <cursor>
```

**Context is loaded on demand, not inlined.** The chat coder already runs with
`Read/Write/Edit/Glob/Grep` (CLI) or `read_file`/`search_files`/`glob` (API)
rooted at the vault, and `BuildChatSystemPrompt` names the vault root. Naming
the path is sufficient for the model to read the file, and it matches the
platform's stated retrieval model ("the broader KB is retrieved on demand via
tools, not injected here"). Inlining an unbounded note into the first message
would fight that design and blow the context on a large file.

`GlobalChatPanel` gains an optional `forceNew` prop: when set, it creates a
chat on mount instead of resolving the most recent one, so "chat about this
file" never lands in an unrelated existing conversation. This is the same
pinned-id mechanism item 5 introduces.

---

## 7. Icon rail: bigger, hover + active affordance, bold icons

`IconRail` items are `size-9` with `size-[18px]` icons; active is `bg-border`,
hover is `bg-border/60` — both nearly invisible against `--chrome`.

Changes:
- rail width `md:w-14` → `md:w-16`; items `size-9` → `size-11`; icons
  `size-[18px]` → `size-5` with `stroke-[2.25]` (lucide's default is 2 at
  24px nominal — at `size-5` it reads thin, so the stroke is set explicitly
  rather than relying on a font-weight, which does nothing to an SVG).
- **active**: `bg-accent-soft text-accent` plus a 3px accent bar on the
  inner edge (left on the desktop vertical rail, top on the mobile bottom bar)
  — a shape cue, not only a color cue.
- **hover**: `hover:bg-muted-surface hover:text-foreground`, a visibly
  different surface from the active tint in both themes.
- **pressed**: `active:scale-95` for tactile feedback, inside the existing
  global reduced-motion suppression.
- avatar `size-8` → `size-9` so the settings target matches the new rhythm.

Layout impact is confined to the rail's own width; `AppShell` positions the
list panel after it in a flex row and needs no change.

---

## Testing

| Item | Verification |
|---|---|
| 1 | `go test ./internal/skilllibrary/... ./internal/prompts/... ./internal/agentrunner/...` — new parse tests + the existing catalog suite |
| 2 | `splitReminders` unit test; RTL test for strikethrough, cap-at-3, "View all" modal, home card |
| 3 | RTL assertions on the selected/hover class of a template card and a command item; visual check in both themes |
| 4 | RTL: selecting "New note" navigates to `/kb?new=note` and the dialog opens |
| 5 | RTL: "New chat" in the panel calls create and swaps the rendered chat id |
| 6 | RTL: the KB header button opens the slide-over with the note path prefilled |
| 7 | RTL: active rail item carries the active classes; whole-suite regression |

Full gates: `go build ./...`, `go test ./... -count=1`, `npm run test`,
`npm run build` (the SPA must still compile into the binary).
