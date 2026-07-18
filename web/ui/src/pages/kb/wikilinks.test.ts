import { Editor } from "@tiptap/core";
import { buildExtensions, toMarkdown, checkFidelity, fidelityRoundTrip } from "./editor";
import { WIKILINK_RE, splitAlias } from "./wikilinks";

describe("WIKILINK_RE", () => {
  test("matches a bare target", () => {
    const m = "type [[other-note]]".match(WIKILINK_RE);
    expect(m?.[1]).toBe("other-note");
  });

  test("matches a target with an alias", () => {
    const m = "[[notes/deep|Deep Note]]".match(WIKILINK_RE);
    expect(m?.[1]).toBe("notes/deep|Deep Note");
  });

  test("does not match unbalanced brackets", () => {
    expect("[[incomplete".match(WIKILINK_RE)).toBeNull();
  });

  test("does not match a single-bracket pair", () => {
    expect("[not a wikilink]".match(WIKILINK_RE)).toBeNull();
  });
});

describe("splitAlias", () => {
  test("a bare target has no alias", () => {
    expect(splitAlias("other-note")).toEqual({ target: "other-note", alias: null });
  });

  test("splits target|alias", () => {
    expect(splitAlias("notes/deep|Deep Note")).toEqual({ target: "notes/deep", alias: "Deep Note" });
  });

  test("a trailing empty alias falls back to null", () => {
    expect(splitAlias("x|")).toEqual({ target: "x", alias: null });
  });
});

function editorWith(md: string): Editor {
  return new Editor({ element: document.createElement("div"), extensions: buildExtensions(), content: md });
}

function findWikilinkNodes(editor: Editor) {
  const found: { target: string }[] = [];
  editor.state.doc.descendants((node) => {
    if (node.type.name === "wikilink") found.push({ target: node.attrs.target as string });
  });
  return found;
}

describe("wikilink node — parse + serialize round-trip", () => {
  test("[[target]] parses into a wikilink atom node with the target attr", () => {
    const editor = editorWith("See [[other-note]].\n");
    const nodes = findWikilinkNodes(editor);
    expect(nodes).toHaveLength(1);
    expect(nodes[0].target).toBe("other-note");
    editor.destroy();
  });

  test("a path-form target parses too", () => {
    const editor = editorWith("See [[notes/deep]].\n");
    const nodes = findWikilinkNodes(editor);
    expect(nodes).toHaveLength(1);
    expect(nodes[0].target).toBe("notes/deep");
    editor.destroy();
  });

  test("serializes back to literal [[target]] — NOT prosemirror-markdown's backslash-escaped form", () => {
    const editor = editorWith("See [[other-note]] and [[notes/deep]].\n");
    const out = toMarkdown(editor);
    expect(out).toContain("[[other-note]]");
    expect(out).toContain("[[notes/deep]]");
    expect(out).not.toContain("\\[\\[");
    editor.destroy();
  });

  test("a target|alias link round-trips losslessly (verbatim, not reconstructed)", () => {
    const md = "[[notes/deep|Deep Note]]\n";
    const editor = editorWith(md);
    const out = toMarkdown(editor);
    expect(out.trim()).toBe(md.trim());
    editor.destroy();
  });
});

describe("legacy escaped content (\\[\\[x\\]\\]) on disk", () => {
  // A note saved with literal backslash-escaped brackets — from an earlier
  // build of this editor, or hand-authored — is not this node's concern:
  // markdown-it's escape rule consumes "\[" as literal punctuation before
  // our inline rule ever sees a "[[" to match, so this text is parsed as
  // plain text, not a wikilink node.
  const ESCAPED = "See \\[\\[x\\]\\].\n";

  test("escaped brackets are parsed as plain text, not a wikilink node", () => {
    const editor = editorWith(ESCAPED);
    expect(findWikilinkNodes(editor)).toHaveLength(0);
    editor.destroy();
  });

  // The brief requires this to be verified, not assumed: does the escaped
  // form survive a WYSIWYG round-trip, or does it get silently corrupted
  // (either dropped to a bare "[[x]]", or promoted into a live wikilink)?
  // Measured (not guessed): prosemirror-markdown's text serializer
  // backslash-escapes literal "[" on the way OUT, symmetrically undoing the
  // markdown-it escape-rule unescape on the way IN — so the exact original
  // bytes come back. checkFidelity is TRUE for this content, meaning such a
  // note is eligible for WYSIWYG and a WYSIWYG save re-writes the identical
  // escaped text: safe, not a corruption case.
  test("round-trips byte-for-byte and is WYSIWYG-eligible (symmetric escape/unescape, not corruption)", () => {
    expect(checkFidelity(ESCAPED)).toBe(true);
    expect(fidelityRoundTrip(ESCAPED).trim()).toBe(ESCAPED.trim());
  });
});
