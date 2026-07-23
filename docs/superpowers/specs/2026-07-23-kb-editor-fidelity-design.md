# KB editor fidelity for system-produced notes — design

**Date:** 2026-07-23
**Status:** approved (design)
**Scope:** make notes produced by the platform's own components (uploaded-doc
conversions, reflected chats, inbox notifications, agent run logs, skill output)
open cleanly in the KB **rich-text** editor instead of falling back to raw
markdown with an alarming warning — without ever silently corrupting content.

Split out of the 2026-07-23 chat batch (its item 2) because it touches the KB
editor, not chat, and carries silent-corruption risk that must be reasoned about
on its own.

---

## Problem

The KB note editor decides WYSIWYG-vs-raw with `checkFidelity(body)`
(`web/ui/src/pages/kb/editor.ts`): it loads the markdown into a headless TipTap
editor, serializes back, and compares the normalized round-trip to the
normalized input. On any mismatch it opens the note in **raw** mode and shows:

> "This note uses formatting the rich text editor can't reproduce exactly, so it
> opened as raw markdown. Editing it in rich text would rewrite those parts,
> including ones you didn't touch."

The user reports this on system-produced notes (e.g. a CSV upload), and wants
**all** platform-produced notes fully viewable/editable in rich text.

## Empirical findings (verified, not assumed)

Round-tripping real `internal/convert` output through the actual editor
(`fidelityRoundTrip` in `editor.ts`) shows **two distinct failure modes**. A
blanket loosening of `normalize` is therefore **wrong** — it would fix one and
**corrupt** the other.

1. **Cosmetic, safe — `_italic_` → `*italic*`.** The tabular converter's
   omitted-rows note `_N further rows omitted._` (`internal/convert/tabular.go`)
   re-serializes with asterisk emphasis. Render-identical; flagged only because
   the byte strings differ. (A clean CSV table with no special cells
   round-trips *perfectly* — `checkFidelity == true`.)

2. **Real corruption, unsafe — escaped pipe in a table cell.** A CSV cell
   containing `|` is emitted as `x \| y` (one cell; `escapeCell` in
   `tabular.go`). TipTap parses it correctly into a single cell, but its
   markdown serializer writes it back **without** re-escaping: `x | y` — which
   is now a **column separator**, turning one cell into two and breaking the row
   against its header. `checkFidelity` is **correctly** refusing this: opening
   in WYSIWYG and saving would genuinely corrupt the table.

The codebase already carries the cautionary tale for mode 2: an earlier
normalize-based "fix" in `editor.ts` silently broke every `[[wikilink]]`; the
real fix was a custom serializer (the wikilink atom node), not a comparator
change. See the top-of-file comment in `editor.ts`.

## Design principle

**Fix serializers and generators; do not loosen the comparator.** The fidelity
check is a data-safety net; weakening it globally would weaken it for
user-authored notes too. Instead:

- Where the round-trip **loses/corrupts** content → make the serializer faithful
  (strictly more correct; benefits user notes too; zero added risk).
- Where the round-trip is **render-equivalent** → make the Go generators emit
  TipTap-canonical markdown so system notes pass unchanged (no comparator
  loosening → user-note safety untouched).
- Whatever still genuinely can't round-trip → keep raw fallback, but reword the
  warning to be accurate and non-alarming.

### Fix A — faithful table-cell serialization (corruption case)

Give TipTap's table-cell markdown output a pipe-re-escaping step so a cell
containing `|` serializes back as `\|`, mirroring the wikilink atom-node
approach already blessed in this codebase.

- **Mechanism:** override/extend the markdown serialization for `tableCell` /
  `tableHeader` in `editor.ts`'s extension set (via `tiptap-markdown`'s
  per-node `markdown.serialize` hook, the same mechanism `wikilinks.ts` uses)
  so literal `|` inside a cell is written as `\|` and embedded newlines are kept
  out of the row. After this, a pipe-bearing cell round-trips faithfully and
  `checkFidelity` passes it **without** any comparator change.
- **Why not fix the generator instead:** the corruption is in the *editor's*
  serializer, not the converter — the converter's `\|` is already correct
  GFM. A user could also type a pipe into a table cell in WYSIWYG; fixing the
  serializer protects that path too. This is the right layer.

### Fix B — canonicalize generator output (cosmetic case)

Make the Go generators emit markdown in TipTap's canonical form so system notes
pass `checkFidelity` with no comparator loosening:

- `internal/convert/tabular.go`: emit `*N further rows omitted.*` (asterisk
  emphasis) instead of `_..._`.
- Audit the other platform generators for the same class of render-equivalent
  drift and align them: `internal/vault/reflect.go` (reflected chats, agent run
  logs), the inbox-note writer, reminder notes, skill output. The frontmatter
  block itself is already handled (stripped opaquely by `splitFrontmatter`) and
  is out of scope here.
- Guiding rule: only change *emphasis/spacing/marker* choices that are
  render-equivalent; never change the actual text or structure a generator
  emits.

### Fix C — reword the raw-fallback warning

For notes that still legitimately can't round-trip (genuinely unusual content),
keep raw mode but replace the alarming copy with an accurate, calm message —
e.g. "Opened as raw markdown to preserve exact formatting. Switch to rich text
to edit visually." The warning is in `web/ui/src/pages/kb/NoteEditor.tsx`
(and the banner in `NoteHeader.tsx`).

## Explicitly rejected

- **Blanket `normalize` loosening** (treat any round-trip diff as cosmetic):
  would open the escaped-pipe note in WYSIWYG and **corrupt the table on save**.
  Rejected on the empirical evidence above.
- **Fixed-point check** (`roundtrip(md) == roundtrip(roundtrip(md))`): tolerates
  a *one-shot* content loss that then stabilizes (e.g. a dropped construct), so
  it is not a safe substitute for the content-preservation the current check
  provides.

## Tension (stated explicitly)

Fixing generators + serializer precisely targets **system-produced** notes and
keeps the safety net intact for user-authored notes, at the cost of touching
each generator. It does **not** help externally-imported markdown that happens
to use `_italic_` — such a note still opens raw. That is acceptable: the
reported problem is about the platform's own output, and the alternative
(loosening the comparator) trades a real data-safety guarantee for cosmetic
convenience on imported files. If imported-file fidelity becomes a real
complaint later, it is a separate decision.

## Testing

- **Golden round-trip tests** (frontend, mirroring the existing
  `web/ui/src/pages/kb/frontmatter.test.ts`): assert `checkFidelity(body) === true`
  for real, representative bodies from each generator —
  - a CSV conversion **including a cell with a literal pipe** and the
    omitted-rows line,
  - a reflected chat transcript body,
  - an inbox notification body,
  - an agent run-log body.
  These pin the fix and catch any future generator that regresses back to raw.
- **Serializer unit test** (`editor.test.ts`): a table cell containing `|`
  round-trips to `\|` and `checkFidelity` passes it.
- **Wikilink regression guard stays green:** the existing `editor.test.ts`
  wikilink fidelity contract must not regress (the new table serializer must not
  disturb it).

## Non-goals

- Changing what content generators produce (only their render-equivalent
  emphasis/spacing form).
- Any comparator loosening.
- Externally-imported markdown fidelity.
- The frontmatter handling (already solved by `splitFrontmatter`).
