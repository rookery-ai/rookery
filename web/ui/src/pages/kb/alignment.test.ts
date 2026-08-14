import { Editor } from "@tiptap/core";
import { buildExtensions, toMarkdown, checkFidelity } from "./editor";

function makeEditor(content: string) {
  return new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content,
  });
}

function md(editor: Editor) {
  return toMarkdown(editor).trim();
}

test("setBlockAlign wraps the current block", () => {
  const editor = makeEditor("Hello **world**");
  editor.commands.selectAll();
  editor.commands.setBlockAlign("center");
  expect(md(editor)).toBe('<div align="center">\n\nHello **world**\n\n</div>');
  editor.destroy();
});

// Wrapping twice would serialize as two nested divs and read on screen as one,
// so the second call must change the attribute instead.
test("aligning an already-aligned block updates it rather than nesting", () => {
  const editor = makeEditor('<div align="center">\n\nHello\n\n</div>');
  editor.commands.selectAll();
  editor.commands.setBlockAlign("right");
  const out = md(editor);
  expect(out).toBe('<div align="right">\n\nHello\n\n</div>');
  expect(out.match(/<div/g)).toHaveLength(1);
  editor.destroy();
});

// Unaligned IS left, so the Left control lifts rather than wrapping in
// align="left" — an extra div that changes nothing visible.
test("clearBlockAlign lifts the wrapper back out", () => {
  const editor = makeEditor('<div align="center">\n\nHello\n\n</div>');
  editor.commands.selectAll();
  editor.commands.clearBlockAlign();
  expect(md(editor)).toBe("Hello");
  editor.destroy();
});

// The defect the columns work surfaced, fixed here too: commands.lift() lifts
// the block range around the SELECTION, so a wrapper holding two paragraphs
// would lose the first and keep the second aligned — a half-cleared block that
// looks like a bug and is one.
test("clearBlockAlign lifts EVERY block out, not just the caret's", () => {
  const editor = makeEditor('<div align="center">\n\nOne\n\nTwo\n\n</div>');
  editor.commands.selectAll();
  editor.commands.clearBlockAlign();
  expect(md(editor)).toBe("One\n\nTwo");
  editor.destroy();
});

// A caret in the block with nothing selected is the ordinary way someone
// reaches for "un-align this", and nodesBetween never visits the ancestor over
// an empty selection.
test("clearBlockAlign works from a bare caret inside the block", () => {
  const editor = makeEditor('<div align="right">\n\nAlpha\n\nBeta\n\n</div>');
  editor.commands.setTextSelection(3);
  expect(editor.commands.clearBlockAlign()).toBe(true);
  expect(md(editor)).toBe("Alpha\n\nBeta");
  editor.destroy();
});

test("clearBlockAlign is a no-op on an unaligned block", () => {
  const editor = makeEditor("Hello");
  editor.commands.selectAll();
  expect(editor.commands.clearBlockAlign()).toBe(false);
  expect(md(editor)).toBe("Hello");
  editor.destroy();
});

// Every operation must land on the canonical form, or the note opens read-only
// on its next load while still looking correct on screen — the failure mode
// this editor's whole fidelity contract exists to prevent.
test("every alignment the toolbar can produce round-trips", () => {
  for (const align of ["center", "right", "left"] as const) {
    const editor = makeEditor("Hello");
    editor.commands.selectAll();
    editor.commands.setBlockAlign(align);
    const out = md(editor) + "\n";
    expect(checkFidelity(out)).toBe(true);
    editor.destroy();
  }
});

// The image is half the point of the feature.
test("an image can be centred and survives the round trip", () => {
  const editor = makeEditor('<p><img src="assets/a.png" alt="a diagram"></p>');
  editor.commands.selectAll();
  editor.commands.setBlockAlign("center");
  const out = md(editor);
  expect(out).toBe('<div align="center">\n\n![a diagram](assets/a.png)\n\n</div>');
  expect(checkFidelity(out + "\n")).toBe(true);
  editor.destroy();
});

// Without the getAttrs guard, the div[style] rule would claim every styled div
// in the vault and wrap it in an alignment nobody asked for.
test("a div that is not aligned is not claimed as an alignment block", () => {
  const editor = makeEditor('<div class="x"><p>Untouched</p></div>');
  expect(editor.getJSON().content?.some((n) => n.type === "kbAlign")).toBeFalsy();
  editor.destroy();
});

test("a styled div IS claimed, and normalises to the canonical attribute", () => {
  const editor = makeEditor('<div style="text-align: right"><p>Signed</p></div>');
  expect(md(editor)).toBe('<div align="right">\n\nSigned\n\n</div>');
  editor.destroy();
});

// The reason the wrapper is a <div> with a blank-line body and not a styled
// <p>: markdown-it does not parse inline markdown inside a type-6 raw HTML
// block, so the <p> spelling would turn this bold into literal asterisks.
test("inline markdown inside an aligned block stays real markup", () => {
  const editor = makeEditor('<div align="center">\n\nHello **world**\n\n</div>');
  const json = JSON.stringify(editor.getJSON());
  expect(json).toContain('"type":"bold"');
  expect(editor.getText()).not.toContain("**");
  editor.destroy();
});
