import { Node, mergeAttributes } from "@tiptap/core";

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbToggle: {
      setToggle: () => ReturnType;
    };
  }
}

// <details>/<summary> is the standard markdown-compatible collapsible.
//
// A custom `markdown.serialize` IS needed on Toggle (unlike most nodes in
// this file) — the generic HTML-node fallback (relying on no spec at all)
// does NOT round-trip the idiomatic, blank-line-body form that a pasted
// README snippet or an agent writing into the vault will actually produce:
//
//   <details>
//   <summary>Show details</summary>
//
//   Body.
//
//   </details>
//
// markdown-it (html:true) parses this deterministically — <details> and
// <summary> are both CommonMark "type 6" HTML block tags, so the opening
// tag(s) become a raw HTML block, the blank-line-separated body becomes
// ordinary markdown blocks (real ProseMirror paragraph/etc. nodes, not
// text glued to the tag), and the closing tag is its own raw HTML block.
// Parsing was always correct (verified: the doc always comes out as
// toggle > [toggleSummary, block+], never losing structure). It's only
// serialization that needs help: the generic fallback renders the node's
// literal DOM subtree as one HTML string (real <p> tags with no blank
// line, glued straight to </summary>), which is a DIFFERENT — though
// visually equivalent — string than what a human or agent would write.
// That mismatched the fidelity contract's byte-for-byte comparison.
//
// The serializer below reconstructs the idiomatic blank-line-body form on
// the way out. The BODY is routed through the normal markdown state
// machinery (state.render / state.closeBlock), so nested marks
// (bold/italic/links) and multi-block bodies get correct markdown syntax
// and blank-line block separation — all for free from the shared
// serializer state, not hand-rolled here (verified: "**bold**" and
// multi-paragraph bodies both round-trip).
//
// The SUMMARY is deliberately left with NO markdown spec of its own, so it
// keeps using the generic HTML-node fallback (raw DOM stringification,
// e.g. a real <strong> tag stays a literal <strong> tag). That was a
// deliberate choice, not an oversight: since <summary> sits on markdown-it's
// raw HTML block line, markdown-it never applies inline-markdown parsing to
// its contents, so a REAL mark can only get into the summary via an actual
// HTML tag (<strong>, not "**") in the source. Routing the summary through
// the normal mark-aware inline serializer (which writes marks as "**"
// markdown syntax) was tried first and produces a mismatched, PROGRESSIVELY
// DIVERGING round trip: "**bold**" written back is literal text on the next
// parse (never re-interpreted as emphasis, same raw-HTML-line reason), which
// then gets backslash-escaped on the pass after that, and escapes further
// with every subsequent save — verified out to a third pass
// ("\\*\\*bold\\*\\*" → "\\\\\\*\\\\\\*bold\\\\\\*\\\\\\*"). Leaving the
// summary as raw HTML sidesteps the mismatch entirely: whatever HTML the
// summary contained comes back out byte-identical, and literal
// non-mark text (including a literal "**bold**" typed as plain text) is
// never escaped since it never goes through state.text().
export const ToggleSummary = Node.create({
  name: "toggleSummary",
  content: "inline*",
  defining: true,
  parseHTML() {
    return [{ tag: "summary" }];
  },
  renderHTML({ HTMLAttributes }) {
    return ["summary", mergeAttributes(HTMLAttributes), 0];
  },
});

export const Toggle = Node.create({
  name: "toggle",
  group: "block",
  // The summary is required and comes first; everything after it is the body.
  content: "toggleSummary block+",
  defining: true,
  parseHTML() {
    return [{ tag: "details" }];
  },
  renderHTML({ HTMLAttributes }) {
    // No `open` attribute here: tiptap-markdown's HTML fallback serializer
    // writes every rendered attribute back into the markdown, so `open`
    // leaked verbatim into the saved note on every round trip (confirmed:
    // `<details open="">` came back out unchanged) — meaning a saved toggle
    // would be permanently force-expanded, defeating the whole point of a
    // collapsible. The body stays visible while editing via a CSS rule
    // instead (see editor.css).
    return ["details", mergeAttributes(HTMLAttributes), 0];
  },
  addCommands() {
    return {
      setToggle:
        () =>
        ({ commands }) =>
          commands.insertContent({
            type: this.name,
            content: [
              { type: "toggleSummary", content: [{ type: "text", text: "Toggle" }] },
              { type: "paragraph" },
            ],
          }),
    };
  },
  addStorage() {
    return {
      markdown: {
        serialize(state: any, node: any) {
          // "<details>" is glued directly to "<summary>" (no newline), the
          // same form the brief itself pins as the fidelity test's input.
          state.write("<details>");
          // Dispatches to the generic HTML-node fallback (ToggleSummary has
          // no markdown spec of its own — see the comment above it) — writes
          // "<summary>...</summary>" as raw DOM HTML, then marks it closed.
          state.render(node.firstChild, node, 0);
          // The blank line before the body is produced by the NEXT write()
          // call (inside the first body block's own serializer) flushing
          // this pending close, exactly the way two ordinary sibling blocks
          // get separated — reuses the shared spacing logic rather than
          // hand-writing "\n\n", which would double up if the body were empty.
          node.forEach((child: any, _offset: number, index: number) => {
            if (index === 0) return; // skip the summary, already handled
            state.render(child, node, index);
          });
          // Same trick in reverse: the pending close from the last body
          // block is flushed by THIS write(), producing exactly one blank
          // line before the closing tag.
          state.write("</details>");
          state.closeBlock(node);
        },
        parse: {
          // handled by markdown-it + parseHTML above — nothing to add here.
        },
      },
    };
  },
});
