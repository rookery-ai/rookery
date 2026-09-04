---
name: docx
description: Use this skill whenever the user wants to read, create, edit, or convert Word documents (.docx). Triggers include "read this word doc", "convert docx to markdown", "create a docx", "edit the word file", "docx to pdf".
version: 2.0.0
license: MIT-0
category: File Processing
metadata:
  requires:
    bins: [pandoc]
  install:
    - kind: binary
      bin: pandoc
      url: https://github.com/jgm/pandoc/releases/download/3.6.4/pandoc-3.6.4-linux-amd64.tar.gz
      strip: 1
---

# DOCX

Reading and writing are different problems here. Reading is a platform tool;
writing is pandoc. Neither is a Python library.

## Reading — use the platform, not a library

```bash
rookery kb convert report.docx --dest notes
```

This runs the platform's own converter, which you will not beat by hand: it
pulls the text, recovers tables, extracts embedded images into the knowledge
base, and **warns in the note's frontmatter when the conversion looked lossy**,
so a document that converted badly says so instead of silently giving you less
than it had.

Then read the markdown note like any other. For a long one, run
`kb_file_map(path=…)` first to see its headings and size, and `read_file` with
`section=` to fetch just the part you need.

**Do not use python-docx to read.** It was previously recommended here and it is
strictly worse: it sees no images, recovers tables poorly, and tells you nothing
when it has missed content.

## Writing — pandoc

```bash
"$HOME/.local/bin/pandoc" notes/report.md -o report.docx
```

Invoke by absolute path — the runtime environment block gives you the resolved
path of every tool a declared skill requires.

For a reference style (fonts, headings matching a house template):

```bash
"$HOME/.local/bin/pandoc" notes/report.md \
  --reference-doc="$HOME/templates/house.docx" -o report.docx
```

## docx → PDF

```bash
"$HOME/.local/bin/pandoc" report.docx -o report.pdf
```

If that fails, pandoc has no PDF engine installed. Convert to markdown and let
the knowledge base export the PDF instead of installing LaTeX — the platform
already has a renderer.

## Editing an existing document

There is no in-place edit worth doing. Convert to markdown, edit the markdown
(where every other tool you have works), and write a new `.docx`. A round trip
loses styling the source had; say so to the user rather than presenting the
result as an edit of their file.
