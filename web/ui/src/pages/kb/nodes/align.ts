import { Node, mergeAttributes } from "@tiptap/core";
import { liftTarget } from "@tiptap/pm/transform";

export const ALIGNMENTS = ["left", "center", "right"] as const;
export type Alignment = (typeof ALIGNMENTS)[number];

function isAlignment(v: string | null): v is Alignment {
  return !!v && (ALIGNMENTS as readonly string[]).includes(v);
}

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbAlign: {
      setBlockAlign: (align: Alignment) => ReturnType;
      clearBlockAlign: () => ReturnType;
    };
  }
}

// Alignment is a BLOCK WRAPPER, not a paragraph attribute, and that is the
// whole design.
//
// Markdown has no alignment, so persisting it means emitting raw HTML — and the
// obvious spelling, `<p style="text-align:center">…</p>`, is a trap: markdown-it
// treats a `<p …>` line as a CommonMark "type 6" raw HTML block and does NOT
// parse inline markdown inside it, so aligning a paragraph would turn its
// `**bold**` into literal asterisks. (The toggle's `<summary>` hits exactly this
// and has to serialize its marks as HTML tags to survive; see nodes/toggle.ts.)
//
// `<div align="…">` with a BLANK-LINE-SEPARATED body sidesteps it entirely:
//
//   <div align="center">
//
//   Hello **world**
//
//   </div>
//
// markdown-it closes the type-6 block at the blank line, so the body is parsed
// as ordinary markdown — real paragraph/list/image nodes with real marks — and
// the closing tag is its own raw HTML block. It is also what real-world markdown
// actually contains: `<div align="center">` is the standard README centring
// idiom, so a pasted snippet or a vault-writing agent produces this form.
//
// As with the toggle, a serializer can reproduce only ONE canonical spelling
// (parsing throws away whether the source was glued or blank-line separated), so
// the two are mutually exclusive choices rather than a preference. The glued
// form `<div align="center">Hello</div>` still PARSES — it just normalises to
// this one on the first save, and the note opens read-only until then. That is
// not a regression: before this node existed, EVERY div form failed fidelity and
// lost its wrapper on save, alignment silently discarded.
//
// The `align` attribute is used rather than `style`, for two reasons. It is the
// idiom above, and it sidesteps prosemirror-model's `renderSpec` hazard, where
// an attrs key literally named `style` is assigned via `dom.style.cssText` and
// canonicalised through the CSSOM — the trap that forces marks/colors.ts and
// kbImage.ts to build their DOM by hand.
export const KBAlign = Node.create({
  name: "kbAlign",
  group: "block",
  content: "block+",
  defining: true,

  addAttributes() {
    return {
      align: {
        default: "center" as Alignment,
        parseHTML: (el) => {
          const attr = el.getAttribute("align");
          if (isAlignment(attr)) return attr;
          // A pasted `style="text-align: right"` parses too — it normalises to
          // the canonical `align` form on the first save.
          const styled = el.style?.textAlign?.trim() ?? "";
          return isAlignment(styled) ? styled : "center";
        },
        renderHTML: (attrs) => ({ align: attrs.align }),
      },
    };
  },

  parseHTML() {
    return [
      { tag: "div[align]" },
      {
        tag: "div[style]",
        // Only claim a styled div that is ACTUALLY aligned. Without this the
        // rule would swallow every `<div style="…">` in the vault and wrap it
        // in an alignment node it never asked for.
        getAttrs: (el) =>
          isAlignment((el as HTMLElement).style?.textAlign?.trim() ?? "") ? null : false,
      },
    ];
  },

  renderHTML({ HTMLAttributes }) {
    return ["div", mergeAttributes(HTMLAttributes, { class: "kb-align" }), 0];
  },

  addCommands() {
    return {
      setBlockAlign:
        (align: Alignment) =>
        ({ commands }) => {
          // Try the attribute update FIRST and fall back to wrapping. Wrapping
          // an already-wrapped block would serialize as two nested divs and
          // read on screen as one.
          //
          // Deliberately not gated on `editor.isActive(name)`: that test is
          // relative to the selection's own ancestry and reports false for an
          // AllSelection (Ctrl+A), whose $from sits at doc depth 0 — so
          // select-all-then-centre on an already-centred note would nest.
          // updateAttributes searches the selected RANGE and answers correctly
          // for both selection shapes, returning false when there is nothing
          // of this type to update.
          if (commands.updateAttributes(this.name, { align })) return true;
          return commands.wrapIn(this.name, { align });
        },
      // Lifts EVERY block out of the wrapper, not just the one the caret is in.
      //
      // `commands.lift(name)` is the obvious implementation and is wrong: it
      // lifts the block range around the SELECTION, so a wrapper holding two
      // paragraphs loses the first and keeps the second aligned — a half-cleared
      // block that looks like a bug and is one. The range is therefore built
      // explicitly over the whole node's content.
      //
      // The ancestor walk covers a bare caret: `nodesBetween` never visits the
      // ancestor over an empty selection, and a caret sitting in the block is
      // the ordinary way someone reaches for "un-align this".
      clearBlockAlign:
        () =>
        ({ state, tr, dispatch }) => {
          const type = state.schema.nodes[this.name];
          if (!type) return false;
          let found: { pos: number; size: number } | null = null;
          state.doc.nodesBetween(state.selection.from, state.selection.to, (node, pos) => {
            if (!found && node.type === type) found = { pos, size: node.content.size };
          });
          if (!found) {
            const $from = state.selection.$from;
            for (let d = $from.depth; d > 0; d--) {
              if ($from.node(d).type === type) {
                found = { pos: $from.before(d), size: $from.node(d).content.size };
                break;
              }
            }
          }
          if (!found) return false;
          const { pos, size } = found;
          const range = tr.doc.resolve(pos + 1).blockRange(tr.doc.resolve(pos + 1 + size));
          if (!range) return false;
          const target = liftTarget(range);
          if (target == null) return false;
          if (dispatch) tr.lift(range, target);
          return true;
        },
    };
  },

  addStorage() {
    return {
      markdown: {
        serialize(state: any, node: any) {
          // The blank lines are load-bearing, not formatting: without them
          // markdown-it keeps the body inside the raw HTML block and its inline
          // markdown stops being parsed.
          state.write(`<div align="${node.attrs.align}">\n\n`);
          node.forEach((child: any, _offset: number, index: number) => {
            state.render(child, node, index);
          });
          state.write("</div>");
          state.closeBlock(node);
        },
        parse: {
          // handled by markdown-it + parseHTML above — nothing to add here.
        },
      },
    };
  },
});

export default KBAlign;
