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

// jsdom doesn't implement EventSource either, and a chat turn now follows its
// progress over one — so ANY test that mounts a chat surface and sends a
// message reaches `new EventSource(...)`.
//
// This is an inert default, not a test double. It exists because the failure it
// prevents is a nasty one: the stream opens asynchronously, AFTER the test's
// assertions have already passed, so the suite reports every test green and the
// run still exits non-zero on an unhandled ReferenceError. That is precisely
// what happened to `chataboutfile.test.tsx` once its button began auto-sending
// — 1203 passed, exit 1.
//
// A test that wants to DRIVE a turn installs the real double over this one
// (`pages/chats/turnTestHarness.ts`); the rest simply need the constructor not
// to throw.
if (typeof globalThis.EventSource === "undefined") {
  globalThis.EventSource = class EventSource {
    url: string;
    readyState = 0;
    onmessage: ((ev: MessageEvent) => void) | null = null;
    onerror: (() => void) | null = null;
    onopen: (() => void) | null = null;
    constructor(url: string) {
      this.url = url;
    }
    addEventListener() {}
    removeEventListener() {}
    close() {
      this.readyState = 2;
    }
  } as unknown as typeof globalThis.EventSource;
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
