import { Editor } from "@tiptap/core";
import { buildExtensions } from "./editor";
import { slashItems, filterSlashItems } from "./slashItems";

function headlessEditor(): Editor {
  return new Editor({
    extensions: buildExtensions(),
    element: document.createElement("div"),
    content: "<p></p>",
  });
}

function findItem(title: string) {
  const item = slashItems.find((i) => i.title === title);
  if (!item) throw new Error(`slash item not found: ${title}`);
  return item;
}

describe("filterSlashItems", () => {
  test("empty query returns all items in declared order", () => {
    expect(filterSlashItems("")).toEqual(slashItems);
  });

  test("matches by keyword substring, not just title", () => {
    const results = filterSlashItems("tod");
    expect(results[0].title).toBe("To-do list");
  });

  test("matches by title substring, case-insensitive", () => {
    const results = filterSlashItems("HEADING 2");
    expect(results).toHaveLength(1);
    expect(results[0].title).toBe("Heading 2");
  });

  test("no match returns an empty array", () => {
    expect(filterSlashItems("zzzznomatch")).toEqual([]);
  });
});

describe("slashItems run() against a headless editor", () => {
  test("all ten items are declared", () => {
    expect(slashItems).toHaveLength(10);
  });

  test("Heading 1 sets an active level-1 heading", () => {
    const editor = headlessEditor();
    findItem("Heading 1").run(editor);
    expect(editor.isActive("heading", { level: 1 })).toBe(true);
    editor.destroy();
  });

  test("Heading 2 sets an active level-2 heading", () => {
    const editor = headlessEditor();
    findItem("Heading 2").run(editor);
    expect(editor.isActive("heading", { level: 2 })).toBe(true);
    editor.destroy();
  });

  test("Heading 3 sets an active level-3 heading", () => {
    const editor = headlessEditor();
    findItem("Heading 3").run(editor);
    expect(editor.isActive("heading", { level: 3 })).toBe(true);
    editor.destroy();
  });

  test("Bullet list activates bulletList", () => {
    const editor = headlessEditor();
    findItem("Bullet list").run(editor);
    expect(editor.isActive("bulletList")).toBe(true);
    editor.destroy();
  });

  test("Numbered list activates orderedList", () => {
    const editor = headlessEditor();
    findItem("Numbered list").run(editor);
    expect(editor.isActive("orderedList")).toBe(true);
    editor.destroy();
  });

  test("To-do list activates taskList", () => {
    const editor = headlessEditor();
    findItem("To-do list").run(editor);
    expect(editor.isActive("taskList")).toBe(true);
    editor.destroy();
  });

  test("Quote activates blockquote", () => {
    const editor = headlessEditor();
    findItem("Quote").run(editor);
    expect(editor.isActive("blockquote")).toBe(true);
    editor.destroy();
  });

  test("Code block activates codeBlock", () => {
    const editor = headlessEditor();
    findItem("Code block").run(editor);
    expect(editor.isActive("codeBlock")).toBe(true);
    editor.destroy();
  });

  test("Divider inserts a horizontalRule node", () => {
    const editor = headlessEditor();
    findItem("Divider").run(editor);
    const json = JSON.stringify(editor.getJSON());
    expect(json).toContain("horizontalRule");
    editor.destroy();
  });

  test("Table inserts a 3x3 table node with a header row", () => {
    const editor = headlessEditor();
    findItem("Table").run(editor);
    // editor.getJSON()'s real return type is a recursive NodeType/TextType
    // union keyed off the editor's exact registered schema, which TS can't
    // narrow through plain `.content` chaining below. This is a doc-shape
    // assertion, not a schema-typed consumer, so a loose structural view is
    // the right tool here.
    type SimpleNode = { type?: string; content?: SimpleNode[] };
    const json = editor.getJSON() as unknown as SimpleNode;
    expect(JSON.stringify(json)).toContain('"type":"table"');

    const table = json.content?.[0];
    expect(table?.type).toBe("table");
    const rows = table?.content ?? [];
    // 3 rows
    expect(rows).toHaveLength(3);
    // each row has exactly 3 cells (column count)
    for (const row of rows) {
      expect(row.content ?? []).toHaveLength(3);
    }
    // header row present: first row's cells are tableHeader, the rest tableCell
    for (const cell of rows[0].content ?? []) {
      expect(cell.type).toBe("tableHeader");
    }
    for (const row of rows.slice(1)) {
      for (const cell of row.content ?? []) {
        expect(cell.type).toBe("tableCell");
      }
    }
    editor.destroy();
  });
});
