# KB editor fidelity for system-produced notes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make platform-produced notes open in the KB rich-text editor instead of falling back to raw markdown — by fixing the two real round-trip failures (table-cell pipe escaping; underscore-vs-asterisk emphasis) at their source, never by loosening the fidelity check.

**Architecture:** Two source-level fixes plus golden tests. (1) A `PipeSafeTable` TipTap extension re-escapes `|` inside table cells during markdown serialization (verified prototype below), fixing real corruption. (2) The tabular converter emits `*…*` instead of `_…_` for its omitted-rows note, a render-equivalent canonicalization. (3) Golden round-trip tests pin real generator outputs as WYSIWYG-safe. (4) The raw-fallback warning copy is softened for genuinely unrepresentable residuals.

**Tech Stack:** TypeScript + TipTap 3 + tiptap-markdown + prosemirror-markdown + vitest; Go (`internal/convert`).

## Global Constraints

- **Conventional Commits**; branch `chat-kb-improvements`, never commit to `main`.
- **Never loosen `checkFidelity` / `normalize`** — the escaped-pipe case proves a blanket loosening would corrupt data on the next WYSIWYG save. Fix serializers/generators only.
- The existing **wikilink fidelity contract** (`editor.test.ts`) must stay green — the table serializer must not disturb `[[wikilink]]` round-tripping.
- Only change generators' **render-equivalent** emphasis/spacing form, never the text or structure they emit.
- The frontmatter handling (`splitFrontmatter`) is already correct and out of scope.
- Frontend tests: `cd web/ui && npx vitest run <file>`. Go tests: `go test ./internal/convert/ -count=1`.

---

### Task 1: Pipe-safe table-cell serialization

**Files:**
- Create: `web/ui/src/pages/kb/pipeSafeTable.ts` (the `PipeSafeTable` extension)
- Modify: `web/ui/src/pages/kb/editor.ts` (use `PipeSafeTable` in `buildExtensions`)
- Test: `web/ui/src/pages/kb/editor.test.ts` (add pipe-cell fidelity cases)

**Background (verified).** tiptap-markdown's table serializer (`node_modules/tiptap-markdown/src/extensions/nodes/table.js`) renders each cell with `state.renderInline(cell)`. prosemirror-markdown's text escaping (`state.esc`) escapes CommonMark punctuation but **not** `|`, so a cell containing a literal pipe serializes back as a column separator — one cell becomes two, corrupting the table. A prototype that wraps `state.esc` to also escape `|` during table serialization was confirmed to round-trip pipe cells, bold cells, and wikilink cells all faithfully.

**Interfaces:**
- Consumes: `@tiptap/extension-table` `Table`, tiptap-markdown's per-node `addStorage().markdown.serialize` mechanism (same one `wikilinks.ts` uses).
- Produces: `export const PipeSafeTable` (a `Table.extend(...)`), consumed by `buildExtensions` in `editor.ts`.

- [ ] **Step 1: Write the failing fidelity test**

Add to `web/ui/src/pages/kb/editor.test.ts`:

```ts
import { checkFidelity } from "./editor";

describe("table cell fidelity", () => {
  it("a cell containing a literal pipe round-trips (no corruption)", () => {
    const md = "| a | b |\n| --- | --- |\n| x \\| y | z |\n";
    expect(checkFidelity(md)).toBe(true);
  });
  it("a plain table still round-trips", () => {
    const md = "| name | qty |\n| --- | --- |\n| Widget | 3 |\n";
    expect(checkFidelity(md)).toBe(true);
  });
  it("marks and wikilinks inside cells still round-trip", () => {
    const md = "| a | b |\n| --- | --- |\n| **bold** and [[note]] | z |\n";
    expect(checkFidelity(md)).toBe(true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts -t "table cell fidelity"`
Expected: the pipe-cell case FAILS (`checkFidelity` returns false); the plain-table case passes.

- [ ] **Step 3: Implement `pipeSafeTable.ts`**

Create `web/ui/src/pages/kb/pipeSafeTable.ts`:

```ts
import { Table } from "@tiptap/extension-table";

// PipeSafeTable overrides tiptap-markdown's table serialization to escape a
// literal "|" inside table cells as "\|". tiptap-markdown's own table
// serializer (extensions/nodes/table.js) renders each cell with
// state.renderInline(), and prosemirror-markdown's text escaping (state.esc)
// does NOT escape "|", so a cell containing a pipe would serialize back as a
// column separator — turning one cell into two and corrupting the table. That
// mismatch is exactly what made checkFidelity refuse CSV notes with pipe-bearing
// cells and force them into raw mode.
//
// The fix wraps state.esc only for the duration of table rendering, so it
// affects ONLY literal-text escaping (where a stray "|" can appear). Marks
// (**bold**, links) and the [[wikilink]] atom (which writes via state.write,
// not esc) are untouched — verified round-tripping pipe/bold/wikilink cells.
//
// This mirrors the "fix the serializer, not the comparator" approach already
// used for wikilinks (see wikilinks.ts) rather than loosening checkFidelity,
// which would open pipe-cell tables in WYSIWYG and destroy them on save.

// Minimal shapes for the serializer state/node tiptap-markdown hands us.
interface SerState {
  esc(str: string, startOfLine?: boolean): string;
  write(s: string): void;
  renderInline(node: PMNode): void;
  ensureNewLine(): void;
  closeBlock(node: PMNode): void;
  inTable: boolean;
}
interface PMNode {
  childCount: number;
  firstChild: PMNode | null;
  textContent: string;
  forEach(cb: (child: PMNode, offset: number, index: number) => void): void;
}

export const PipeSafeTable = Table.extend({
  addStorage() {
    return {
      markdown: {
        serialize(state: SerState, node: PMNode) {
          state.inTable = true;
          const origEsc = state.esc.bind(state);
          state.esc = (str: string, startOfLine?: boolean) =>
            origEsc(str, startOfLine).replace(/\|/g, "\\|");
          node.forEach((row: PMNode, _p: number, i: number) => {
            state.write("| ");
            row.forEach((col: PMNode, _q: number, j: number) => {
              if (j) state.write(" | ");
              const cellContent = col.firstChild;
              if (cellContent && cellContent.textContent.trim()) {
                state.renderInline(cellContent);
              }
            });
            state.write(" |");
            state.ensureNewLine();
            if (!i) {
              const delimiterRow = Array.from({ length: row.childCount })
                .map(() => "---")
                .join(" | ");
              state.write(`| ${delimiterRow} |`);
              state.ensureNewLine();
            }
          });
          state.esc = origEsc;
          state.closeBlock(node);
          state.inTable = false;
        },
        parse: {},
      },
    };
  },
});
```

> Note: this serializer intentionally does not reproduce tiptap-markdown's `isMarkdownSerializable` HTML-fallback branch (for spanned/multi-block cells). The KB editor never creates colspan/rowspan cells, and system-generated tables are always simple; a simple table is the only shape this codebase produces or edits. If a spanned table ever needs support, port that branch from `table.js` — but do not add it speculatively (YAGNI).

- [ ] **Step 4: Use `PipeSafeTable` in `editor.ts`**

In `web/ui/src/pages/kb/editor.ts`, replace the table import + usage. Change:

```ts
import { Table } from "@tiptap/extension-table";
```

to:

```ts
import { PipeSafeTable } from "./pipeSafeTable";
```

and in `buildExtensions`, replace:

```ts
    Table.configure({ resizable: false }),
```

with:

```ts
    PipeSafeTable.configure({ resizable: false }),
```

(Leave `TableRow`, `TableCell`, `TableHeader` imports and usage unchanged — only the top-level `Table` node owns serialization.)

- [ ] **Step 5: Run the new + existing editor tests**

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts`
Expected: PASS — the new pipe-cell cases pass AND every existing case (including the wikilink fidelity contract) still passes.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/kb/pipeSafeTable.ts web/ui/src/pages/kb/editor.ts web/ui/src/pages/kb/editor.test.ts
git commit -m "fix(web/kb): escape pipes in table-cell markdown so pipe cells stay WYSIWYG-safe"
```

---

### Task 2: Canonicalize the tabular converter's emphasis output

**Files:**
- Modify: `internal/convert/tabular.go` (omitted-rows note: `_…_` → `*…*`)
- Test: `internal/convert/tabular_test.go` (assert the asterisk form)

**Background (verified).** The omitted-rows note `_%d further rows omitted (%d total)._` uses underscore emphasis, which TipTap re-serializes as `*…*` — render-identical but enough to fail `checkFidelity` and force raw mode. Emitting `*…*` at the source makes the note WYSIWYG-safe with no comparator change. `maxTableRows` (50000) is impractical to exceed in a test, so factor the note into a tiny pure helper and test that directly.

**Interfaces:**
- Consumes: the `fmt.Fprintf(&sb, "\n_%d further rows omitted (%d total)._\n", omitted, len(body))` line inside `tabularToMarkdown`.
- Produces: `func omittedRowsNote(omitted, total int) string` in `tabular.go`.

- [ ] **Step 1: Write the failing test**

Add to `internal/convert/tabular_test.go`:

```go
func TestOmittedRowsNote(t *testing.T) {
	got := omittedRowsNote(3, 5)
	want := "\n*3 further rows omitted (5 total).*\n"
	if got != want {
		t.Errorf("omittedRowsNote(3,5) = %q, want %q", got, want)
	}
	if strings.Contains(got, "_") {
		t.Errorf("omitted-rows note must not use underscore emphasis: %q", got)
	}
}
```

(Ensure `"strings"` is imported in the test file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/convert/ -run TestOmittedRowsNote -v`
Expected: FAIL (`omittedRowsNote` undefined).

- [ ] **Step 3: Implement — factor the note into a helper using asterisk emphasis**

In `internal/convert/tabular.go`, add the helper:

```go
// omittedRowsNote renders the truncation notice appended when a table exceeds
// maxTableRows. It uses ASTERISK emphasis (not underscore) so the note
// round-trips through the KB rich-text editor unchanged — underscore emphasis
// re-serializes as asterisk and would force the whole note into raw mode.
func omittedRowsNote(omitted, total int) string {
	return fmt.Sprintf("\n*%d further rows omitted (%d total).*\n", omitted, total)
}
```

and replace the inline `fmt.Fprintf(&sb, "\n_%d further rows omitted (%d total)._\n", omitted, len(body))` call with:

```go
			sb.WriteString(omittedRowsNote(omitted, len(body)))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/convert/ -count=1 -v -run TestOmittedRowsNote`
Expected: PASS. Then `go test ./internal/convert/ -count=1` — whole package PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/convert/tabular.go internal/convert/tabular_test.go
git commit -m "fix(convert): emit asterisk emphasis in omitted-rows note for editor fidelity"
```

---

### Task 3: Golden round-trip tests for real generator outputs

**Files:**
- Create: `web/ui/src/pages/kb/generatorFidelity.test.ts` (new)

**Purpose:** Pin that representative bodies from each platform generator pass `checkFidelity` after Tasks 1–2, so a future generator change that reintroduces a raw-mode fallback is caught. Mirrors the intent of the existing `frontmatter.test.ts`.

**Interfaces:**
- Consumes: `checkFidelity`, `splitFrontmatter` (to strip frontmatter exactly as `NoteEditor` does before the fidelity check).

- [ ] **Step 1: Write the golden test**

Create `web/ui/src/pages/kb/generatorFidelity.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { checkFidelity } from "./editor";
import { splitFrontmatter } from "./frontmatter";

// Representative bodies from the platform's own note generators. The editor
// checks fidelity on the body AFTER frontmatter is split off (see NoteEditor),
// so these mirror that: split, then assert the body is WYSIWYG-safe.
const notes: Record<string, string> = {
  csvWithPipeAndOmitted:
    "---\ntype: upload\n---\n\n" +
    "| item | note |\n| --- | --- |\n| A | x \\| y |\n| B | ok |\n\n" +
    "*3 further rows omitted (7 total).*\n",
  reflectedChat:
    "---\ntype: chat\n---\n\n# Chat 2026-07-23 15:04\n\n**You:** hello\n\n**Assistant:** hi there\n",
  inboxNote:
    "---\ntype: inbox\n---\n\n# Agent notification\n\nYour agent finished a run and found 2 new items.\n",
  agentRunLog:
    "---\ntype: run\n---\n\n# Run 2026-07-23\n\n- started\n- fetched data\n- done\n",
};

describe("generator output opens in rich text", () => {
  for (const [name, md] of Object.entries(notes)) {
    it(name, () => {
      const { body } = splitFrontmatter(md);
      expect(checkFidelity(body)).toBe(true);
    });
  }
});
```

- [ ] **Step 2: Run test**

Run: `cd web/ui && npx vitest run src/pages/kb/generatorFidelity.test.ts`
Expected: PASS for all cases with Tasks 1–2 in place.

- [ ] **Step 3: If any case fails, identify and canonicalize the source generator**

If a case fails, print the round-trip to see the exact drift (a throwaway `console.log(fidelityRoundTrip(body))`), locate the emitting generator (`internal/vault/reflect.go` for chat/run notes, the inbox-note writer for inbox), and apply the same **render-equivalent** canonicalization discipline as Task 2 (change only emphasis/spacing form, never content). Add the fix as its own commit, then re-run. Do **not** loosen `checkFidelity` to make a case pass.

> Expectation from the design's audit: chat transcripts, inbox notes, and agent run logs already pass once frontmatter is stripped (per the existing note in `frontmatter.ts`), so the CSV case is the one that depends on Tasks 1–2. The other three are regression guards.

- [ ] **Step 4: Commit**

```bash
git add web/ui/src/pages/kb/generatorFidelity.test.ts
git commit -m "test(web/kb): golden fidelity tests for platform-generated notes"
```

---

### Task 4: Soften the raw-fallback warning copy

**Files:**
- Modify: `web/ui/src/pages/kb/NoteEditor.tsx:27` (the fallback message constant)
- Modify: `web/ui/src/pages/kb/NoteHeader.tsx:180-181` (the banner copy)
- Test: `web/ui/src/pages/kb/NoteEditor.test.tsx:136` (update the matched string)

**Purpose:** For notes that still genuinely can't round-trip, keep raw mode but replace the alarming "would rewrite those parts, including ones you didn't touch" copy with an accurate, calm message.

**Interfaces:** Copy-only change; no behavioural change.

- [ ] **Step 1: Update the failing test's expected copy**

In `web/ui/src/pages/kb/NoteEditor.test.tsx` (~line 136), the assertion currently matches `/can.t reproduce exactly/`. Change it to match the new phrasing, e.g. `/preserve its exact formatting/`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/NoteEditor.test.tsx`
Expected: FAIL (old copy still in the component).

- [ ] **Step 3: Reword the message in `NoteEditor.tsx`**

Replace the constant at line ~27:

```ts
  "This note uses formatting the rich text editor can't reproduce exactly, so it opened as raw " +
```

(and its continuation) with:

```ts
  "Opened as raw markdown to preserve its exact formatting. Switch to rich text to edit it visually — " +
  "a few uncommon formatting details would be reformatted if you do.";
```

(Keep it a single exported constant; adjust the concatenation so the full sentence reads naturally. Match the exact string your test asserts.)

- [ ] **Step 4: Reword the banner in `NoteHeader.tsx`**

Replace lines ~180–181:

```tsx
              This note uses formatting the rich editor can&rsquo;t reproduce exactly. Saving from
              rich text will rewrite those parts. Switch to Raw to edit it as-is.
```

with:

```tsx
              Opened as raw markdown to preserve its exact formatting. Switch to rich text to edit
              visually — a few uncommon details would be reformatted if you do.
```

- [ ] **Step 5: Run tests**

Run: `cd web/ui && npx vitest run src/pages/kb/NoteEditor.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/pages/kb/NoteEditor.tsx web/ui/src/pages/kb/NoteHeader.tsx web/ui/src/pages/kb/NoteEditor.test.tsx
git commit -m "fix(web/kb): calmer raw-fallback copy for unrepresentable notes"
```

---

## Final verification

- [ ] `cd web/ui && npx vitest run src/pages/kb/` — all KB editor tests PASS (pipe-cell fidelity, wikilink contract, generator goldens, note-editor copy).
- [ ] `go test ./internal/convert/ -count=1` — PASS.
- [ ] `cd web/ui && npm run build` (or `make ui`) — succeeds (TypeScript compiles; the loose serializer-state shapes in `pipeSafeTable.ts` type-check).
- [ ] Manual smoke (optional, throwaway data dir per live-instance-safety): upload a CSV containing a cell with a `|`, open the resulting note, confirm it opens in **rich text** (no raw-mode banner) and the pipe cell is intact; edit and save, confirm the table is unchanged.
