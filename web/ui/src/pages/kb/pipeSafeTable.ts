import { Table } from "@tiptap/extension-table";

// PipeSafeTable overrides tiptap-markdown's table serialization to escape a
// literal "|" inside table cells as "\|". tiptap-markdown's own table
// serializer (extensions/nodes/table.js) renders each cell with
// state.renderInline(), and prosemirror-markdown's text escaping (state.esc)
// does NOT escape "|", so a cell containing a pipe would serialize back as a
// column separator — turning one cell into two and corrupting the table. That
// mismatch is exactly what made checkFidelity refuse CSV notes with pipe-bearing
// cells and force them into raw mode.
//
// The fix wraps state.esc only for the duration of table rendering, so it
// affects ONLY literal-text escaping (where a stray "|" can appear). Marks
// (**bold**, links) and the [[wikilink]] atom (which writes via state.write,
// not esc) are untouched — verified round-tripping pipe/bold/wikilink cells.
//
// This mirrors the "fix the serializer, not the comparator" approach already
// used for wikilinks (see wikilinks.ts) rather than loosening checkFidelity,
// which would open pipe-cell tables in WYSIWYG and destroy them on save.

// Minimal shapes for the serializer state/node tiptap-markdown hands us.
interface SerState {
  esc(str: string, startOfLine?: boolean): string;
  write(s: string): void;
  renderInline(node: PMNode): void;
  ensureNewLine(): void;
  closeBlock(node: PMNode): void;
  inTable: boolean;
}
interface PMNode {
  childCount: number;
  firstChild: PMNode | null;
  textContent: string;
  forEach(cb: (child: PMNode, offset: number, index: number) => void): void;
}

export const PipeSafeTable = Table.extend({
  addStorage() {
    return {
      markdown: {
        serialize(state: SerState, node: PMNode) {
          state.inTable = true;
          const origEsc = state.esc.bind(state);
          state.esc = (str: string, startOfLine?: boolean) =>
            origEsc(str, startOfLine).replace(/\|/g, "\\|");
          node.forEach((row: PMNode, _p: number, i: number) => {
            state.write("| ");
            row.forEach((col: PMNode, _q: number, j: number) => {
              if (j) state.write(" | ");
              const cellContent = col.firstChild;
              if (cellContent && cellContent.textContent.trim()) {
                state.renderInline(cellContent);
              }
            });
            state.write(" |");
            state.ensureNewLine();
            if (!i) {
              const delimiterRow = Array.from({ length: row.childCount })
                .map(() => "---")
                .join(" | ");
              state.write(`| ${delimiterRow} |`);
              state.ensureNewLine();
            }
          });
          state.esc = origEsc;
          state.closeBlock(node);
          state.inTable = false;
        },
        parse: {},
      },
    };
  },
});
