# KB Editor Formatting, Image Resize, and AI Selection Actions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add underline, a fixed colour palette, callouts, toggle lists and image resize to the knowledge-base editor, plus four AI actions on a text selection backed by a new endpoint.

**Architecture:** New TipTap marks and nodes live in `web/ui/src/pages/kb/marks/` and `web/ui/src/pages/kb/nodes/`, one file per construct, appended to `buildExtensions()`. Each is defined by its markdown representation first, because `checkFidelity()` decides whether a note opens editable or read-only. The AI actions are a single blocking `POST /api/v1/kb/assist` driven by a panel that replaces the bubble toolbar's contents.

**Tech Stack:** React 19 + TypeScript, Tailwind v4, TipTap 3, tiptap-markdown 0.9, vitest + @testing-library/react, Go 1.x + Echo v4.

This is plan **B of two** derived from `docs/superpowers/specs/2026-08-06-kb-editor-and-connections-design.md`. It covers spec sections 2, 3 and 4 (build-order steps 4–6). Plan A (`2026-08-06-kb-shell-fixes-and-connections.md`) covers sections 1, 5 and 6. **Plan A must land first** — Task 1 here edits `editor.css`, which Plan A Task 1 also edits.

## Global Constraints

- **Branch, never commit to `main`.** Work happens on `worktree-kb-editor-brainstorm` in `/home/rookie/rookery/.claude/worktrees/kb-editor-brainstorm`. Run every command from there.
- **Conventional Commits.** `type(scope): summary`.
- **No new npm or Go dependencies.** Everything needed is installed: `@tiptap/extension-underline` ships inside StarterKit, and colour/callout/toggle are hand-written marks and nodes.
- **Every new construct ships with a fidelity round-trip test in `editor.test.ts`, and does not ship without one.** `checkFidelity()` compares `markdown → doc → markdown`; a construct that fails it forces every note containing it into a read-only rich view. This is the single most important constraint in this plan.
- **No prompt text outside `internal/prompts`.** Standing repo rule; the assist endpoint gets no exception.
- **Frontend commands run from `web/ui/`:** `npm run test`, `npx tsc -b`, `npx oxlint`. **Go commands from the repo root.**
- **`node_modules` may not be installed in this worktree.** Run `npm ci` in `web/ui/` first if `npm run test` fails to start.

### Verified library facts (do not re-derive)

These were checked against the installed `tiptap-markdown@0.9.0` source and are what several tasks depend on:

- **Serialization falls back to HTML automatically.** `serialize/MarkdownSerializer.js` maps *every* schema mark to `extensions/marks/html.js` and *every* schema node to `extensions/nodes/html.js`, then overrides only those extensions that declare a `markdown` storage spec. So a mark whose `renderHTML` emits `<span style="color:#ef4444">` serializes to exactly that in markdown with **no custom serializer**. Same for `<u>` and `<details>`.
- **Two parse hooks exist**, both called from `parse/MarkdownParser.js`: `markdown.parse.setup(md)` receives the markdown-it instance before rendering, and `markdown.parse.updateDOM(element)` receives the rendered DOM afterwards. Callouts use `updateDOM` — rewriting a `<blockquote>` whose first paragraph starts `[!kind]` is far simpler and more robust than a markdown-it block rule.
- **`html: true` is already on** (`editor.ts:55`), so inline and block HTML in a note round-trips through markdown-it untouched.

---

### Task 1: Underline

StarterKit v3 already bundles `@tiptap/extension-underline`, so the mark and `toggleUnderline()` exist today with no import and no dependency — there is simply no way to reach them. Serialization needs no code: the HTML fallback emits `<u>text</u>`.

**Files:**
- Modify: `web/ui/src/pages/kb/BubbleToolbar.tsx`
- Test: `web/ui/src/pages/kb/editor.test.ts`

**Interfaces:**
- Consumes: `buildExtensions`, `checkFidelity`, `toMarkdown` from `./editor`
- Produces: the `ToolbarButton` pattern later tasks reuse for the colour trigger

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/kb/editor.test.ts`:

```ts
test("underline survives a markdown round trip", () => {
  // No custom serializer: tiptap-markdown maps every mark with no markdown
  // spec to its HTML representation, and html:true parses it back.
  expect(checkFidelity("Some <u>underlined</u> text.\n")).toBe(true);
});

test("toggleUnderline writes a <u> tag", () => {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: "<p>alpha</p>",
  });
  editor.commands.selectAll();
  editor.commands.toggleUnderline();
  expect(toMarkdown(editor)).toContain("<u>alpha</u>");
  editor.destroy();
});
```

If `Editor` / `toMarkdown` / `buildExtensions` are not already imported at the top of
that file, add them: `import { Editor } from "@tiptap/core";` and extend the existing
`./editor` import.

- [ ] **Step 2: Run the tests**

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts`
Expected: both PASS immediately. That is the point — this task adds no editor
capability, only a way to reach one that already exists. If either FAILS, stop: the
HTML-fallback assumption above is wrong and every later task in this plan depends on it.

- [ ] **Step 3: Add the toolbar button**

In `web/ui/src/pages/kb/BubbleToolbar.tsx`, add `Underline as UnderlineIcon` to the
`lucide-react` import, then insert this button immediately after the Italic button:

```tsx
        <ToolbarButton
          label="Underline"
          active={editor.isActive("underline")}
          onClick={() => editor.chain().focus().toggleUnderline().run()}
        >
          <UnderlineIcon className="size-4" />
        </ToolbarButton>
```

- [ ] **Step 4: Verify the gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/pages/kb/BubbleToolbar.tsx web/ui/src/pages/kb/editor.test.ts
git commit -m "feat(kb): add underline to the editor toolbar

StarterKit already bundles the underline mark; it had no affordance.
Serialization needs no code — tiptap-markdown maps a mark with no
markdown spec to its HTML form, so it round-trips as <u>."
```

---

### Task 2: Colour marks and the palette

Two marks, kept separate so they compose (red text on a yellow highlight). Stored as inline `style` attributes, which is what makes a coloured note render correctly in Obsidian and in HTML export.

**The palette is fixed and measured.** No single hex reaches WCAG AA body text (4.5:1) against *both* `--background: #ffffff` and `#191919` — the best any colour in the Tailwind mid-range achieves is 4.15 (violet-500). The eight text colours below floor at **3.53:1**, which is AA for large text and for non-text contrast. Yellow and amber are excluded from the text set (2.9–3.2 on white) and appear only as highlight backgrounds.

A highlight span always carries an explicit `#18181b` foreground alongside its background. Without it a pale tint inherits the near-white `--foreground` on the dark theme and becomes white-on-yellow. With it every tint measures ≥12.2:1 in both themes, because the pair is self-contained.

**Files:**
- Create: `web/ui/src/pages/kb/marks/colors.ts`
- Create: `web/ui/src/pages/kb/marks/kbPalette.test.ts`
- Modify: `web/ui/src/pages/kb/editor.ts`
- Modify: `web/ui/src/pages/kb/BubbleToolbar.tsx`
- Test: `web/ui/src/pages/kb/editor.test.ts`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `TEXT_COLORS: { name: string; hex: string }[]`
  - `HIGHLIGHT_COLORS: { name: string; hex: string }[]`
  - `HIGHLIGHT_FG: string` (`"#18181b"`)
  - `KBTextColor`, `KBBgColor` — TipTap `Mark`s
  - `toHex(css: string): string | null`
  - Commands: `setKBTextColor(hex)`, `unsetKBTextColor()`, `setKBBgColor(hex)`, `unsetKBBgColor()`

- [ ] **Step 1: Write the failing tests**

Create `web/ui/src/pages/kb/marks/kbPalette.test.ts`:

```ts
import { TEXT_COLORS, HIGHLIGHT_COLORS, HIGHLIGHT_FG, toHex } from "./colors";

// Computed from the values, never trusted — the same approach contrast.test.ts
// takes for the design tokens. A palette edit that drops below these floors
// fails the build instead of shipping unreadable text.
const LIGHT_BG = "#ffffff";
const DARK_BG = "#191919";

function lin(c: number) {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
}
function luminance(hex: string) {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}
function ratio(a: string, b: string) {
  const [la, lb] = [luminance(a), luminance(b)];
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

test("every text colour holds 3.5:1 in BOTH themes", () => {
  // 3.5 is the honest floor, not 4.5. A single fixed hex cannot reach AA body
  // text against both #ffffff and #191919 — the best any candidate manages is
  // 4.15 — and a fixed hex is what makes the note portable to Obsidian.
  for (const { name, hex } of TEXT_COLORS) {
    expect(`${name} light ${ratio(hex, LIGHT_BG).toFixed(2)}`).toBe(
      `${name} light ${Math.max(ratio(hex, LIGHT_BG), 3.5).toFixed(2)}`,
    );
    expect(`${name} dark ${ratio(hex, DARK_BG).toFixed(2)}`).toBe(
      `${name} dark ${Math.max(ratio(hex, DARK_BG), 3.5).toFixed(2)}`,
    );
  }
});

test("every highlight tint carries legible text in both themes", () => {
  // The pinned foreground is why this works at all: the pair is
  // self-contained and never inherits the page's --foreground.
  for (const { name, hex } of HIGHLIGHT_COLORS) {
    expect(`${name} ${ratio(hex, HIGHLIGHT_FG) >= 4.5}`).toBe(`${name} true`);
  }
});

test("no yellow or amber in the text set", () => {
  // Both measure under 3.2 on white. They belong as backgrounds only.
  expect(TEXT_COLORS.map((c) => c.name)).not.toContain("yellow");
  expect(TEXT_COLORS.map((c) => c.name)).not.toContain("amber");
});

test("toHex normalizes what the DOM gives back", () => {
  // Reading el.style.color returns a normalized rgb() string, NOT the hex the
  // markdown carried. Without this the round trip would rewrite every
  // "#ef4444" as "rgb(239, 68, 68)" and fail checkFidelity, dropping the note
  // into a read-only view.
  expect(toHex("rgb(239, 68, 68)")).toBe("#ef4444");
  expect(toHex("#EF4444")).toBe("#ef4444");
  expect(toHex("#ef4444")).toBe("#ef4444");
  expect(toHex("")).toBe(null);
  expect(toHex("not-a-colour")).toBe(null);
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/marks/kbPalette.test.ts`
Expected: FAIL — `./colors` does not exist.

- [ ] **Step 3: Write the marks and the palette**

Create `web/ui/src/pages/kb/marks/colors.ts`:

```ts
import { Mark, mergeAttributes } from "@tiptap/core";

// Fixed palette, no picker. Stored as an inline `style` attribute so a
// coloured note renders correctly in Obsidian and in HTML export — the vault
// stays portable.
//
// Contrast is the cost of that choice and is stated honestly: no single hex
// reaches WCAG AA body text (4.5:1) against BOTH #ffffff and #191919. The best
// any candidate in the Tailwind mid-range manages is 4.15 (violet-500). These
// eight floor at 3.53:1 — AA for large text and for non-text contrast.
// kbPalette.test.ts computes and pins that floor.
export const TEXT_COLORS = [
  { name: "red", hex: "#ef4444" },      // 3.76 light / 4.67 dark
  { name: "orange", hex: "#ea580c" },   // 3.56 / 4.94
  { name: "green", hex: "#059669" },    // 3.77 / 4.67
  { name: "teal", hex: "#0d9488" },     // 3.74 / 4.70
  { name: "blue", hex: "#3b82f6" },     // 3.68 / 4.78
  { name: "violet", hex: "#8b5cf6" },   // 4.23 / 4.15
  { name: "pink", hex: "#ec4899" },     // 3.53 / 4.98  ← the floor
  { name: "grey", hex: "#71717a" },     // 4.83 / 3.64
];

// Pale tints. Each is paired with HIGHLIGHT_FG in the SAME span, which is what
// keeps it legible on the dark theme — an inherited near-white --foreground on
// a pale yellow is unreadable. Every pair measures >= 12.2:1.
export const HIGHLIGHT_COLORS = [
  { name: "yellow", hex: "#fef08a" },
  { name: "orange", hex: "#fed7aa" },
  { name: "red", hex: "#fecaca" },
  { name: "green", hex: "#bbf7d0" },
  { name: "teal", hex: "#99f6e4" },
  { name: "blue", hex: "#bfdbfe" },
  { name: "purple", hex: "#e9d5ff" },
  { name: "pink", hex: "#fbcfe8" },
];

export const HIGHLIGHT_FG = "#18181b";

// The DOM normalizes a style colour to rgb() on read, so a mark parsed straight
// out of el.style would serialize back as "rgb(239, 68, 68)" and no longer
// match the "#ef4444" the file carried — checkFidelity would fail and the note
// would open read-only. Normalizing both directions to lowercase hex is what
// makes the round trip exact.
export function toHex(css: string): string | null {
  if (!css) return null;
  const rgb = css.match(/^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i);
  if (rgb) {
    return (
      "#" +
      [rgb[1], rgb[2], rgb[3]]
        .map((n) => Number(n).toString(16).padStart(2, "0"))
        .join("")
    );
  }
  const hex = css.trim().match(/^#([0-9a-f]{6})$/i);
  if (hex) return "#" + hex[1].toLowerCase();
  const short = css.trim().match(/^#([0-9a-f]{3})$/i);
  if (short) {
    return "#" + short[1].toLowerCase().split("").map((c) => c + c).join("");
  }
  return null;
}

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbColors: {
      setKBTextColor: (hex: string) => ReturnType;
      unsetKBTextColor: () => ReturnType;
      setKBBgColor: (hex: string) => ReturnType;
      unsetKBBgColor: () => ReturnType;
    };
  }
}

export const KBTextColor = Mark.create({
  name: "kbTextColor",

  addAttributes() {
    return {
      color: {
        default: null,
        parseHTML: (el) => toHex((el as HTMLElement).style.color),
        renderHTML: (attrs) =>
          attrs.color ? { style: `color:${attrs.color}` } : {},
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: "span[style]",
        getAttrs: (el) => {
          const style = (el as HTMLElement).style;
          // A highlight span also carries a `color` (the pinned foreground).
          // Claiming it here would parse one span as both marks and duplicate
          // it on the way back out.
          if (style.backgroundColor) return false;
          const color = toHex(style.color);
          return color ? { color } : false;
        },
      },
    ];
  },

  renderHTML({ HTMLAttributes }) {
    return ["span", mergeAttributes(HTMLAttributes), 0];
  },

  addCommands() {
    return {
      setKBTextColor:
        (hex: string) =>
        ({ commands }) =>
          commands.setMark(this.name, { color: hex }),
      unsetKBTextColor:
        () =>
        ({ commands }) =>
          commands.unsetMark(this.name),
    };
  },
});

export const KBBgColor = Mark.create({
  name: "kbBgColor",

  addAttributes() {
    return {
      bg: {
        default: null,
        parseHTML: (el) => toHex((el as HTMLElement).style.backgroundColor),
        // The foreground rides along in the SAME declaration on purpose — see
        // HIGHLIGHT_FG above.
        renderHTML: (attrs) =>
          attrs.bg
            ? { style: `background-color:${attrs.bg};color:${HIGHLIGHT_FG}` }
            : {},
      },
    };
  },

  parseHTML() {
    return [
      {
        tag: "span[style]",
        getAttrs: (el) => {
          const bg = toHex((el as HTMLElement).style.backgroundColor);
          return bg ? { bg } : false;
        },
      },
    ];
  },

  renderHTML({ HTMLAttributes }) {
    return ["span", mergeAttributes(HTMLAttributes), 0];
  },

  addCommands() {
    return {
      setKBBgColor:
        (hex: string) =>
        ({ commands }) =>
          commands.setMark(this.name, { bg: hex }),
      unsetKBBgColor:
        () =>
        ({ commands }) =>
          commands.unsetMark(this.name),
    };
  },
});
```

- [ ] **Step 4: Run the palette tests**

Run: `cd web/ui && npx vitest run src/pages/kb/marks/kbPalette.test.ts`
Expected: PASS (4 tests).

- [ ] **Step 5: Register the marks and write the fidelity test**

In `web/ui/src/pages/kb/editor.ts`, add `import { KBTextColor, KBBgColor } from "./marks/colors";`
and add both to the array returned by `buildExtensions()`, immediately after `Wikilink`:

```ts
    Wikilink,
    KBTextColor,
    KBBgColor,
    ...extra,
```

Append to `web/ui/src/pages/kb/editor.test.ts`:

```ts
test("a text colour survives a markdown round trip", () => {
  expect(
    checkFidelity('The <span style="color:#ef4444">deadline</span> is Friday.\n'),
  ).toBe(true);
});

test("a highlight survives a markdown round trip", () => {
  expect(
    checkFidelity(
      'Due <span style="background-color:#fef08a;color:#18181b">Friday</span>.\n',
    ),
  ).toBe(true);
});

test("a note with no colours is byte-for-byte unchanged", () => {
  // The whole fidelity contract: adding these marks must not alter any note
  // that does not use them.
  const md = "# Title\n\nPlain prose with a [[wikilink]] and **bold**.\n";
  expect(fidelityRoundTrip(md).trim()).toBe(md.trim());
});
```

Ensure `fidelityRoundTrip` is imported from `./editor` in that file.

- [ ] **Step 6: Run the fidelity tests**

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts`
Expected: PASS.

If the colour round trips FAIL, inspect the actual output with
`console.log(fidelityRoundTrip('<span style="color:#ef4444">x</span>'))`. The two
likely causes, in order: `toHex` not being applied on parse (output shows `rgb(...)`),
or the two marks both claiming the same span (output shows a doubled span). Both are
covered by the guards in step 3 — verify they were copied exactly.

- [ ] **Step 7: Add the swatch UI**

In `web/ui/src/pages/kb/BubbleToolbar.tsx`, add to the imports:

```tsx
import { useState } from "react";
import { Baseline, Ban } from "lucide-react";
import {
  TEXT_COLORS,
  HIGHLIGHT_COLORS,
  HIGHLIGHT_FG,
} from "./marks/colors";
```

Add this component above `BubbleToolbar`:

```tsx
// Fixed swatch grid — deliberately not a colour picker. Two rows of eight plus
// a "none" control per row.
function ColorSwatches({ editor, onDone }: { editor: Editor; onDone: () => void }) {
  return (
    <div className="w-56 space-y-2 p-2">
      <div>
        <div className="mb-1 text-xs text-muted-2">Text</div>
        <div className="flex flex-wrap gap-1">
          {TEXT_COLORS.map((c) => (
            <button
              key={c.name}
              type="button"
              title={`Text ${c.name}`}
              aria-label={`Text ${c.name}`}
              // Mousedown, not click: a click steals focus and collapses the
              // selection the toolbar is acting on.
              onMouseDown={(e) => {
                e.preventDefault();
                editor.chain().focus().setKBTextColor(c.hex).run();
                onDone();
              }}
              className="size-5 rounded-sm border border-border"
              style={{ backgroundColor: c.hex }}
            />
          ))}
          <button
            type="button"
            title="No text colour"
            aria-label="No text colour"
            onMouseDown={(e) => {
              e.preventDefault();
              editor.chain().focus().unsetKBTextColor().run();
              onDone();
            }}
            className="flex size-5 items-center justify-center rounded-sm border border-border"
          >
            <Ban className="size-3 text-muted-2" />
          </button>
        </div>
      </div>
      <div>
        <div className="mb-1 text-xs text-muted-2">Highlight</div>
        <div className="flex flex-wrap gap-1">
          {HIGHLIGHT_COLORS.map((c) => (
            <button
              key={c.name}
              type="button"
              title={`Highlight ${c.name}`}
              aria-label={`Highlight ${c.name}`}
              onMouseDown={(e) => {
                e.preventDefault();
                editor.chain().focus().setKBBgColor(c.hex).run();
                onDone();
              }}
              className="size-5 rounded-sm border border-border"
              style={{ backgroundColor: c.hex, color: HIGHLIGHT_FG }}
            />
          ))}
          <button
            type="button"
            title="No highlight"
            aria-label="No highlight"
            onMouseDown={(e) => {
              e.preventDefault();
              editor.chain().focus().unsetKBBgColor().run();
              onDone();
            }}
            className="flex size-5 items-center justify-center rounded-sm border border-border"
          >
            <Ban className="size-3 text-muted-2" />
          </button>
        </div>
      </div>
    </div>
  );
}
```

In `BubbleToolbar`, add `const [colorsOpen, setColorsOpen] = useState(false);` at the
top of the component, add this button after the Underline button:

```tsx
        <ToolbarButton
          label="Colour"
          active={colorsOpen}
          onClick={() => setColorsOpen((v) => !v)}
        >
          <Baseline className="size-4" />
        </ToolbarButton>
```

and render the panel by replacing the toolbar's outer `<div>` contents conditionally —
wrap the existing row so the component returns:

```tsx
    <BubbleMenu editor={editor} shouldShow={({ state }) => !state.selection.empty}>
      <div className="rounded-md border border-border bg-popover shadow-md">
        {colorsOpen ? (
          <ColorSwatches editor={editor} onDone={() => setColorsOpen(false)} />
        ) : (
          <div className="flex items-center gap-0.5 p-1">
            {/* … the existing buttons, unchanged … */}
          </div>
        )}
      </div>
    </BubbleMenu>
```

- [ ] **Step 8: Run the gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add web/ui/src/pages/kb/marks/ web/ui/src/pages/kb/editor.ts \
        web/ui/src/pages/kb/editor.test.ts web/ui/src/pages/kb/BubbleToolbar.tsx
git commit -m "feat(kb): text and highlight colours from a fixed palette

Stored as inline style attributes so a coloured note stays portable to
Obsidian and HTML export. No single hex reaches AA body text against both
themes (best is 4.15), so the eight text colours floor at 3.53:1 and a
test computes and pins that. Highlights carry a pinned #18181b foreground
so a pale tint stays legible on the dark theme."
```

---

### Task 3: Callouts

Five kinds using Obsidian's `> [!note]` syntax, which the vault's Obsidian-style model makes the right target. Parsing uses `tiptap-markdown`'s verified `parse.updateDOM` hook: markdown-it renders `> [!note]\n> body` as `<blockquote><p>[!note]\nbody</p></blockquote>`, and the hook rewrites any blockquote whose first paragraph opens with `[!kind]` into the callout node's DOM.

**Files:**
- Create: `web/ui/src/pages/kb/nodes/callout.ts`
- Modify: `web/ui/src/pages/kb/editor.ts`
- Modify: `web/ui/src/pages/kb/editor.css`
- Modify: `web/ui/src/pages/kb/slashItems.ts`
- Modify: `web/ui/src/pages/kb/SlashMenu.tsx`
- Test: `web/ui/src/pages/kb/editor.test.ts`, `web/ui/src/pages/kb/slash.test.ts`

**Interfaces:**
- Consumes: nothing
- Produces: `Callout` (TipTap `Node`), `CALLOUT_KINDS: readonly string[]`, command `setCallout(kind: string)`

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/kb/editor.test.ts`:

```ts
test.each(["note", "tip", "info", "warning", "danger"])(
  "a %s callout survives a markdown round trip",
  (kind) => {
    expect(checkFidelity(`> [!${kind}]\n> Body text here.\n`)).toBe(true);
  },
);

test("a plain blockquote is still a plain blockquote", () => {
  // The updateDOM hook must claim ONLY blockquotes opening with [!kind].
  const md = "> Just a quotation.\n";
  expect(fidelityRoundTrip(md).trim()).toBe(md.trim());
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts`
Expected: the five callout cases FAIL (they currently parse as plain blockquotes, so
the `[!note]` marker survives as literal text and the round trip differs). The plain
blockquote case PASSES.

- [ ] **Step 3: Write the node**

Create `web/ui/src/pages/kb/nodes/callout.ts`:

```ts
import { Node, mergeAttributes } from "@tiptap/core";

export const CALLOUT_KINDS = ["note", "tip", "info", "warning", "danger"] as const;
export type CalloutKind = (typeof CALLOUT_KINDS)[number];

function isKind(v: string): v is CalloutKind {
  return (CALLOUT_KINDS as readonly string[]).includes(v);
}

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbCallout: {
      setCallout: (kind: CalloutKind) => ReturnType;
    };
  }
}

export const Callout = Node.create({
  name: "callout",
  group: "block",
  content: "block+",
  defining: true,

  addAttributes() {
    return {
      kind: {
        default: "note",
        parseHTML: (el) => {
          const k = el.getAttribute("data-callout") || "note";
          return isKind(k) ? k : "note";
        },
        renderHTML: (attrs) => ({ "data-callout": attrs.kind }),
      },
    };
  },

  parseHTML() {
    return [{ tag: "div[data-callout]" }];
  },

  renderHTML({ HTMLAttributes }) {
    return ["div", mergeAttributes(HTMLAttributes, { class: "kb-callout" }), 0];
  },

  addCommands() {
    return {
      setCallout:
        (kind: CalloutKind) =>
        ({ commands }) =>
          commands.wrapIn(this.name, { kind }),
    };
  },

  addStorage() {
    return {
      markdown: {
        // Obsidian callout syntax: a blockquote whose first line is the kind
        // marker. wrapBlock applies the "> " prefix to everything written
        // inside it, so the marker and the body both get quoted.
        serialize(state: any, node: any) {
          state.wrapBlock("> ", null, node, () => {
            state.write(`[!${node.attrs.kind}]`);
            state.ensureNewLine();
            state.renderContent(node);
          });
        },
        parse: {
          // markdown-it renders "> [!note]\n> body" as a plain blockquote
          // whose first paragraph opens with the literal marker. Rewriting the
          // DOM afterwards is far simpler and more robust than a markdown-it
          // block rule, and updateDOM is a first-class hook (see
          // tiptap-markdown parse/MarkdownParser.js).
          updateDOM(element: HTMLElement) {
            element.querySelectorAll("blockquote").forEach((bq) => {
              const first = bq.firstElementChild;
              if (!first || first.tagName !== "P") return;
              const m = first.innerHTML.match(/^\[!([a-z]+)\]\s*(?:<br\s*\/?>|\n)?/i);
              if (!m) return;
              const kind = m[1].toLowerCase();
              if (!isKind(kind)) return;
              first.innerHTML = first.innerHTML.slice(m[0].length);
              // An empty first paragraph would round-trip as a stray blank
              // line inside the callout.
              if (!first.innerHTML.trim()) first.remove();
              const div = element.ownerDocument.createElement("div");
              div.setAttribute("data-callout", kind);
              while (bq.firstChild) div.appendChild(bq.firstChild);
              bq.replaceWith(div);
            });
          },
        },
      },
    };
  },
});
```

- [ ] **Step 4: Register it and run the tests**

In `web/ui/src/pages/kb/editor.ts`, add `import { Callout } from "./nodes/callout";`
and add `Callout,` to the `buildExtensions()` array after `KBBgColor`.

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts`
Expected: PASS.

**If a callout round trip fails on spacing** (the output has a blank line between the
marker and the body), replace `state.ensureNewLine();` in the serializer with:

```ts
            state.write("\n");
```

Re-run. If it still differs, print the actual output with
`console.log(JSON.stringify(fidelityRoundTrip("> [!note]\n> Body text here.\n")))`
and match the serializer to the input exactly — the input form in the test is the
contract.

- [ ] **Step 5: Style the callouts**

Append to `web/ui/src/pages/kb/editor.css`:

```css
/* Callouts. Colours map to existing tokens rather than new ones, so
   contrast.test.ts's guarantees already cover them and a palette edit cannot
   silently break a callout. */
.note-editor-content .tiptap .kb-callout {
  border-left: 3px solid var(--color-accent);
  background: var(--color-accent-soft);
  border-radius: 0.375rem;
  padding: 0.6em 0.9em;
}
.note-editor-content .tiptap .kb-callout > * + * { margin-top: 0.5em; }
.note-editor-content .tiptap .kb-callout[data-callout="tip"] {
  border-left-color: var(--color-ok);
  background: var(--color-ok-soft);
}
.note-editor-content .tiptap .kb-callout[data-callout="warning"] {
  border-left-color: var(--color-warn);
  background: var(--color-warn-soft);
}
.note-editor-content .tiptap .kb-callout[data-callout="danger"] {
  border-left-color: var(--color-danger);
  background: var(--color-danger-soft);
}
```

- [ ] **Step 6: Add the slash items**

In `web/ui/src/pages/kb/slashItems.ts`, add `import { CALLOUT_KINDS } from "./nodes/callout";`
and insert these entries immediately after the `Quote` entry (the array's order IS the
display contract — `filterSlashItems` never reorders):

```ts
  ...CALLOUT_KINDS.map((kind) => ({
    title: `Callout: ${kind}`,
    keywords: `callout admonition ${kind} aside box`,
    run: (editor: Editor) => editor.chain().focus().setCallout(kind).run(),
  })),
```

In `web/ui/src/pages/kb/SlashMenu.tsx`, add to the `lucide-react` import
`Info, Lightbulb, AlertTriangle, OctagonAlert, StickyNote,` and add to `ICONS`:

```tsx
  "Callout: note": StickyNote,
  "Callout: tip": Lightbulb,
  "Callout: info": Info,
  "Callout: warning": AlertTriangle,
  "Callout: danger": OctagonAlert,
```

- [ ] **Step 7: Pin the icon-map contract**

Append to `web/ui/src/pages/kb/slash.test.ts`:

```ts
import { slashItems } from "./slashItems";
import { ICONS } from "./SlashMenu";

test("every slash item has an icon", () => {
  // A missing entry renders the row with no icon rather than failing, so
  // nothing else would catch it.
  const missing = slashItems.filter((i) => !ICONS[i.title]).map((i) => i.title);
  expect(missing).toEqual([]);
});
```

Export `ICONS` from `SlashMenu.tsx` by changing `const ICONS` to `export const ICONS`.

- [ ] **Step 8: Run the gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add web/ui/src/pages/kb/nodes/ web/ui/src/pages/kb/editor.ts \
        web/ui/src/pages/kb/editor.css web/ui/src/pages/kb/slashItems.ts \
        web/ui/src/pages/kb/SlashMenu.tsx web/ui/src/pages/kb/editor.test.ts \
        web/ui/src/pages/kb/slash.test.ts
git commit -m "feat(kb): callouts in Obsidian syntax

Five kinds written as '> [!note]', parsed back via tiptap-markdown's
updateDOM hook — rewriting a blockquote whose first paragraph opens with
the marker is simpler and more robust than a markdown-it block rule.
Colours map to existing tokens so contrast.test.ts already covers them."
```

---

### Task 4: Toggle lists

A `<details>`/`<summary>` pair. Needs **no markdown code at all**: block HTML passes through markdown-it untouched under `html: true`, and the HTML serialization fallback writes it back.

**Files:**
- Create: `web/ui/src/pages/kb/nodes/toggle.ts`
- Modify: `web/ui/src/pages/kb/editor.ts`, `editor.css`, `slashItems.ts`, `SlashMenu.tsx`
- Test: `web/ui/src/pages/kb/editor.test.ts`

**Interfaces:**
- Consumes: nothing
- Produces: `Toggle`, `ToggleSummary` (TipTap `Node`s), command `setToggle()`

- [ ] **Step 1: Write the failing test**

```ts
test("a toggle list survives a markdown round trip", () => {
  expect(
    checkFidelity(
      "<details><summary>Show details</summary>\n\nHidden body.\n\n</details>\n",
    ),
  ).toBe(true);
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts`
Expected: FAIL — with no node claiming `<details>`, the generic HTML node handling
does not preserve the structure.

- [ ] **Step 3: Write the nodes**

Create `web/ui/src/pages/kb/nodes/toggle.ts`:

```ts
import { Node, mergeAttributes } from "@tiptap/core";

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbToggle: {
      setToggle: () => ReturnType;
    };
  }
}

// <details>/<summary> is the standard markdown-compatible collapsible. No
// markdown spec is declared on purpose: html:true passes block HTML through
// markdown-it untouched, and tiptap-markdown's HTML node fallback serializes
// any node without a spec back to its HTML form. So this round-trips for free.
export const ToggleSummary = Node.create({
  name: "toggleSummary",
  content: "inline*",
  defining: true,
  parseHTML() {
    return [{ tag: "summary" }];
  },
  renderHTML({ HTMLAttributes }) {
    return ["summary", mergeAttributes(HTMLAttributes), 0];
  },
});

export const Toggle = Node.create({
  name: "toggle",
  group: "block",
  // The summary is required and comes first; everything after it is the body.
  content: "toggleSummary block+",
  defining: true,
  parseHTML() {
    return [{ tag: "details" }];
  },
  renderHTML({ HTMLAttributes }) {
    // `open` so the body is visible while editing — a collapsed toggle in the
    // editor would hide content the user is trying to write.
    return ["details", mergeAttributes(HTMLAttributes, { open: "" }), 0];
  },
  addCommands() {
    return {
      setToggle:
        () =>
        ({ commands }) =>
          commands.insertContent({
            type: this.name,
            content: [
              { type: "toggleSummary", content: [{ type: "text", text: "Toggle" }] },
              { type: "paragraph" },
            ],
          }),
    };
  },
});
```

- [ ] **Step 4: Register and run**

In `editor.ts` add `import { Toggle, ToggleSummary } from "./nodes/toggle";` and add
`ToggleSummary,` then `Toggle,` to the array after `Callout`.

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts`
Expected: PASS.

If it fails on the `open` attribute appearing in the serialized markdown, drop it from
`renderHTML` (return `["details", mergeAttributes(HTMLAttributes), 0]`) and add
`.note-editor-content .tiptap details > *:not(summary) { display: block; }` to
`editor.css` in the next step so the body stays visible while editing.

- [ ] **Step 5: Style and add the slash item**

Append to `editor.css`:

```css
.note-editor-content .tiptap details {
  border: 1px solid var(--color-border);
  border-radius: 0.375rem;
  padding: 0.5em 0.75em;
}
.note-editor-content .tiptap details > summary {
  cursor: pointer;
  font-weight: 600;
}
```

In `slashItems.ts`, add after the callout entries:

```ts
  {
    title: "Toggle list",
    keywords: "toggle collapsible details accordion expand fold",
    run: (editor) => editor.chain().focus().setToggle().run(),
  },
```

In `SlashMenu.tsx`, add `ChevronRight` to the lucide import and `"Toggle list": ChevronRight,` to `ICONS`.

- [ ] **Step 6: Run the gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS, including `slash.test.ts`'s every-item-has-an-icon assertion.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src/pages/kb/nodes/toggle.ts web/ui/src/pages/kb/editor.ts \
        web/ui/src/pages/kb/editor.css web/ui/src/pages/kb/slashItems.ts \
        web/ui/src/pages/kb/SlashMenu.tsx web/ui/src/pages/kb/editor.test.ts
git commit -m "feat(kb): toggle lists via details/summary

No markdown spec needed: html:true passes block HTML through markdown-it
and tiptap-markdown's HTML node fallback writes it back, so it
round-trips without custom serialization."
```

---

### Task 5: Image resize

Width stored as Obsidian's pipe syntax, `![alt|420](assets/foo.png)`. The `src` attribute keeps its existing behaviour exactly — stored as the portable vault path, rendered through `/api/v1/kb/raw`.

**Files:**
- Modify: `web/ui/src/pages/kb/kbImage.ts`
- Create: `web/ui/src/pages/kb/imageResize.ts`
- Create: `web/ui/src/pages/kb/imageResize.test.ts`
- Modify: `web/ui/src/pages/kb/editor.css`
- Test: `web/ui/src/pages/kb/editor.test.ts`

**Interfaces:**
- Consumes: `assetDisplayURL`, `vaultPathFromSrc` from `./kbImage`
- Produces:
  - `clampImageWidth(desired: number, columnWidth: number): number`
  - `splitAltWidth(alt: string): { alt: string; width: number | null }`
  - `joinAltWidth(alt: string, width: number | null): string`

- [ ] **Step 1: Write the failing tests**

Create `web/ui/src/pages/kb/imageResize.test.ts`:

```ts
import { clampImageWidth, splitAltWidth, joinAltWidth } from "./imageResize";

// jsdom cannot drive a pointer drag, so the maths is extracted and tested
// directly — the same tactic placeMenu in SlashMenu.tsx uses.
test("width is clamped to the column and a sane minimum", () => {
  expect(clampImageWidth(400, 800)).toBe(400);
  expect(clampImageWidth(2000, 800)).toBe(800);
  expect(clampImageWidth(10, 800)).toBe(80);
  expect(clampImageWidth(400.6, 800)).toBe(401);
});

test("splitAltWidth reads a trailing pipe width", () => {
  expect(splitAltWidth("Architecture|420")).toEqual({ alt: "Architecture", width: 420 });
  expect(splitAltWidth("Architecture")).toEqual({ alt: "Architecture", width: null });
  expect(splitAltWidth("")).toEqual({ alt: "", width: null });
});

test("an alt that genuinely contains a pipe is not corrupted", () => {
  // Split on the LAST pipe, and only when what follows is a bare integer.
  expect(splitAltWidth("a|b")).toEqual({ alt: "a|b", width: null });
  expect(splitAltWidth("a|b|300")).toEqual({ alt: "a|b", width: 300 });
});

test("joinAltWidth is the inverse", () => {
  expect(joinAltWidth("Architecture", 420)).toBe("Architecture|420");
  expect(joinAltWidth("Architecture", null)).toBe("Architecture");
  expect(joinAltWidth("", 420)).toBe("|420");
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/imageResize.test.ts`
Expected: FAIL — `./imageResize` does not exist.

- [ ] **Step 3: Write the helpers**

Create `web/ui/src/pages/kb/imageResize.ts`:

```ts
// Pure geometry + alt-text parsing, extracted so it is actually testable:
// jsdom has no layout engine and cannot drive a pointer drag, so a test
// against the NodeView could prove an image renders but never that a resize
// lands on the right width. Same reasoning as placeMenu in SlashMenu.tsx.

export const MIN_IMAGE_WIDTH = 80;

export function clampImageWidth(desired: number, columnWidth: number): number {
  const max = Math.max(MIN_IMAGE_WIDTH, Math.round(columnWidth));
  return Math.min(Math.max(Math.round(desired), MIN_IMAGE_WIDTH), max);
}

// Obsidian's convention puts the width in the alt slot: ![alt|420](src).
// Split on the LAST pipe and only when the tail is a bare integer, so an alt
// that genuinely contains a pipe survives.
export function splitAltWidth(alt: string): { alt: string; width: number | null } {
  const i = alt.lastIndexOf("|");
  if (i === -1) return { alt, width: null };
  const tail = alt.slice(i + 1);
  if (!/^\d+$/.test(tail)) return { alt, width: null };
  return { alt: alt.slice(0, i), width: parseInt(tail, 10) };
}

export function joinAltWidth(alt: string, width: number | null): string {
  return width === null ? alt : `${alt}|${width}`;
}
```

- [ ] **Step 4: Run the helper tests**

Run: `cd web/ui && npx vitest run src/pages/kb/imageResize.test.ts`
Expected: PASS.

- [ ] **Step 5: Write the fidelity test**

Append to `web/ui/src/pages/kb/editor.test.ts`:

```ts
test("a sized image survives a markdown round trip", () => {
  expect(checkFidelity("![Architecture|420](assets/arch.png)\n")).toBe(true);
});

test("an unsized image is byte-for-byte unchanged", () => {
  // The whole point of putting the width in the alt slot: a note with no
  // resized image must serialize exactly as it does today.
  const md = "![Architecture](assets/arch.png)\n";
  expect(fidelityRoundTrip(md).trim()).toBe(md.trim());
});
```

- [ ] **Step 6: Add the width attribute and the markdown serializer**

Rewrite the `KBImage` export at the bottom of `web/ui/src/pages/kb/kbImage.ts`:

```ts
import { splitAltWidth, joinAltWidth } from "./imageResize";

export const KBImage = Image.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      src: {
        default: null,
        parseHTML: (el) => vaultPathFromSrc(el.getAttribute("src")),
        // The stored attr value stays the portable path; only the rendered DOM
        // attribute is rewritten to the served URL.
        renderHTML: (attrs) => (attrs.src ? { src: assetDisplayURL(attrs.src as string) } : {}),
      },
      // The alt slot carries the width in Obsidian's ![alt|420](src) form, so
      // the ATTRIBUTE holds only the real alt text and the width is its own
      // attribute. Splitting happens on parse, rejoining on serialize.
      alt: {
        default: null,
        parseHTML: (el) => splitAltWidth(el.getAttribute("alt") || "").alt || null,
        renderHTML: (attrs) => (attrs.alt ? { alt: attrs.alt as string } : {}),
      },
      width: {
        default: null,
        parseHTML: (el) => splitAltWidth(el.getAttribute("alt") || "").width,
        renderHTML: (attrs) =>
          attrs.width ? { width: String(attrs.width), style: `width:${attrs.width}px` } : {},
      },
    };
  },

  addStorage() {
    return {
      markdown: {
        // tiptap-markdown's own image serializer writes ![alt](src) and knows
        // nothing about the width, so it is overridden here rather than
        // letting the width silently drop on every save.
        serialize(state: any, node: any) {
          const label = joinAltWidth(node.attrs.alt || "", node.attrs.width ?? null);
          const title = node.attrs.title ? ` "${node.attrs.title}"` : "";
          state.write(`![${state.esc(label)}](${node.attrs.src}${title})`);
        },
        parse: {
          // handled by markdown-it + parseHTML above
        },
      },
    };
  },
});
```

- [ ] **Step 7: Run the fidelity tests**

Run: `cd web/ui && npx vitest run src/pages/kb/editor.test.ts src/pages/kb/generatorFidelity.test.ts`
Expected: PASS. `generatorFidelity.test.ts` and `corpus.test.ts` are the broad
regression nets for the serializer — if either fails, the image serializer above is
writing something the old one did not, and that is a real regression, not a flaky test.

- [ ] **Step 8: Add the drag handle**

Add a NodeView to `KBImage` by appending this method inside the `Image.extend({ … })`
object, after `addStorage()`:

```ts
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = document.createElement("span");
      dom.className = "kb-image";

      const img = document.createElement("img");
      img.src = assetDisplayURL(node.attrs.src as string);
      if (node.attrs.alt) img.alt = node.attrs.alt as string;
      if (node.attrs.width) img.style.width = `${node.attrs.width}px`;
      dom.appendChild(img);

      const handle = document.createElement("span");
      handle.className = "kb-image-handle";
      handle.setAttribute("aria-hidden", "true");
      dom.appendChild(handle);

      handle.addEventListener("pointerdown", (event) => {
        event.preventDefault();
        const startX = event.clientX;
        const startWidth = img.getBoundingClientRect().width;
        const column = (dom.parentElement?.getBoundingClientRect().width ?? startWidth);

        const onMove = (e: PointerEvent) => {
          const next = clampImageWidth(startWidth + (e.clientX - startX), column);
          img.style.width = `${next}px`;
        };
        const onUp = () => {
          window.removeEventListener("pointermove", onMove);
          window.removeEventListener("pointerup", onUp);
          const final = parseInt(img.style.width, 10);
          if (typeof getPos === "function" && !Number.isNaN(final)) {
            editor.view.dispatch(
              editor.view.state.tr.setNodeMarkup(getPos(), undefined, {
                ...node.attrs,
                width: final,
              }),
            );
          }
        };
        window.addEventListener("pointermove", onMove);
        window.addEventListener("pointerup", onUp);
      });

      return { dom };
    };
  },
```

Add `clampImageWidth` to the `./imageResize` import at the top of the file.

- [ ] **Step 9: Style the handle**

Append to `editor.css`:

```css
.note-editor-content .tiptap .kb-image {
  position: relative;
  display: inline-block;
  line-height: 0;
}
.note-editor-content .tiptap .kb-image img { max-width: 100%; height: auto; border-radius: 0.5rem; }
.note-editor-content .tiptap .kb-image-handle {
  position: absolute;
  right: -4px;
  bottom: -4px;
  width: 12px;
  height: 12px;
  border: 2px solid var(--color-background);
  border-radius: 9999px;
  background: var(--color-accent);
  cursor: nwse-resize;
  opacity: 0;
  transition: opacity 120ms;
}
.note-editor-content .tiptap .kb-image:hover .kb-image-handle { opacity: 1; }
```

- [ ] **Step 10: Run the gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add web/ui/src/pages/kb/kbImage.ts web/ui/src/pages/kb/imageResize.ts \
        web/ui/src/pages/kb/imageResize.test.ts web/ui/src/pages/kb/editor.css \
        web/ui/src/pages/kb/editor.test.ts
git commit -m "feat(kb): resize images with a drag handle

Width is stored in Obsidian's pipe syntax, ![alt|420](src), so a note
stays portable and an unsized image serializes byte-for-byte as before.
The clamp and alt-split maths are pure functions with direct tests —
jsdom cannot drive a pointer drag."
```

---

### Task 6: The assist endpoint

**Files:**
- Create: `internal/prompts/kbassist.go`
- Create: `internal/prompts/kbassist_test.go`
- Create: `web/api_kb_assist.go`
- Create: `web/api_kb_assist_test.go`
- Modify: `internal/agentrunner/runner.go:690` (export `friendlyRunError`)
- Modify: every call site of `friendlyRunError` inside `internal/agentrunner`
- Modify: `web/api_kb.go` (`registerKBAPI`)
- Modify: `web/api_parity_test.go` (the `want` table)

**Interfaces:**
- Consumes: `s.coderForWorkspace(workspaceID) *coder.Coder`, `coder.Generate(ctx, workspaceID, prompt) (*coder.Result, error)`, `jsonErr(c, status, code, msg)`
- Produces:
  - `prompts.BuildKBAssistPrompt(action, path, selection string) string`
  - `prompts.KBAssistActions() []string` → `["improve", "proofread", "explain", "reformat"]`
  - `agentrunner.FriendlyRunError(err error, coderName string) string`
  - `POST /api/v1/kb/assist` → `{"action": string, "result": string}`

- [ ] **Step 1: Write the failing prompt test**

Create `internal/prompts/kbassist_test.go`:

```go
package prompts

import "strings"
import "testing"

func TestKBAssistActionsIsClosed(t *testing.T) {
	got := KBAssistActions()
	want := []string{"improve", "proofread", "explain", "reformat"}
	if len(got) != len(want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("actions = %v, want %v", got, want)
		}
	}
}

func TestBuildKBAssistPromptCarriesSelectionAndPath(t *testing.T) {
	p := BuildKBAssistPrompt("improve", "notes/ci.md", "the pipeline runs on merge")
	if !strings.Contains(p, "the pipeline runs on merge") {
		t.Error("prompt does not carry the selection")
	}
	if !strings.Contains(p, "notes/ci.md") {
		t.Error("prompt does not carry the note path")
	}
}

func TestBuildKBAssistPromptExplainDoesNotRewrite(t *testing.T) {
	// Explain is the one action whose output is not a replacement. If its
	// prompt reads like the other three, the model returns a rewrite and the
	// panel presents an explanation that is actually edited prose.
	p := strings.ToLower(BuildKBAssistPrompt("explain", "notes/ci.md", "release-please"))
	if !strings.Contains(p, "explain") {
		t.Error("explain prompt does not ask for an explanation")
	}
	if strings.Contains(p, "return only the rewritten") {
		t.Error("explain prompt asks for a rewrite")
	}
}

func TestBuildKBAssistPromptRewritesReturnOnlyTheText(t *testing.T) {
	for _, a := range []string{"improve", "proofread", "reformat"} {
		p := strings.ToLower(BuildKBAssistPrompt(a, "notes/ci.md", "x"))
		if !strings.Contains(p, "return only the rewritten") {
			t.Errorf("%s prompt does not constrain the output to the text alone", a)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/prompts/ -run KBAssist -count=1 -v`
Expected: FAIL — undefined `KBAssistActions` / `BuildKBAssistPrompt`.

- [ ] **Step 3: Write the prompt builder**

Create `internal/prompts/kbassist.go`:

```go
package prompts

import "fmt"

// KBAssistActions is the closed set of actions the KB editor's selection panel
// offers. The handler validates against this exact slice, so the API and the
// prompt builder can never disagree about what is supported.
func KBAssistActions() []string {
	return []string{"improve", "proofread", "explain", "reformat"}
}

// kbAssistInstruction is the per-action body. Three of the four return
// replacement text; "explain" deliberately does not, because its result is
// shown read-only and must never be pasted over the user's prose.
func kbAssistInstruction(action string) string {
	switch action {
	case "proofread":
		return "Correct spelling, grammar and punctuation in the passage below. " +
			"Preserve the author's wording, voice and meaning — fix errors only, do not " +
			"rewrite for style. Return only the rewritten passage, with no preamble, " +
			"no explanation and no code fence."
	case "reformat":
		return "Reformat the passage below for readability using markdown — headings, " +
			"lists, emphasis or a table where the content genuinely calls for one. " +
			"Do not add, remove or reword any information. Return only the rewritten " +
			"passage, with no preamble, no explanation and no code fence."
	case "explain":
		return "Explain the passage below in plain language: what it means, and any " +
			"term, reference or assumption a reader might not know. Be concise — a " +
			"short paragraph, not an essay. Do NOT rewrite or correct the passage; " +
			"this explanation is shown alongside it and is never pasted into the note."
	default: // "improve"
		return "Improve the writing of the passage below: clearer, tighter and better " +
			"organised, in the author's own voice. Keep every fact and claim exactly as " +
			"stated — do not add information, and do not remove any. Return only the " +
			"rewritten passage, with no preamble, no explanation and no code fence."
	}
}

// BuildKBAssistPrompt builds the one-shot, text-only prompt behind
// POST /api/v1/kb/assist. The note path is context, not an instruction to open
// the file: this call runs with WithNoTools, so the model has no file access
// and the passage in the prompt is all it can see.
func BuildKBAssistPrompt(action, path, selection string) string {
	return fmt.Sprintf(`You are helping edit a note in a personal knowledge base.

The note is %q. You cannot open it — the passage below is the only content you have.

%s

Passage:
---
%s
---`, path, kbAssistInstruction(action), selection)
}
```

- [ ] **Step 4: Run the prompt tests**

Run: `go test ./internal/prompts/ -run KBAssist -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Export the friendly-error mapper**

In `internal/agentrunner/runner.go`, rename `friendlyRunError` to `FriendlyRunError`
and give it this doc comment:

```go
// FriendlyRunError converts a coder failure into one user-facing sentence.
//
// Exported because the KB assist endpoint (web/api_kb_assist.go) needs the same
// wording: a workspace out of quota must not get one sentence from a scheduled
// run and a different one from the note editor.
func FriendlyRunError(err error, coderName string) string {
```

Update every call site inside `internal/agentrunner` (find them with
`grep -rn "friendlyRunError" internal/`).

Run: `go build ./... && go test ./internal/agentrunner/ -count=1`
Expected: PASS.

- [ ] **Step 6: Write the failing handler test**

Create `web/api_kb_assist_test.go`:

```go
package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Reuse whatever helper the other KB API tests in this package use to build a
// server + cookies (see web/api_kb_export_inline_test.go). Do NOT add a second
// one.

func TestKBAssistRejectsUnknownAction(t *testing.T) {
	s, cookies := newKBTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "translate", "path": "notes/a.md", "selection": "x"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Errorf("body = %s, want invalid_request", rec.Body.String())
	}
}

func TestKBAssistRejectsEmptySelection(t *testing.T) {
	s, cookies := newKBTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "notes/a.md", "selection": "   "}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestKBAssistRejectsOversizeSelection(t *testing.T) {
	s, cookies := newKBTestServer(t)
	// cap+1 is rejected, not truncated: a silently shortened passage would
	// come back as a rewrite of something the user did not select.
	big := strings.Repeat("a", maxAssistSelectionBytes+1)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "notes/a.md", "selection": big}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestKBAssistRejectsPathTraversal(t *testing.T) {
	s, cookies := newKBTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "../../etc/passwd", "selection": "x"}, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_path") {
		t.Errorf("body = %s, want invalid_path", rec.Body.String())
	}
}

func TestKBAssistAcceptsSelectionAtTheCap(t *testing.T) {
	s, cookies := newKBTestServer(t)
	atCap := strings.Repeat("a", maxAssistSelectionBytes)
	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/assist",
		map[string]string{"action": "improve", "path": "notes/a.md", "selection": atCap}, cookies)
	// The coder is not configured in tests, so this must NOT be a 400 — it
	// fails later, at the coder call. Anything in the 4xx range other than a
	// coder-unavailable 503 means the cap is off by one.
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("a selection exactly at the cap was rejected: %s", rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
}
```

If the KB test helpers in this package are named differently, use the existing names.

- [ ] **Step 7: Run to verify it fails**

Run: `go test ./web/ -run TestKBAssist -count=1 -v`
Expected: FAIL — route not registered, `maxAssistSelectionBytes` undefined.

- [ ] **Step 8: Write the handler**

Create `web/api_kb_assist.go`:

```go
package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ilijad1/rookery/internal/agentrunner"
	"github.com/ilijad1/rookery/internal/coder"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/prompts"
	"github.com/labstack/echo/v4"
)

// maxAssistSelectionBytes caps the passage sent to the model. Deliberately NOT
// internal/iolimit's 25 MiB: that cap governs ingest doors (uploads,
// attachments, the KB bridge), and reusing it here would admit a payload no
// single LLM call should carry. Over-cap is REJECTED rather than truncated — a
// silently shortened passage comes back as a rewrite of something the user
// never selected.
const maxAssistSelectionBytes = 16 << 10 // 16 KiB

type apiKBAssistRequest struct {
	Action    string `json:"action"`
	Path      string `json:"path"`
	Selection string `json:"selection"`
}

type apiKBAssistResponse struct {
	Action string `json:"action"`
	Result string `json:"result"`
}

func validAssistAction(action string) bool {
	for _, a := range prompts.KBAssistActions() {
		if a == action {
			return true
		}
	}
	return false
}

// apiKBAssist runs one text-only coder call over a selected passage.
//
// POST /api/v1/kb/assist {action,path,selection} → 200 {action,result}
func (s *Server) apiKBAssist(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)

	var req apiKBAssistRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if !validAssistAction(req.Action) {
		return jsonErr(c, http.StatusBadRequest, "invalid_request",
			"unknown action: "+req.Action)
	}
	if strings.TrimSpace(req.Selection) == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_request",
			"select some text first")
	}
	if len(req.Selection) > maxAssistSelectionBytes {
		return jsonErr(c, http.StatusBadRequest, "invalid_request",
			"that selection is too long — select a smaller passage")
	}
	// The path is only prompt context, but it still goes through the vault's
	// safety primitive: an endpoint that echoes an unvalidated path into a
	// model prompt is the kind of thing that quietly becomes a read later.
	if _, err := s.vault.Resolve(w.ID, req.Path); err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "invalid note path")
	}

	prompt := prompts.BuildKBAssistPrompt(req.Action, req.Path, req.Selection)
	result, err := s.coderForWorkspace(w.ID).WithNoTools().Generate(c.Request().Context(), w.ID, prompt)
	if err != nil {
		if errors.Is(err, coder.ErrUsageLimit) ||
			errors.Is(err, coder.ErrRateLimited) ||
			errors.Is(err, coder.ErrAPIAuth) {
			// One wording for a quota/auth failure across the whole product:
			// a scheduled run and the note editor must not disagree.
			return jsonErr(c, http.StatusServiceUnavailable, "coder_unavailable",
				agentrunner.FriendlyRunError(err, ""))
		}
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}

	return c.JSON(http.StatusOK, apiKBAssistResponse{
		Action: req.Action,
		Result: strings.TrimSpace(result.Text),
	})
}
```

Register it in `web/api_kb.go`'s `registerKBAPI`, alongside the other KB routes:

```go
	g.POST("/kb/assist", s.apiKBAssist)
```

Add `POST /api/v1/kb/assist` to the `want` table in `web/api_parity_test.go` — that
table is the merge gate asserting the registered route set matches the planned one, so
the build fails without it.

- [ ] **Step 9: Run the Go tests**

Run: `go test ./web/... ./internal/prompts/... ./internal/agentrunner/... -count=1`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/prompts/kbassist.go internal/prompts/kbassist_test.go \
        internal/agentrunner/runner.go \
        web/api_kb_assist.go web/api_kb_assist_test.go \
        web/api_kb.go web/api_parity_test.go
git commit -m "feat(kb): add POST /api/v1/kb/assist

One blocking, text-only coder call over a selected passage, with a closed
action set and a 16 KiB cap that rejects rather than truncates. Exports
agentrunner.FriendlyRunError so a workspace out of quota gets the same
sentence here as it does from a scheduled run."
```

---

### Task 7: The selection panel

**Files:**
- Create: `web/ui/src/lib/kbAssist.ts`
- Create: `web/ui/src/pages/kb/AIActions.tsx`
- Create: `web/ui/src/pages/kb/aiactions.test.tsx`
- Modify: `web/ui/src/pages/kb/BubbleToolbar.tsx`
- Modify: `web/ui/src/pages/kb/ChatAboutFileButton.tsx`

**Interfaces:**
- Consumes: `POST /api/v1/kb/assist` from Task 6; `TEXT_COLORS` etc. untouched
- Produces:
  - `useKBAssist()` — react-query mutation over `{action, path, selection}`
  - `AIActions` — the panel component
  - `selectionChatPrompt(path: string, selection: string): string`

- [ ] **Step 1: Write the failing tests**

Create `web/ui/src/pages/kb/aiactions.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Editor } from "@tiptap/core";
import AIActions from "./AIActions";
import { buildExtensions } from "./editor";
import { selectionChatPrompt } from "./ChatAboutFileButton";

function makeEditor() {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: "<p>the pipeline runs on merge</p>",
  });
  editor.commands.selectAll();
  return editor;
}

function renderPanel(editor: Editor) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <AIActions editor={editor} path="notes/ci.md" />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.restoreAllMocks());

test("a rewrite action shows the result with Accept and Discard", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "improve", result: "The pipeline runs on every merge." }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
  const editor = makeEditor();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  expect(await screen.findByText("The pipeline runs on every merge.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Accept/ })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Discard/ })).toBeInTheDocument();
});

test("Discard leaves the document untouched", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "improve", result: "REWRITTEN" }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
  const editor = makeEditor();
  const before = editor.getHTML();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await screen.findByText("REWRITTEN");
  await user.click(screen.getByRole("button", { name: /Discard/ }));
  expect(editor.getHTML()).toBe(before);
});

test("Accept replaces the captured range, not the live selection", async () => {
  // The range is captured at CLICK time and applied on Accept. If Accept used
  // the live selection, anything that moved the caret during the round trip
  // would paste the rewrite in the wrong place.
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "improve", result: "REWRITTEN" }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
  const editor = makeEditor();
  renderPanel(editor);

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  await screen.findByText("REWRITTEN");
  editor.commands.setTextSelection(1); // collapse the selection mid-flight
  await user.click(screen.getByRole("button", { name: /Accept/ }));
  await waitFor(() => expect(editor.getText()).toContain("REWRITTEN"));
});

test("Explain offers Copy and never Accept", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ action: "explain", result: "It means X." }),
      { status: 200, headers: { "Content-Type": "application/json" } }),
  );
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: /Explain/ }));
  expect(await screen.findByText("It means X.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /Copy/ })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Accept/ })).not.toBeInTheDocument();
});

test("a failed request shows the server's message", async () => {
  const user = userEvent.setup();
  vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(JSON.stringify({ error: { code: "coder_unavailable", message: "⚠️ out of quota" } }),
      { status: 503, headers: { "Content-Type": "application/json" } }),
  );
  renderPanel(makeEditor());

  await user.click(screen.getByRole("button", { name: /Improve/ }));
  expect(await screen.findByText(/out of quota/)).toBeInTheDocument();
});

test("the selection chat prompt names the file and quotes the passage", () => {
  const p = selectionChatPrompt("notes/ci.md", "the pipeline runs on merge");
  expect(p).toContain("notes/ci.md");
  expect(p).toContain("the pipeline runs on merge");
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/aiactions.test.tsx`
Expected: FAIL — `./AIActions` does not exist.

- [ ] **Step 3: Write the mutation hook**

Create `web/ui/src/lib/kbAssist.ts`:

```ts
import { useMutation } from "@tanstack/react-query";
import { api } from "@/lib/api";

export type KBAssistAction = "improve" | "proofread" | "explain" | "reformat";

export type KBAssistResponse = { action: KBAssistAction; result: string };

export function useKBAssist() {
  return useMutation({
    mutationFn: (input: { action: KBAssistAction; path: string; selection: string }) =>
      api.post<KBAssistResponse>("/api/v1/kb/assist", input),
  });
}
```

If `api.post`'s signature in `web/ui/src/lib/api.ts` differs (e.g. it takes the body as
a second argument with a different generic position), match it exactly — read that file
before writing this one.

- [ ] **Step 4: Write the panel**

Create `web/ui/src/pages/kb/AIActions.tsx`:

```tsx
import { useState } from "react";
import type { Editor } from "@tiptap/core";
import { Sparkles, SpellCheck, Lightbulb, WandSparkles, Loader2, Copy, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useSlideOver } from "@/components/shell/AppShell";
import { GlobalChatPanel } from "@/components/chat/GlobalChatButton";
import { useKBAssist, type KBAssistAction } from "@/lib/kbAssist";
import { toMarkdown } from "./editor";
import { selectionChatPrompt } from "./ChatAboutFileButton";

const ACTIONS: { id: KBAssistAction; label: string; icon: typeof Sparkles }[] = [
  { id: "improve", label: "Improve", icon: Sparkles },
  { id: "proofread", label: "Proofread", icon: SpellCheck },
  { id: "explain", label: "Explain", icon: Lightbulb },
  { id: "reformat", label: "Reformat", icon: WandSparkles },
];

// The markdown of the SELECTED SLICE, not its plain text: Reformat needs to see
// structure, and an accepted result is parsed back through markdown so a
// returned list becomes a real list rather than literal "- " characters.
function selectionMarkdown(editor: Editor): string {
  const { from, to } = editor.state.selection;
  const slice = editor.state.doc.slice(from, to);
  const scratch = editor.storage.markdown?.serializer;
  if (scratch) return scratch.serialize(slice.content);
  return editor.state.doc.textBetween(from, to, "\n");
}

export default function AIActions({ editor, path }: { editor: Editor; path: string }) {
  const assist = useKBAssist();
  const { open } = useSlideOver();
  // Captured at CLICK time. The selection must survive an async round trip and
  // a re-render; applying to the LIVE selection would paste the rewrite
  // wherever the caret happens to be when the response lands.
  const [range, setRange] = useState<{ from: number; to: number } | null>(null);
  const [action, setAction] = useState<KBAssistAction | null>(null);

  function run(id: KBAssistAction) {
    const { from, to } = editor.state.selection;
    setRange({ from, to });
    setAction(id);
    assist.mutate({ action: id, path, selection: selectionMarkdown(editor) });
  }

  function reset() {
    setRange(null);
    setAction(null);
    assist.reset();
  }

  function accept() {
    if (!range || !assist.data) return;
    const html = editor.storage.markdown?.parser?.parse(assist.data.result, { inline: true });
    editor
      .chain()
      .focus()
      .deleteRange(range)
      .insertContentAt(range.from, html ?? assist.data.result)
      .run();
    reset();
  }

  if (!action) {
    return (
      <div className="flex items-center gap-0.5 border-t border-border p-1">
        {ACTIONS.map(({ id, label, icon: Icon }) => (
          <button
            key={id}
            type="button"
            aria-label={label}
            title={label}
            // Mousedown, not click — a click collapses the selection first.
            onMouseDown={(e) => {
              e.preventDefault();
              run(id);
            }}
            className="inline-flex items-center gap-1 rounded-sm px-1.5 py-1 text-xs text-foreground hover:bg-accent"
          >
            <Icon className="size-3.5" />
            {label}
          </button>
        ))}
        <button
          type="button"
          aria-label="Edit with AI"
          title="Edit with AI"
          onMouseDown={(e) => {
            e.preventDefault();
            open(
              <GlobalChatPanel
                forceNew
                initialText={selectionChatPrompt(path, selectionMarkdown(editor))}
              />,
              { title: "Chat" },
            );
          }}
          className="inline-flex items-center gap-1 rounded-sm px-1.5 py-1 text-xs text-accent hover:bg-accent-soft"
        >
          Edit with AI
        </button>
      </div>
    );
  }

  const label = ACTIONS.find((a) => a.id === action)?.label ?? "";
  return (
    <div className="w-80 border-t border-border p-2 text-sm">
      <div className="mb-1 flex items-center justify-between text-xs text-muted-2">
        <span>{label}</span>
        <button type="button" aria-label="Close" onMouseDown={(e) => { e.preventDefault(); reset(); }}>
          <X className="size-3.5" />
        </button>
      </div>

      {assist.isPending && (
        <div className="flex items-center gap-2 py-3 text-muted-2">
          <Loader2 className="size-4 animate-spin" /> Working…
        </div>
      )}

      {assist.isError && (
        <div className="py-2 text-danger">
          {assist.error instanceof Error ? assist.error.message : "That didn't work."}
        </div>
      )}

      {assist.data && (
        <>
          <div className="max-h-56 overflow-y-auto whitespace-pre-wrap rounded-sm bg-chrome p-2">
            {assist.data.result}
          </div>
          <div className="mt-2 flex items-center justify-end gap-2">
            {action === "explain" ? (
              // Explain never writes to the note — it is a question about the
              // passage, not an edit of it.
              <Button
                size="sm"
                variant="outline"
                onMouseDown={(e) => {
                  e.preventDefault();
                  void navigator.clipboard?.writeText(assist.data!.result);
                }}
              >
                <Copy className="size-4" />
                Copy
              </Button>
            ) : (
              <>
                <Button size="sm" variant="ghost" onMouseDown={(e) => { e.preventDefault(); reset(); }}>
                  Discard
                </Button>
                <Button size="sm" onMouseDown={(e) => { e.preventDefault(); accept(); }}>
                  Accept
                </Button>
              </>
            )}
          </div>
        </>
      )}
    </div>
  );
}
```

- [ ] **Step 5: Add the selection chat prompt**

In `web/ui/src/pages/kb/ChatAboutFileButton.tsx`, add below the existing `chatPrompt`:

```ts
// The selection-scoped sibling of chatPrompt. It NAMES the file and QUOTES the
// passage — the passage because that is the thing the user is asking about,
// the path because the chat coder runs rooted at the vault with file tools and
// can open the note itself for surrounding context.
//
// Exported for direct unit testing: the exact wording is the contract between
// this button and the coder's ability to act on the right text.
export function selectionChatPrompt(path: string, selection: string): string {
  return `In my knowledge base file \`${path}\`, I've selected this passage:

> ${selection.split("\n").join("\n> ")}

`;
}
```

- [ ] **Step 6: Mount the panel in the toolbar**

In `BubbleToolbar.tsx`, accept a `path` prop and render `AIActions` under the button
row. Change the signature to
`export default function BubbleToolbar({ editor, path }: { editor: Editor | null; path: string })`
and add inside the outer popover `<div>`, after the button row and outside the
`colorsOpen` conditional:

```tsx
        <AIActions editor={editor} path={path} />
```

Add `import AIActions from "./AIActions";`.

In `NoteEditor.tsx`, pass the path: find the `<BubbleToolbar editor={editor} />` render
site and change it to `<BubbleToolbar editor={editor} path={path} />`. `WysiwygEditor`
does not currently receive `path` — add it to its props type and pass it from the
parent, matching how `content` and `editable` are already threaded.

- [ ] **Step 7: Run the tests**

Run: `cd web/ui && npx vitest run src/pages/kb/aiactions.test.tsx`
Expected: PASS.

- [ ] **Step 8: Run the gate**

Run: `cd web/ui && npm run test && npx tsc -b && npx oxlint`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add web/ui/src/lib/kbAssist.ts web/ui/src/pages/kb/AIActions.tsx \
        web/ui/src/pages/kb/aiactions.test.tsx \
        web/ui/src/pages/kb/BubbleToolbar.tsx \
        web/ui/src/pages/kb/ChatAboutFileButton.tsx \
        web/ui/src/pages/kb/NoteEditor.tsx
git commit -m "feat(kb): AI actions on a text selection

Improve, Proofread, Explain and Reformat run in the bubble toolbar with
an Accept/Discard preview; Explain is read-only and offers Copy instead.
The selection range is captured at click time so an async round trip
cannot paste the rewrite wherever the caret drifted to. Edit with AI
hands off to the global chat slide-over with the passage quoted."
```

---

### Task 8: Full local gate

**Files:** none

- [ ] **Step 1: Run the complete PR gate**

Run (from the repo root): `make ci`
Expected: PASS — gofmt, `go vet`, `go test -race` (900s timeout), the six-target
cross-compile, and the frontend build.

- [ ] **Step 2: Manually exercise the editor**

Build and run, then open a note and confirm by eye: a bullet list shows bullets, a
colour swatch applies, a callout renders with its tint, a toggle collapses, an image
resizes by dragging its corner, and Improve returns text with Accept/Discard.

```bash
make deploy && make logs
```

`make deploy` restarts the server from this branch — that is the documented
local-branch testing path and is fine before the PR merges.

- [ ] **Step 3: Confirm no note was pushed into read-only**

Open two or three existing notes that predate this branch and confirm none shows the
read-only banner. A construct whose round trip is not exact would put every note
containing it into a read-only rich view, and the fidelity tests only cover the shapes
they name.

- [ ] **Step 4: Push and open a draft PR**

```bash
git push -u origin worktree-kb-editor-brainstorm
gh pr create --draft \
  --title "feat(kb): editor formatting, image resize, and AI selection actions" \
  --body "$(cat <<'EOF'
Implements sections 2, 3 and 4 of
`docs/superpowers/specs/2026-08-06-kb-editor-and-connections-design.md`.

- **Formatting.** Underline, a fixed 8x2 colour palette, five Obsidian
  callouts and toggle lists. Each is defined by its markdown form first and
  ships with a `checkFidelity` round-trip test, because a construct that does
  not round-trip exactly forces every note containing it into a read-only view.
- **Colour contrast is stated honestly.** No fixed hex reaches AA body text
  against both themes (best is 4.15), so the text palette floors at 3.53:1 and
  `kbPalette.test.ts` computes and pins that. Highlights carry a pinned
  `#18181b` foreground so a pale tint stays legible on the dark theme.
- **Image resize** via a drag handle, stored as `![alt|420](src)`. An unsized
  image serializes byte-for-byte as before.
- **AI actions** — Improve / Proofread / Explain / Reformat with an
  Accept/Discard preview, plus Edit with AI handing off to the chat
  slide-over. One blocking `POST /api/v1/kb/assist`, text-only coder call,
  closed action set, 16 KiB cap that rejects rather than truncates.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review

**Spec coverage.** Section 2 (formatting): underline → Task 1, colours + palette →
Task 2, callouts → Task 3, toggle → Task 4, slash-menu entries → Tasks 3 and 4,
bubble-toolbar surfaces → Tasks 1, 2 and 7. Section 3 (image resize) → Task 5.
Section 4 (AI actions): endpoint + prompt + `FriendlyRunError` export → Task 6, panel +
Accept/Discard + Explain-is-read-only + Edit with AI → Task 7. Sections 1, 5 and 6 are
Plan A. No gaps.

**Placeholders.** None. Every code step carries real code. Three steps say "match the
existing helper/signature" (Task 6 step 6 test helpers, Task 7 step 3 `api.post`,
Task 7 step 6 prop threading) — each names the exact file to read first and what to
match, because inventing a second helper where one exists is the worse outcome. Task 3
step 4 and Task 4 step 4 carry named, exact fallbacks rather than "adjust as needed",
because prosemirror-markdown's block spacing is the one thing that cannot be settled
without running it.

**Type consistency.** `TEXT_COLORS`/`HIGHLIGHT_COLORS`/`HIGHLIGHT_FG`/`toHex` are
defined in Task 2 step 3 and consumed under those names in Task 2 steps 1 and 7.
`CALLOUT_KINDS` is defined in Task 3 step 3 and consumed in Task 3 step 6.
`clampImageWidth`/`splitAltWidth`/`joinAltWidth` are defined in Task 5 step 3 and
consumed in Task 5 steps 1, 6 and 8. `KBAssistActions`/`BuildKBAssistPrompt` are
defined in Task 6 step 3 and consumed in Task 6 steps 1 and 8.
`agentrunner.FriendlyRunError` is exported in Task 6 step 5 and called in step 8.
`maxAssistSelectionBytes` is defined in Task 6 step 8 and referenced by the tests in
step 6 — the tests are written first and fail on the undefined constant, which is the
intended TDD order. `useKBAssist`/`KBAssistAction` are defined in Task 7 step 3 and
consumed in step 4. `selectionChatPrompt` is defined in Task 7 step 5 and consumed in
Task 7 steps 1 and 4.

**Ordering risk.** Tasks 1, 2, 3, 4 and 7 all edit `BubbleToolbar.tsx`, and Tasks 1–5
all edit `editor.ts` or `editor.css`. They must land in sequence, not in parallel.
Task 2 step 7 restructures the toolbar's JSX (wrapping the button row for the colour
panel); Task 7 step 6 depends on that structure existing.
