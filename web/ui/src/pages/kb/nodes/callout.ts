import { Node, mergeAttributes } from "@tiptap/core";

export const CALLOUT_KINDS = ["note", "tip", "info", "warning", "danger"] as const;
export type CalloutKind = (typeof CALLOUT_KINDS)[number];

function isKind(v: string): v is CalloutKind {
  return (CALLOUT_KINDS as readonly string[]).includes(v);
}

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbCallout: {
      setCallout: (kind: CalloutKind) => ReturnType;
    };
  }
}

export const Callout = Node.create({
  name: "callout",
  group: "block",
  content: "block+",
  defining: true,

  addAttributes() {
    return {
      kind: {
        default: "note",
        parseHTML: (el) => {
          const k = el.getAttribute("data-callout") || "note";
          return isKind(k) ? k : "note";
        },
        renderHTML: (attrs) => ({ "data-callout": attrs.kind }),
      },
    };
  },

  parseHTML() {
    return [{ tag: "div[data-callout]" }];
  },

  renderHTML({ HTMLAttributes }) {
    return ["div", mergeAttributes(HTMLAttributes, { class: "kb-callout" }), 0];
  },

  addCommands() {
    return {
      setCallout:
        (kind: CalloutKind) =>
        ({ commands }) =>
          commands.wrapIn(this.name, { kind }),
    };
  },

  addStorage() {
    return {
      markdown: {
        // Obsidian callout syntax: a blockquote whose first line is the kind
        // marker. wrapBlock applies the "> " prefix to everything written
        // inside it, so the marker and the body both get quoted.
        serialize(state: any, node: any) {
          state.wrapBlock("> ", null, node, () => {
            state.write(`[!${node.attrs.kind}]`);
            state.ensureNewLine();
            state.renderContent(node);
          });
        },
        parse: {
          // markdown-it renders "> [!note]\n> body" as a plain blockquote
          // whose first paragraph opens with the literal marker. Rewriting the
          // DOM afterwards is far simpler and more robust than a markdown-it
          // block rule, and updateDOM is a first-class hook (see
          // tiptap-markdown parse/MarkdownParser.js).
          updateDOM(element: HTMLElement) {
            element.querySelectorAll("blockquote").forEach((bq) => {
              const first = bq.firstElementChild;
              if (!first || first.tagName !== "P") return;
              const m = first.innerHTML.match(/^\[!([a-z]+)\]\s*(?:<br\s*\/?>|\n)?/i);
              if (!m) return;
              const kind = m[1].toLowerCase();
              if (!isKind(kind)) return;
              first.innerHTML = first.innerHTML.slice(m[0].length);
              // An empty first paragraph would round-trip as a stray blank
              // line inside the callout.
              if (!first.innerHTML.trim()) first.remove();
              const div = element.ownerDocument.createElement("div");
              div.setAttribute("data-callout", kind);
              while (bq.firstChild) div.appendChild(bq.firstChild);
              bq.replaceWith(div);
            });
          },
        },
      },
    };
  },
});
