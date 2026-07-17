import "@testing-library/jest-dom/vitest";

// jsdom doesn't implement ResizeObserver — cmdk (the ⌘K command palette's
// underlying library) uses it to track list height and hangs the whole
// render effect chain without this stub.
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class ResizeObserver {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

// jsdom also doesn't implement scrollIntoView — cmdk calls it when the
// keyboard-selected item changes.
if (typeof Element.prototype.scrollIntoView !== "function") {
  Element.prototype.scrollIntoView = () => {};
}

// jsdom's Range doesn't implement getClientRects/getBoundingClientRect —
// ProseMirror's EditorView.coordsAtPos (used by its own scrollIntoView
// positioning logic, e.g. on focus() or any selection-changing transaction)
// calls target.getClientRects() and falls back to
// target.getBoundingClientRect() when empty. Without these, any test that
// actually types into a real TipTap/ProseMirror-mounted editor (as opposed
// to driving commands against a detached, unfocused headless editor) throws
// "target.getClientRects is not a function" from inside dispatchTransaction.
if (typeof Range.prototype.getClientRects !== "function") {
  Range.prototype.getClientRects = () => ({ length: 0, item: () => null }) as unknown as DOMRectList;
}
if (typeof Range.prototype.getBoundingClientRect !== "function") {
  Range.prototype.getBoundingClientRect = () =>
    ({ top: 0, left: 0, bottom: 0, right: 0, width: 0, height: 0, x: 0, y: 0, toJSON() {} }) as DOMRect;
}

// jsdom doesn't implement document.elementFromPoint — ProseMirror's mousedown
// handler (EditorView.posAtCoords) calls it to resolve a click's document
// position. Without a stub, a real `userEvent.click()` on the editor's
// contenteditable throws inside ProseMirror's own event handler.
if (typeof document.elementFromPoint !== "function") {
  document.elementFromPoint = () => null;
}
