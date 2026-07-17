import { useEffect, useRef, useState, type KeyboardEvent } from "react";
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
};

export function Composer({ onSend, busy, placeholder, autoFocus, focusSignal }: ComposerProps) {
  const [value, setValue] = useState("");
  const ref = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (focusSignal === undefined) return;
    ref.current?.focus();
    // Only re-run when the signal itself changes — mount-time autoFocus is a
    // separate concern handled by the autoFocus prop.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusSignal]);

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
