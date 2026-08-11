# KB table editing — design

**Date:** 2026-08-10
**Unit:** E of the [onboarding, brand and platform batch](2026-08-10-onboarding-brand-and-platform-batch-design.md)

A table in the KB editor could be inserted at a fixed 3×3 and then never changed.
There was no way to add a row, delete a column, or choose a different shape.

This is a **control surface, not a capability**. TipTap's table extension already
implements `addRowBefore`, `addRowAfter`, `deleteRow`, `addColumnBefore`,
`addColumnAfter` and `deleteColumn`; nothing in the UI reached any of them.

## The constraint that shapes everything

`pipeSafeTable.ts` and `generatorFidelity.test.ts` exist because a serialization
mismatch makes `checkFidelity` open the note **read-only**. So a table operation
that produced a subtly different document would lock the user out of their own
note on the next load — with the table looking perfectly correct on screen.

Two consequences, both non-negotiable:

- **Every action goes through an editor command, never a DOM edit.** The commands
  produce the canonical document; hand-editing the table element would not.
- **Every operation is round-trip tested** through `toMarkdown` → `checkFidelity`,
  including at every size the picker can produce and on a table with a
  pipe-bearing cell (the case `pipeSafeTable` was written for).

## Insertion: a size picker

The slash command now dispatches `kb:insertTable` and a React dialog opens — the
same window-event pattern Image and File attachment already use, because a dialog
cannot be opened from a plain editor command.

The picker is a hover grid rather than two number inputs. The shape of a table is
a spatial question, and every editor that has solved this answers it the same
way, so it needs no instructions. `pickerSize` is one-based: hovering the first
cell means a 1×1 table, not a 0×0 one.

**The header-row checkbox carries a real caveat, stated in the dialog.** Markdown
tables have no way to express a table *without* a header row — the delimiter line
is mandatory in the grammar. A headerless table therefore has its first data row
promoted into the header on the next save. The checkbox is really "should the
first row be a header, or should it become one", and saying so is better than
letting someone discover it after a save.

## Editing: hover handles

Handles appear against the row or column the pointer is over, carrying insert
before, insert after, and delete.

Direct manipulation rather than a toolbar. A toolbar button acting on the caret's
cell is the cheaper build and the one that makes people count rows before
clicking; a handle against the thing it acts on means "delete this row" needs no
reading to work out which row "this" is.

Three details that are easy to get wrong:

- **Handles sit against the table's edge, not the cell's.** The column handle is
  above the *table* and the row handle to the *left of the table*, so hovering
  any cell in a column shows the control in one fixed place instead of having it
  chase the pointer down the page.
- **Only one handle shows at a time**, chosen by which edge of the cell the
  pointer is nearer. Showing both doubles the click targets over one cell and
  makes neither obviously the one that acts on what you are pointing at.
- **Hovering sets the caret** into the cell, because TipTap's commands are
  selection-relative. Without it the buttons would silently operate on whichever
  cell was clicked last — the worst possible failure, since it looks like it
  worked and edits the wrong row.

The handle holds itself open while the pointer is over it (`overHandle` ref), or
moving from cell to button would fire the `mouseleave` that unmounts the control
out from under the cursor.

## Geometry is pure, for the reason `placeMenu` is

`tableGeometry.ts` takes rectangles and returns coordinates. jsdom has no layout
engine, so every `getBoundingClientRect` it reports is zeroes: a test driving the
real editor can prove a handle **mounts** but never where it lands. Extracting the
arithmetic is the only way placement is testable at all — the same reasoning
already recorded for `SlashMenu`'s `placeMenu`, and the reason this reuses that
pattern rather than adding a third floating-UI implementation.

`clampToViewport` pushes a handle back inside the edge rather than hiding it. A
table at the very top or left of the scroll container would otherwise put its
controls off-screen where they cannot be clicked — the bug the slash menu already
had to fix once. An overlapping handle is still usable; an invisible one is not.

`cellCoords` honours `colSpan`. A merged cell shifts every cell after it, and
getting that wrong would insert a column in the wrong place on precisely the
tables that are hardest to repair by hand.

## Testing

`tableGeometry.test.ts` (13) — handle placement against the table edge; container
-relative coordinates surviving a scrolled container; clamping at all four edges;
a fitting handle left alone; never hiding instead of moving; row/column
resolution including `colSpan`; a cell outside any table returning null; row and
column counts; one-based picker sizing.

`tableEditing.test.ts` (7) — the load-bearing set. Insert row, insert column,
delete row and delete column each keep `checkFidelity` true; column operations
leave every row the same width, header and delimiter included; the delimiter line
stays exactly one (the pattern requires a dash per cell, or it also matches the
newly inserted blank row and proves nothing); **every size the picker can
produce, 1×1 through 8×8, round-trips**; and a pipe-bearing cell survives a row
insert with its escaping intact.

Two existing tests were updated rather than worked around: the slash-item event
test now covers `kb:insertTable`, and the fixed-3×3 assertion was removed with a
comment pointing at what replaced it.

## Not built

Merging and splitting cells. `pipeSafeTable`'s existing fallback already handles a
`colspan`/`rowspan` cell by dropping the note to the HTML/placeholder path — the
content survives but the note stops being WYSIWYG-safe. Offering a UI that
produces that state on purpose would be offering a button that makes the note
read-only, so it stays out until the serializer can express merged cells.
