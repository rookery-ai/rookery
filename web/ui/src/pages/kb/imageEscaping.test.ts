import { Editor } from "@tiptap/core";
import { buildExtensions, toMarkdown } from "./editor";

// kbImage's serializer escapes parens in the src and quotes in the title, but
// not the BACKSLASH that does the escaping. That makes the escape scheme
// non-injective: a src whose own last character is a backslash serialises to
// `![pic](assets/a\)`, where the `\)` reads as an escaped paren, the link
// destination never terminates, and the IMAGE NODE IS LOST — the picture
// disappears from the note.
//
// The damage lands on SAVE, which is the worst place for it: checkFidelity is a
// load-time gate, so it cannot catch a note the editor is about to write. The
// file's own comment says exactly this about the escapes it does perform.

function serialize(attrs: Record<string, unknown>): string {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: {
      type: "doc",
      content: [{ type: "image", attrs: { alt: "pic", width: null, title: null, ...attrs } }],
    },
  });
  const md = toMarkdown(editor);
  editor.destroy();
  return md.trim();
}

function roundTrip(attrs: Record<string, unknown>): Record<string, unknown> | null {
  const editor = new Editor({
    element: document.createElement("div"),
    extensions: buildExtensions(),
    content: serialize(attrs),
  });
  const json = editor.getJSON();
  editor.destroy();
  return findImage(json);
}

function findImage(node: any): Record<string, unknown> | null {
  if (!node) return null;
  if (node.type === "image") return node.attrs ?? {};
  for (const child of node.content ?? []) {
    const hit = findImage(child);
    if (hit) return hit;
  }
  return null;
}

test.each([
  ["a trailing backslash", "assets/a\\"],
  ["a backslash before a paren", "assets/a\\(b.png"],
  ["a windows-style path", "assets\\sub\\a.png"],
])("an image whose src has %s is not destroyed by serialization", (_name, src) => {
  expect(roundTrip({ src })).not.toBeNull();
});

test("a trailing backslash does not swallow the destination terminator", () => {
  // The concrete corruption: unescaped, the output is `![pic](assets/a\)`,
  // which has no closing paren left and parses as prose.
  const md = serialize({ src: "assets/a\\" });
  expect(md.endsWith(")")).toBe(true);
  expect(md).not.toMatch(/[^\\]\\\)$/);
});

// A title is NOT run through markdown-it's link normalizer, so unlike a src it
// round-trips byte-for-byte once the backslash is escaped.
test.each([
  ["a trailing backslash", "cap\\"],
  ["a backslash before a quote", 'cap\\"quoted"'],
  ["a doubled backslash", "cap\\\\end"],
])("an image title with %s round-trips exactly", (_name, title) => {
  expect(roundTrip({ src: "assets/a.png", title })?.title).toBe(title);
});

// Recorded rather than fixed: markdown-it's normalizeLink percent-encodes a
// backslash in a link destination, so a backslash-bearing src comes back as
// %5C no matter how it was escaped on the way out. That is upstream of this
// serializer and outside its reach. What IS in reach — and what this change
// buys — is that the image survives at all instead of vanishing. A vault asset
// path is slash-separated (vault.Resolve calls ToSlash), so this only bites a
// filename that genuinely contains a backslash.
test("a backslash in a src is preserved as a percent-escape, not lost", () => {
  expect(roundTrip({ src: "assets\\sub\\a.png" })?.src).toBe("assets%5Csub%5Ca.png");
});

test("ordinary sources and titles are untouched", () => {
  expect(serialize({ src: "assets/a.png" })).toBe("![pic](assets/a.png)");
  expect(serialize({ src: "assets/a.png", title: "A caption" })).toBe(
    '![pic](assets/a.png "A caption")',
  );
  // Parens are still escaped — that behaviour is unchanged.
  expect(serialize({ src: "assets/a(1).png" })).toBe("![pic](assets/a\\(1\\).png)");
  expect(roundTrip({ src: "assets/a(1).png" })?.src).toBe("assets/a(1).png");
});

test("the width still rides in the alt slot", () => {
  expect(serialize({ src: "assets/a.png", width: 420 })).toBe("![pic|420](assets/a.png)");
});
