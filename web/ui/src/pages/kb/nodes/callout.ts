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
      // Obsidian's own docs lead with "> [!note] Title" as the common form —
      // a marker followed by title text on the same line. Stored as a plain
      // data attribute (not editable content) rather than a child node/NodeView:
      // simplest thing that (a) round-trips through parseHTML/renderHTML and
      // (b) is displayable via CSS generated content (editor.css).
      title: {
        default: "",
        parseHTML: (el) => el.getAttribute("data-title") || "",
        renderHTML: (attrs) => (attrs.title ? { "data-title": attrs.title } : {}),
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
            if (node.attrs.title) {
              state.write(" ");
              // state.text escapes markdown-significant characters (the same
              // path prose text goes through), so a title containing e.g. "*"
              // doesn't get misread as emphasis on the next parse.
              state.text(node.attrs.title);
            }
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
              const markerMatch = first.innerHTML.match(/^\[!([a-z]+)\]/i);
              if (!markerMatch) return;
              const kind = markerMatch[1].toLowerCase();
              if (!isKind(kind)) return;
              const rest = first.innerHTML.slice(markerMatch[0].length);
              // Everything after the marker up to the first line break (a
              // literal "\n" — this editor runs with breaks:false, so a soft
              // break renders as a raw newline character, not <br> — or an
              // explicit <br> from a hard break) is the optional title, e.g.
              // "[!note] My Title\nBody." Obsidian's own docs lead with this
              // form. A temp element decodes any HTML entities in that
              // fragment into plain text (title is a plain attribute, not
              // marked-up content, so it can't keep inline formatting).
              const breakMatch = rest.match(/<br\s*\/?>|\n/i);
              const titleHTML = breakMatch ? rest.slice(0, breakMatch.index) : rest;
              const body = breakMatch ? rest.slice((breakMatch.index ?? 0) + breakMatch[0].length) : "";
              const titleHolder = element.ownerDocument.createElement("div");
              titleHolder.innerHTML = titleHTML;
              const title = (titleHolder.textContent || "").trim();
              first.innerHTML = body;
              // An empty first paragraph would round-trip as a stray blank
              // line inside the callout.
              if (!first.innerHTML.trim()) first.remove();
              const div = element.ownerDocument.createElement("div");
              div.setAttribute("data-callout", kind);
              if (title) div.setAttribute("data-title", title);
              while (bq.firstChild) div.appendChild(bq.firstChild);
              bq.replaceWith(div);
            });
          },
        },
      },
    };
  },
});
