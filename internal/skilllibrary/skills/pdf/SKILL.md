---
name: pdf
description: Use this skill whenever the user wants to read, extract text/tables from, merge, split, rotate, or convert PDF files. Triggers include "extract text from pdf", "read this pdf", "merge pdfs", "split pdf", "pdf to markdown", "pdf metadata".
version: 2.0.0
license: MIT-0
category: File Processing
metadata:
  requires:
    anyBins: [pdftotext, qpdf]
  install:
    - kind: pip
      package: pypdf
---

# PDF

## Reading — the platform already does this well

```bash
rookery kb convert paper.pdf --dest notes
```

This is not a thin wrapper. It prefers `pdftotext -layout` when poppler is
installed, falls back to a pure-Go extractor otherwise, **recovers `-layout`
column blocks into real markdown tables**, and **falls back to OCR** (`pdftoppm`
+ `tesseract`) when the PDF has no usable text layer at all.

Most importantly it **warns when the extraction looks thin**, so a scanned
document cannot quietly pass as a clean one. That warning lands in the note's
frontmatter. Read it: an empty-looking PDF is nearly always a scan, and the
answer is OCR rather than a different library.

**Do not install pdfplumber to read a PDF.** It was previously recommended here
and it does less: no OCR fallback, no thin-extraction warning, and a table
recovery you have to drive yourself.

## Reading a long one

```
kb_file_map(path="notes/paper.md")
read_file(path="notes/paper.md", section="Results")
```

A converted paper is often tens of thousands of words. Map it, then fetch the
section you need — reading the whole thing to answer one question is how a run
spends its turn budget and returns nothing.

## Tables inside a PDF

The converter already turns `-layout` column blocks into markdown tables, so
once converted:

```
kb_table_query(path="notes/paper.md", op="sum", column="amount")
```

If the table did not survive conversion, the frontmatter warning will say the
extraction was thin. That is a scan — OCR it rather than reaching for a parsing
library.

## Manipulating the file itself

Merging, splitting, rotating and reading metadata are not conversion, and there
is no platform tool for them. `pypdf` is declared for exactly this:

```python
from pypdf import PdfReader, PdfWriter
w = PdfWriter()
for path in ["a.pdf", "b.pdf"]:
    for page in PdfReader(path).pages:
        w.add_page(page)
with open("merged.pdf", "wb") as f:
    w.write(f)
```

`qpdf` does the same from the shell if it is installed
(`qpdf --empty --pages a.pdf b.pdf -- merged.pdf`) and is worth preferring when
it is: one command beats a program.

## Producing a PDF

Write markdown into the knowledge base and export it from there. The platform
has a renderer already (`rookery browser install` provides one), so this needs
no LaTeX and no extra dependency.
