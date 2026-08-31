# Knowledge-base import fidelity and the PDF pipeline

**Date:** 2026-08-31
**Status:** design
**Scope:** `internal/convert`, `internal/export`, `internal/vault`, `internal/onboard`,
`web/ui/src/pages/kb`

## The problem in one sentence

The KB editor learned a rich construct vocabulary — alignment, columns, toggles,
callouts, colour marks, underline, resizable images, editable tables — and
`internal/convert` was never taught any of it, so an imported document arrives
both **poorer than it was** (structure and every embedded image discarded) and
**frequently uneditable** (the note opens read-only because the markdown does not
survive the editor's round trip).

Separately, PDF export is dead on a host that already has a working Chromium, and
scanned PDFs are told OCR is unavailable on a host where both OCR tools are
installed and declared.

## Evidence

Everything below was measured on this host at commit `96729ee` (v0.11.0), not
inferred.

### Imported notes open read-only

`checkFidelity` (`web/ui/src/pages/kb/editor.ts:135`) is normalized string
equality between a note's body and that body after a full parse/serialize round
trip through the real editor. When it fails the note is not editable
(`NoteEditor.tsx:887`), `onDirty` never fires (`:113-116`), and no save path can
run (`:539`, `:624`). That is the "I can't edit it later" symptom, exactly.

Driving the **real editor** over 43 realistic converter outputs, **17 failed**:

| Input written by a converter | Editor's canonical form |
|---|---|
| `a < b`, `Growth > 10` | `a &lt; b`, `Growth &gt; 10` |
| `note[^1]`, `[12]` | `note\[^1\]`, `\[12\]` |
| `5* higher` | `5\* higher` |
| `` the ` key `` | ``the \` key`` |
| `~50 items` | `\~50 items` |
| `C:\Users\ada` | `C:\\Users\\ada` |
| `-40 degrees` (line start) | `\-40 degrees` |
| `1) First` | `1. First` |
| `Caf&eacute;` | `Café` |
| `<ops@example.com>` | `[ops@example.com](mailto:ops@example.com)` |
| `> line one\n> line two` | `> line one and line two` |
| `\|A\|B\|` (unpadded table) | `\| A \| B \|` |
| `\| a < b \|` (cell) | `\| a &lt; b \|` |
| `![pic](uploads/my file.png)` | `!\[pic\](uploads/my file.png)` — ceases to be an image |
| `![pic](uploads/a(1).png)` | `![pic](uploads/a\(1\).png)` |
| `Line one` + two trailing spaces + newline | `Line one\\` + newline |
| `Title\n=====` | `# Title` |

Equally important, the **negative** list — verified safe, and over-escaping any of
these would be its own regression: `&`, `_`, `|` in prose, `#` mid-sentence *and*
at line start, `+` at line start, `1990.` at line start, loose *and* tight bullet
lists, nested lists, canonical tables, empty-alt images, headings containing links
or bold, code fences with a language or with embedded backticks.

The root cause is single and mechanical: converters write extracted document text
into markdown **verbatim**. `escapeCell` (`tabular.go:118`) is the only escaper in
the package and handles `|` and newlines only.

### Converters cannot express what the editor supports

Audited emission sites across `internal/convert`. Apart from tables, headings and
flat bullet lists, **only the HTML converter can emit a link or an image at all.**

- **Embedded images are dropped from docx, pptx, xlsx and pdf — silently.** No
  code path reads `word/media/`, `ppt/media/`, `xl/media/` or a PDF image XObject,
  and no warning is appended, though `Warnings` exists precisely to declare a lossy
  conversion.
- **`<img>` with an empty or missing `alt` emits nothing at all**, `src` included
  (`html.go:157` gates the whole case on `alt != ""`). Most real-world images have
  empty alt, so this silently strips images from web pages and HTML email.
- **Ordered lists become unordered** everywhere: `<ol>` has no case and only `<li>`
  is handled, always as `- `; docx's `numPr` sets a boolean. Nesting flattens to
  siblings with no indentation.
- **Table cells are flattened through `textOf`**, destroying bold, links and images
  inside a cell. The same function flattens headings, so a heading containing a link
  loses it.
- **`<details>`, `<div align>`, `<u>`, `<span style="color">` fall through to plain
  text** — precisely the constructs the editor now supports.
- **`<blockquote>` shares a case with `<p>`** and emits no `>` marker, so neither a
  quote nor a callout can survive.
- **Only three HTML attributes are ever read** (`href`, `alt`, `src`), which
  structurally rules out alignment, columns, colspan and code-fence languages.
- **docx never reads the rels part**, so hyperlink targets are lost while link text
  survives; `w:rPr` is never inspected, so bold and italic are lost.

### PDF

**Export is one path lookup away from working.** `findPDFEngine`
(`export/pdf.go:92`) probes `PATH` for six binaries; none is on `PATH` here. But
the platform installs its own Chromium via `rookery browser install`
(`internal/browser`), and `/healthz` reports `"browser":true` at the same moment
the export menu says "PDF (unavailable)" and tells the operator to install
Chromium. Running the **exact argv from `export/pdf.go:59`** against that binary:

```
chrome --headless --no-sandbox --disable-gpu --print-to-pdf=note.pdf note.html
→ 23640 bytes, %PDF-1.4, round-trips back through pdftotext correctly
```

The renderer exists, works, and `internal/export` cannot see it. The output also
carries a `8/31/26, 10:39 AM` header, so the argv needs `--no-pdf-header-footer`.

**Scanned PDFs are told OCR is unavailable on a host that has OCR.** `pdf.go` has
no OCR path and writes *"OCR is not available"* on zero text (`pdf.go:62`), yet
`pdftoppm` (same poppler package as the already-required `pdftotext`) and
`tesseract` (a declared host tool, `hosttools.go:43`) are both present. Measured:

```
pdftoppm -png -r 150 simple.pdf out && tesseract out-1.png stdout
→ "Quarterly Revenue Report / Revenue grew twelve percent this quarter…"
```

**`-layout` structure is preserved and then destroyed.** `runPdftotext` passes
`-layout` to keep columns and tables; `paragraphize` (`pdf.go:206`) then joins
every line of a block with a single space, so a PDF table becomes run-on prose.

**Failure reporting is inverted.** `runPdftotext` captures no stderr and
`extractPDFText` discards the error with no log line, after which the pure-Go
fallback warns *"install poppler-utils on the host"* — blaming the operator for a
missing tool they demonstrably have. The sibling `runTesseract` (`image.go:75`)
does the opposite and folds stderr into the error.

**No engine's argv has ever executed in a test** — `runEngine` is stubbed in every
`ToPDF` test, which is how both packages stay green while the feature is dead.
Consequences: `pandoc note.html -o note.pdf` needs a LaTeX engine it does not
bundle, so on a pandoc host `findPDFEngine` reports PDF *available* and the call
then fails as an opaque 500 — an honest "unavailable" turned into a broken
promise; `google-chrome`, `google-chrome-stable` and `soffice` are never probed
though they are the common binary names; and `pdf.go:141` compares a string to
itself, so the libreoffice rename branch is dead code.

## Design

Three independent changes, shipped as three reviewable PRs. They are ordered so
each is useful alone and none blocks the next.

### PR 1 — the round-trip contract

**`internal/convert/mdtext.go`: one escaper, derived from the editor's own
serializer.**

```go
// EscapeInline escapes text extracted from a document so it survives the KB
// editor's markdown round trip unchanged.
func EscapeInline(s string) string
// EscapeCell composes EscapeInline with the table-cell rules.
func EscapeCell(s string) string
```

`EscapeInline` HTML-escapes `<` and `>` (never `&`), backslash-escapes
`` \ ` * [ ] ~ ``, and escapes a leading `-` that is not followed by a space. It
deliberately does **not** touch `_`, `|`, `#`, `+` or a leading digit-run, because
those were measured safe and escaping them would corrupt ordinary prose.
`EscapeCell` keeps the existing `|`/newline handling and adds `EscapeInline`.

Every converter routes extracted text through it. Text that a converter *itself*
authored as markup (a `# ` prefix, a `- ` bullet, a table pipe) is written as
before — the escaper applies to **document content**, never to the scaffolding.

**The corpus becomes cross-language, because today's bridge can drift.**
`generatorFidelity.test.ts` already asserts converter-shaped markdown survives the
editor, but its fixtures are *hand-written approximations*. A Go-side change can
therefore break real imports while that test stays green.

Instead: `internal/convert` gains a golden test that writes real `ToMarkdown`
output for every fixture to `internal/convert/testdata/fidelity/*.md`, and the
vitest suite reads that directory and runs `checkFidelity` over each file. The
assertion runs against bytes the Go code actually produced. A converter change
that breaks the editor now fails the frontend gate, which is the only place the
editor can be run.

### PR 2 — construct coverage and embedded media

**`Result.Assets` keeps `convert` pure.** `internal/convert` must not gain a vault
or a filesystem — that purity is what makes it testable against golden fixtures.
So the converter returns extracted media rather than writing it:

```go
type Asset struct {
    Name        string // suggested file name, e.g. "image1.png"
    ContentType string
    Data        []byte
}
type Result struct {
    // … existing fields …
    Assets []Asset
}
```

Converters emit a reference to a **stable placeholder path** (`assets/<name>`), and
`vault.ImportFile` — already the single choke point for all four ingest doors —
writes each asset into `uploads/` through the existing `assetName` collision
scheme and rewrites the reference to the real vault-relative path. That is exactly
the shape the editor's image picker and the export inliner already consume, so
extracted images flow into HTML/PDF export for free.

Media extraction is capped by the existing `iolimit` budget and the count is
reported in `Warnings`, so a lossy conversion still declares itself.

**Construct mapping**, each to the canonical form measured in PR 1:

| Source | Emit |
|---|---|
| `<img>` (any alt, incl. empty) | `![alt](src)` — remove the `alt != ""` gate |
| `<ol>` / docx `numPr` with a numbering id | `1.` ordered list |
| nested `<ul>`/`<ol>` | two-space indented children |
| `<blockquote>` | `> ` per line, one line per paragraph |
| `<details>`/`<summary>` | `<details>\n<summary>…</summary>\n\n…\n\n</details>` |
| `<div align>` / `style="text-align"` | `<div align="…">\n\n…\n\n</div>` |
| `<u>` | `<u>…</u>` |
| `<span style="color">` | `<span style="color:#hex">` (lowercase, no space) |
| `<pre><code class="language-go">` | ```` ```go ```` |
| table cell inline content | render marks instead of `textOf` |
| docx `w:rPr` b/i | `**` / `*` |
| docx `w:hyperlink` + rels | `[text](href)` |

Columns are **not** synthesised. No source format carries a construct that maps
cleanly onto `data-cols`, and inventing one would produce layouts the user never
asked for. The node stays available for hand authoring.

### PR 3 — PDF

**Export discovers the browser the platform already installs.** `findPDFEngine`
consults `browser.Probe()` and the Playwright cache path before falling back to
`PATH`, and the Chromium argv gains `--no-pdf-header-footer`. `google-chrome`,
`google-chrome-stable` and `soffice` join the probe list. `pandoc` is **dropped**:
it cannot render HTML→PDF without a LaTeX engine, and a probe that promises a
capability which then fails opaquely is worse than reporting the truth. Engine
stderr is captured and folded into the error so a failure is diagnosable rather
than a bare 500.

A renderer is added to `onboard.HostTools`, `install.sh`, `install.ps1`, the
Dockerfile and the deb/rpm `recommends` (per-format, since the package names
differ) so a host without the browser feature can still export. `packaging/scripts_test.go`
already enforces coverage across all four delivery surfaces and will guide this.

**Import gains OCR and keeps its layout.** On zero or thin text, `pdf.go`
rasterises with `pdftoppm` and OCRs with `tesseract` — both already present, one
of them already a declared host tool — and reports `extractor: "pdftotext+ocr"`.
The "OCR is not available" wording survives only for a host that genuinely lacks
tesseract. Page count is bounded so a 400-page scan cannot pin the CPU.

`paragraphize` learns to recognise a `-layout` tabular block — consecutive lines
sharing multi-space column gaps — and emits a markdown table instead of collapsing
it. Everything else paragraphises as before.

`runPdftotext` captures stderr and folds it into the error; `extractPDFText` logs
the discarded failure; and the pure-Go fallback stops advising an install when the
binary was found and failed.

**One test executes a real engine.** Gated on the engine being present so CI
without one still passes, but it runs the actual argv and asserts `%PDF` magic —
the gap that let this ship dead.

## Testing

- Go unit tests per converter for each construct and each escape rule, against
  golden files.
- The cross-language corpus above: Go writes real converter output, vitest runs the
  real `checkFidelity` over it.
- Asset extraction: fixtures with embedded images for docx, pptx, xlsx; assert the
  bytes come out and the reference resolves after `ImportFile`.
- PDF: a genuinely scanned fixture (the existing `textless.pdf` is blank, so it
  cannot prove OCR); a `-layout` table fixture; the real-engine export test.
- `packaging/scripts_test.go` for the new host tool across all four surfaces.

## Risks and non-goals

- **Over-escaping is the main risk.** The negative list is pinned by tests for
  exactly this reason; escaping `_` or `|` would corrupt identifiers and prose.
- **Notes imported before this change stay read-only** until re-imported or saved
  through the editor's explicit override. Rewriting existing notes in place is a
  migration over user data and is deliberately out of scope.
- **Columns are not synthesised** from any source format (above).
- **DOCX export still degrades images to alt text** — unchanged; that is
  `internal/export/docx.go`'s stated limitation, not part of this work.
