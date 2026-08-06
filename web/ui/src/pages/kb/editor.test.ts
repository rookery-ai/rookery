import { Editor } from "@tiptap/core";
import { fidelityRoundTrip, checkFidelity, buildExtensions, toMarkdown } from "./editor";

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
    // never happen. The app now runs with `html: true` (editor.ts), so a cell
    // the simple pipe grammar can't hold is deferred to the HTML fallback,
    // which preserves BOTH paragraphs as real <table> HTML rather than writing
    // the old lossy "[table]" placeholder (which discarded the whole table).
    // Strictly safer: no content is dropped on the save path.
    const hasParaOne = out.includes("PARA_ONE");
    const hasParaTwo = out.includes("PARA_TWO");
    expect(hasParaOne && !hasParaTwo).toBe(false);
    expect(hasParaOne && hasParaTwo).toBe(true);
    expect(out).toContain("<table");
  });

  // Companion to the two-paragraph case above: exercises the OTHER arm of
  // isMarkdownSerializable -- a merged (colspan) cell. Such a table can arrive
  // by pasting from a webpage/Word/Sheets. It must take the same honest
  // fallback (whole table -> real HTML <table> under html:true, preserving the
  // cell content) rather than emit a delimiter row sized off the first row's
  // childCount, which would produce a column-count-mismatched, malformed table.
  it("a table with a colspan cell falls back instead of emitting a malformed table", () => {
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
    // One body cell spanning both columns (colspan: 2) — hasSpan() -> not
    // markdown-serializable.
    const spanCell = schema.nodes.tableCell.create(
      { colspan: 2 },
      schema.nodes.paragraph.create(null, schema.text("SPAN_CELL")),
    );
    const bodyRow = schema.nodes.tableRow.create(null, [spanCell]);
    const table = schema.nodes.table.create(null, [headerRow, bodyRow]);
    const doc = schema.nodes.doc.create(null, table);

    const markdownStorage = editor.storage.markdown as unknown as {
      serializer: { serialize(node: typeof doc): string };
    };
    const out: string = markdownStorage.serializer.serialize(doc);
    editor.destroy();

    // Fallback fired: the whole table is deferred to the HTML fallback (which
    // preserves the cell content), not rendered as an ordinary (and here
    // malformed) markdown table.
    expect(out).toContain("SPAN_CELL");
    expect(out).toContain("<table");
    expect(out).not.toMatch(/\|\s*---/); // no markdown delimiter row was emitted
  });
});

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

// Composition: KBBgColor is registered before KBTextColor in editor.ts on
// purpose. Registration order sets mark rank, which sets DOM nesting order
// on serialize (lower rank = outer). With the highlight outer and the text
// colour inner, the text-colour span sits closest to the actual text, so
// its own `color` — which isn't inherited, it directly styles that span —
// wins over the highlight's pinned foreground. Getting the order backwards
// makes red text on a yellow highlight unreachable: the highlight's pinned
// #18181b would always win.
describe("colour mark composition", () => {
  it("renders the text-colour span innermost regardless of application order", () => {
    // Two different source nestings should normalize to the SAME output
    // nesting, because ProseMirror sorts a node's marks by rank, not by
    // how the HTML happened to nest them going in.
    const highlightThenColor = fidelityRoundTrip(
      '<span style="background-color:#fef08a;color:#18181b"><span style="color:#ef4444">beta</span></span>',
    );
    const colorThenHighlight = fidelityRoundTrip(
      '<span style="color:#ef4444"><span style="background-color:#fef08a;color:#18181b">beta</span></span>',
    );
    for (const out of [highlightThenColor, colorThenHighlight]) {
      // The highlight span is outer (carries the background), the text
      // colour span is inner (sits directly on "beta") — so #ef4444 is the
      // color actually applied to the text, not the highlight's #18181b.
      expect(out).toMatch(
        /<span style="background-color:#fef08a;color:#18181b"><span style="color:#ef4444">beta<\/span><\/span>/,
      );
    }
  });

  it("the source order matching mark rank (highlight outer) round-trips byte-for-byte", () => {
    expect(
      checkFidelity(
        '<span style="background-color:#fef08a;color:#18181b"><span style="color:#ef4444">beta</span></span>\n',
      ),
    ).toBe(true);
  });

  // KNOWN LIMITATION, not a regression from this fix: ProseMirror sorts a
  // node's marks by RANK on every serialize, so composed marks always come
  // back out in ONE canonical nesting order — never in whatever order the
  // source HTML happened to use. Exactly one of the two possible source
  // nestings can match that canonical order byte-for-byte; the other
  // necessarily fails checkFidelity's strict string comparison, even though
  // no information is lost (same two marks, same two colours — only the
  // literal span nesting differs) and the visual result is identical either
  // way. This was already true before this fix (previously the OTHER source
  // order — text-colour outer — was the one that matched); putting KBBgColor
  // first only relocated which direction wins, it did not introduce a new
  // failure. A hand-authored or externally-imported note using the
  // now-nonconforming nesting falls back to raw/read-only view on open —
  // narrow (composed colour+highlight HTML from outside this editor), but
  // real. Flagged in the task-2 fix report rather than worked around, per
  // instruction: fixing it for real would need a custom nesting-preserving
  // serializer, which is out of scope here.
  it("the other source order (text colour outer) is detected as non-matching, not silently corrupted", () => {
    expect(
      checkFidelity(
        '<span style="color:#ef4444"><span style="background-color:#fef08a;color:#18181b">beta</span></span>\n',
      ),
    ).toBe(false);
  });

  it("a highlight with no text colour still carries the pinned foreground", () => {
    const out = fidelityRoundTrip(
      '<span style="background-color:#fef08a;color:#18181b">Friday</span>\n',
    );
    expect(out).toContain('background-color:#fef08a;color:#18181b');
  });
});

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

// Fix round 1, finding 1: "> [!kind] Title" (marker followed by title text on
// the same line) is Obsidian's own documented, most common callout form.
// Before this fix the serializer always emitted a bare "> [!kind]" and
// reflowed any title text onto the body line, so this exact input failed
// checkFidelity and an imported Obsidian vault using titled callouts opened
// read-only.
test("a titled note callout survives a markdown round trip", () => {
  expect(checkFidelity("> [!note] My Title\n> Body.\n")).toBe(true);
});

test("a titled callout of another kind (warning) survives a markdown round trip", () => {
  expect(checkFidelity("> [!warning] Careful now\n> Body.\n")).toBe(true);
});

test("an untitled callout still round-trips exactly as before (no title regression)", () => {
  expect(checkFidelity("> [!note]\n> Body text here.\n")).toBe(true);
  const out = fidelityRoundTrip("> [!note]\n> Body text here.\n");
  // No stray title text/attribute leaked onto or before the body line.
  expect(out.trim()).toBe("> [!note]\n> Body text here.".trim());
});

// markdown-it (html:true) treats "<details>" and "<summary>" as type-6 HTML
// block tags: the opening line "<details><summary>Show details</summary>" is
// consumed verbatim up to the next blank line, and a blank-line-separated
// body in between is rendered as an ordinary paragraph ("<p>...</p>"), not
// left as bare text. Since Toggle/ToggleSummary declare no markdown spec,
// serialization falls back to tiptap-markdown's generic HTML-node writer,
// which reflects the doc's real DOM shape back out — literal "<p>" tags,
// glued directly to the preceding element (no blank line). That output form
// is what a round trip actually stabilizes on, so it — not the blank-line
// style a human might hand-type — is the byte-for-byte fixed point the
// fidelity contract requires.
test("a toggle list survives a markdown round trip", () => {
  expect(
    checkFidelity(
      "<details>\n<summary>Show details</summary><p>Hidden body.</p>\n</details>\n",
    ),
  ).toBe(true);
});

test("a toggle inserted via setToggle() and saved immediately round trips", () => {
  // The empty-body case a user hits by opening the slash menu and saving
  // before typing anything: an empty <p></p> body must not be lost.
  expect(
    checkFidelity("<details>\n<summary>Toggle</summary><p></p>\n</details>\n"),
  ).toBe(true);
});
