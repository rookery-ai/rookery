import { Editor } from "@tiptap/core";
import { buildExtensions, toMarkdown, checkFidelity } from "./editor";
import { MIN_COLUMNS, MAX_COLUMNS } from "./nodes/columns";
import { filterSlashItems } from "./slashItems";

function makeEditor(content = "") {
  return new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content,
  });
}

function md(editor: Editor) {
  return toMarkdown(editor).trim();
}

test("insertColumns inserts a block with that many empty cells", () => {
  const editor = makeEditor("");
  editor.commands.insertColumns(3);
  const block = editor.getJSON().content?.find((n) => n.type === "kbColumns");
  expect(block).toBeTruthy();
  expect(block!.attrs?.cols).toBe(3);
  expect(block!.content).toHaveLength(3);
  editor.destroy();
});

// The count is the only thing configurable about the node, so a value outside
// the supported range must not reach the CSS — there is no grid rule for it and
// the block would silently render as a single stacked column.
test("the column count is clamped to the supported range", () => {
  for (const [asked, want] of [
    [1, MIN_COLUMNS],
    [0, MIN_COLUMNS],
    [-3, MIN_COLUMNS],
    [9, MAX_COLUMNS],
    [2, 2],
    [4, 4],
  ] as const) {
    const editor = makeEditor("");
    editor.commands.insertColumns(asked);
    const block = editor.getJSON().content?.find((n) => n.type === "kbColumns");
    expect(block!.attrs?.cols).toBe(want);
    editor.destroy();
  }
});

// A hand-edited or agent-written note can carry anything here.
test("a garbage data-cols parses to the minimum rather than NaN", () => {
  const editor = makeEditor('<div data-cols="banana">\n\nA\n\nB\n\n</div>');
  expect(md(editor)).toBe(`<div data-cols="${MIN_COLUMNS}">\n\nA\n\nB\n\n</div>`);
  editor.destroy();
});

test("setColumns updates an existing block rather than nesting another", () => {
  const editor = makeEditor('<div data-cols="2">\n\nA\n\nB\n\n</div>');
  editor.commands.selectAll();
  editor.commands.setColumns(3);
  const out = md(editor);
  expect(out).toBe('<div data-cols="3">\n\nA\n\nB\n\n</div>');
  expect(out.match(/<div/g)).toHaveLength(1);
  editor.destroy();
});

test("clearColumns lifts the cells back out as ordinary blocks", () => {
  const editor = makeEditor('<div data-cols="2">\n\nA\n\nB\n\n</div>');
  editor.commands.selectAll();
  editor.commands.clearColumns();
  expect(md(editor)).toBe("A\n\nB");
  editor.destroy();
});

// The caret sits inside a cell and nothing is selected — the ordinary way
// someone reaches for "remove this layout". nodesBetween never visits the
// ancestor over an empty selection, so this exercises the depth-walk fallback.
test("clearColumns works from a caret inside a cell, with nothing selected", () => {
  const editor = makeEditor('<div data-cols="2">\n\nAlpha\n\nBeta\n\n</div>');
  editor.commands.setTextSelection(3);
  expect(editor.commands.clearColumns()).toBe(true);
  expect(md(editor)).toBe("Alpha\n\nBeta");
  editor.destroy();
});

test("clearColumns is a no-op outside a columns block", () => {
  const editor = makeEditor("Just prose");
  editor.commands.selectAll();
  expect(editor.commands.clearColumns()).toBe(false);
  editor.destroy();
});

// A block that looks correct on screen but serializes to a non-canonical form
// opens READ-ONLY on the next load — the failure this editor's fidelity
// contract exists to prevent, and the one a layout feature is most likely to
// trip.
test("every column count the slash menu can insert round-trips", () => {
  for (let cols = MIN_COLUMNS; cols <= MAX_COLUMNS; cols++) {
    const editor = makeEditor("");
    editor.commands.insertColumns(cols);
    editor.commands.insertContentAt(2, "cell one");
    const out = md(editor) + "\n";
    expect(checkFidelity(out)).toBe(true);
    editor.destroy();
  }
});

// The parse rule keys on data-cols specifically. A plain <div> in a vault note
// must not become a layout nobody asked for.
test("a div with no data-cols is not claimed as a columns block", () => {
  const editor = makeEditor('<div class="x"><p>Untouched</p></div>');
  expect(editor.getJSON().content?.some((n) => n.type === "kbColumns")).toBeFalsy();
  editor.destroy();
});

// Cells are ordinary markdown, which is the entire reason the wrapper is a
// blank-line-separated <div> and not a nested structure.
test("marks inside a cell stay real markup", () => {
  const editor = makeEditor('<div data-cols="2">\n\nLeft **bold**\n\nRight\n\n</div>');
  expect(JSON.stringify(editor.getJSON())).toContain('"type":"bold"');
  expect(editor.getText()).not.toContain("**");
  editor.destroy();
});

test("the slash menu offers one entry per supported column count", () => {
  const byColumns = filterSlashItems("columns").map((i) => i.title);
  for (let cols = MIN_COLUMNS; cols <= MAX_COLUMNS; cols++) {
    expect(byColumns).toContain(`${cols} columns`);
  }
  // "grid" is what someone reaches for when they do not know the feature's
  // name, so it has to match too.
  expect(filterSlashItems("grid").map((i) => i.title)).toContain("2 columns");
});
