import { Node, mergeAttributes } from "@tiptap/core";

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbToggle: {
      setToggle: () => ReturnType;
    };
  }
}

// <details>/<summary> is the standard markdown-compatible collapsible. No
// markdown spec is declared on purpose: html:true passes block HTML through
// markdown-it untouched, and tiptap-markdown's HTML node fallback serializes
// any node without a spec back to its HTML form. So this round-trips for free.
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
});
