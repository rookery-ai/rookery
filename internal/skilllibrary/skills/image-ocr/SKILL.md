---
name: image-ocr
description: Use this skill to read text out of an image — a screenshot, a scanned page, a photo of a receipt or a whiteboard. Runs OCR via the tesseract command line tool, installing it first if it is missing. Triggers include "what does this screenshot say", "read this scan", "extract the text from this image", "ocr this", "what's written in the photo".
version: 1.0.0
license: MIT-0
category: File Processing
metadata:
  openclaw:
    requires:
      bins: [tesseract]
---

# Image OCR

Read text out of an image — a screenshot, a scanned document, or a photo —
using the `tesseract` command line tool directly. No Python wrapper is
needed; tesseract's own CLI is the whole toolchain.

## Find tesseract before running it

Check `$HOME/.local/bin/tesseract` first — that's where the
`cli-tool-installer` skill puts it, and it is NOT on the sandboxed agent's
PATH — then fall back to whatever `command -v tesseract` resolves on PATH:

```bash
if [ -x "$HOME/.local/bin/tesseract" ]; then
  TESSERACT="$HOME/.local/bin/tesseract"
elif command -v tesseract >/dev/null 2>&1; then
  TESSERACT="$(command -v tesseract)"
else
  TESSERACT=""
fi
```

If `$TESSERACT` is empty, install it via the `cli-tool-installer` skill
before doing anything else — do not attempt to read the image any other way.
Once installed it will be at `$HOME/.local/bin/tesseract`; always invoke it
by that absolute path, never bare `tesseract`.

## Run OCR

```bash
"$HOME/.local/bin/tesseract" image.png stdout -l eng
```

`stdout` as the output argument keeps the recognised text in the pipe
instead of writing a `.txt` file next to the image. Redirect to a file in
`$TMPDIR` (never `/tmp`) only if the caller specifically needs one on disk.

## Page segmentation mode: block of text vs. full page

Tesseract's default segmentation assumes a full page of text with normal
layout (columns, paragraphs). For a screenshot, a receipt, or any image that
is essentially one block of text with no page structure, force single-block
mode with `--psm 6` — it is usually both faster and more accurate for these
cases:

```bash
# A screenshot, receipt, or single paragraph — one block of text
"$HOME/.local/bin/tesseract" screenshot.png stdout -l eng --psm 6

# A scanned multi-column document or full page — leave psm at its default
"$HOME/.local/bin/tesseract" scanned-page.png stdout -l eng
```

If the result looks garbled with one mode, retrying with the other is a
reasonable first troubleshooting step before concluding the image is
unreadable.

## Quality depends on resolution — report garbage, don't guess at it

OCR accuracy is directly tied to image resolution and sharpness. A
low-resolution, heavily compressed, or blurry source will produce
nonsensical or fragmented output — stray characters, broken words, wrong
punctuation. When the recognised text looks like that, say so explicitly
("the image is too blurry/low-resolution for reliable OCR — here's the raw
output, but treat it as unreliable") rather than trying to smooth it into a
plausible-sounding answer. Never invent or "correct" words that weren't
actually recognisable; a partial, honestly-flagged result is more useful
than a confident guess.

## Multiple languages

Pass a `+`-joined language list when the image mixes languages tesseract has
data for (default install typically ships `eng` only — additional language
packs need to be installed separately, which is out of scope for this
skill unless the user asks):

```bash
"$HOME/.local/bin/tesseract" image.png stdout -l eng+deu --psm 6
```

## Notes

- Empty output (`tesseract` exits cleanly but stdout is blank) usually means
  the image contains no readable text at that segmentation mode — try the
  other `--psm` value before reporting failure.
- For a PDF instead of an image, use the `pdf` skill first (scanned/image-only
  PDFs need OCR too, but convert pages to images before running tesseract).
- Always work from a copy in `$TMPDIR`, never `/tmp`, if the source image
  needs any preprocessing.
