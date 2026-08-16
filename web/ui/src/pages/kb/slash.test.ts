import { Editor } from "@tiptap/core";
import { buildExtensions } from "./editor";
import { slashItems, filterSlashItems } from "./slashItems";
import { ICONS } from "./SlashMenu";

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
  test("all twenty-one items are declared", () => {
    expect(slashItems).toHaveLength(21);
  });

  test("Image, File attachment and Table dispatch their window events", () => {
    const editor = headlessEditor();
    const events: string[] = [];
    const onImg = () => events.push("kb:insertImage");
    const onAtt = () => events.push("kb:insertAttachment");
    const onTable = () => events.push("kb:insertTable");
    window.addEventListener("kb:insertImage", onImg);
    window.addEventListener("kb:insertAttachment", onAtt);
    window.addEventListener("kb:insertTable", onTable);
    findItem("Image").run(editor);
    findItem("File attachment").run(editor);
    findItem("Table").run(editor);
    window.removeEventListener("kb:insertImage", onImg);
    window.removeEventListener("kb:insertAttachment", onAtt);
    window.removeEventListener("kb:insertTable", onTable);
    editor.destroy();
    expect(events).toEqual(["kb:insertImage", "kb:insertAttachment", "kb:insertTable"]);
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

  test.each(["note", "tip", "info", "warning", "danger"])(
    "Callout: %s wraps the selection in a callout node of that kind",
    (kind) => {
      const editor = headlessEditor();
      findItem(`Callout: ${kind}`).run(editor);
      expect(editor.isActive("callout", { kind })).toBe(true);
      editor.destroy();
    },
  );

  test("Toggle list inserts a toggle with a summary and a bulleted body", () => {
    const editor = headlessEditor();
    findItem("Toggle list").run(editor);
    expect(editor.isActive("toggle")).toBe(true);
    const json = JSON.stringify(editor.getJSON());
    expect(json).toContain('"type":"toggle"');
    expect(json).toContain('"type":"toggleSummary"');
    // A toggle is nearly always used to hide a list, so the body starts as one
    // rather than as a bare paragraph the user has to convert by hand.
    expect(json).toContain('"type":"bulletList"');
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

  // Table used to insert a fixed 3x3 here. It now opens the size picker (see
  // the window-event test above), and the shape it produces is covered by
  // tableEditing.test.ts, which additionally proves every size the picker can
  // offer round-trips through the markdown serializer.
});

test("every slash item has an icon", () => {
  // A missing entry renders the row with no icon rather than failing, so
  // nothing else would catch it.
  const missing = slashItems.filter((i) => !ICONS[i.title]).map((i) => i.title);
  expect(missing).toEqual([]);
});
