import { createElement, useEffect, useRef, useState } from "react";

export const PANE_MIN = 200;
export const PANE_MAX = 560;
export const PANE_DEFAULT = 256;
export const STORAGE_KEY = "sa.paneWidth";

const STEP = 16;

export function clampPaneWidth(n: number): number {
  return Math.min(PANE_MAX, Math.max(PANE_MIN, n));
}

// Reads the persisted pane width. A corrupt or out-of-range stored value
// falls back to the default rather than being clamped — an out-of-range
// value means the storage is untrustworthy (e.g. edited by hand, or written
// by a future version with a wider range), not that the user dragged to an
// extreme. Live drag input is clamped separately in clampPaneWidth.
export function readStoredWidth(): number {
  const raw = localStorage.getItem(STORAGE_KEY);
  if (raw === null) return PANE_DEFAULT;
  const n = parseInt(raw, 10);
  if (Number.isNaN(n) || n < PANE_MIN || n > PANE_MAX) return PANE_DEFAULT;
  return n;
}

export function usePaneWidth() {
  const [width, setWidth] = useState<number>(readStoredWidth);

  useEffect(() => {
    localStorage.setItem(STORAGE_KEY, String(width));
  }, [width]);

  const reset = () => setWidth(PANE_DEFAULT);

  return { width, setWidth, reset };
}

export function PaneResizeHandle({
  width,
  setWidth,
  reset,
}: {
  width: number;
  setWidth: (n: number) => void;
  reset: () => void;
}) {
  const dragRef = useRef<{ startX: number; startWidth: number } | null>(null);

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    // jsdom (unit tests) doesn't implement pointer capture; real browsers
    // always do, so this guard only ever skips in the test environment.
    e.currentTarget.setPointerCapture?.(e.pointerId);
    dragRef.current = { startX: e.clientX, startWidth: width };
    document.body.style.userSelect = "none";
  };

  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    setWidth(clampPaneWidth(drag.startWidth + (e.clientX - drag.startX)));
  };

  const endDrag = (e: React.PointerEvent<HTMLDivElement>) => {
    if (!dragRef.current) return;
    dragRef.current = null;
    e.currentTarget.releasePointerCapture?.(e.pointerId);
    document.body.style.userSelect = "";
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    switch (e.key) {
      case "ArrowLeft":
        e.preventDefault();
        setWidth(clampPaneWidth(width - STEP));
        break;
      case "ArrowRight":
        e.preventDefault();
        setWidth(clampPaneWidth(width + STEP));
        break;
      case "Home":
        e.preventDefault();
        setWidth(PANE_MIN);
        break;
      case "End":
        e.preventDefault();
        setWidth(PANE_MAX);
        break;
    }
  };

  // This file is .ts, not .tsx (per the module layout this task specifies),
  // so the element is built with createElement rather than JSX syntax.
  return createElement("div", {
    role: "separator",
    "aria-orientation": "vertical",
    "aria-label": "Resize sidebar",
    "aria-valuenow": width,
    "aria-valuemin": PANE_MIN,
    "aria-valuemax": PANE_MAX,
    tabIndex: 0,
    className:
      "absolute top-0 right-0 h-full w-1 cursor-col-resize touch-none select-none hover:bg-primary/30 active:bg-primary/50",
    onPointerDown,
    onPointerMove,
    onPointerUp: endDrag,
    onPointerCancel: endDrag,
    onKeyDown,
    onDoubleClick: reset,
  });
}
