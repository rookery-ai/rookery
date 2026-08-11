import { describe, expect, it } from "vitest";
import {
  HANDLE_GAP,
  HANDLE_THICKNESS,
  cellCoords,
  clampToViewport,
  columnCount,
  pickerSize,
  placeColumnHandle,
  placeRowHandle,
  rowCount,
} from "./tableGeometry";

// These are pure functions for the same reason SlashMenu's placeMenu is: jsdom
// has no layout engine, so every rect it reports is zeroes. A test driving the
// real editor can prove a handle MOUNTS but never where it lands.

const container = { top: 100, left: 50, width: 800, height: 600 };
const table = { top: 140, left: 70, width: 400, height: 200 };
const cell = { top: 180, left: 170, width: 100, height: 40 };
const viewport = { width: 1440, height: 900 };

describe("handle placement", () => {
  it("puts the column handle above the table, aligned to the cell", () => {
    const p = placeColumnHandle(cell, table, container);
    // Above the TABLE, not the cell: hovering any row in a column then shows
    // the control in one fixed place instead of chasing the pointer down.
    expect(p.top).toBe(table.top - container.top - HANDLE_THICKNESS - HANDLE_GAP);
    expect(p.left).toBe(cell.left - container.left);
    expect(p.size).toBe(cell.width);
  });

  it("puts the row handle left of the table, aligned to the cell", () => {
    const p = placeRowHandle(cell, table, container);
    expect(p.left).toBe(table.left - container.left - HANDLE_THICKNESS - HANDLE_GAP);
    expect(p.top).toBe(cell.top - container.top);
    expect(p.size).toBe(cell.height);
  });

  // Coordinates are relative to the positioned wrapper, so a scrolled container
  // must not shift the handle away from its cell.
  it("stays relative to the container, not the page", () => {
    const scrolled = { ...container, top: -400 };
    const p = placeRowHandle(cell, table, scrolled);
    expect(p.top).toBe(cell.top + 400);
  });
});

describe("viewport clamping", () => {
  // A table at the very top or left would otherwise put its handles off-screen
  // where they cannot be clicked — the bug placeMenu already had to fix once.
  it("pulls a handle back inside the top edge", () => {
    const tight = { top: 0, left: 200, width: 800, height: 600 };
    const p = clampToViewport({ top: -30, left: 10, size: 40 }, tight, viewport);
    expect(tight.top + p.top).toBeGreaterThanOrEqual(0);
  });

  it("pulls a handle back inside the left edge", () => {
    const tight = { top: 100, left: 4, width: 800, height: 600 };
    const p = clampToViewport({ top: 10, left: -40, size: 40 }, tight, viewport);
    expect(tight.left + p.left).toBeGreaterThanOrEqual(0);
  });

  it("pulls a handle back inside the right edge", () => {
    const p = clampToViewport({ top: 10, left: 1500, size: 40 }, container, viewport);
    expect(container.left + p.left + HANDLE_THICKNESS).toBeLessThanOrEqual(viewport.width);
  });

  it("leaves a handle that already fits alone", () => {
    const place = { top: 10, left: 20, size: 40 };
    expect(clampToViewport(place, container, viewport)).toEqual(place);
  });

  it("never hides the handle instead of moving it", () => {
    const p = clampToViewport({ top: -999, left: -999, size: 40 }, container, viewport);
    expect(Number.isFinite(p.top)).toBe(true);
    expect(Number.isFinite(p.left)).toBe(true);
  });
});

function tableFrom(html: string) {
  const host = document.createElement("div");
  host.innerHTML = html;
  return host.querySelector("table")!;
}

describe("cell coordinates", () => {
  it("reads row and column off the DOM", () => {
    const t = tableFrom(`
      <table><tbody>
        <tr><th>a</th><th>b</th><th>c</th></tr>
        <tr><td>d</td><td>e</td><td>f</td></tr>
      </tbody></table>`);
    const cells = [...t.querySelectorAll("td, th")] as HTMLTableCellElement[];
    expect(cellCoords(cells[1])).toEqual({ row: 0, col: 1 });
    expect(cellCoords(cells[5])).toEqual({ row: 1, col: 2 });
  });

  // A merged cell shifts everything after it. Getting this wrong inserts a
  // column in the wrong place on exactly the tables hardest to repair by hand.
  it("honours colspan when counting columns", () => {
    const t = tableFrom(`
      <table><tbody>
        <tr><td colspan="2">wide</td><td>after</td></tr>
      </tbody></table>`);
    const cells = [...t.querySelectorAll("td")] as HTMLTableCellElement[];
    expect(cellCoords(cells[0])).toEqual({ row: 0, col: 0 });
    expect(cellCoords(cells[1])).toEqual({ row: 0, col: 2 });
  });

  it("returns null for a cell outside any table", () => {
    const orphan = document.createElement("td");
    expect(cellCoords(orphan)).toBeNull();
  });

  it("counts rows and columns, spans included", () => {
    const t = tableFrom(`
      <table><tbody>
        <tr><td colspan="3">x</td></tr>
        <tr><td>a</td><td>b</td><td>c</td></tr>
      </tbody></table>`);
    expect(rowCount(t)).toBe(2);
    expect(columnCount(t)).toBe(3);
  });
});

describe("picker sizing", () => {
  // One-based: hovering the first cell must mean a 1x1 table, not a 0x0 one.
  it("is one-based so the first cell is a 1x1", () => {
    expect(pickerSize(0, 0)).toEqual({ rows: 1, cols: 1 });
    expect(pickerSize(2, 3)).toEqual({ rows: 3, cols: 4 });
  });
});
