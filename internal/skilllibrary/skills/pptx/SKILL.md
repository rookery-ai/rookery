---
name: pptx
description: Use this skill any time a .pptx file is involved — reading slides, extracting text, or creating a new presentation/deck. Triggers include "read this powerpoint", "extract slides", "create a deck", "make a presentation".
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

# PPTX

## Reading a deck

```bash
rookery kb convert deck.pptx --dest notes
```

The platform's converter reads the slide XML directly and extracts embedded
images into the knowledge base. **Do not install markitdown for this** — it was
previously recommended here and the platform now does the same job with the
image extraction and the lossy-conversion warning it lacks.

Slides become headings, so `kb_file_map(path=…)` gives you the deck's outline
and `read_file(path=…, section="…")` fetches one slide without reading all of
them.

## Creating a deck

Write the content as markdown with one `##` heading per slide, then let pandoc
build it:

```bash
"$HOME/.local/bin/pandoc" notes/deck.md -o deck.pptx
```

Invoke by absolute path — the runtime environment block gives you the resolved
path of every declared tool.

A reference deck carries your template's theme, master slides and fonts:

```bash
"$HOME/.local/bin/pandoc" notes/deck.md \
  --reference-doc="$HOME/templates/house.pptx" -o deck.pptx
```

**Do not install a JavaScript deck builder.** `pptxgenjs` was previously
recommended here; it means generating a program that positions boxes by
coordinate, which is a far harder job to get right than writing the words and
letting pandoc lay them out — and the result is much harder for the user to
edit afterwards.

## What a deck is for

Slides are a summary medium. If the content only makes sense as continuous
prose, say so and produce a document instead: a deck of dense paragraphs is
worse than the note it came from.
