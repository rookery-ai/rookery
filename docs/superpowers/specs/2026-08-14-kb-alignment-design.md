# Aligning text and images in the knowledge base

The KB editor can bold, colour, highlight, quote, call out and collapse — and
cannot centre anything. Markdown has no alignment, which is why: persisting it
means emitting raw HTML into a file whose round-trip fidelity decides whether
the note stays editable at all.

## The constraint that decides the design

`checkFidelity` compares a note's bytes against the same bytes after a headless
load-and-serialize. A note that fails opens **read-only**. So any new construct
must have a canonical serialized form that parses back to the identical
document — and a serializer can reproduce only **one** spelling, because parsing
throws away which one the source used. That makes plausible spellings mutually
exclusive choices, not preferences. `nodes/toggle.ts` records the same lesson
for `<details>`/`<summary>`.

### The obvious spelling is a trap

`<p style="text-align: center">…</p>` — the shape `@tiptap/extension-text-align`
produces — fails for a reason that is invisible until you try it. markdown-it
treats a `<p …>` line as a CommonMark **type 6** raw HTML block and does **not**
parse inline markdown inside it. So aligning a paragraph would turn its
`**bold**` into literal asterisks. The toggle's `<summary>` hits exactly this and
has to serialize its marks as HTML tags to survive.

It would also walk into prosemirror-model's `renderSpec` hazard, where an attrs
key literally named `style` is assigned through `dom.style.cssText` and
canonicalised by the CSSOM — the trap `marks/colors.ts` and `kbImage.ts` already
work around by building their DOM by hand.

### What works

A block wrapper with a **blank-line-separated body**:

```markdown
<div align="center">

Hello **world**

</div>
```

markdown-it closes the type-6 block at the blank line, so the body is parsed as
ordinary markdown — real paragraph, list, table and image nodes carrying real
marks — and the closing tag is its own raw HTML block. It is also what
real-world markdown contains: `<div align="center">` is the standard README
centring idiom, so a pasted snippet or a vault-writing agent produces this form.

Measured against the current build, before writing any of it:

| Input | Today | With `KBAlign` |
|---|---|---|
| `<div align="center">\n\nHello **world**\n\n</div>` | **lossy** — wrapper dropped, `OUT: "Hello **world**"` | **clean**, byte-for-byte |
| the same wrapping an image | **lossy** — wrapper dropped | **clean** |
| `<div align="center">Hello</div>` (glued) | **lossy** | normalises to the canonical form |
| `<div style="text-align: center">…` | **lossy** | normalises to `align="center"` |
| `<div class="x">…` (not aligned) | lossy | **unchanged** — the rule declines it |

The first row is the point: **every** div spelling loses its wrapper today, so a
note containing one already opens read-only *and* has its alignment silently
stripped on the first save. Two of the five inputs still normalise on first save
rather than round-tripping — the same read-only-until-first-save gap the toggle
has, and a strict improvement on discarding the alignment outright.

## Design

### `nodes/align.ts` — one node

```ts
KBAlign: { name: "kbAlign", group: "block", content: "block+", defining: true }
```

- **One attribute**, `align` ∈ `left | center | right`, rendered as the HTML
  `align` attribute. Not `style`: it is the idiom above, and it sidesteps the
  `renderSpec` hazard entirely.
- **`parseHTML`** claims `div[align]`, plus `div[style]` **only when
  `style.textAlign` is actually an alignment**. That guard matters: without it
  the rule swallows every styled div in the vault and wraps it in an alignment
  node nobody asked for.
- **`markdown.serialize`** writes `<div align="…">\n\n`, renders the children
  through the normal block machinery (so nested marks, lists, tables and
  wikilinks all serialize correctly for free), then `</div>` + `closeBlock`.
  The blank lines are load-bearing, not formatting — without them markdown-it
  keeps the body inside the raw HTML block and stops parsing its markdown.
- **Registration order does not matter here.** Unlike `KBBgColor`/`KBTextColor`,
  whose order sets DOM nesting and therefore colour precedence, this is a node,
  not a mark.

### Commands and controls

`setBlockAlign(align)` wraps the current block, or — when already inside a
wrapper — **updates the attribute** rather than nesting a second one, which
would serialize as two divs and read as one. `clearBlockAlign()` lifts out.

Three buttons join `BubbleToolbar` in their own group after the lists: Left,
Centre, Right. Direct buttons rather than a `ColorSwatches`-style sub-panel,
because the pressed state *is* the answer to "how is this aligned?" — a panel
would hide the very thing the user is looking for. Left is pressed when there is
no wrapper (unaligned **is** left) and clicking it lifts.

The bubble menu's `shouldShow` is `!selection.empty`, and a `NodeSelection` on
an image is non-empty, so **images get the same three buttons** with no separate
surface. An image lives in a paragraph, and centring the paragraph centres it.

### CSS (`editor.css`)

`.kb-align[align="center"]` etc. set `text-align` explicitly rather than relying
on the browser's legacy mapping of the `align` attribute. Lists inside a wrapper
also get `list-style-position: inside; padding-left: 0`, or a "centred" list
renders its text centred with its bullets still pinned to the left margin —
which reads as a bug.

### Export

`internal/export` builds goldmark **without** `WithUnsafe()`, so the two `<div>`
lines are replaced with `<!-- raw HTML omitted -->` while the body — being
ordinary markdown outside the HTML block — survives with its formatting intact.
Alignment is lost; content is not. That is the mildest of the degradations
already recorded (the toggle loses its summary text entirely). Documented, not
fixed: rendering it would mean enabling unsafe HTML, which is what stops a note
injecting a `<script>` into an export.

## Files

| File | Change |
|---|---|
| `web/ui/src/pages/kb/nodes/align.ts` | new — the node, its serializer, its commands |
| `web/ui/src/pages/kb/editor.ts` | register `KBAlign` |
| `web/ui/src/pages/kb/BubbleToolbar.tsx` | three alignment buttons |
| `web/ui/src/pages/kb/editor.css` | `.kb-align` rules |
| `web/ui/src/pages/kb/corpus.test.ts` | alignment entries in the fidelity corpus |

## Testing

The mechanical expression of "don't break anything" in this repo is
`checkFidelity` plus the existing corpora, so alignment goes **into** them
rather than beside them:

- `corpus.test.ts` gains clean entries for centre/right/left, an aligned image,
  an aligned list, an aligned table, an aligned heading, a multi-block body,
  a wrapper with neighbours either side, and two adjacent wrappers with
  different alignments — plus `expectLossy` entries for the glued and
  `style=`-spelled forms, each with its reason.
- A dedicated `alignment.test.ts`: `setBlockAlign` wraps; a second call
  **updates** the attribute instead of nesting; Left lifts; the round trip
  survives every operation; and a `<div class="x">` is **not** claimed.
- A toolbar test: the three buttons render, Left is pressed on unaligned text,
  and clicking Centre produces `align="center"` in the serialized markdown.

## Not doing

- **Per-paragraph `textAlign` attributes.** The whole point of the wrapper is
  that the body stays parseable markdown.
- **Justify.** A fourth value nobody asked for, with no markdown idiom behind
  it.
- **Table-cell alignment.** Markdown expresses it natively with `:--`/`--:`, and
  `corpus.test.ts` already pins `table-with-alignment` as **lossy** because the
  serializer normalises those colons away. Fixing that is a table-serializer
  change, not an alignment feature, and folding it in here would hide it.
- **Rendering alignment in exports.** Needs `WithUnsafe()`, which is a security
  decision, not a formatting one.
