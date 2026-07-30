# UI overhaul, spec 3 of 3: gaps and fixes

**Date:** 2026-07-30
**Status:** approved
**Depends on:** spec 1 (design system foundation) — consumes the icon rules, the
button contract and the density floor.

## Scope

The remaining discrete items. Three of them are **narrower than they first
appear** because the functionality already exists and is merely invisible or
conditional; one is a **defect that must be reproduced before it is fixed**.

## Inbox: make mark-all-read visible

Already built. `web/api_home.go:30` registers `POST /api/v1/inbox/read-all`,
`lib/home.ts:148` exports `useMarkAllInboxRead`, `HomePage.tsx:183` calls it, and
`home.test.tsx:219` covers it.

It is invisible because `HomePage.tsx:234` renders it as
`<Button variant="ghost" size="xs">Mark all read</Button>` — a 24px-tall, 12px
grey **text** button, inside a narrow context pane, and **only when
`unread > 0`**.

Change:

- `variant="outline"`, `size="sm"` (spec 1 makes that `h-9`).
- Leading `CheckCheck` icon.
- Rendered whenever the inbox has **any** messages, disabled when `unread === 0`
  — so the affordance is discoverable before it is needed, instead of appearing
  and vanishing.

No API or hook change.

## KB tree action menu

`FileTree.tsx:624-648`. Two changes.

### Icons on every item

`New note…` → `FilePlus`; `New folder…` → `FolderPlus`; `Change icon…` →
`Smile`; `Rename…` → `Pencil`; `Delete…` → **`Trash2`**, red (the item already
uses `variant="destructive"`; the icon makes it scannable).

The `⋯` trigger keeps `MoreHorizontal` and picks up spec 1's `size-7` target
and always-mounted reveal.

### The confirmation modal already exists

`FileTree.tsx:377` renders a delete dialog with "This can't be undone.". It is
**reused, not rebuilt**. The only change is that its confirm button gets a
`Trash2` icon and `variant="destructive"` — and note that per spec 1's carve-out,
a dialog footer *pair* stays text-only, so this is the one intentional
exception: the destructive confirm keeps its icon because the icon is the
warning.

### Which items each node kind gets

The menu is **conditional, not sparse** — the reported "only Delete in it" was a
protected node behaving correctly. Recorded so it isn't mistaken for a bug again:

| Node kind | Menu items |
|---|---|
| Folder (unprotected) | New note · New folder · Change icon · Rename · Delete |
| File (unprotected) | Change icon · Rename · Delete |
| Protected (`isProtectedPath`: agents, chats, inbox, skills, reminders) | Change icon **only** |

`isProtectedPath` withholds Rename/Delete on system-managed, DB-backed nodes
because renaming or deleting the file would orphan the backing record — those are
deleted from the item's own page. This behaviour is **kept as-is**.

## Workspace menu icons

`WorkspaceMenu.tsx:75-88`. Each item gets an icon, per spec 1's rule that every
action carries one:

- switch rows — keep the per-workspace `WorkspaceAvatar` (it is the recognition
  affordance) and add nothing, since an avatar *is* the icon
- `Change image…` → `Image`
- `+ Create workspace` → `Plus` (replacing the literal `+` in the label text)
- `Leave workspace` → `LogOut`

## Emoji: the full Unicode set, still zero runtime dependencies

`pages/kb/emojiData.ts` is a hand-curated ~160-emoji table. Its own header names
emoji-mart as the documented escape hatch, but a ~200 KB runtime dependency in a
binary-embedded SPA conflicts with this repo's lean-dependency posture, and it
would need theming to match.

Instead: a **build-time generator**.

```
web/ui/scripts/gen-emoji.mjs      → web/ui/src/pages/kb/emojiData.generated.ts
```

- Input: Unicode CLDR annotation data (emoji + names + keywords), vendored as a
  data file so the generator needs no network at build time.
- Output: a compact typed table, ~1900 emoji with search keywords, grouped by the
  **standard Unicode categories** (Smileys & Emotion, People & Body, Animals &
  Nature, Food & Drink, Travel & Places, Activities, Objects, Symbols, Flags).
- Size: ~80–120 KB gzipped. No runtime dependency.
- The generated file is **committed** so `npm ci && vite build` needs no
  generation step, and a test asserts it is in sync with the generator's output
  (so a stale commit fails CI rather than silently shipping).

`emojiData.ts` keeps its `filterEmojis` API and re-exports from the generated
table, so `EmojiPicker.tsx` changes only cosmetically.

`EmojiPicker.tsx` gains:

- **category tabs** (the grid is far too long to scroll blind at 1900 entries)
- a **sticky search field**
- `size-9` cells (spec 1)
- keyword search across the generated keywords, not just the glyph

Skin-tone variants are **out of scope** — the base set is the ask.

## Workspace images: 12 → 28 presets

`lib/workspaceIcons.tsx` currently defines 12 presets, each a two-stop gradient
plus one geometric motif rendered as **inline SVG**. That design is deliberate
and stated in the file's own header: no upload endpoint, no vault storage, no
size/MIME validation, no extra request, and it inherits page scaling and theme
instead of being a fixed-resolution bitmap.

Expand to **28** by adding 16 more gradient/motif pairs in the same system. The
motifs must stay legible at 20px — the size they are actually seen at in the rail
— so each stays a single simple shape, per the existing constraint.

`web/api_settings.go`'s `workspaceIcons` validator must be extended with the new
slugs **in the same commit**, since it rejects any slug outside the known set.
A test asserts the Go validator's set and the TS `WORKSPACE_ICONS` slugs are
identical — they are two lists that must agree, which is exactly the kind of
pair that drifts.

`WorkspaceIconPicker` gets a scrollable grid; 28 tiles no longer fit comfortably
in a fixed dialog.

### Custom upload is deferred

Explicitly **out of scope**, in its own future spec. It is the only item in the
original request needing backend work: a multipart endpoint, a 25 MiB `iolimit`
cap, MIME sniffing (PNG/JPEG/WebP/SVG — and SVG is an XSS vector that needs
sanitising or rasterising), a storage location under the vault, backup
implications, and relaxing the slug validator into a two-shape field
(`preset:<slug>` vs `upload:<id>`). Bundling that here would put a security
review on the critical path of a visual-polish change.

## Defect: KB search results are not clickable

**Reproduce before fixing. No fix is specified here, deliberately.**

The wiring is correct in the code as read:

- `SearchBox.tsx` `SearchResults` renders each hit as a `<button>` with
  `onClick={() => onSelect(hit.path)}`
- `KBPage.tsx:286` passes `onSelect={(p) => openPath(p, false)}`
- `openPath` sets the `?path=` search param

So the cause is **not visible statically**, and a guessed fix would send the
implementation the wrong way. The reproduction step that splits the space:

> Click a result and watch the `?path=` query param in the URL.

| Observation | Meaning | Suspects |
|---|---|---|
| `?path=` **changes**, nothing renders | the click works; the *open* fails | path shape from `internal/vault/search.go` vs what `GET /api/v1/kb/note` expects; the `dir` hint; a 404 swallowed by `FileViewer`'s "Couldn't load this file." |
| `?path=` **does not change** | the click never reaches the handler | `PaneResizeHandle` (`absolute top-0 right-0 h-full w-1`) overlaying the pane; the pane's `overflow-y-auto` container; a stacking/pointer-events issue |

Work this under `superpowers:systematic-debugging`: reproduce, form a hypothesis,
prove it with an observation, then fix. Add a regression test at whichever layer
the bug turns out to live in.

## Testing

- Inbox: the mark-all button renders with any messages present, is disabled at
  `unread === 0`, carries its icon, and still calls `/inbox/read-all`.
- `FileTree`: every menu item renders an icon; delete is red with `Trash2`; the
  three node kinds expose exactly the item sets tabled above (a protected node
  shows Change icon only); the existing confirm dialog still gates deletion.
- `WorkspaceMenu`: every non-switch item renders an icon.
- Emoji: the generated table parses, is non-trivially large (>1500 entries),
  every entry has keywords, `filterEmojis` matches on keyword not just glyph,
  category tabs switch groups, and **the committed generated file matches the
  generator's output**.
- Workspace icons: 28 presets; every motif renders; **the Go validator's slug
  set equals the TS slug set**; the picker grid scrolls.
- KB search: a regression test at the layer the reproduction identifies.
- `make ci` green.

## Risks

- **The emoji generator adds a build-time input.** Mitigated by committing the
  generated file and testing it against the generator, so CI catches staleness
  without needing the generator to run in the release path.
- **28 inline SVG presets grow the bundle slightly.** Each is a handful of path
  elements; the total is well under a single bitmap avatar.
- **The KB search defect may not reproduce**, in which case it is
  environment-specific and the finding (with the observations taken) is reported
  rather than a speculative change being shipped.
</content>
</invoke>
