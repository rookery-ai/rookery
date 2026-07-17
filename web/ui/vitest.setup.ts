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
