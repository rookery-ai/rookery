import { fidelityRoundTrip, checkFidelity } from "./editor";

const CLEAN = `# Title

Some **bold** and *italic* and \`code\`.

- a list
- another item

Then some todos:

- [ ] a todo
- [x] done

> quote

\`\`\`js
const x = 1;
\`\`\`
`;

const HTML_COMMENT = `# About Me

<!-- Add your name, location, role, and background here -->
`;

test("clean markdown round-trips", () => {
  expect(checkFidelity(CLEAN)).toBe(true);
});

// tiptap-markdown@0.9.0 (TipTap 3.28) models bulletList and taskList as
// mutually-exclusive ProseMirror node types. When markdown-it-task-lists
// tags the whole <ul> as a taskList but only SOME of its <li> children as
// task items (a list that mixes plain bullets with `- [ ]` checkboxes),
// ProseMirror's content-fitting logic injects a phantom empty checkbox item
// and splits the list — genuine, verified data loss on WYSIWYG round-trip
// (confirmed via editor.getJSON(), not just markdown string diffing). This
// is exactly the case the fidelity gate exists to catch, so it correctly
// resolves to false -> raw mode, not a bug to route around.
test("a list mixing plain bullets and task items in one block is detected as lossy", () => {
  expect(checkFidelity("- a list\n- [ ] a todo\n- [x] done\n")).toBe(false);
});

// UPDATE (task 5, supersedes the task-3 finding above): the round-trip-unsafe
// behaviour documented below was fixed by modeling [[wikilinks]] as a proper
// TipTap node (see wikilinks.ts) instead of leaving them as plain text for
// prosemirror-markdown's generic serializer to escape. The node's
// `markdown.serialize` writes "[[target]]" literally via `state.write`
// (bypassing the escaping `state.text` would apply), so a note containing
// wikilinks now round-trips byte-for-byte and is safe to edit in WYSIWYG —
// internal/vault/links.go's wikilinkRE (literal-bracket match only) sees
// exactly what it saw on disk. wikilinks.test.ts covers the node's
// parse/serialize contract in more detail; this test just confirms the
// gate itself no longer forces these notes into raw mode.
test("wikilinks round-trip losslessly as a wikilink node (WYSIWYG-safe)", () => {
  expect(checkFidelity("See [[other-note]] and [[notes/deep]].\n")).toBe(true);
  const out = fidelityRoundTrip("See [[other-note]] and [[notes/deep]].\n");
  expect(out).toContain("[[other-note]]");
  expect(out).toContain("[[notes/deep]]");
  expect(out).not.toContain("\\[\\[");
});

test("HTML comments are detected as lossy (raw-mode fallback)", () => {
  // The memory scaffolds (USER.md/SOUL.md) contain HTML placeholder comments;
  // if tiptap-markdown ever round-trips them cleanly this test flips — then
  // remove the raw-mode forcing for comments, not the test.
  expect(checkFidelity(HTML_COMMENT)).toBe(false);
});

test("fidelityRoundTrip returns the re-serialized markdown", () => {
  expect(fidelityRoundTrip(CLEAN).trim().length).toBeGreaterThan(0);
});
