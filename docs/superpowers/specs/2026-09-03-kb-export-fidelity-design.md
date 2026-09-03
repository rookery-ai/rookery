# Knowledge-base export fidelity

**Date:** 2026-09-03
**Status:** design, approved for implementation

## The report

> I tried to export a document which had resized images and a grid layout and
> the document exporter didn't do the job well. When I exported PDF it
> downloaded the images but they were full size and not in a grid layout. When
> I exported docx it didn't even download the images nor the grid layout.
> Links and attachments should also be included if possible.

Every clause of that is accurate, and each names a different defect.

## What is actually wrong

Three independent bugs, verified against the source rather than inferred.

### 1. An image's width is written and never read

The editor serialises a resized image in Obsidian's form, `![alt|420](src)`, with
the width in the **alt slot** (`web/ui/src/pages/kb/kbImage.ts`). Nothing on the
export side splits it: `web/api_kb.go`'s `inlineVaultAssets` rewrites the
destination into a `data:` URI and copies the alt text through verbatim, so
goldmark emits `<img alt="before|420">` with no width at all.

The width is therefore lost in HTML, in PDF, and in DOCX. This is half the
reported symptom on its own, and it is the smallest fix in this document.

### 2. The layout wrappers are dropped as raw HTML

`kbColumns` and `kbAlign` serialise as `<div data-cols="2">` and
`<div align="center">` wrappers around blank-line-separated markdown
(`web/ui/src/pages/kb/nodes/{columns,align}.ts`). `internal/export`'s goldmark is
built **without** `html.WithUnsafe()`, deliberately, so every raw HTML block is
replaced with the literal comment `<!-- raw HTML omitted -->`.

Measured output for a two-column block of two resized images:

```
<p>Intro paragraph.</p>
<!-- raw HTML omitted -->
<p><img src="uploads/before.png" alt="before|420"></p>
<p><img src="uploads/after.png" alt="after|420"></p>
<!-- raw HTML omitted -->
```

The cells survive; the grid does not.

### 3. DOCX has no image support at all

`internal/export/docx.go` builds a four-part OOXML package —
`[Content_Types].xml`, `_rels/.rels`, `word/_rels/document.xml.rels`,
`word/document.xml` — and its block switch has no `ast.Image` case. The export
handler knows this and deliberately skips inlining for DOCX, since data URIs
would only bloat a file that cannot render them.

So DOCX loses images, widths and layout together. That is why it reads as worse
than PDF rather than differently wrong.

### 4. Attachments dangle

`inlineVaultAssets` is image-only by design: goldmark blanks a `data:` URI in an
`<a href>` (a security property this path keeps), so a linked PDF or spreadsheet
keeps its vault-relative path. Once the exported file leaves the machine that
path resolves to nothing, and the document gives no hint that anything was
referenced.

## Design

### The central decision: one AST transformation, two renderers

The enabling fact was measured, not assumed. goldmark parses the wrappers as
**separate sibling `HTMLBlock` nodes** (CommonMark type 6), with the body between
them as ordinary block nodes carrying real inline marks:

```
1. Paragraph
2. HTMLBlock   type=6  raw="<div data-cols=\"2\">"
3. Paragraph      ← a real Image node, alt="before|420"
4. Paragraph      ← a real Image node, alt="after|420"
5. HTMLBlock   type=6  raw="</div>"
```

That is the same behaviour markdown-it has on the editor side, and it is what
makes a transformer possible: the wrapper is addressable and the content is not
trapped inside it.

So `internal/export` gains a goldmark `ASTTransformer` that runs after parse and
before render. It matches exactly two patterns, finds the matching `</div>`
sibling, and moves the nodes between them into a custom node — `kbColumnsNode` or
`kbAlignNode`.

**This is not a `WithUnsafe()` violation, and the code must say so.** We never
pass user HTML through. We recognise two known shapes and emit our own markup
from a fixed whitelist; every other raw HTML block still renders as
`<!-- raw HTML omitted -->`, unchanged. Enabling `WithUnsafe()` would render a
note's `<script>` into a file that travels, which this repo refuses in two
places.

Because **both** renderers walk this AST, one transformation fixes HTML, PDF and
DOCX at once. This is the property a pandoc pipeline could not have had (see
*Rejected alternatives*).

Two edge cases are decided up front:

- **Nesting.** The two wrappers nest — the editor produces `align` inside
  `columns` and it round-trips in both directions — so the scan recurses into a
  node it has just created.
- **An unbalanced opener** (no matching `</div>`) leaves the AST untouched.
  Degrading to today's behaviour is correct; the alternative is swallowing the
  rest of the document into a wrapper the author never closed.

### Image widths

`splitAltWidth` becomes a Go function mirroring the editor's TypeScript one:
`"before|420"` → `("before", 420)`. A trailing `|` with no number, or a
non-numeric suffix, is alt text and is left alone — an alt string legitimately
contains a pipe.

HTML/PDF emit `width="420"` together with `max-width:100%`. Both halves matter:
the attribute honours the author's size, and the CSS lets an image wider than the
page scale **down** without ever scaling up.

### DOCX

The largest piece, and the one that grows the package beyond its current four
parts.

**Images** need a `word/media/imageN.<ext>` part, a relationship in
`document.xml.rels`, a `<Default>` or `<Override>` in `[Content_Types].xml` for
the image type, and an inline `<w:drawing>` referencing the relationship.

Sizing is in EMU (English Metric Units), `px × 9525`. The requested width is
honoured and the height derived from the image's real aspect ratio via stdlib
`image.DecodeConfig` (png, jpeg, gif). **A format it cannot decode is skipped
rather than guessed at** — a wrong aspect ratio produces a visibly distorted
image, which is worse than an absent one and much harder to attribute.

**Columns** become a **borderless single-row table**: one cell per child, all
`tblBorders` set to `none`, fixed layout, each cell `w:tcW` a `pct` share. Word
has no CSS grid, and a table is the only construct that expresses side-by-side.

**Alignment** becomes `<w:jc>` on each contained paragraph.

### Attachments

`inlineVaultAssets` is unchanged — the `data:`-href property stays.

The export handler collects vault-relative link destinations that are **not**
images into `export.Options.Attachments` (name + path). Both renderers append an
**Attachments** section listing them.

The link itself stays relative, so it still resolves when the note travels
alongside its `uploads/` folder. The document states what it references even when
it does not carry it. Embedding is explicitly out of scope: see *Rejected
alternatives*.

## Testing

- **Transformer**: AST-shape tests for a plain wrapper, nested wrappers, an
  unbalanced opener, and a raw HTML block that must still be dropped.
- **HTML/PDF**: golden files, including that `minmax(0, 1fr)` is emitted and not
  a bare `1fr` — a grid item's automatic minimum size is content-based (CSS Grid
  §6.6), a trap this repo has already recorded twice (`DialogContent`,
  `PageContainer`).
- **DOCX**: a test that **unzips the output** and asserts the media part exists,
  the relationship points at it, the content-type override is present, and the
  drawing's EMU width equals the requested pixels × 9525. A well-formed file that
  Word lays out wrongly is the failure mode here, so the assertions must reach
  real values rather than well-formedness.
- **The reported case end to end**: two resized images inside a two-column grid,
  through all three formats.
- **`TestFidelityCorpus` re-run.** This change only *reads* the image
  serialisation, but the corpus deliberately spans Go and TypeScript precisely
  because a change on one side can pass its own tests while breaking the other.

## Rejected alternatives

**`html.WithUnsafe()`.** Would render the wrappers for free and would also render
a note's `<script>` into an exported file that travels. Refused in two places in
this codebase; not reopened here.

**pandoc as a DOCX engine.** Considered and dropped for two independent reasons.
It cannot be verified — pandoc is not installed on the development host, so every
claim about its output would be untested, which is the standing of an
`unverified: true` connector applied to the component whose entire justification
is fidelity. And it does not solve the reported problem: **Word has no CSS grid**,
so pandoc converting a `<div data-cols>` produces stacked cells — exactly the
symptom being fixed. It would also have turned `AvailableFormats()`'s
unconditional `DOCX: true` into a host probe, changing a stated property of the
package for a result worse than the pure-Go path.

Recorded in *Known gaps* so it is not re-proposed without the grid limitation.

**Embedding attachments.** DOCX can carry a file as an OLE object; HTML and PDF
cannot. That is real OOXML complexity for one format out of three, producing an
icon a reader must double-click, and it would leave the three formats
inconsistent in what an export means. The attachment list is honest and uniform.

## Files

| Path | Change |
|---|---|
| `internal/export/layout.go` | new — the AST transformer and its two node types |
| `internal/export/image.go` | new — `splitAltWidth`, EMU conversion, `image.DecodeConfig` sizing |
| `internal/export/html.go` | node renderers, image width, attachments section, print CSS |
| `internal/export/docx.go` | images (media parts, rels, content types, `w:drawing`), columns table, `w:jc`, attachments |
| `internal/export/markdown.go` | register the transformer on both the renderer and the parser |
| `web/api_kb.go` | collect attachments; stop skipping image inlining for DOCX |
| `CLAUDE.md` | the `internal/export` row and the export-fidelity section |
| `rookery-web` | `concepts/knowledge-base.md` |

## Out of scope

Export remains one-directional out of markdown; `internal/convert` stays
one-directional into it. Nothing here changes what the editor writes, so no
migration is required and existing notes export better with no action from the
owner.
