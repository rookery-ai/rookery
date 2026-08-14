# Columns in the knowledge base

Two images side by side. Two short paragraphs next to each other. A markdown
note can express neither, and the editor's only grid — a table — forces a
header row it promotes on save.

This is the sibling of `2026-08-14-kb-alignment-design.md` and rests on the same
mechanism; read that one first for why a raw-HTML wrapper is the only way to
persist layout at all, and why `checkFidelity` is the constraint that decides
the shape.

## The design, and the one trick in it

```markdown
<div data-cols="2">

![before](assets/before.png)

![after](assets/after.png)

</div>
```

**Each direct child of the wrapper is one cell. There is no cell node.**

That is what makes it round-trip. The obvious shape — a wrapper div containing
one div per cell — nests raw HTML blocks inside raw HTML blocks. The shape above
reuses exactly the mechanism `nodes/align.ts` already proves: one `<div …>`
opening tag, a blank line, ordinary markdown blocks, a blank line, `</div>`. So
markdown-it closes the CommonMark type-6 block at the first blank line and every
cell is parsed as real markdown with real marks, not as text inside an HTML
block.

Measured before writing the controls:

| Input | Fidelity |
|---|---|
| two images, 2 columns | **clean** |
| two paragraphs with bold/italic | **clean** |
| three cells | **clean** |
| two headings | **clean** |
| wikilinks in both cells | **clean** |
| a list beside a paragraph | **clean** |
| neighbours before and after | **clean** |
| a plain note with no columns | **clean** (unaffected) |
| the glued spelling `<div data-cols="2">A</div>` | normalises on first save |
| **two adjacent lists as two cells** | **lossy — see below** |

## Two costs, both stated rather than designed around

**A cell is one block.** A cell holding a heading *and* a paragraph needs
nesting, which this deliberately does not do. Two images, or two short
paragraphs, is what this is for.

**Two adjacent lists cannot be two cells** — and this is a markdown limitation,
not a defect in the node. `- a\n- b\n\n- c\n- d` is *one loose list* in
CommonMark; there is no way to write two adjacent lists as separate blocks. It
was verified outside any wrapper and fails identically there, so the corpus pins
it with that reason rather than implying the columns block introduced it. A list
beside anything that is *not* a list works and is pinned clean.

**Portability is a third cost, and choosing a different attribute does not fix
it.** GitHub's sanitiser strips `class`, `data-*` and `style` alike, so *no*
div-based grid renders as a grid anywhere but here. Only a `<table>` would, and
a markdown table forces a header row this editor promotes on save. Outside
Rookery a columns block degrades to its cells stacked in order, with every image
and mark intact — the right failure. That is also why the wrapper carries no
styling of its own in the file: `class="kb-columns"` is added by `renderHTML`
for the editor's CSS and is never written to the note.

## Surface

Three slash-menu entries — **2 columns**, **3 columns**, **4 columns**.

`insertColumns(n)` inserts a block with `n` **empty** cells rather than wrapping
the current block. The slash menu runs on the empty paragraph left after the
`/query` range is deleted, so wrapping there would produce a one-cell columns
block — a layout with nothing to lay out. `setColumns`/`clearColumns` exist for
completeness and mirror `align.ts`'s commands exactly, including the
`updateAttributes`-before-`wrapIn` order and the AllSelection fallback in
`clearColumns`.

One entry per count rather than an inserted 2-column block the user then
reconfigures: there is no control surface on the node itself, so the count has
to be chosen at insert time.

### CSS

`grid-template-columns: repeat(n, minmax(0, 1fr))` — **not** a bare `1fr`. A
grid item's automatic minimum size is content-based (CSS Grid §6.6), so one long
unbroken word or a wide image would stretch its track and push the others off;
this repository has already recorded that exact trap in `DialogContent`'s
`grid-cols-1` fix. Cells lose their outer vertical margins so the columns line
up at the top, images are capped at their cell width, and below 720px the grid
collapses to one column — four unreadable slivers is not a layout.

## Files

| File | Change |
|---|---|
| `web/ui/src/pages/kb/nodes/columns.ts` | new — node, serializer, commands |
| `web/ui/src/pages/kb/editor.ts` | register `KBColumns` |
| `web/ui/src/pages/kb/slashItems.ts` | three entries |
| `web/ui/src/pages/kb/editor.css` | `.kb-columns` grid |
| `web/ui/src/pages/kb/corpus.test.ts` | columns entries in the fidelity corpus |

## Testing

- Corpus entries for every clean case in the table above, plus `expectLossy`
  entries for the glued spelling and the adjacent-lists case, each carrying its
  reason.
- `columns.test.ts`: `insertColumns` produces `n` empty cells; the count clamps
  to 2–4 (including a `NaN`/garbage `data-cols` on parse); `setColumns` updates
  rather than nests; `clearColumns` lifts; every insert round-trips; a
  `<div>` with no `data-cols` is **not** claimed.
- A slash-menu test that the three entries exist and filter on "columns" and on
  "grid".

## Not doing

- **Multi-block cells.** Needs a cell node, which needs nested raw HTML blocks,
  which is the thing this design exists to avoid.
- **Per-column widths.** Another attribute, another normalisation, and equal
  columns cover the case that motivated this.
- **A drag-to-resize splitter.** `kbImage`'s resize handle is the precedent for
  how much machinery that is, and it buys nothing the count does not.
- **Rendering columns in exports.** Same `WithUnsafe()` security decision as
  alignment.
