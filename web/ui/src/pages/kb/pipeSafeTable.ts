import { Table } from "@tiptap/extension-table";
import { getHTMLFromFragment, elementFromString } from "@tiptap/core";
import { Fragment, type Node as PMNode } from "@tiptap/pm/model";

// PipeSafeTable overrides tiptap-markdown's table markdown serializer to escape a
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
//
// IMPORTANT: this file must otherwise faithfully reproduce tiptap-markdown's
// vendored table.js serializer (node_modules/tiptap-markdown/src/extensions/nodes/table.js),
// with exactly the two additions above (pipe-escaping + a try/finally around
// the state.esc swap). In particular the isMarkdownSerializable() guard +
// HTML/placeholder fallback (node_modules/tiptap-markdown/src/extensions/nodes/html.js)
// MUST be preserved: a cell with more than one block child (e.g. two
// paragraphs — an ordinary "click in cell, press Enter" edit) or a
// colspan/rowspan (merged) cell is NOT representable by the simple
// "| a | b |" markdown table grammar. Rendering only col.firstChild for such
// a cell would silently drop the rest of the cell's content on save, and
// sizing the delimiter row off row.childCount would be wrong for a row with
// merged cells. Falling back to the HTML/placeholder path instead means the
// content survives (as literal HTML, or as a "[table]" placeholder note that
// keeps the note in raw mode rather than lying about being WYSIWYG-safe).
// tiptap-markdown's own ../../util/prosemirror is not importable here (the
// package's "exports" field blocks deep ./src/... imports), so childNodes()
// is inlined below.

function childNodes(node: PMNode): PMNode[] {
  const nodes: PMNode[] = [];
  node.forEach((child) => nodes.push(child));
  return nodes;
}

function hasSpan(node: PMNode): boolean {
  return node.attrs.colspan > 1 || node.attrs.rowspan > 1;
}

function isMarkdownSerializable(node: PMNode): boolean {
  const rows = childNodes(node);
  const firstRow = rows[0];
  const bodyRows = rows.slice(1);

  // Deliberate deviation from the vendored table.js (which assumes at least
  // one row and would throw on `childNodes(undefined)`): guard an empty
  // table by treating it as non-serializable rather than crashing. The
  // schema shouldn't produce a rowless table, but "fall back safely" is a
  // strictly better failure mode than an unhandled exception here.
  if (
    !firstRow ||
    childNodes(firstRow).some(
      (cell) => cell.type.name !== "tableHeader" || hasSpan(cell) || cell.childCount > 1,
    )
  ) {
    return false;
  }

  if (
    bodyRows.some((row) =>
      childNodes(row).some(
        (cell) => cell.type.name === "tableHeader" || hasSpan(cell) || cell.childCount > 1,
      ),
    )
  ) {
    return false;
  }

  return true;
}

// Minimal shape for the serializer state tiptap-markdown hands us.
interface SerState {
  esc(str: string, startOfLine?: boolean): string;
  write(s: string): void;
  renderInline(node: PMNode): void;
  ensureNewLine(): void;
  closeBlock(node: PMNode): void;
  inTable: boolean;
}

export const PipeSafeTable = Table.extend({
  addStorage() {
    return {
      markdown: {
        serialize(this: { editor: { storage: { markdown: { options: { html: boolean } } } } }, state: SerState, node: PMNode, parent: PMNode | Fragment) {
          if (!isMarkdownSerializable(node)) {
            // Faithfully reproduce tiptap-markdown's html.js fallback so a
            // non-simple table (multi-block cell, merged cell) never
            // silently loses content: real HTML when html mode is on,
            // otherwise the same "[table]" placeholder + closeBlock the
            // stock node writes. This app configures Markdown with
            // `html: false` (see editor.ts), so the placeholder branch is
            // the one that actually runs today.
            if (this.editor.storage.markdown.options.html) {
              const html = getHTMLFromFragment(Fragment.from(node), node.type.schema);
              const isTopLevelBlock =
                node.isBlock && (parent instanceof Fragment || parent.type.name === node.type.schema.topNodeType.name);
              state.write(isTopLevelBlock ? formatHTMLBlock(html) : html);
            } else {
              // eslint-disable-next-line no-console
              console.warn(`Tiptap Markdown: "${node.type.name}" node is only available in html mode`);
              state.write(`[${node.type.name}]`);
            }
            if (node.isBlock) {
              state.closeBlock(node);
            }
            return;
          }

          state.inTable = true;
          const origEsc = state.esc.bind(state);
          try {
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
          } finally {
            state.esc = origEsc;
          }
          state.closeBlock(node);
          state.inTable = false;
        },
        parse: {},
      },
    };
  },
});

// format html block as per the commonmark spec — verbatim port of
// tiptap-markdown's html.js formatBlock. Only reached on the html:true path,
// which this app doesn't currently enable (editor.ts configures
// `Markdown.configure({ html: false })`), but kept faithful for parity.
function formatHTMLBlock(html: string): string {
  const dom = elementFromString(html);
  const element = dom.firstElementChild as HTMLElement;
  element.innerHTML = element.innerHTML.trim() ? `\n${element.innerHTML}\n` : "\n";
  return element.outerHTML;
}
