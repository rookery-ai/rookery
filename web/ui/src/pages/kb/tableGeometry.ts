// Geometry for the table hover handles, extracted as pure functions.
//
// This mirrors SlashMenu's `placeMenu` and exists for the identical reason:
// jsdom has no layout engine, so every getBoundingClientRect it returns is
// zeroes. A test driving the real editor can prove a handle MOUNTS but never
// where it lands. Keeping the arithmetic in a function that takes rectangles and
// returns coordinates is the only way the placement itself is testable at all.
//
// See pages/kb/slashPlacement.ts for the same pattern on the slash menu, and
// scripts/verify-kb-layout.py for the browser-driven harness that covers what
// neither can.

export type Rect = {
  top: number;
  left: number;
  width: number;
  height: number;
};

export type HandlePlacement = {
  /** Offset from the positioned ancestor, in px. */
  top: number;
  left: number;
  /** Length of the handle along the axis it runs. */
  size: number;
};

/** How far outside the table edge a handle sits, and how thick it is. */
export const HANDLE_THICKNESS = 14;
export const HANDLE_GAP = 4;

/**
 * Places the handle for a column, given the hovered cell and the table.
 *
 * The handle runs the width of the cell and sits above the table — above the
 * TABLE rather than above the cell, so that hovering any row in a column shows
 * the control in one fixed place instead of having it chase the pointer down the
 * page. Coordinates are relative to `container`, which is expected to be the
 * table's positioned wrapper.
 */
export function placeColumnHandle(
  cell: Rect,
  table: Rect,
  container: Rect,
): HandlePlacement {
  return {
    top: table.top - container.top - HANDLE_THICKNESS - HANDLE_GAP,
    left: cell.left - container.left,
    size: cell.width,
  };
}

/**
 * Places the handle for a row. Runs the height of the cell, sits to the left of
 * the table for the same reason the column handle sits above it.
 */
export function placeRowHandle(
  cell: Rect,
  table: Rect,
  container: Rect,
): HandlePlacement {
  return {
    top: cell.top - container.top,
    left: table.left - container.left - HANDLE_THICKNESS - HANDLE_GAP,
    size: cell.height,
  };
}

/**
 * Clamps a handle so it stays inside the viewport.
 *
 * A table at the very top or the very left of the scroll container would put its
 * handles off-screen, where they cannot be clicked — the same class of bug the
 * slash menu had before `placeMenu` bounded it. Rather than hide the control,
 * the handle is pushed just inside the edge: an overlapping handle is still
 * usable, an invisible one is not.
 */
export function clampToViewport(
  place: HandlePlacement,
  container: Rect,
  viewport: { width: number; height: number },
): HandlePlacement {
  const absLeft = container.left + place.left;
  const absTop = container.top + place.top;

  let { left, top } = place;
  if (absLeft < 0) left = -container.left;
  if (absTop < 0) top = -container.top;
  if (absLeft + HANDLE_THICKNESS > viewport.width) {
    left = viewport.width - HANDLE_THICKNESS - container.left;
  }
  if (absTop + HANDLE_THICKNESS > viewport.height) {
    top = viewport.height - HANDLE_THICKNESS - container.top;
  }
  return { ...place, left, top };
}

/**
 * Resolves a cell element to its zero-based row and column within its table.
 *
 * Read off the DOM rather than the ProseMirror document because the hover that
 * triggers it is a DOM event and the mapping back into document positions is
 * exactly the part TipTap's own commands already do — they act on the CURRENT
 * selection, so all this has to do is put the caret in the right cell first.
 *
 * `colSpan` is honoured: a merged cell shifts every cell after it, and getting
 * that wrong would insert a column in the wrong place on precisely the tables
 * that are hardest to fix by hand.
 */
export function cellCoords(cell: HTMLTableCellElement): { row: number; col: number } | null {
  const rowEl = cell.parentElement as HTMLTableRowElement | null;
  const section = rowEl?.parentElement;
  const table = section?.closest("table");
  if (!rowEl || !table) return null;

  const rows = [...table.querySelectorAll("tr")];
  const row = rows.indexOf(rowEl);
  if (row < 0) return null;

  let col = 0;
  for (const c of [...rowEl.children] as HTMLTableCellElement[]) {
    if (c === cell) return { row, col };
    col += c.colSpan || 1;
  }
  return null;
}

/** Number of columns in a table, counting spans. */
export function columnCount(table: HTMLTableElement): number {
  const first = table.querySelector("tr");
  if (!first) return 0;
  let n = 0;
  for (const c of [...first.children] as HTMLTableCellElement[]) n += c.colSpan || 1;
  return n;
}

/** Number of rows in a table. */
export function rowCount(table: HTMLTableElement): number {
  return table.querySelectorAll("tr").length;
}

/**
 * Grid size for the insert-table picker, given the cell the pointer is over.
 *
 * One-based, because "hovering the first cell" must mean a 1x1 table rather than
 * a 0x0 one — the off-by-one that would otherwise let someone insert a table
 * with no cells in it.
 */
export function pickerSize(rowIndex: number, colIndex: number) {
  return { rows: rowIndex + 1, cols: colIndex + 1 };
}
