import { Node, mergeAttributes } from "@tiptap/core";
import { liftTarget } from "@tiptap/pm/transform";

export const MIN_COLUMNS = 2;
export const MAX_COLUMNS = 4;

declare module "@tiptap/core" {
  interface Commands<ReturnType> {
    kbColumns: {
      insertColumns: (cols: number) => ReturnType;
      setColumns: (cols: number) => ReturnType;
      clearColumns: () => ReturnType;
    };
  }
}

function clampCols(n: number): number {
  if (!Number.isFinite(n)) return MIN_COLUMNS;
  return Math.min(MAX_COLUMNS, Math.max(MIN_COLUMNS, Math.round(n)));
}

// A columns block lays its DIRECT CHILDREN out side by side — one block per
// cell. There is no separate cell node.
//
// That is the whole trick, and it is what makes the construct round-trip. The
// obvious shape, a wrapper div containing one div per cell, nests raw HTML
// blocks inside raw HTML blocks; the shape here reuses exactly the mechanism
// nodes/align.ts already proves — a single `<div …>` opening tag, a blank line,
// ordinary markdown blocks, a blank line, `</div>` — so markdown-it closes the
// CommonMark type-6 block at the first blank line and every cell is parsed as
// real markdown with real marks, not as text inside an HTML block.
//
//   <div data-cols="2">
//
//   ![before](assets/before.png)
//
//   ![after](assets/after.png)
//
//   </div>
//
// The cost, stated plainly: **a cell is one block.** A cell holding a heading
// AND a paragraph needs nesting, which this deliberately does not do. Two images
// side by side, or two short paragraphs, is what this is for.
//
// The second cost is portability, and it is not fixable by choosing a different
// attribute. GitHub's sanitiser strips `class`, `data-*` and `style` alike, so
// NO div-based grid renders as a grid anywhere but here; only a `<table>` would,
// and a markdown table forces a header row that this editor promotes on save
// (see pipeSafeTable's header-row caveat). Outside Rookery a columns block
// degrades to its cells stacked in order, with every image and mark intact —
// which is the right failure, and why the wrapper carries no styling of its own
// in the file.
export const KBColumns = Node.create({
  name: "kbColumns",
  group: "block",
  content: "block+",
  defining: true,

  addAttributes() {
    return {
      cols: {
        default: MIN_COLUMNS,
        parseHTML: (el) => clampCols(Number(el.getAttribute("data-cols"))),
        renderHTML: (attrs) => ({ "data-cols": String(attrs.cols) }),
      },
    };
  },

  parseHTML() {
    return [{ tag: "div[data-cols]" }];
  },

  renderHTML({ HTMLAttributes }) {
    // The class is applied on RENDER only and never written to the file: the
    // editor's own CSS needs a hook, the saved note does not need our styling
    // vocabulary baked into it.
    return ["div", mergeAttributes(HTMLAttributes, { class: "kb-columns" }), 0];
  },

  addCommands() {
    return {
      // The slash-menu entry point. It inserts a block with N EMPTY cells
      // rather than wrapping whatever the caret happens to be in: the slash
      // menu runs on the empty paragraph left behind after the "/query" range
      // is deleted, so wrapping there would produce a one-cell columns block —
      // a layout with nothing to lay out.
      insertColumns:
        (cols: number) =>
        ({ commands }) => {
          const n = clampCols(cols);
          return commands.insertContent({
            type: this.name,
            attrs: { cols: n },
            content: Array.from({ length: n }, () => ({ type: "paragraph" })),
          });
        },
      setColumns:
        (cols: number) =>
        ({ commands }) => {
          const n = clampCols(cols);
          // Update before wrapping, for the same reason nodes/align.ts does:
          // wrapping an already-wrapped block nests a second div that reads on
          // screen as one, and `editor.isActive` reports false for an
          // AllSelection so it cannot be used to tell the two cases apart.
          if (commands.updateAttributes(this.name, { cols: n })) return true;
          return commands.wrapIn(this.name, { cols: n });
        },
      // Lifts EVERY cell out, not just the one the caret is in.
      //
      // `commands.lift(name)` is the obvious implementation and is wrong here:
      // it lifts the block range around the SELECTION, so on a two-cell block
      // it takes the first cell out and leaves the second still wrapped — a
      // half-cleared layout that looks like a bug and is one. The range is
      // therefore built explicitly over the whole node's content.
      clearColumns:
        () =>
        ({ state, tr, dispatch }) => {
          const type = state.schema.nodes[this.name];
          if (!type) return false;
          let found: { pos: number; size: number } | null = null;
          state.doc.nodesBetween(state.selection.from, state.selection.to, (node, pos) => {
            if (!found && node.type === type) found = { pos, size: node.content.size };
          });
          // Also handle a caret sitting INSIDE the block, where nodesBetween
          // over an empty selection never visits the ancestor.
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
          // Blank lines are load-bearing, not formatting — without them
          // markdown-it keeps the cells inside the raw HTML block and stops
          // parsing their markdown.
          state.write(`<div data-cols="${node.attrs.cols}">\n\n`);
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

export default KBColumns;
