---
name: docx
description: Use this skill whenever the user wants to read, create, edit, or convert Word documents (.docx). Triggers include "read this word doc", "convert docx to markdown", "create a docx", "edit the word file", "docx to pdf".
version: 1.0.0
license: MIT-0
category: File Processing
metadata:
  openclaw:
    requires:
      bins: [pandoc]
    install:
      - kind: binary
        bin: pandoc
        url: https://github.com/jgm/pandoc/releases/download/3.6.4/pandoc-3.6.4-linux-amd64.tar.gz
        strip: 1
      - kind: pip
        package: python-docx
---

# DOCX

Read, create, edit, and convert Word `.docx` files. Uses `pandoc` for
reading/conversion and `python-docx` for creation/editing.

## Requirements

- `pandoc` (CLI) — read DOCX, convert DOCX↔Markdown/HTML/PDF.
- Python `python-docx` — create/edit: `python3 -m pip install --user python-docx`.
- For `.doc`→`.docx` or DOCX→PDF via LibreOffice: optional `soffice` (heavy; not
  required). Use pandoc for most conversions.

The runtime environment block gives you pandoc's absolute path. Invoke it by
that path.

## Read / convert (pandoc)

```bash
# DOCX -> Markdown (clean)
pandoc input.docx -t gfm --wrap=none -o output.md

# DOCX -> plain text
pandoc input.docx -t plain -o output.txt

# Markdown -> DOCX
pandoc input.md -o output.docx

# DOCX -> HTML
pandoc input.docx -s -o output.html
```

## Create a new document (python-docx)

```python
from docx import Document
doc = Document()
doc.add_heading("Report Title", 0)
doc.add_paragraph("Body text here.")
doc.add_heading("Section", 1)
doc.add_paragraph("More text.")
doc.save("output.docx")
```

## Edit an existing document

```python
from docx import Document
import sys
doc = Document(sys.argv[1])
for para in doc.paragraphs:
    if "OLD" in para.text:
        para.text = para.text.replace("OLD", "NEW")
doc.save(sys.argv[1])
```

## Converting a .docx

`pandoc` handles every direction. Resolve it at `$HOME/.local/bin/pandoc` first (where the
cli-tool-installer skill puts it — it is NOT on the sandboxed PATH), then fall back to
`command -v pandoc`:

```bash
"$HOME/.local/bin/pandoc" notes.docx -t markdown          # .docx to markdown on stdout
"$HOME/.local/bin/pandoc" notes.docx -o notes.md          # write the file directly
"$HOME/.local/bin/pandoc" notes.md -o notes.docx          # and back again
```

If pandoc is missing, report that and point at the cli-tool-installer skill rather than
attempting a partial conversion.

## Best practices

- For tracked changes, comments, and fidelity beyond text, pandoc's `--track-changes=all`
  preserves them in the Markdown output.
- Set page size explicitly when creating (python-docx defaults to A4-ish).
- Use the `markdown` skill for richer format conversion once text is in Markdown.
- Write outputs into the vault, never `/tmp`.