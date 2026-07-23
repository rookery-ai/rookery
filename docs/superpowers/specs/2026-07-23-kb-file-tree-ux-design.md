# KB file-tree & pane UX (Theme A) — design

**Date:** 2026-07-23
**Status:** approved (design)
**Scope:** Knowledge-base browser usability — Notion-style emoji icons on notes
and folders, folders that open as pages, a folder picker for new notes, header
alignment, and multi-select with bulk delete/move.

Covers reported items #1 (header alignment), #2 (folder-target for the top `+`),
#5 (multi-select + bulk actions), #6 (custom icons). First of five KB themes;
brainstormed with the user (the rest — editor fidelity, export, images, links —
are separate specs).

---

## 1. Icon storage (backend, out-of-band)

Icons are stored the same way tree ordering already is (`kb_order` in
`web/api_kb.go`): a single JSON blob per workspace in the `workspace_settings`
table under key **`kb_icons`**, shape `{"<vault-relative path>": "<emoji>"}`.
Nothing is written into the vault, so agents' reads and the file tree stay clean.

- **`loadKBIcons(workspaceID) map[string]string`** / save helper, mirroring
  `loadKBOrder`/`apiSaveKBOrder`. A missing/corrupt value degrades to "no
  custom icons", never an error.
- **Tree response** (`apiKBTree`) gains `icon string` per `apiKBNode` (empty =
  client uses its default lucide icon).
- **Note response** (`apiGetKBNote`) gains `icon string`.
- **`PUT /api/v1/kb/icon {path, icon}`** — sets the icon; empty `icon` clears
  it. `path` is `cleanKBParam`'d + `vault.Resolve`-validated for path safety
  (like `apiSaveKBOrder`), even though it only touches settings.
- **`GET /api/v1/kb/folders`** → `{"folders": ["", "notes", "projects", ...]}`
  — a flat list of every folder path (walk the vault, dirs only, skip `.kb`).
  Feeds the new-note folder picker and the bulk-Move picker.

**Map maintenance on rename/move/delete (load-bearing — icons key by full path,
unlike `kb_order`'s name-within-dir keys, so an un-migrated key orphans):**
- `apiRenameKBNote`: after a successful rename, re-key `kb_icons` — move the
  entry for `from`→`to`, and for a folder move, re-key every descendant key
  (`from/…` → `to/…`).
- `apiDeleteKBNoteAPI`: drop the key for the deleted path and, for a folder, all
  descendant keys.

Covered by Go tests in `web/api_kb_test.go`.

## 2. Emoji icons (Notion-style)

Every note and folder shows an emoji before its title, in **both** the tree row
and on the page. Clicking the icon opens an **emoji-picker popover**.

- **Picker component** (`web/ui/src/pages/kb/EmojiPicker.tsx`): a searchable
  grid built from a curated in-house emoji set (~150 common emojis grouped by
  category, each with search keywords) in `emojiData.ts`. **No new npm
  dependency** — consistent with the codebase's lean approach. A "Remove" action
  clears back to the default icon. (emoji-mart is the documented fallback if the
  full Unicode set is ever wanted.)
- **Display:** `iconFor` in `FileTree.tsx` returns the user emoji (rendered as a
  text span) when set, else the existing lucide icon. System dirs keep their
  lucide default but can be overridden.
- **Where the picker opens:** the page's title-area icon button (folder page and
  note header), and a "Change icon…" item in each tree row's `⋯` menu. The tree
  row itself shows the icon read-only (clicking the row still selects/opens).
- **Setting flow:** `useSetKBIcon()` mutation → `PUT /kb/icon` → invalidate the
  affected tree level + the open note. Optimistic update for snappiness.

## 3. Folders become pages

Clicking a folder still toggles its tree expansion (unchanged) **and** opens it
in the main viewer as a **`FolderPage`** instead of today's empty state.

- **`web/ui/src/pages/kb/FolderPage.tsx`**: header = folder icon (click →
  picker) + folder name; body = a list of the folder's children (each with its
  icon + display name, clickable to open via the same `openPath`), plus
  "New note" / "New folder" buttons (reusing `NewEntryDialog` scoped to this
  folder). Reads from the existing `useKBTree(folderPath)` — **no new backend**.
- **`KBPage.tsx`**: when the active path is a directory (`dir=1`), render
  `FolderPage` rather than `KBEmptyState`. `KBEmptyState` remains for the
  no-path case.
- Empty folder → a friendly "This folder is empty" with the create buttons.

## 4. New-note folder picker + header alignment

- **#1 alignment:** fix the `KBPaneHeader` Upload/Plus button row — wrap both in
  one flex container with consistent `icon-sm` sizing and gap so they align
  optically. Cosmetic, no behavior change.
- **#2 folder picker:** `NewEntryDialog` gains a **"Location" `<select>`**
  populated from `GET /kb/folders`, defaulting to the caller-provided folder
  (the pane-header `+` passes the currently-selected folder, or root if none;
  per-row "New note…" keeps passing that row's folder and pre-selects it). The
  created path becomes `location + "/" + name`.

## 5. Multi-select + bulk actions

- **Selection state** lifts to `FileTree`: a `Set<string>` of selected paths,
  kept **separate** from `selectedPath` (the currently open doc). Provided to
  rows via context (alongside the existing `DragCtx`).
- **Interaction:**
  - Plain click → clear multi-select, select this one, open it (today's
    behavior).
  - **Cmd/Ctrl-click** → toggle this path in the set (no navigation).
  - **Shift-click** → select the range from the last-clicked anchor to this row
    over the currently-**visible** (expanded) rows.
- **Visible-order registry (the one hard part):** the lazily-loaded nested
  `TreeLevel`s have no central flattened order. A `VisibleOrderCtx` collects each
  rendered row's path in DOM order (rows register/unregister on mount) so
  Shift-range has a sequence to slice. Range is bounded to loaded/expanded rows
  (a collapsed subtree isn't traversed) — standard file-explorer behavior.
- **Floating action bar** (`SelectionActionBar`): shown when `selection.size >=
  2`. Displays the count and **Move…** (opens a folder picker → renames each
  selected item into the target folder, one at a time, skipping no-op/illegal
  moves), **Delete** (routes through the existing `useDeferredDelete` undo-toast
  flow, extended to a set), and **Clear**.
- **Drag:** dragging any row that is part of the selection drags the **whole
  selection** onto a target folder (extends `DraggedNode` to optionally carry the
  set; `handleMoveInto` moves each). Dragging an unselected row behaves as today.
- **Move mechanics:** a move is a rename into `targetFolder + "/" + basename`.
  Reuse `useRenameNote`; on any per-item failure, toast which item failed and
  continue the rest. `onMoved` fires per item so an open doc follows.

## 6. Testing

- **Frontend (vitest):** icon display + picker set/clear; `NewEntryDialog`
  location default logic; `FolderPage` renders children and navigates; multi-
  select toggle/range/clear; bulk move (rename-per-item) and bulk delete
  (deferred-undo over a set).
- **Backend (Go):** `kb_icons` set/clear; **rename re-keys the icon map (incl.
  folder descendants)**; **delete drops keys (incl. descendants)**;
  `GET /kb/folders` output; route registration in `api_parity_test.go`.

## Non-goals

- Reordering semantics beyond today's `kb_order` (unchanged).
- Icons for anything outside the vault path space.
- Full Unicode emoji set (curated set is enough; emoji-mart is a later swap).

## Rollout

Likely two execution phases in the plan: (1) icon storage + emoji picker +
folder pages; (2) multi-select + folder pickers + header alignment. One spec,
one branch.
