import { Node, mergeAttributes } from "@tiptap/core";

// Width of the disclosure arrow's clickable zone, measured from the summary's
// left edge. Must match the `summary::before` box in editor.css: too wide and
// clicking the first character of the title collapses the toggle instead of
// placing a caret.
const ARROW_HIT_PX = 22;

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
// the way out, with <details> and <summary> on SEPARATE lines (as above).
// This is a DELIBERATE, load-bearing choice, not a stylistic default:
// <details> and <summary> glued onto one line ("<details><summary>...")
// parses to the exact same doc and was tried first — it is NOT compatible
// with the separate-line form as a simultaneous fixed point. A serializer
// can only ever reproduce ONE canonical spelling for a given doc (parsing
// throws away whether the source had them glued or on separate lines), so
// the two forms are mutually exclusive canonical choices. Separate lines
// won because it's GitHub's own documented convention and the form that
// dominates real-world markdown — a pasted README snippet or a
// vault-writing agent is far more likely to produce this than the glued
// form. Do NOT "fix" this back to gluing them: that would just move the
// read-only-until-first-save gap onto the more common input instead of the
// rarer one. (A prior revision of this file glued them and pinned the
// glued form in the test — reverted; see git history / task report if the
// full reasoning is needed.)
//
// The BODY is routed through the normal markdown state machinery
// (state.render / state.closeBlock), so nested marks (bold/italic/links)
// and multi-block bodies get correct markdown syntax and blank-line block
// separation — all for free from the shared serializer state, not
// hand-rolled here (verified: "**bold**" and multi-paragraph bodies both
// round-trip).
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
  // The disclosure arrow's clickable zone, measured from the summary's left
  // edge, and kept in step with the `summary::before` box in editor.css.
  //
  // Only this zone toggles. Making the whole summary toggle would mean you
  // could not click into the title to edit it without collapsing the thing
  // you were trying to read.
  addNodeView() {
    return ({ editor }) => {
      const dom = document.createElement("details");
      // Editor-only state, held on the DOM and nowhere else.
      //
      // `open` is deliberately NOT a node attribute and NOT in renderHTML:
      // tiptap-markdown's HTML fallback writes every rendered attribute back
      // into the saved markdown, so it leaked as `<details open="">` and
      // force-expanded the toggle forever. Keeping it here means the
      // serializer never sees it, fidelity is untouched, and toggling
      // dispatches no transaction (so it cannot dirty the note or trigger an
      // autosave for state the format cannot store).
      //
      // The trade-off, stated plainly: open/closed is not persisted. Markdown
      // has nowhere to put it, so a toggle opens expanded and collapses only
      // for the current session.
      dom.open = true;

      dom.addEventListener("click", (event) => {
        // A read-only note (failed checkFidelity, not yet opted into editing)
        // still mounts NodeViews — same guard kbImage's resize handle uses.
        if (!editor.isEditable) return;
        const target = event.target as HTMLElement | null;
        const summary = target?.closest?.("summary");
        if (!summary || !dom.contains(summary)) return;
        if (event.clientX - summary.getBoundingClientRect().left > ARROW_HIT_PX) return;
        event.preventDefault();
        dom.open = !dom.open;
      });

      return {
        dom,
        // contentDOM is the <details> itself so <summary> stays a DIRECT
        // child. The markdown serializer stringifies the summary's own DOM,
        // so any wrapper element invented here would land in the saved note
        // and break every toggle fidelity test.
        contentDOM: dom,
        // Keep this DOM across updates instead of letting ProseMirror destroy
        // and rebuild the NodeView, which would reset `open` to true. Content
        // edits are applied through contentDOM by ProseMirror itself, so there
        // is nothing to re-render here; without this, typing in the summary of
        // a COLLAPSED toggle would pop it open on each keystroke.
        update: (node: { type: { name: string } }) => node.type.name === "toggle",
        // Without this, ProseMirror's DOMObserver sees the `open` attribute
        // change, finds a contentDOM (so the base ignoreMutation returns
        // false), marks the node dirty and re-renders it from doc state —
        // wiping `open` on every single click. This is the reason a plain
        // <details> in the editor never collapsed.
        // Typed structurally rather than as MutationRecord: ProseMirror's
        // ViewMutationRecord is a union that also carries a synthetic
        // {type:"selection"} record, which MutationRecord does not describe.
        ignoreMutation: (mutation: { type: string }) => mutation.type === "attributes",
      };
    };
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
              // A bulleted body, not a bare paragraph: a toggle is nearly
              // always used to hide a list, and every candidate bullet form
              // was verified to round-trip through checkFidelity before this
              // changed (a body that is not a fixed point would make the
              // first save open the note read-only).
              {
                type: "bulletList",
                content: [{ type: "listItem", content: [{ type: "paragraph" }] }],
              },
            ],
          }),
    };
  },
  addStorage() {
    return {
      markdown: {
        serialize(state: any, node: any) {
          // "<details>" and "<summary>" on separate lines — see the
          // "DELIBERATE, load-bearing choice" comment above ToggleSummary
          // for why this, and not gluing them onto one line, is correct.
          state.write("<details>\n");
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
