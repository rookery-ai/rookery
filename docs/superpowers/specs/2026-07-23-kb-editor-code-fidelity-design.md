# KB editor code/YAML fidelity (Theme B) — design

**Date:** 2026-07-23
**Status:** approved (design, self-authorized under user delegation)
**Scope:** the remaining "opens as RAW / YAML looks weird" cases after the
merged `2026-07-23-kb-editor-fidelity` work — specifically **fenced code blocks
with a language info string** (```yaml, ```json, ```python, …), which that spec
did not address.

Reported item #7. Builds directly on `2026-07-23-kb-editor-fidelity-design.md`
(tables/emphasis, already merged) and inherits its **design principle: fix
serializers/generators, never loosen the fidelity comparator.**

---

## Problem

The user still sees notes open in raw mode and "YAML always displayed weird —
by the converter or when agents write to notes." The earlier fidelity spec fixed
table-cell pipes and emphasis drift but did **not** touch fenced code blocks.

Two suspected, must-be-verified failure modes for a ```lang fenced block:

1. **Info string dropped/altered on round-trip.** TipTap StarterKit's
   `codeBlock` stores a `language` attribute, and `tiptap-markdown` should write
   it back as ```lang — but if the language is lost (```yaml → ```), the
   round-trip differs and `checkFidelity` sends the note to raw. A YAML/JSON
   block is the most visible victim.
2. **Code content re-escaped or reflowed.** Indentation, blank lines inside a
   fence, or trailing whitespace normalized differently on the way out.

## Diagnosis first (systematic-debugging)

Before any change, run representative bodies through `fidelityRoundTrip`
(`editor.ts`, headless) and diff:
- a ```yaml block (frontmatter-style keys, list items, nested map),
- a ```json block,
- a ```python block with blank lines and indentation,
- a bare ``` fence with no language,
- an agent-written note that mixes prose + a ```yaml config example.

The diff pins which of the two modes fires (and whether both do). The fix is
chosen from the evidence, exactly as the prior spec did with tables.

## Design (contingent on diagnosis; expected shape)

### Fix A — preserve the code-fence language through the round-trip
If mode 1 is confirmed, configure the code block so the info string survives:
- Prefer configuring StarterKit's `codeBlock` / `tiptap-markdown` so the
  `language` attribute is parsed from and serialized back to the ```lang fence.
- If `tiptap-markdown`'s default serializer drops it, add a per-node
  `markdown.serialize` hook (the same mechanism `wikilinks.ts` and the merged
  table-cell fix use) that writes ```` ```<language> ```` and the verbatim
  content. **No comparator change.**

### Fix B — verbatim code content
Ensure the code block serializer emits the fence body byte-for-byte (no
re-escaping of `|`, `*`, backslashes inside code; no trailing-space trimming
that would change content). Code is literal — nothing inside a fence should be
transformed.

### Fix C — syntax highlighting (readability, optional within this theme)
So YAML/JSON/code *reads* correctly rather than as flat monospace, add
`@tiptap/extension-code-block-lowlight` + `lowlight` (highlight.js) OR a
CSS-only treatment. **Recommendation:** ship the fidelity fix (A+B) first; add
highlighting only if it does not disturb round-trip fidelity. Highlighting is
render-only and must not touch the markdown serialization. If it risks the
round-trip, defer it — the user's core complaint is "displayed weird / opens
raw," which A+B resolve.

### Fix D — generator audit for code fences
If any Go generator (`internal/convert`, `internal/vault` reflectors, agent
output) emits a code fence in a non-canonical form (e.g. ~~~ tildes, or an
indented code block where a fence is canonical), align it to the TipTap-canonical
```lang fence — same render-equivalent-only rule as the prior spec's Fix B.

## Explicitly rejected
- **Replacing TipTap.** It represents code blocks with a language attribute
  natively; the problem is serialization fidelity, not the editor's capability.
  Replacement is YAGNI and would discard the merged fidelity work.
- **Loosening `normalize`/`checkFidelity`.** Same reasoning as the prior spec:
  it weakens the safety net for user notes.

## Testing
- Golden round-trip tests (`editor.test.ts`) asserting `checkFidelity === true`
  for ```yaml, ```json, ```python, and a mixed prose+YAML body.
- A serializer unit test: a ```yaml block round-trips to ```yaml (language
  preserved) with content unchanged.
- The existing wikilink + table-cell fidelity guards must stay green.
- If highlighting lands: a render test that the block still serializes to the
  same markdown (highlighting is DOM-only).

## Non-goals
- Externally-imported markdown fidelity (inherited non-goal).
- Comparator changes.
- Changing what generators emit beyond render-equivalent fence form.
