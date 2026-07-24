---
name: pptx
description: Use this skill any time a .pptx file is involved — reading slides, extracting text, or creating a new presentation/deck. Triggers include "read this powerpoint", "extract slides", "create a deck", "make a presentation".
version: 1.0.0
license: MIT-0
category: File Processing
metadata:
  install:
    - kind: pip
      package: markitdown
    - kind: node
      package: pptxgenjs
      bins: [pptxgenjs]
---

# PPTX

Read PowerPoint `.pptx` slides (via `markitdown`) or create decks from scratch
(via `pptxgenjs` on Node). Mixed runtime: Python for reading, Node for creating.

## Requirements

- Read: `markitdown` (Python) — `python3 -m pip install --user markitdown`.
- Create: `pptxgenjs` (Node) — `npm install -g --prefix "$HOME/.local" pptxgenjs`.
  The runtime env block tells you the node module path; invoke via `node`.

## Read a presentation

```bash
# markitdown dumps slide text as Markdown
python3 -m markitdown input.pptx
```

```python
# Slide-by-slide structure
from pptx import Presentation
import json, sys
prs = Presentation(sys.argv[1])
out = []
for i, slide in enumerate(prs.slides):
    texts = [p.text for s in slide.shapes if s.has_text_frame for p in s.text_frame.paragraphs if p.text.strip()]
    notes = slide.notes_slide.notes_text_frame.text if slide.has_notes_slide else ""
    out.append({"slide": i+1, "texts": texts, "notes": notes})
print(json.dumps(out))
```
(`python-pptx` is a fallback reader: `pip install --user python-pptx`.)

## Create a deck (pptxgenjs, Node)

```javascript
const pptxgen = require("pptxgenjs");
const p = new pptxgen();
const s = p.addSlide();
s.addText("Hello World", { x:1, y:1, w:8, h:1, fontSize:32, bold:true });
p.writeFile({ fileName: "output.pptx" });
```

## Best practices

- Reading is reliable; creating from scratch needs design care (palette, fonts).
  Prefer editing an existing template deck when the user has one.
- Always output into the vault.
- For PDF export of a deck, LibreOffice (`soffice`) is needed — surface that
  dependency rather than failing silently.