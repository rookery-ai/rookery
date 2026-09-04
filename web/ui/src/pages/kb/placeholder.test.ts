import { readFileSync } from "node:fs";
import { Editor } from "@tiptap/core";
import { buildExtensions } from "./editor";

function makeEditor(content: string) {
  return new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content,
  });
}

function paragraphs(editor: Editor) {
  return Array.from(editor.view.dom.querySelectorAll("p"));
}

// The reported bug: the hint only ever appeared on the first line of an empty
// note, because editor.css matched `p.is-editor-empty:first-child`. TipTap adds
// `is-editor-empty` only when the WHOLE document is empty, so on a note with
// any content at all there was nothing to match and clicking a blank line
// showed nothing.
test("a blank line below existing content carries the hint", () => {
  const editor = makeEditor("<p>Hello</p><p></p>");
  editor.commands.focus("end");

  const [first, second] = paragraphs(editor);
  expect(second.classList.contains("is-empty")).toBe(true);
  expect(second.getAttribute("data-placeholder")).toBe("Press / for commands…");
  // The line the caret is NOT on must stay clean, or every blank line in a
  // long note would render the hint at once.
  expect(first.classList.contains("is-empty")).toBe(false);

  editor.destroy();
});

// The class and the attribute must land on the SAME node: the CSS is
// `content: attr(data-placeholder)`, so a split would render an empty ::before.
test("the class and the placeholder attribute decorate one node", () => {
  const editor = makeEditor("<p></p>");
  editor.commands.focus("end");

  const hinted = paragraphs(editor).filter((p) => p.classList.contains("is-empty"));
  expect(hinted).toHaveLength(1);
  expect(hinted[0].getAttribute("data-placeholder")).toBeTruthy();

  editor.destroy();
});

test("a line with text on it carries no hint", () => {
  const editor = makeEditor("<p>Hello</p>");
  editor.commands.focus("end");

  expect(paragraphs(editor)[0].classList.contains("is-empty")).toBe(false);

  editor.destroy();
});

// Nested empty paragraphs get nothing, and that is the documented default
// rather than an oversight — the plugin's showOnlyCurrent/includeChildren
// defaults resolve node(1), the top-level block, and a callout wrapper is not
// a textblock. Pinned so that turning includeChildren on (which would also
// fire in every empty table cell and toggle summary) is a deliberate act.
test("an empty paragraph nested inside a callout carries no hint", () => {
  const editor = makeEditor("<p>Hello</p>\n\n> [!note] Title\n> \n");
  editor.commands.focus("end");

  const hinted = paragraphs(editor).filter((p) => p.classList.contains("is-empty"));
  expect(hinted).toHaveLength(0);

  editor.destroy();
});

// jsdom has no layout engine and does not compute ::before content, so nothing
// above can prove the words are actually PAINTED — only that the hooks the CSS
// keys on are in the right place. This asserts the other half: that the rule
// keys on `p.is-empty` and not on the `:first-child` form that caused the bug.
// A real browser is the only thing that could check the rendering itself.
test("the stylesheet targets any empty paragraph, not just the first", () => {
  // Read from the project root rather than import.meta.url: the jsdom
  // environment rewrites that to an http: URL and readFileSync rejects it.
  const css = readFileSync("src/pages/kb/editor.css", "utf8");
  expect(css).toContain("p.is-empty::before");
  expect(css).not.toContain("p.is-editor-empty:first-child::before");
});
