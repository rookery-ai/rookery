---
name: pdf
description: Use this skill whenever the user wants to read, extract text/tables from, merge, split, rotate, or convert PDF files. Triggers include "extract text from pdf", "read this pdf", "merge pdfs", "split pdf", "pdf to markdown", "pdf metadata".
version: 1.0.0
license: MIT-0
category: File Processing
metadata:
  openclaw:
    requires:
      anyBins: [pdftotext, pandoc]
    install:
      - kind: binary
        bin: pandoc
        url: https://github.com/jgm/pandoc/releases/download/3.6.4/pandoc-3.6.4-linux-amd64.tar.gz
        strip: 1
      - kind: pip
        package: pdfplumber
      - kind: pip
        package: pypdf
---

# PDF

Read, extract, merge, split, and convert PDF files. Mixed toolchain: CLI tools
(pdftotext/pandoc) for fast text, Python (pdfplumber/pypdf) for tables and
manipulation.

## Requirements

- `pdftotext` (poppler) — fastest plain-text extraction. Installed via the
  `cli-tool-installer` skill if missing (no clean static binary; pip fallback
  below covers text).
- `pandoc` — for PDF↔Markdown/DOCX round-trips.
- Python: `pdfplumber` (text+tables), `pypdf` (merge/split/rotate/metadata).
  Install with `python3 -m pip install --user pdfplumber pypdf`.

The runtime environment block tells you the absolute path of any installed CLI
tool. Invoke CLI tools by that absolute path; invoke Python via `python3`.

## Extract text

```bash
# Fastest — poppler
pdftotext -layout input.pdf output.txt
```

```python
# pdfplumber — page-by-page with layout preserved
import pdfplumber, json, sys
with pdfplumber.open(sys.argv[1]) as pdf:
    pages = [{"page": i+1, "text": (p.extract_text() or "")} for i, p in enumerate(pdf.pages)]
print(json.dumps(pages))
```

## Extract tables

```python
import pdfplumber, json, sys
with pdfplumber.open(sys.argv[1]) as pdf:
    tables = [{"page": i+1, "rows": t} for i, p in enumerate(pdf.pages) for t in p.extract_tables()]
print(json.dumps(tables))
```

## Merge / split / rotate / metadata

```python
from pypdf import PdfReader, PdfWriter
import sys
# Merge
w = PdfWriter()
for f in sys.argv[2:]:
    w.append(f)
w.write(sys.argv[1])  # output path
# Split: write each page
r = PdfReader(sys.argv[1])
for i, page in enumerate(r.pages):
    o = PdfWriter(); o.add_page(page)
    with open(f"page-{i+1}.pdf", "wb") as fh: o.write(fh)
```

## Notes

- Scanned/image-only PDFs return empty text — they need OCR (tesseract + the
  `cli-tool-installer` skill). Surface this to the user rather than reporting
  "empty".
- Always write outputs into the vault or `$TMPDIR`, never `/tmp`.
- `extract_text()` can return `None` for image pages — guard with `or ""`.