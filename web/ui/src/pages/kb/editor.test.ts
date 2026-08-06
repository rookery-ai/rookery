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

// AIActions.tsx's selectionMarkdown/accept() cast editor.storage.markdown
// through `unknown` to reach `.parser.parse`/`.serializer.serialize` — real
// runtime fields (tiptap-markdown's Markdown.js addStorage) that its own
// published .d.ts doesn't declare, so TypeScript can't catch a rename on a
// dependency bump. Both call sites optional-chain with a silent fallback: a
// missing `.serializer` degrades Reformat's selection markdown to plain text
// (selectionMarkdown falls back to textBetween), and a missing `.parser`
// makes accept() insert the LLM's raw markdown as literal text — a returned
// "- item" list becomes literal "- item" characters in the note instead of a
// real bullet, with zero error or warning either way. This test turns a
// silent capability regression into a CI failure.
test("editor.storage.markdown exposes .parser.parse and .serializer.serialize as functions", () => {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: "<p></p>",
  });
  const storage = editor.storage.markdown as unknown as {
    parser?: { parse: unknown };
    serializer?: { serialize: unknown };
  };
  expect(typeof storage.parser?.parse).toBe("function");
  expect(typeof storage.serializer?.serialize).toBe("function");
  editor.destroy();
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

// The canonical, pinned round-trip form: <details> and <summary> on
// SEPARATE lines, blank-line-separated body. This is GitHub's own
// documented convention and the form that dominates real-world markdown —
// the form a pasted README snippet or a vault-writing agent will most
// likely produce — so it, not the glued single-line form, is what this
// serializer targets as its one canonical output.
//
// IMPORTANT: the glued form ("<details><summary>Show details</summary>")
// is NOT a fixed point here, and that is intentional, not a bug to fix.
// Both spellings parse to the identical ProseMirror doc (verified), so a
// serializer can only ever reproduce ONE of them — reproducing both
// simultaneously is impossible, not merely unimplemented. See the long
// comment above `ToggleSummary` in nodes/toggle.ts for the full reasoning
// and an explicit "do not fix this back" note. Do not "fix" this test by
// gluing the tags back together.
test("a toggle list survives a markdown round trip", () => {
  expect(
    checkFidelity(
      "<details>\n<summary>Show details</summary>\n\nHidden body.\n\n</details>\n",
    ),
  ).toBe(true);
});

test("a toggle inserted via setToggle() and saved immediately round trips", () => {
  // The empty-body case a user hits by opening the slash menu and saving
  // before typing anything: an empty body must not be lost.
  expect(
    checkFidelity("<details>\n<summary>Toggle</summary>\n\n</details>\n"),
  ).toBe(true);
});

test("a toggle with a multi-paragraph body round-trips", () => {
  expect(
    checkFidelity(
      "<details>\n<summary>Show details</summary>\n\nFirst paragraph.\n\nSecond paragraph.\n\n</details>\n",
    ),
  ).toBe(true);
});

test("a toggle body containing bold/italic marks round-trips", () => {
  // The body is ordinary markdown (not on markdown-it's raw HTML block
  // line), so "**bold**"/"*italic*" parse as real marks and serialize back
  // through the normal markdown mark machinery — same path any other
  // paragraph's marks take.
  expect(
    checkFidelity(
      "<details>\n<summary>Show details</summary>\n\nSome **bold** and *italic* text.\n\n</details>\n",
    ),
  ).toBe(true);
});

test("a toggle summary containing an inline mark round-trips", () => {
  // Unlike the body, the summary sits on markdown-it's raw HTML block line,
  // so a real mark can only get in via an actual HTML tag in the source
  // (markdown "**" syntax typed there stays literal text — verified
  // separately, see nodes/toggle.ts's comment above ToggleSummary). This
  // pins that <strong> case specifically because it was the one that broke
  // under an earlier, since-reverted design (routing the summary through
  // the mark-aware inline serializer): that design wrote a real <strong>
  // mark back out as markdown "**", which is literal text on the next
  // parse, then gets backslash-escaped, and diverges further with every
  // subsequent save. Leaving the summary on the generic raw-HTML fallback
  // (see nodes/toggle.ts) avoids the mismatch entirely.
  expect(
    checkFidelity(
      "<details>\n<summary>Show <strong>bold</strong> details</summary>\n\nBody.\n\n</details>\n",
    ),
  ).toBe(true);
});

test("a sized image survives a markdown round trip", () => {
  expect(checkFidelity("![Architecture|420](assets/arch.png)\n")).toBe(true);
});

test("an unsized image is byte-for-byte unchanged", () => {
  // The whole point of putting the width in the alt slot: a note with no
  // resized image must serialize exactly as it does today.
  const md = "![Architecture](assets/arch.png)\n";
  expect(fidelityRoundTrip(md).trim()).toBe(md.trim());
});

// checkFidelity only runs at note LOAD, never on the save path (toMarkdown),
// so an editing-session insert (kb:insertImage -> commands.setImage) that
// produces a src containing markdown-significant characters is never
// re-checked. tiptap-markdown's stock image serializer backslash-escapes
// parens in the destination and quotes in the title
// (prosemirror-markdown's `image` node spec); KBImage's custom serializer
// (needed to carry the width) must preserve that escaping exactly, or a
// resized/inserted image whose path legitimately contains a paren
// serializes to markdown that mis-parses on the very next load.
test("an image src containing parens survives the save path", () => {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: "<p></p>",
  });
  editor.commands.setImage({ src: "assets/img(1).png", alt: "shot" });
  const out = toMarkdown(editor);
  editor.destroy();
  expect(out).toContain("assets/img\\(1\\).png");
  expect(checkFidelity(out)).toBe(true);
});
