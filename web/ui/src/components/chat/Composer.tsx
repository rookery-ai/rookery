import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { cn } from "@/lib/utils";

const MIN_ROWS = 1;
const MAX_ROWS = 6;

type ComposerProps = {
  onSend: (value: string) => void;
  busy?: boolean;
  placeholder?: string;
  autoFocus?: boolean;
  // Bump this (e.g. a counter) to imperatively focus the textarea — used by
  // DesignerSurface's "Make changes"/"Request changes" quick-action buttons,
  // which live outside the composer and have no ref of their own to grab.
  // Optional + additive: existing callers (ChatWindow, GlobalChatPanel) never
  // pass it and see no behavior change.
  focusSignal?: number;
  // Seeds (and re-seeds) the textarea's text — used by the ⌘K command
  // palette's "Ask assistant about '<query>'" action to prefill the global
  // chat composer. Same signal-on-change contract as focusSignal: an effect
  // keyed on this value, not plain useState initial-value, so it also fires
  // when the CALLER passes a new string into an already-mounted Composer
  // (e.g. the palette re-opened with a different query while the chat panel
  // stayed mounted) — a lazy useState initializer would only apply on the
  // very first mount and miss that case. Undefined (the default for every
  // existing caller) never touches `value`, so this is purely additive.
  initialText?: string;
  // Optional content rendered before the textarea, in the same row — used by
  // ChatWindow's attach-file button so it sits inline with Send rather than
  // in a second bar (which would double the border and vertical space).
  // Undefined (every caller but ChatWindow) renders nothing, so this is
  // purely additive.
  leftSlot?: ReactNode;
};

export function Composer({ onSend, busy, placeholder, autoFocus, focusSignal, initialText, leftSlot }: ComposerProps) {
  const [value, setValue] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);
  // Set by send() when the textarea itself was the active element at the
  // moment of sending — i.e. the user pressed Enter, not clicked Send. The
  // `disabled={busy}` below blurs a focused textarea (a browser behavior, not
  // ours) and re-enabling never restores focus, so an Enter-send silently
  // cost the caret. Restored by the effect below when busy clears.
  const wasFocusedRef = useRef(false);

  useEffect(() => {
    if (initialText === undefined) return;
    // Only seed an EMPTY composer. GlobalChatPanel isn't remounted when the
    // slide-over's content is swapped for another <GlobalChatPanel
    // initialText=.../> (AppShell re-renders the same node in place, same
    // component type, new props) — so re-invoking the ⌘K palette's "Ask
    // assistant" with a different query while a draft is already sitting in
    // the composer must never clobber it. A functional update (reading
    // `current` from React, not the closed-over `value`) keeps this correct
    // without needing `value` in the dependency array — depending on `value`
    // directly would re-run (and re-check) on every keystroke, which is
    // unnecessary since only a change to `initialText` itself should ever
    // trigger a seed attempt.
    setValue((current) => (current.trim() === "" ? initialText : current));
  }, [initialText]);

  useEffect(() => {
    if (focusSignal === undefined) return;
    ref.current?.focus();
    // Only re-run when the signal itself changes — mount-time autoFocus is a
    // separate concern handled by the autoFocus prop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusSignal]);

  // Deliberately separate from the caller-driven `focusSignal` above: this is
  // internal restoration of focus the component itself took away, not a
  // caller asking for focus. Clicking Send never triggers it (activeElement
  // was the button at send time), so the button doesn't steal focus back —
  // which is exactly the asymmetry we want.
  useEffect(() => {
    if (busy) return;
    if (!wasFocusedRef.current) return;
    wasFocusedRef.current = false;
    ref.current?.focus();
  }, [busy]);

  function autosize(el: HTMLTextAreaElement) {
    const style = window.getComputedStyle(el);
    const lineHeight = parseFloat(style.lineHeight || "20") || 20;
    el.style.height = "auto";
    const maxHeight = lineHeight * MAX_ROWS;
    el.style.height = `${Math.min(el.scrollHeight, maxHeight)}px`;
  }

  function send() {
    if (busy) return;
    const trimmed = value.trim();
    if (!trimmed) return;
    // Captured BEFORE onSend, which may synchronously flip `busy` true and
    // blur the textarea — by the time the effect above runs, activeElement is
    // already gone.
    wasFocusedRef.current = document.activeElement === ref.current;
    onSend(trimmed);
    setValue("");
    const el = ref.current;
    if (el) {
      el.style.height = "auto";
    }
  }

  function handleKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    // isComposing guards IME composition (e.g. Japanese/Chinese input
    // methods use Enter to confirm a candidate, not to submit).
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      send();
    }
  }

  return (
    <div className="flex items-end gap-2 border-t border-border p-3">
      {leftSlot}
      <textarea
        ref={ref}
        rows={MIN_ROWS}
        value={value}
        disabled={busy}
        autoFocus={autoFocus}
        placeholder={placeholder ?? "Message…"}
        onChange={(e) => {
          setValue(e.target.value);
          autosize(e.target);
        }}
        onKeyDown={handleKeyDown}
        className={cn(
          "flex-1 resize-none rounded-lg border border-border bg-background px-3 py-2 text-sm",
          "focus:outline-none focus:ring-2 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        )}
      />
      <button
        type="button"
        onClick={send}
        disabled={busy || !value.trim()}
        className={cn(
          "rounded-lg bg-foreground px-3 py-2 text-sm font-medium text-background",
          "disabled:cursor-not-allowed disabled:opacity-40",
        )}
      >
        Send
      </button>
    </div>
  );
}
