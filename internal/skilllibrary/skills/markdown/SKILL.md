---
name: markdown
description: Use this skill whenever the user wants to convert between Markdown and other document formats (DOCX, HTML, PDF, EPUB, LaTeX), extract front matter, render Markdown, or round-trip documents through pandoc. Triggers include "convert to markdown", "docx to md", "markdown to pdf", "render this markdown".
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
---

# Markdown Conversion (pandoc)

Convert between Markdown and DOCX/HTML/PDF/EPUB/LaTeX using `pandoc`. Pandoc is
the industry-standard document converter.

## Requirements

- `pandoc` (CLI). On this platform it is installed into the user's local bin dir
  and invoked by absolute path — the runtime environment block tells you the
  exact path. If it is missing, install it via the `cli-tool-installer` skill or
  download the static binary yourself.
- For PDF output: pandoc uses a LaTeX engine (`pdflatex`/`xelatex`) OR
  `wkhtmltopdf`. If neither is available, convert to HTML first, then ask the
  user how they want a PDF, or use `--pdf-engine=weasyprint` (pip).

## Convert a file

```bash
# Markdown -> DOCX
pandoc input.md -o output.docx

# DOCX -> Markdown (clean, with metadata block)
pandoc input.docx -t gfm -o output.md

# Markdown -> HTML (standalone, with <head>)
pandoc input.md -s -o output.html

# Markdown -> PDF (requires a LaTeX engine; see above)
pandoc input.md -o output.pdf
```

## Extract front matter / metadata

```bash
# Dump the YAML/title metadata block as JSON
pandoc input.md --to json | jq '.meta'
```

## Round-trip with a reference template

```bash
# Use a reference docx for styling
pandoc input.md --reference-doc=reference.docx -o output.docx
```

## Best practices

- Always write output into the user's vault or your own agent dir, never `/tmp`
  (use `$TMPDIR` for scratch).
- For tables/footnotes: GFM (`-t gfm`) drops some features; prefer `-t markdown`
  or `-t commonmark` when fidelity matters.
- When converting DOCX→MD, pipe through `pandoc --wrap=none` to avoid hard line
  wraps.
- Verify the output exists and is non-empty before reporting success.