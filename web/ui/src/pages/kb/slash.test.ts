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

  test("Table inserts a 3x3 table node", () => {
    const editor = headlessEditor();
    findItem("Table").run(editor);
    const json = editor.getJSON();
    const str = JSON.stringify(json);
    expect(str).toContain('"type":"table"');
    // 3 rows
    expect((json.content?.[0]?.content ?? [])).toHaveLength(3);
    editor.destroy();
  });
});
