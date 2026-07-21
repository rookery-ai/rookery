import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { railItems } from "@/components/shell/IconRail";

// The suppression guard (spec §6.2, "the rule that keeps this from being
// infuriating"): single-key shortcuts (j, k, ?) must never fire while the
// user is typing — this app has a WYSIWYG note editor, a chat composer, a
// designer conversation, and a template textarea, all places where "j"
// means the letter j. Suppressed inside an input/textarea/contenteditable,
// or inside the ⌘K palette (cmdk sets a `cmdk-root` attribute on its own
// root, so any keystroke that lands inside the open palette is exempt too).
// Modifier shortcuts (⌘1–7) do NOT use this guard — they stay active
// everywhere, including while typing.
export function isTypingTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName.toLowerCase();
  return (
    tag === "input" ||
    tag === "textarea" ||
    el.isContentEditable ||
    el.closest("[cmdk-root]") !== null
  );
}

// Global ⌘/Ctrl+1..7 → jump to the seven rail destinations, in IconRail's
// existing order (railItems). Mounted once in AppShell. Active even while
// typing, per spec §6.2 — only bare-key shortcuts are input-suppressed.
//
// Known limitation (see task-4-report.md): on Chrome/Firefox/Edge, ⌘/Ctrl+1
// through ⌘/Ctrl+8(-9) are commonly reserved by the browser chrome itself
// for tab-switching and may never reach this listener. That reservation
// happens above the DOM event layer and can't be detected or worked around
// from page JS; there was no live desktop browser available in this
// (headless) environment to confirm empirically either way.
export function useRailShortcuts() {
  const navigate = useNavigate();
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (!(e.metaKey || e.ctrlKey)) return;
      const n = Number(e.key);
      if (!Number.isInteger(n)) return;
      const idx = n - 1;
      if (idx < 0 || idx >= railItems.length) return;
      e.preventDefault();
      navigate(railItems[idx].to);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [navigate]);
}

// Opt-in hook for a context-pane list: j/k move a highlighted index, Enter
// activates the highlighted row. Deliberately a hook each list mounts for
// itself, not a global registry every list must register with.
//
// Excluded: the KB file tree. Its rows are already individually focusable
// (tabIndex=0) with their own Enter/Space handling (FileTree.tsx); layering
// an index-based Enter on top would fight that existing interaction rather
// than complement it (spec §9 sanctions leaving a conflicting list out).
export function useListNav<T>(items: T[], onActivate: (item: T) => void, active = true) {
  const [highlightedIndex, setHighlightedIndex] = useState(0);

  // Keep the index in range as the list shrinks/grows (e.g. after a filter).
  useEffect(() => {
    setHighlightedIndex((i) => Math.min(Math.max(i, 0), Math.max(items.length - 1, 0)));
  }, [items.length]);

  useEffect(() => {
    if (!active) return;
    function onKeyDown(e: KeyboardEvent) {
      if (isTypingTarget(e.target)) return;
      if (e.key === "j") {
        if (items.length === 0) return;
        e.preventDefault();
        setHighlightedIndex((i) => Math.min(items.length - 1, i + 1));
      } else if (e.key === "k") {
        if (items.length === 0) return;
        e.preventDefault();
        setHighlightedIndex((i) => Math.max(0, i - 1));
      } else if (e.key === "Enter") {
        const item = items[highlightedIndex];
        if (item === undefined) return;
        e.preventDefault();
        onActivate(item);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [active, items, highlightedIndex, onActivate]);

  return { highlightedIndex, setHighlightedIndex };
}
