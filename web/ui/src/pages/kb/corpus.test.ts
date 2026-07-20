import { checkFidelity } from "./editor";

// Real-world markdown corpus pinned against checkFidelity's WYSIWYG-safety
// gate. The point is not "does everything round-trip" — it's that the raw
// vs. WYSIWYG boundary is asserted, not assumed, entry by entry. A
// tiptap/tiptap-markdown upgrade that shifts what round-trips cleanly must
// fail one of these named cases loudly instead of silently letting a note
// through WYSIWYG that then corrupts on save (or vice versa: forcing a note
// into raw mode that no longer needs to be).
//
// EXPECTED_LOSSY — entries pinned expectLossy:true, with why:
//   - memory-scaffold-user / memory-scaffold-soul: HTML placeholder comments
//     (`<!-- ... -->`) — Markdown.configure({ html: false }) escapes them to
//     entities on serialize (see editor.test.ts's HTML_COMMENT case; this
//     is the exact content internal/vault/vault.go:164,171 scaffolds).
//   - mixed-task-bullet-one-block: markdown-it-task-lists tags the whole
//     <ul> as taskList but only some <li> get task-item treatment;
//     ProseMirror's content-fitting injects a phantom empty checkbox and
//     splits the list. Pinned in Task 3 (editor.test.ts), kept consistent
//     here.
//   - mixed-task-bullet-adjacent: same underlying tiptap-markdown
//     bulletList/taskList mutual-exclusivity bug, newly observed here —
//     a plain bullet list and a task list separated by ONLY a blank line
//     (no intervening block element) still get merged into one ProseMirror
//     list and a phantom "- [ ] " item is injected at the top. Genuinely
//     separate blocks (see mixed-task-bullet-separated-by-heading below,
//     which IS clean) need a real block-level break, not just a blank line.
//   - table-with-alignment: alignment colons (`:--`, `:-:`, `--:`) are not
//     preserved by the table serializer — all columns normalize to plain
//     `---` on the way out. Structure/content survive; alignment doesn't.
//   - code-fence-tilde: a `~~~` fence is valid CommonMark but the serializer
//     always re-emits fenced code with backtick delimiters.
//   - reference-style-links: `[a][ref]` + a `[ref]: url "title"` definition
//     is rewritten to an inline link `[a](url "title")` — same rendered
//     result, different source bytes.
//   - image-alt-brackets: `@tiptap/extension-image` (editor.ts) round-trips
//     plain images (`![alt](url)`, `![](url)`, `![alt](url "title")`, alt
//     text with quotes/pipes/parens/relative URLs — all pinned clean below)
//     losslessly via prosemirror-markdown's default image serializer. But an
//     image node's `alt` is a plain-string ATTRIBUTE, not inline content, so
//     literal `[`/`]` bytes inside it go through the same backslash-escaping
//     text serializer as regular prose (see the top-of-file NOTE in
//     editor.ts) — `![a[b]c](url)` re-serializes as `![a\[b\]c](url)`.
//     CommonMark-equivalent, different bytes, correctly caught as lossy
//     (raw-mode fallback) rather than silently rewriting the alt text.
//   - hard-line-break: a two-space trailing hard break is re-serialized as
//     a backslash hard break (`\` at end of line) — CommonMark-equivalent
//     rendering, different literal bytes.
//   - setext-heading: `Title\n=====` is normalized to the ATX form `# Title`
//     on the way out.
//   - html-inline-tag: literal inline HTML (e.g. `<kbd>`) is escaped to
//     entities by the same `html: false` Markdown config as the memory
//     scaffolds' comments.
//   - agent-state-md-template: internal/agentdesigner/statefile.go's
//     RenderStateTemplate — a REAL FINDING, not a design choice. Two
//     independent, unrelated causes, each sufficient on its own:
//     (a) the intro's `_..._` underscore emphasis is re-serialized as
//     `*...*` — same delimiter-substitution class as code-fence-tilde
//     above, just for emphasis instead of code fences; (b) the intro
//     sentence is written across two source lines joined by a single `\n`
//     (a soft line break within one paragraph, not a hard break — no
//     trailing spaces) and tiptap-markdown collapses that to one line
//     joined by a plain space — same "same render, different bytes" class
//     as hard-line-break above, just for soft breaks instead of hard ones.
//     Both are verified independently (isolated single-cause repros) and
//     together on the exact template bytes. This means the design intent
//     at docs/superpowers/specs/2026-07-17-agent-files-as-documents-design.md
//     §4.1 ("Italic prose round-trips clean") is NOT actually true for the
//     template as authored: every fresh agent's state.md opens in RAW mode,
//     not WYSIWYG. A trivial fix exists (asterisk delimiter + keep the
//     intro on one line — verified clean in isolation) but is deliberately
//     NOT applied here: it would need pairing with a migration pass for
//     every already-written state.md (the Task 3 migration already wrote
//     the current, lossy bytes for existing agents), which is a follow-up
//     decision, not a Task 7 edit. Pinning this lossy is the honest
//     baseline — it still catches a FUTURE regression (e.g. the editor
//     upgrade making things worse, or a template edit that changes which
//     bytes are lossy) even though it isn't clean today.
const EXPECTED_LOSSY = new Set([
  "memory-scaffold-user",
  "memory-scaffold-soul",
  "mixed-task-bullet-one-block",
  "mixed-task-bullet-adjacent",
  "table-with-alignment",
  "code-fence-tilde",
  "reference-style-links",
  "image-alt-brackets",
  "hard-line-break",
  "setext-heading",
  "html-inline-tag",
  "agent-state-md-template",
]);

interface CorpusEntry {
  name: string;
  md: string;
  expectLossy: boolean;
}

const CORPUS: CorpusEntry[] = [
  // internal/vault/vault.go:164 — USER.md scaffold, exact bytes.
  {
    name: "memory-scaffold-user",
    md: "# About Me\n\n<!-- Add your name, location, role, and background here -->\n",
    expectLossy: true,
  },
  // internal/vault/vault.go:171 — SOUL.md scaffold, exact bytes.
  {
    name: "memory-scaffold-soul",
    md: "# Communication Style\n\n<!-- Add your preferred tone, language, and response style here -->\n",
    expectLossy: true,
  },
  {
    name: "nested-lists-3-deep",
    md: "- a\n  - b\n    - c\n- d\n",
    expectLossy: false,
  },
  {
    name: "mixed-task-bullet-one-block",
    md: "- a list\n- [ ] a todo\n- [x] done\n",
    expectLossy: true,
  },
  {
    name: "mixed-task-bullet-adjacent",
    md: "- a\n- b\n\n- [ ] todo1\n- [x] todo2\n",
    expectLossy: true,
  },
  {
    name: "mixed-task-bullet-separated-by-heading",
    md: "- a\n- b\n\n## Todos\n\n- [ ] todo1\n- [x] todo2\n",
    expectLossy: false,
  },
  {
    name: "table-with-alignment",
    md: "| a | b | c |\n| :-- | :-: | --: |\n| 1 | 2 | 3 |\n",
    expectLossy: true,
  },
  {
    name: "code-fence-lang-inner-backticks",
    md: "```js\nconst s = `template ${1}`;\n```\n",
    expectLossy: false,
  },
  {
    name: "code-fence-tilde",
    md: "~~~js\nconst x = 1;\n~~~\n",
    expectLossy: true,
  },
  {
    name: "reference-style-links",
    md: 'See [a][ref] for more.\n\n[ref]: https://example.com "Example"\n',
    expectLossy: true,
  },
  {
    name: "image",
    md: "![alt text](https://example.com/img.png)\n",
    expectLossy: false,
  },
  {
    name: "image-no-alt",
    md: "![](https://example.com/img.png)\n",
    expectLossy: false,
  },
  {
    name: "image-with-title",
    md: '![alt](https://example.com/img.png "a title")\n',
    expectLossy: false,
  },
  {
    name: "image-pipe-alt-and-title",
    md: '![a|b](https://example.com/img.png "t")\n',
    expectLossy: false,
  },
  {
    name: "image-alt-quotes",
    md: '![a "quote" b](https://example.com/img.png)\n',
    expectLossy: false,
  },
  {
    name: "image-alt-brackets",
    md: "![a[b]c](https://example.com/img.png)\n",
    expectLossy: true,
  },
  {
    name: "hard-line-break",
    md: "line one  \nline two\n",
    expectLossy: true,
  },
  {
    name: "skills-header",
    md: "# Skills: csv, pdf\n",
    expectLossy: false,
  },
  {
    name: "wikilinks-inline-everywhere",
    md: "Start [[a]] middle [[b|Alias B]] end.\n\n- [[c]] in a list\n\n> [[d]] in a quote\n",
    expectLossy: false,
  },
  {
    name: "em-dash-unicode-emoji",
    md: "This — that — 日本語 — 🎉 café\n",
    expectLossy: false,
  },
  {
    name: "setext-heading",
    md: "Title\n=====\n\nBody text.\n",
    expectLossy: true,
  },
  {
    name: "html-inline-tag",
    md: "Press <kbd>Ctrl</kbd>+<kbd>S</kbd> to save.\n",
    expectLossy: true,
  },
  // Pins internal/agentdesigner/statefile.go's RenderStateTemplate("Gmail Digest",
  // "{\n  \"a\": 1\n}") output. Pinned LOSSY — see the "agent-state-md-template" entry
  // in the EXPECTED_LOSSY doc block above for why (underscore-emphasis
  // normalization + soft-line-break collapse in the intro paragraph; the json
  // fence itself round-trips fine). Still a real regression guard: it fails
  // loudly if a future editor upgrade changes WHICH bytes are lossy, or makes
  // things worse, even though it does not start from clean today.
  //
  // This literal is NOT hand-maintained truth — it is a drift trap. It must stay
  // byte-for-byte identical to the live template output, so
  // internal/agentdesigner/statefile_corpus_test.go reads THIS file at Go test
  // time, extracts this exact string, and asserts it equals a live call to
  // RenderStateTemplate. Changing the Go template, or editing this literal so it
  // no longer matches, fails that Go test — do not hand-edit this string without
  // also running `go test ./internal/agentdesigner/...`.
  {
    name: "agent-state-md-template",
    md: '# State — Gmail Digest\n\n_Managed by Simple Agents. The block below is this agent\'s memory between runs —\nedit it if you need to fix something by hand._\n\n```json\n{\n  "a": 1\n}\n```\n',
    expectLossy: true,
  },
];

test("every corpus entry's expectLossy is accounted for in EXPECTED_LOSSY", () => {
  for (const entry of CORPUS) {
    expect(EXPECTED_LOSSY.has(entry.name)).toBe(entry.expectLossy);
  }
});

describe.each(CORPUS)("$name", ({ md, expectLossy }) => {
  test(`fidelity is ${expectLossy ? "lossy (raw-mode fallback)" : "clean (WYSIWYG-safe)"}`, () => {
    expect(checkFidelity(md)).toBe(!expectLossy);
  });
});
