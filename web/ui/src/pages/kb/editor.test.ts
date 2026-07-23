import { Editor } from "@tiptap/core";
import { fidelityRoundTrip, checkFidelity, buildExtensions } from "./editor";

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

// @tiptap/extension-image is registered in buildExtensions()'s default set
// (editor.ts) — the same schema NoteEditor's WYSIWYG editor mounts (via
// WysiwygEditor's buildExtensions([slashSuggestion()])). Locks in the
// registration itself, not just the round-trip behavior corpus.test.ts pins.
test("buildExtensions() registers an image node and markdown round-trips a plain image", () => {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: "<p></p>",
  });
  expect(editor.schema.nodes.image).toBeDefined();
  editor.destroy();

  const md = "![alt text](https://example.com/img.png)\n";
  expect(checkFidelity(md)).toBe(true);
  const out = fidelityRoundTrip(md);
  expect(out).toContain("![alt text](https://example.com/img.png)");
});

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

  // Regression guard: a table cell with two paragraphs (an ordinary edit --
  // click in a cell, press Enter) is not representable by the simple
  // "| a | b |" markdown table grammar. tiptap-markdown's stock serializer
  // detects this via isMarkdownSerializable() and falls back to HTML/a
  // placeholder instead of rendering the table as simple markdown; an
  // earlier version of PipeSafeTable dropped that guard entirely and always
  // rendered `col.firstChild`, which would silently keep the first paragraph
  // and discard the second one -- a plausible-looking but corrupted table,
  // with no load-time re-check (checkFidelity only runs at note load, not on
  // the save path toMarkdown(editor)) to ever catch it.
  //
  // Constructing this doc from markdown input is impossible (there's no
  // markdown syntax for two block-level paragraphs inside one table cell),
  // so it's built directly via the real ProseMirror schema and run through
  // the actual tiptap-markdown serializer (with PipeSafeTable registered via
  // buildExtensions()) -- proving the invariant against the real save path,
  // not a re-derived one.
  it("a table cell with two paragraphs never silently drops the second paragraph on save", () => {
    const editor = new Editor({
      element: document.createElement("div"),
      extensions: buildExtensions(),
      content: "<p></p>",
    });
    const { schema } = editor;

    const headerRow = schema.nodes.tableRow.create(null, [
      schema.nodes.tableHeader.create(null, schema.nodes.paragraph.create(null, schema.text("a"))),
      schema.nodes.tableHeader.create(null, schema.nodes.paragraph.create(null, schema.text("b"))),
    ]);
    const twoParaCell = schema.nodes.tableCell.create(null, [
      schema.nodes.paragraph.create(null, schema.text("PARA_ONE")),
      schema.nodes.paragraph.create(null, schema.text("PARA_TWO")),
    ]);
    const otherCell = schema.nodes.tableCell.create(
      null,
      schema.nodes.paragraph.create(null, schema.text("z")),
    );
    const bodyRow = schema.nodes.tableRow.create(null, [twoParaCell, otherCell]);
    const table = schema.nodes.table.create(null, [headerRow, bodyRow]);
    const doc = schema.nodes.doc.create(null, table);

    // tiptap-markdown's public MarkdownStorage type only declares
    // options/getMarkdown(); .serializer exists at runtime (see
    // MarkdownSerializer.js) but isn't part of the published .d.ts.
    const markdownStorage = editor.storage.markdown as unknown as {
      serializer: { serialize(node: typeof doc): string };
    };
    const out: string = markdownStorage.serializer.serialize(doc);
    editor.destroy();

    // The dangerous case the old code produced: PARA_ONE present but
    // PARA_TWO silently missing (a corrupted-but-plausible table). That must
    // never happen -- either both paragraphs survive, or the whole table is
    // honestly deferred to the non-markdown fallback (neither rendered as an
    // ordinary cell), which is what actually happens here: this app runs
    // with `html: false` (editor.ts), so the fallback writes the same
    // "[table]" placeholder the stock tiptap-markdown html node writes,
    // keeping the note out of a false WYSIWYG-safe state instead of quietly
    // discarding PARA_TWO.
    const hasParaOne = out.includes("PARA_ONE");
    const hasParaTwo = out.includes("PARA_TWO");
    expect(hasParaOne && !hasParaTwo).toBe(false);
    expect(out).toContain("[table]");
  });
});
