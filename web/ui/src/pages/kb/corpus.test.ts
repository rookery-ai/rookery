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
//
// agent-state-md-template (internal/agentdesigner/statefile.go's
// RenderStateTemplate) is deliberately NOT in EXPECTED_LOSSY — it is pinned
// CLEAN. It was briefly lossy for two independent reasons (underscore
// emphasis re-serializing as asterisk; a soft line break inside the intro
// paragraph collapsing to a plain space), both fixed by authoring the
// template with asterisk emphasis on a single physical source line. See
// task-7-report.md's "Fix: template round-trip" section for the empirical
// before/after. This entry stays in the corpus specifically so a future
// tiptap-markdown upgrade that reopens either failure mode fails loudly
// instead of silently pinning every agent's state.md into raw mode again.
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
  // Alignment (nodes/align.ts): the canonical form is the blank-line-separated
  // one, which is what real-world markdown contains and what keeps the body
  // parseable as markdown. A serializer can reproduce only ONE spelling, so
  // these two normalise to it on the first save and the note opens read-only
  // until then. That is a strict improvement on the previous behaviour, where
  // EVERY div spelling lost its wrapper entirely and the alignment was
  // discarded on save — see the design doc's measured before/after table.
  "align-glued-tags",
  "align-style-attribute",
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
  // "{\n  \"a\": 1\n}") output. Pinned CLEAN — the template's intro uses asterisk
  // emphasis (matches what tiptap-markdown always re-emits) on a single physical
  // source line (no soft line break to collapse), so it round-trips WYSIWYG-safe.
  // This is the fix for the real finding this entry originally pinned as LOSSY
  // (underscore-emphasis normalization + soft-line-break collapse) — see git
  // history / task-7-report.md for that finding and the follow-up fix.
  //
  // This literal is NOT hand-maintained truth — it is a drift trap. It must stay
  // byte-for-byte identical to the live template output, so
  // internal/agentdesigner/statefile_corpus_test.go reads THIS file at Go test
  // time, extracts this exact string, and asserts it equals a live call to
  // RenderStateTemplate. Changing the Go template, or editing this literal so it
  // no longer matches, fails that Go test — do not hand-edit this string without
  // also running `go test ./internal/agentdesigner/...`.
  // -- Block alignment (nodes/align.ts) -------------------------------------
  {
    name: "align-center-paragraph",
    md: '<div align="center">\n\nHello **world**\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-right-paragraph",
    md: '<div align="right">\n\nSigned, me\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-left-paragraph",
    md: '<div align="left">\n\nHard left\n\n</div>\n',
    expectLossy: false,
  },
  // The image case is half the feature's point — a centred image is what most
  // people reach for alignment to do at all.
  {
    name: "align-centered-image",
    md: '<div align="center">\n\n![a diagram](assets/diagram.png)\n\n</div>\n',
    expectLossy: false,
  },
  // The blank-line body is what keeps these parseable as MARKDOWN rather than
  // as text inside a raw HTML block, which is the whole reason the wrapper is a
  // <div> and not a styled <p>.
  {
    name: "align-centered-list",
    md: '<div align="center">\n\n- one\n- two\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-centered-table",
    md: '<div align="center">\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-centered-heading",
    md: '<div align="center">\n\n# Title\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-multi-block-body",
    md: '<div align="center">\n\nOne\n\nTwo\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-with-neighbours",
    md: 'Before\n\n<div align="center">\n\nMiddle\n\n</div>\n\nAfter\n',
    expectLossy: false,
  },
  {
    name: "align-two-adjacent-blocks",
    md: '<div align="center">\n\nA\n\n</div>\n\n<div align="right">\n\nB\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-wikilink-inside",
    md: '<div align="center">\n\nSee [[notes/other]]\n\n</div>\n',
    expectLossy: false,
  },
  {
    name: "align-glued-tags",
    md: '<div align="center">Hello</div>\n',
    expectLossy: true,
  },
  {
    name: "align-style-attribute",
    md: '<div style="text-align: center">\n\nHello\n\n</div>\n',
    expectLossy: true,
  },
  {
    name: "agent-state-md-template",
    md: '# State — Gmail Digest\n\n*Managed by Rookery. The block below is this agent\'s memory between runs — edit it if you need to fix something by hand.*\n\n```json\n{\n  "a": 1\n}\n```\n',
    expectLossy: false,
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
