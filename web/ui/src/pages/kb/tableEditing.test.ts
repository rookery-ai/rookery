import { Editor } from "@tiptap/core";
import { buildExtensions, checkFidelity, toMarkdown } from "./editor";

// The binding constraint on every table operation: the result must serialize to
// the canonical markdown form. pipeSafeTable.ts and generatorFidelity.test.ts
// exist because a round-trip mismatch makes checkFidelity open the note
// READ-ONLY — so a row insert that produced a subtly different document would
// lock the user out of their own note on the next load, with the table looking
// perfectly fine on screen.
//
// TipTap already implements addRowAfter/deleteColumn/etc.; what was missing was
// any way to reach them. These tests cover the half that can actually break.

function editorWith(markdown: string) {
  return new Editor({
    extensions: buildExtensions(),
    content: markdown,
    // markdown-it parsing is what tiptap-markdown installs; content given as a
    // string goes through it, which is the same path a loaded note takes.
  });
}

const TABLE = `| Name | Role |
| --- | --- |
| Ada | Engineer |
| Grace | Admiral |
`;

test("a plain table round-trips before anything touches it", () => {
  expect(checkFidelity(TABLE)).toBe(true);
});

test("inserting a row keeps the note WYSIWYG-safe", () => {
  const editor = editorWith(TABLE);
  // Put the caret inside the table the way hovering a cell does.
  editor.commands.setTextSelection(5);
  editor.commands.addRowAfter();

  const out = toMarkdown(editor);
  expect(out).toContain("| Name | Role |");
  // Header + delimiter + three data rows. The delimiter line must stay exactly
  // one: a second one is not a table any markdown parser reads back.
  const pipeRows = out.split("\n").filter((l) => l.trim().startsWith("|"));
  expect(pipeRows.length).toBe(5);
  // Each cell must carry at least one dash, or the pattern also matches the
  // newly inserted blank row (`|  |  |`) and stops proving anything.
  const isDelimiter = (l: string) => /^\|(\s*:?-+:?\s*\|)+$/.test(l.trim());
  expect(isDelimiter(pipeRows[1])).toBe(true);
  expect(pipeRows.filter(isDelimiter).length).toBe(1);
  expect(checkFidelity(out)).toBe(true);
  editor.destroy();
});

test("inserting a column keeps the note WYSIWYG-safe", () => {
  const editor = editorWith(TABLE);
  editor.commands.setTextSelection(5);
  editor.commands.addColumnAfter();

  const out = toMarkdown(editor);
  expect(checkFidelity(out)).toBe(true);
  // Every row must gain the cell, header and delimiter included — a column
  // added to some rows only is not a table any markdown parser will read back.
  const pipeRows = out.split("\n").filter((l) => l.trim().startsWith("|"));
  const widths = new Set(pipeRows.map((l) => l.split("|").length));
  expect(widths.size).toBe(1);
  editor.destroy();
});

test("deleting a row keeps the note WYSIWYG-safe", () => {
  const editor = editorWith(TABLE);
  // Into the second data row.
  editor.commands.setTextSelection(30);
  editor.commands.deleteRow();

  const out = toMarkdown(editor);
  expect(checkFidelity(out)).toBe(true);
  editor.destroy();
});

test("deleting a column keeps the note WYSIWYG-safe", () => {
  const editor = editorWith(TABLE);
  editor.commands.setTextSelection(5);
  editor.commands.deleteColumn();

  const out = toMarkdown(editor);
  expect(checkFidelity(out)).toBe(true);
  const pipeRows = out.split("\n").filter((l) => l.trim().startsWith("|"));
  const widths = new Set(pipeRows.map((l) => l.split("|").length));
  expect(widths.size).toBe(1);
  editor.destroy();
});

// The picker can insert any size up to 8x8. A shape that serializes badly at one
// size and well at another is exactly the kind of thing a single 3x3 test misses.
test("every size the picker can insert round-trips", () => {
  for (const rows of [1, 2, 5, 8]) {
    for (const cols of [1, 2, 5, 8]) {
      const editor = new Editor({ extensions: buildExtensions(), content: "" });
      editor.commands.insertTable({ rows, cols, withHeaderRow: true });
      const out = toMarkdown(editor);
      expect(checkFidelity(out), `${cols}x${rows} table`).toBe(true);
      editor.destroy();
    }
  }
});

// A pipe inside a cell is what pipeSafeTable is for. Editing such a table must
// not undo that escaping.
test("a pipe-bearing cell survives a row insert", () => {
  const withPipe = `| Expression | Meaning |
| --- | --- |
| a \\| b | either |
`;
  expect(checkFidelity(withPipe)).toBe(true);

  const editor = editorWith(withPipe);
  editor.commands.setTextSelection(5);
  editor.commands.addRowAfter();
  const out = toMarkdown(editor);

  expect(out).toContain("a \\| b");
  expect(checkFidelity(out)).toBe(true);
  editor.destroy();
});
