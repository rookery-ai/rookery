# KB icon picker containment + removing the vault `inbox/` projection

**Date:** 2026-08-03
**Status:** approved (user pre-authorised implementation without a review gate)

Two unrelated complaints, one branch:

1. The KB "change icon" dialog spills its contents far outside the modal box.
2. The KB `inbox/` folder shows a stack of identically-titled notes
   (`⏰ Reminder` ×4, `🤖 check-wheader (cron)` ×2) that distinguish nothing.

The first is a design-system bug affecting every dialog in the app. The second
turns out to be a question of whether the projection should exist at all.

---

## Part A — dialog containment (`components/ui/dialog.tsx`)

### Evidence

Reproduced headlessly against the app's own built stylesheet, dialog box
outlined red: the search input, the category tab strip and the emoji grid all
extend ~400px past the right edge of the modal. Screenshot method: render the
exact `EmojiPicker` markup with `dist/assets/index-*.css`, screenshot at
1440×900 with the vendored Chromium.

### Root cause 1 — `sm:max-w-lg` defeats every caller's width

`DialogContent`'s base class list ends with `sm:max-w-lg`. Callers pass
`max-w-2xl`, `max-w-3xl`, `max-w-sm`. Verified directly:

```
twMerge('… max-w-[calc(100%-2rem)] … sm:max-w-lg', 'max-w-2xl')
  → 'fixed grid w-full p-6 sm:max-w-lg max-w-2xl'
```

tailwind-merge treats `sm:max-w-lg` and `max-w-2xl` as **different conflict
groups** (responsive variant vs. base), so both survive; Tailwind v4 emits
variants after base utilities, so `sm:max-w-lg` wins at ≥640px. Every dialog in
the app has been 512px wide regardless of what it asked for. The same merge also
*drops* `max-w-[calc(100%-2rem)]` (same group as the caller's `max-w-*`),
removing the small-viewport inset.

This is the third instance of the trap CLAUDE.md already records for
`ChatScroll` (`p-4` vs `px-[10%]`) and `PageContainer` (`p` vs `px`).

### Root cause 2 — the grid track blows out

`DialogContent` is `display: grid` with implicit `auto` tracks. CSS Grid §6.6:
a grid item's automatic minimum size is content-based **only when the track's
_min_ sizing function is `auto`** — which it is. The category tab strip's
max-content width is ~880px (nine `whitespace-nowrap` tabs); its
`overflow-x: auto` does **not** zero its min-content contribution. So the track
sizes to 880px and every sibling stretches with it, outside the 512px box.

### Fix

In `DialogContent`'s base classes:

- `w-full max-w-[calc(100%-2rem)] … sm:max-w-lg` → `w-[calc(100%-2rem)] max-w-lg`
  — the inset moves to the `w` group (never merged away), the cap is unprefixed
  so a caller's `max-w-2xl` cleanly replaces it.
- add `grid-cols-1` — Tailwind emits `repeat(1, minmax(0, 1fr))`; a `0` min
  sizing function drops the item's automatic minimum to zero, fixing the
  blowout. No `min-w-0` needed anywhere.
- add `max-h-[calc(100dvh-4rem)]` so a tall dialog can never exceed the
  viewport (the emoji picker's `50vh` body plus chrome came close).

**Verified in the same harness:** at 1440px the emoji dialog now measures
exactly 672px with every child contained and the tab strip scrolling; at 700px
both dialogs honour the 1rem inset.

### Blast radius (deliberate, not a regression)

Ten call sites pass `max-w-sm` (Workspaces ×2, ChatWindow, AgentDetailPage,
SecretsPage, FileViewer, NoteHeader, FileTree ×4, SkillDetailPage). They render
at 512px today and at their requested **384px** after the fix. Screenshotted a
worst case — a delete-confirm with a long vault path — and 384px reads fine with
the existing `truncate`. `CommandPalette` (768px), `TemplateGallery` (672px) and
`HomePage`'s inbox dialog (448px) likewise start honouring their declared
widths.

### Test

`dialog.test.tsx`: assert `DialogContent`'s rendered `class` contains no
`sm:max-w-*` token and carries `grid-cols-1`, and that a caller-supplied
`max-w-2xl` is the only `max-w-*` present. This is the same style of pinning
guard as `density.test.ts` and the sheet-width agreement test.

---

## Part B — the icon picker itself (`pages/kb/EmojiPicker.tsx`)

Search already exists; it was simply rendered outside the modal, which is why it
read as missing. With containment fixed it works. Three targeted improvements:

- **Responsive grid.** `grid-cols-10` (fixed 10 columns of `size-9`) becomes
  `grid-cols-[repeat(auto-fill,minmax(2.25rem,1fr))]`, so the grid fills
  whatever width the dialog has instead of assuming one. Verified: 6 columns at
  a 700px viewport, ~14 at 1440px.
- **Width.** Keep `max-w-2xl` — now actually 672px.
- **Search feedback.** Show the match count (`123 matches`) beside the input
  while a query is active, and make the empty state name the query. Cheap, and
  it makes it obvious the field is live.

The dialog becomes a flex column (`flex flex-col`) so the scroll region takes
the remaining height under the new `max-h`, rather than the body pushing the
dialog past the viewport.

---

## Part C — remove the vault `inbox/` projection

### The question

Make it useful, or delete it? The user pre-authorised either. Three checks
decided it.

1. **It pollutes retrieval.** `kbExcludedDirs = {"chats", "agents", FilesDir}` —
   `inbox` is *not* excluded. So `vault.BuildKBContext` quotes inbox notes into
   every agent-designer and skill-designer turn, and `Indexer().Search` (the
   `search_files` LLM tool) ranks them against real notes. The live vault's
   contents are four copies of `🌤 Brajchino: 25°C (feels like 27°C), clear sky`.
   That is noise competing with the user's actual knowledge.
2. **Agent notifications are already archived.** `ReflectAgentRun` writes
   `agents/<id>/logs/run_<ts>.md` with an `## Output sent to user` section
   holding the exact `[CHAT]` lines. The inbox note is a third copy (DB row +
   run log + note).
3. **Reminder notes contradict the stated design.** CLAUDE.md: *"Reminders live
   only in the DB and the reminders UI tab — they are NOT reflected to the
   vault."* `ReflectReminder` indeed has no callers — but
   `reminder.Service.recordInbox` reflects the fired reminder through
   `ReflectInbox`, reintroducing exactly what that rule excludes, with the body
   duplicated in the heading (`# ⏰ Reminder` over `⏰ Reminder: put quinoa`).

A daily-digest rewrite was considered and rejected: `Reflector.write()`
hard-couples the markdown note to a per-message
`.kb/db-export/inbox_messages/<id>.json` sidecar, so a digest would bound only
the *visible* tree while the filesystem kept growing per message — and it would
need a bespoke write path, a vault mutex, a startup fold-in migration and a
documented deviation from the Unreflect contract, for an archive that duplicates
the run log.

### What is removed

- `vault.InboxNote`, `Reflector.ReflectInbox`, `Reflector.UnreflectInbox`.
- The two call sites: `agentrunner.Runner.recordInbox` and
  `reminder.Service.recordInbox` keep creating the **DB row** (the notification
  itself) and stop reflecting. Both lose their now-unused `reflector` plumbing
  only if nothing else uses it — `agentrunner` still needs it for
  `ReflectAgentRun`, `reminder` does not and drops the field.
- `web/api_home.go`'s `UnreflectInbox` call on message delete.
- Dead siblings, since the branch is already in this file:
  `vault.ReminderNote`, `ReflectReminder`, `UnreflectReminder` (no callers, and
  the `reminders/` directory has never existed).
- `inbox` and `reminders` from `vault.protectedTopDirs`,
  `web.kbSystemFolderLabels`, `web.kbDisplayTitle`'s reflected-note switch, and
  `links.go`'s `linkSourceExcluded` / `namePriority`. Once the platform does not
  own those names, a user folder called `inbox` is an ordinary user folder and
  must not be protected, relabelled or demoted in link resolution.

### What stays

- The `inbox_messages` table, the `/api/v1/inbox` endpoints, the Home inbox and
  its poll/read/delete flow — unchanged. This is the notification surface, and
  it already renders sender icon, name and the full body grouped by day.
- `ReflectChat`, `ReflectAgentRun`, `Unreflect`, `UnreflectChat`,
  `UnreflectAgentRuns`.

### Cleanup of existing installs

`vault.RemoveLegacyInboxNotes(workspaceID)` deletes the `inbox/` directory and
`.kb/db-export/inbox_messages/`, called from `serve` for every workspace
alongside the other startup migrations. Idempotent and near-free once the dirs
are gone (one `stat`). Both removals are best-effort: a failure logs and does
not block boot, exactly like the other reflection paths.

It deletes rather than archives because the notes are a projection whose source
rows are still in the database, and Part C's whole point is that the projection
was never the record.

---

## Found during implementation

Two things the design missed, both caught before the PR:

- **The vertical axis had the same bug.** Capping the dialog at
  `max-h-[calc(100dvh-4rem)]` does nothing while the scroll region keeps
  `min-h-72` — a `min-height` is a floor that beats flex shrinking, so at a
  470px viewport height the emoji grid pushed straight out of the bottom of the
  modal. Verified by screenshot, then fixed by giving the dialog a definite
  height (`h-[min(36rem,calc(100dvh-4rem))]`) and letting the scroll region flex
  into the remainder (`min-h-0 flex-1`). Re-verified at 470/620/900px.
- **The SPA had its own copies of the system-folder list.**
  `web/api_kb.go`'s `kbSystemDirs`, `lib/kb.ts`'s `PROTECTED_TOP_DIRS` and
  `NoteEditor.tsx`'s `isSystemLogNote` all still claimed `inbox`/`reminders`.
  Left alone, a user folder named `inbox` would render as muted chrome, refuse
  to rename, and hide its backlink strip. All three now match the Go side.

## Testing

- `dialog.test.tsx` — the width/`grid-cols-1` pinning guard (new).
- `EmojiPicker` — existing tests plus one asserting the grid class is the
  auto-fill template, not a fixed column count.
- `internal/vault/reflect_test.go`, `unreflect_test.go`, `links_test.go`,
  `vault_test.go` — drop the inbox/reminder cases; keep and extend the chat and
  agent-run ones.
- `internal/vault/migrate_test.go` — `RemoveLegacyInboxNotes` removes both dirs,
  leaves `notes/` untouched, and is a no-op on a vault that never had them.
- `internal/agentrunner/inbox_test.go`, `internal/reminder/tick_test.go` — the
  DB row is still created; nothing is written to the vault.
- `web/handlers_kb_title_test.go`, `web/api_kb_test.go`, `web/api_home_test.go`
  — drop inbox-path expectations.
- `make ci` before opening the PR.

## Out of scope

- Search inside the Home inbox.
- Any change to how chats or agent runs are reflected.
- Custom (non-emoji) icons.
