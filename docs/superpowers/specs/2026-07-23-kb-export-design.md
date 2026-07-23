# KB note export (Theme C) — design

**Date:** 2026-07-23
**Status:** approved (design, self-authorized under user delegation)
**Scope:** export a KB markdown note to other formats — HTML, DOCX, PDF (plus the
already-available raw `.md` download). Reported item #3.

CLAUDE.md notes this as "a planned future KB action" and stresses conversion is
otherwise one-directional (into markdown). Export is the sanctioned reverse
direction, kept OUT of the `internal/convert` package (which stays
into-markdown-only) in a new `internal/export` package.

---

## Approach: guaranteed pure-Go formats + best-effort PDF

- **HTML** and **DOCX** are produced **pure-Go, always available** (no host
  deps), mirroring how `internal/convert` reads DOCX with `archive/zip` +
  `encoding/xml`.
- **PDF** is **best-effort via a headless tool on PATH** (detected at request
  time), with a clear "install X for PDF" message when none is present — the
  exact philosophy `internal/convert` already uses for `pdftotext`. A home
  server should not be forced to bundle a browser engine.

## `internal/export` package

Pure function surface, testable against golden fixtures (no vault/network):

- **`ToHTML(md []byte, opts) ([]byte, error)`** — goldmark render (reuse the
  same goldmark config as `web/handlers_kb.go`'s `renderMarkdown`, minus the
  dead `/dashboard/kb/view` wikilink rewrite — see Theme E; here wikilinks
  render as their display text or an intra-doc anchor, external links preserved).
  Wrapped in a minimal self-contained HTML document with readable default CSS
  inlined (headings, code, tables). This HTML is also the PDF source.
- **`ToDOCX(md []byte, opts) ([]byte, error)`** — build a minimal OOXML package
  (`[Content_Types].xml`, `word/document.xml`, rels) with `archive/zip` +
  `encoding/xml`. Support the block set the editor produces: headings,
  paragraphs, bold/italic/code runs, bullet/numbered lists, blockquotes, code
  blocks (monospace), tables, horizontal rules, links. Unsupported constructs
  degrade to plain paragraphs rather than failing.
- **`ToPDF(md []byte, opts) ([]byte, error)`** — render `ToHTML` then convert via
  the first available of: `weasyprint`, `chromium --headless --print-to-pdf`,
  `wkhtmltopdf`, `libreoffice --headless --convert-to pdf`, `pandoc`. Detection
  via `exec.LookPath`. None found → typed `ErrNoPDFEngine` so the handler returns
  a helpful 501/422 message. Runs with a context timeout; the child reads HTML
  from a temp file in `$TMPDIR`, writes PDF to another; both cleaned up.
- **`AvailableFormats()`** → reports which formats are usable right now (html +
  docx always; pdf iff an engine is on PATH) so the UI can grey out PDF when
  unavailable.

## Endpoint

`GET /api/v1/kb/export?path=<rel>&format=html|docx|pdf`
- Owner + active-workspace + setup guards (same group as the rest of `/kb`).
- `.md` only (a non-markdown file 400s — export is for notes).
- Reads the note via `s.vault.ReadNote`, splits frontmatter off (frontmatter is
  not rendered into the export body; optionally rendered as a small metadata
  header — **default: omitted**), calls the matching `export.To*`, streams with
  `Content-Disposition: attachment; filename="<note-stem>.<ext>"` and the right
  `Content-Type`.
- `format=pdf` with no engine → 422 `pdf_unavailable` + message listing the
  tools that would enable it.
- `GET /api/v1/kb/export/formats` → `AvailableFormats()` for the UI.

## UI

- An **Export** control in `NoteHeader.tsx` (a dropdown next to the existing
  rename/delete affordances): "Download as HTML / Word (.docx) / PDF / Markdown
  (.md)". PDF is disabled with a tooltip when `formats` reports it unavailable.
  Markdown reuses the existing `GET /kb/raw`.
- Each item triggers a browser download of the export URL (anchor with
  `download`, or fetch→blob→object-URL for auth-header cases; the API is
  cookie-session-authed, so a direct navigation/anchor works).

## Testing

- **Go golden tests** (`internal/export`): a representative markdown body →
  HTML (assert structure/escaping), → DOCX (unzip in the test, assert
  `document.xml` contains expected runs/tables), → PDF path with a **stubbed
  engine** (a fake `exec` via a PATH shim or an injected runner) asserting the
  HTML→engine handoff and the `ErrNoPDFEngine` branch.
- Handler tests: format routing, `.md`-only guard, Content-Disposition,
  pdf-unavailable 422.
- Frontend: the Export menu renders, greys PDF when unavailable, and hits the
  right URL.

## Non-goals
- Round-trip export→re-import (export is terminal).
- Exporting folders/whole-vault as an archive (single note only for now).
- Bundling a PDF engine.
