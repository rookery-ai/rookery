import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { pickerSize } from "./tableGeometry";

const MAX_ROWS = 8;
const MAX_COLS = 8;

/**
 * The size picker the "/table" slash command opens.
 *
 * The command used to insert a fixed 3x3 with no way to say otherwise, and no
 * way to change it afterwards either. Picking the shape up front is the cheap
 * half of the fix; TableControls is the half that lets it change later.
 *
 * A hover grid rather than two number inputs: the shape of a table is a spatial
 * question, and every editor that has solved this — Word, Notion, Google Docs —
 * answers it the same way, so it needs no instructions.
 *
 * `withHeaderRow` is offered because it is not a cosmetic choice here. Markdown
 * tables have no way to express a table WITHOUT a header row: the delimiter line
 * is mandatory in the grammar. A headerless table therefore serializes with its
 * first row of data promoted into the header on the next save, so the checkbox
 * is really "should the first row be a header, or should it become one".
 */
export function TableSizePicker({
  open,
  onOpenChange,
  onPick,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPick: (opts: { rows: number; cols: number; withHeaderRow: boolean }) => void;
}) {
  const [hover, setHover] = useState<{ rows: number; cols: number }>({ rows: 3, cols: 3 });
  const [withHeaderRow, setWithHeaderRow] = useState(true);

  function pick(rows: number, cols: number) {
    onPick({ rows, cols, withHeaderRow });
    onOpenChange(false);
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Insert table</DialogTitle>
          <DialogDescription>
            {hover.cols} × {hover.rows} — you can add and remove rows and columns
            afterwards.
          </DialogDescription>
        </DialogHeader>

        <div
          className="grid w-max gap-1"
          style={{ gridTemplateColumns: `repeat(${MAX_COLS}, 1.25rem)` }}
          onMouseLeave={() => setHover({ rows: 3, cols: 3 })}
          role="grid"
          aria-label="Table size"
        >
          {Array.from({ length: MAX_ROWS * MAX_COLS }, (_, i) => {
            const rowIndex = Math.floor(i / MAX_COLS);
            const colIndex = i % MAX_COLS;
            const { rows, cols } = pickerSize(rowIndex, colIndex);
            const on = rows <= hover.rows && cols <= hover.cols;
            return (
              <button
                key={i}
                type="button"
                aria-label={`${cols} by ${rows}`}
                onMouseEnter={() => setHover({ rows, cols })}
                onFocus={() => setHover({ rows, cols })}
                onClick={() => pick(rows, cols)}
                className={`size-5 rounded-[3px] border transition-colors ${
                  on ? "border-accent bg-accent-soft" : "border-border bg-chrome"
                }`}
              />
            );
          })}
        </div>

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={withHeaderRow}
            onChange={(e) => setWithHeaderRow(e.target.checked)}
            className="size-4 accent-accent"
          />
          First row is a header
        </label>
        <p className="text-muted-2 text-xs">
          Markdown tables always have a header row, so an unchecked table gets one
          on the next save — its first row becomes the header.
        </p>
      </DialogContent>
    </Dialog>
  );
}
