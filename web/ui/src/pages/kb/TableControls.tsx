import { useCallback, useEffect, useRef, useState } from "react";
import type { Editor } from "@tiptap/react";
import { ArrowLeft, ArrowRight, ArrowUp, ArrowDown, Trash2 } from "lucide-react";
import {
  HANDLE_THICKNESS,
  cellCoords,
  clampToViewport,
  placeColumnHandle,
  placeRowHandle,
  type HandlePlacement,
} from "./tableGeometry";

// Hover handles for editing a table's rows and columns.
//
// TipTap already implements every command used here (addRowAfter, deleteColumn,
// and the rest); what was missing was any way to reach them. A table could be
// inserted at a fixed 3x3 and never changed again.
//
// Direct manipulation rather than a toolbar: the handle appears against the row
// or column the pointer is over, so "delete this row" needs no reading to work
// out which row "this" is. A toolbar button acting on the caret's cell is the
// cheaper build and the one that makes people count rows before clicking.
//
// Every action goes through an editor command, never a DOM edit. That is what
// keeps the serialized form canonical — pipeSafeTable + generatorFidelity exist
// because a round-trip mismatch makes checkFidelity open the note READ-ONLY, so
// a table op that produced a subtly different document would lock the user out
// of their own note on the next load.

type Target =
  | { kind: "row"; index: number; place: HandlePlacement }
  | { kind: "col"; index: number; place: HandlePlacement };

function toRect(el: Element) {
  const r = el.getBoundingClientRect();
  return { top: r.top, left: r.left, width: r.width, height: r.height };
}

export function TableControls({
  editor,
  editable,
  containerRef,
}: {
  editor: Editor | null;
  editable: boolean;
  containerRef: React.RefObject<HTMLElement | null>;
}) {
  const [target, setTarget] = useState<Target | null>(null);
  // Held so a click on the handle itself does not first fire the mouseleave
  // that would unmount it out from under the pointer.
  const overHandle = useRef(false);

  const clear = useCallback(() => {
    if (!overHandle.current) setTarget(null);
  }, []);

  useEffect(() => {
    const root = containerRef.current;
    if (!editor || !editable || !root) return;
    // Narrowed once here so the listener closes over a non-null editor; the
    // effect re-runs whenever it changes.
    const ed = editor;

    function onMove(e: MouseEvent) {
      const el = e.target as HTMLElement | null;
      const cell = el?.closest?.("td, th") as HTMLTableCellElement | null;
      if (!cell) {
        clear();
        return;
      }
      const table = cell.closest("table");
      const container = containerRef.current;
      if (!table || !container) return;

      const coords = cellCoords(cell);
      if (!coords) return;

      const cellRect = toRect(cell);
      const tableRect = toRect(table);
      const containerRect = toRect(container);
      const viewport = { width: window.innerWidth, height: window.innerHeight };

      // Whichever edge the pointer is nearer decides which handle is offered.
      // Showing both at once doubles the click targets over the same cell and
      // makes neither obviously the one that acts on what you are pointing at.
      const nearTop = e.clientY - cellRect.top < cellRect.height / 2;
      const nearLeft = e.clientX - cellRect.left < cellRect.width / 2;
      const preferColumn = nearTop && !nearLeft ? true : nearTop;

      setTarget(
        preferColumn
          ? {
              kind: "col",
              index: coords.col,
              place: clampToViewport(
                placeColumnHandle(cellRect, tableRect, containerRect),
                containerRect,
                viewport,
              ),
            }
          : {
              kind: "row",
              index: coords.row,
              place: clampToViewport(
                placeRowHandle(cellRect, tableRect, containerRect),
                containerRect,
                viewport,
              ),
            },
      );
      // Put the caret in the hovered cell so TipTap's selection-relative
      // commands act on it. Without this the buttons would silently operate on
      // whichever cell was last clicked.
      const pos = ed.view.posAtDOM(cell, 0);
      if (pos >= 0) ed.commands.setTextSelection(pos);
    }

    root.addEventListener("mousemove", onMove);
    root.addEventListener("mouseleave", clear);
    return () => {
      root.removeEventListener("mousemove", onMove);
      root.removeEventListener("mouseleave", clear);
    };
  }, [editor, editable, containerRef, clear]);

  if (!editor || !editable || !target) return null;

  const isRow = target.kind === "row";
  const run = (fn: () => void) => () => {
    fn();
    setTarget(null);
  };

  const actions = isRow
    ? [
        { label: "Insert row above", icon: ArrowUp, run: () => editor.chain().focus().addRowBefore().run() },
        { label: "Insert row below", icon: ArrowDown, run: () => editor.chain().focus().addRowAfter().run() },
        { label: "Delete row", icon: Trash2, run: () => editor.chain().focus().deleteRow().run(), danger: true },
      ]
    : [
        { label: "Insert column left", icon: ArrowLeft, run: () => editor.chain().focus().addColumnBefore().run() },
        { label: "Insert column right", icon: ArrowRight, run: () => editor.chain().focus().addColumnAfter().run() },
        { label: "Delete column", icon: Trash2, run: () => editor.chain().focus().deleteColumn().run(), danger: true },
      ];

  return (
    <div
      data-testid="kb-table-handle"
      data-kind={target.kind}
      onMouseEnter={() => (overHandle.current = true)}
      onMouseLeave={() => {
        overHandle.current = false;
        setTarget(null);
      }}
      className="absolute z-20 flex items-center justify-center gap-0.5 rounded-md border border-border bg-background p-0.5 shadow-sm"
      style={
        isRow
          ? { top: target.place.top, left: target.place.left, minHeight: target.place.size, flexDirection: "column" }
          : { top: target.place.top, left: target.place.left, minWidth: target.place.size }
      }
    >
      {actions.map(({ label, icon: Icon, run: action, danger }) => (
        <button
          key={label}
          type="button"
          aria-label={label}
          title={label}
          onClick={run(action)}
          className={`flex size-5 shrink-0 items-center justify-center rounded transition-colors hover:bg-chrome ${
            danger ? "text-danger" : "text-muted-2"
          }`}
          style={{ maxHeight: HANDLE_THICKNESS + 8 }}
        >
          <Icon className="size-3.5" />
        </button>
      ))}
    </div>
  );
}
