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
//
// `el.isContentEditable` is checked first (the real-browser-correct signal,
// and the one that also transparently follows the spec's inheritance rules
// for any element nested inside an editable host). It is deliberately
// backed up by an attribute-based `.closest()` walk: jsdom does not
// implement the `isContentEditable` getter at all (it's `undefined`
// unconditionally, confirmed against both a bare contenteditable div and a
// real mounted TipTap/ProseMirror editor), so without this fallback the
// guard's most safety-critical branch — the one protecting the WYSIWYG note
// editor, a `<div>` that `tag === "input"`/`"textarea"` never catches — had
// zero regression coverage: every test in this suite passed even with the
// `isContentEditable` check deleted outright. The attribute selector works
// in jsdom (unlike the property) and is what `keyboardnav.test.tsx`'s
// contenteditable-region tests actually pin.
export function isTypingTarget(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  const tag = el.tagName.toLowerCase();
  return (
    tag === "input" ||
    tag === "textarea" ||
    // A real <select> (CoderSection.tsx, SetupWizard.tsx) has native
    // type-ahead on a bare keystroke (typing "j" jumps to the next option
    // starting with j) — without this, a bare "j"/"k" fires both that
    // native behaviour AND the app's navigation at once.
    tag === "select" ||
    el.isContentEditable ||
    el.closest('[contenteditable="true"], [contenteditable=""]') !== null ||
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

// Any node with role="dialog" and an OPEN Radix data-state sitting over the
// page (the ⌘K palette, the "?" shortcuts overlay, a Sheet/slide-over) means
// the background list must not react to j/k/Enter meant for that modal.
// Radix's own Dialog/Sheet primitives stamp both attributes on the same
// content node (confirmed against the real primitive in
// keyboardnav.test.tsx, not a hand-rolled stand-in) — this is a plain DOM
// query rather than a registry every modal must opt into.
function isOpenDialogPresent(): boolean {
  return document.querySelector('[role="dialog"][data-state="open"]') !== null;
}

// The listener is window-level (unscoped), so an Enter keydown while focus
// sits on a real activating control (a <button>, a link, anything
// role="button" — e.g. Home's "Mark all read") would otherwise ALSO invoke
// onActivate on top of that control's own native click: a genuine
// double-fire. Neither of those tags is a typing target, so isTypingTarget
// alone doesn't catch this — the control should handle its own Enter.
function isActivatingControl(el: EventTarget | null): boolean {
  if (!(el instanceof HTMLElement)) return false;
  return el.closest("button, a, [role='button']") !== null;
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
  // This is also the (deliberate) policy for "the highlighted item itself
  // got deleted": deletion shrinks `items` in place (the caller filters it
  // out before it ever reaches this hook), so the same clamp that handles
  // ordinary shrinkage also decides what happens here — the numeric index
  // is left alone, and since removing a row shifts everything after it up
  // by one slot, that index now names whatever row moved into it (the item
  // that used to be immediately below the deleted one), or the new last row
  // if the deleted item was last. In effect: deleting the highlighted row
  // moves the highlight to the next row down. This is index-tracking, not
  // identity-tracking — deleting a row ABOVE the highlighted one silently
  // shifts which item that same index now points at too. That's a known,
  // out-of-scope limitation of an index-based hook, not something this
  // change tries to solve.
  useEffect(() => {
    setHighlightedIndex((i) => Math.min(Math.max(i, 0), Math.max(items.length - 1, 0)));
  }, [items.length]);

  useEffect(() => {
    if (!active) return;
    function onKeyDown(e: KeyboardEvent) {
      if (isTypingTarget(e.target)) return;
      if (isOpenDialogPresent()) return;
      if (e.key === "j") {
        if (items.length === 0) return;
        e.preventDefault();
        setHighlightedIndex((i) => Math.min(items.length - 1, i + 1));
      } else if (e.key === "k") {
        if (items.length === 0) return;
        e.preventDefault();
        setHighlightedIndex((i) => Math.max(0, i - 1));
      } else if (e.key === "Enter") {
        if (isActivatingControl(e.target)) return;
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
