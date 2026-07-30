# UI polish, round two

**Date:** 2026-07-30
**Status:** approved (implement without further review)
**Builds on:** the three `2026-07-30-ui-*` specs

Ten follow-up items from using the overhauled UI. Each is stated with the root
cause found in the source, not the symptom as reported — three of them turned
out to be different bugs than they looked like.

## 1. Chrome is still under-scaled

The overhaul raised the type scale and the density floor, but three pieces of
chrome kept their pre-overhaul sizes and now read as small relative to
everything around them.

- **Icon rail** — `md:w-16` with `size-11` items and `size-5` glyphs. Goes to
  `md:w-20`, `size-12`, `size-6`.
- **Page title** — `text-xl` heading with a `size-5` icon. Goes to `text-2xl`
  and `size-6`. This is the "title bar should be bigger, and the icon in it".
- **Global search input** — `CommandInput` inherits a compact height and
  `text-sm`. The palette *panel* was widened to `max-w-3xl` in the last round
  but its input was not, which is why it still reads as small. Goes to a taller
  row with `text-base`.

## 2. KB tree and note icons

- **Tree rows**: `NodeIcon` renders at `size-4` (emoji at `text-base`). Goes to
  `size-5` / `text-lg`. The chevron stays `size-4` — it is a disclosure control,
  not content, and growing it competes with the icon beside it.
- **Open note**: `NoteHeader`'s icon button is `size-7` with a `text-lg` emoji
  and a `size-4` fallback glyph. Goes to `size-10`, `text-2xl`, `size-6`. It is
  the largest single affordance on the note and was smaller than the tree row
  that led to it.

## 3. KB system folders are lowercase and undifferentiated

`enrichKBDisplayNames` (`web/handlers_kb.go`) resolves display names for
**files** and for `agents/<id>` directories, but leaves every other directory
with its raw on-disk name. So the vault's own top-level folders render as
`notes`, `memory`, `skills`, `agents`, `chats` — lowercase, styled identically
to a user's own folder.

Fix in two parts:

- **Server**: a `kbSystemFolderLabels` map applied to top-level directories only
  (`parentPath == ""`), giving `Notes`, `Memory`, `Skills`, `Agents`, `Chats`,
  `Inbox`, `Reminders`. Done server-side, not in the tree component, so the
  tree, breadcrumbs and global search cannot disagree about what a folder is
  called. `display_name` is presentation only — `path` and `name` keep the real
  lowercase directory, so navigation, rename guards and `Resolve` are untouched.
- **Client**: top-level rows render `font-medium`, so the vault's structural
  folders read as structure rather than as items.

## 4. Owner Backup section uses the old heading style

Not a font-loading problem. When Owner settings was split into five pages, four
sections were promoted to a page-level `<h2 className="text-lg font-bold">` with
an icon. `BackupSection` lives in its own module and was missed, so it still
renders `<h3 className="text-sm font-bold text-muted-2">Backup</h3>` — smaller,
muted, no icon — which is why that one page looks like it is set in a different
face. It renders that heading in **four** places (loading, error, locked and
loaded states) and all four need it.

`OwnerIcon` is currently a private helper in `OwnerSections.tsx`; it gets
exported so `BackupSection` uses the same one rather than a second copy.

## 5. Home quick actions do not match the Agents page

`QuickActions` renders all four as `outline` at `size="sm"`. The Agents page
header renders its primary action as a default-variant `<Button asChild>` at
default size. Home now matches: **New agent** is `default` at default size (it
is the primary action on the page), the other three are `outline` at default
size.

## 6. Secrets "Add" button sits below the inputs

The form row is `sm:items-end`, and each field column is *label + input +
helper paragraph*. Aligning to `end` therefore aligns the button to the bottom
of the helper text, not to the input.

Fix: the button moves into its own column carrying an invisible `<Label>`
spacer, and the row becomes `sm:items-start`. The spacer is the same `Label`
component the real fields use, so the button stays aligned if the label's font
size ever changes — a hardcoded top margin would not.

## 7. Inbox click does not clear the rail badge

Real bug, and the root cause is a query-key mismatch. `useMarkInboxRead`,
`useMarkAllInboxRead` and `useDeleteInboxMessage` invalidate `["inbox"]`. The
rail's unread badge reads `useInboxPoll`, whose key is **`["inbox-poll"]`** —
a different query. Nothing invalidates it, so the badge keeps its stale count
until the 30-second `refetchInterval` happens to fire.

All three mutations now invalidate both keys. Delete is included because
deleting an unread message changes the unread count just as much as reading it.

## Testing

- Rail width/item size, page-title size and icon size asserted from the
  rendered class strings.
- `CommandInput` height/size asserted.
- Tree `NodeIcon` and `NoteHeader` icon sizes asserted.
- Go: `enrichKBDisplayNames` labels top-level system dirs, leaves nested dirs
  and user folders alone, and never alters `Name`/`Path`.
- Client: a top-level system folder renders its capitalised label.
- `BackupSection` renders an `<h2>` in every one of its four states.
- `QuickActions`: New agent is the primary variant, the rest are outline.
- Secrets: the Add button aligns via the spacer column (row is `items-start`).
- Inbox: marking read/all-read/deleting invalidates `inbox-poll`.
- `make ci` green.

## Out of scope

No API surface changes (`display_name` already exists in the tree DTO), no new
dependencies, and no change to which folders are protected from rename/delete.
