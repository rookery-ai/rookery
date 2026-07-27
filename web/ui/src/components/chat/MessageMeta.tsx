import { useEffect, useRef, useState } from "react";
import { Check, Copy, X } from "lucide-react";
import { cn, formatMessageTime } from "@/lib/utils";
import { useTimeZone } from "@/lib/timezone";

const COPIED_MS = 1500;

// The per-message footer: a small timestamp and a copy button under each
// bubble, revealed on hover.
//
// It is ALWAYS mounted and only its opacity changes. Rendering it on hover
// instead would insert a node under the cursor mid-gesture, reflowing the
// bubble and cancelling an in-progress drag-select — the exact thing hover
// affordances are expected not to do. `select-none` is scoped to this row so
// the chrome never joins a selection of the message text, and is never applied
// to the message body itself.
//
// focus-within/focus-visible keep the button reachable by keyboard: tabbing to
// it makes the row visible even with no pointer anywhere near it.
export function MessageMeta({ content, createdAt }: { content: string; createdAt?: string }) {
  const timeZone = useTimeZone();
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  const timerRef = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timerRef.current), []);

  // legacyCopy is the non-secure-context path, and for this app it is the
  // MAIN path, not an exotic fallback: a self-hosted install is reached over
  // plain HTTP on the LAN (http://<host>:8080), and `navigator.clipboard` only
  // exists in a secure context (https, or localhost). Without this the button
  // did nothing at all.
  //
  // document.execCommand("copy") is deprecated but is the only API that works
  // without a secure context, and it copies the current SELECTION — hence the
  // off-screen textarea, and hence restoring whatever the user had selected
  // afterwards, so copying a message never eats their in-progress selection.
  function legacyCopy(text: string): boolean {
    const ta = document.createElement("textarea");
    ta.value = text;
    // Off-screen rather than hidden: display:none / visibility:hidden makes the
    // node unselectable, so the copy silently yields an empty clipboard.
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.top = "-9999px";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    const previous = document.getSelection()?.rangeCount ? document.getSelection()!.getRangeAt(0) : null;
    ta.select();
    let ok = false;
    try {
      ok = document.execCommand("copy");
    } catch {
      ok = false;
    }
    document.body.removeChild(ta);
    if (previous) {
      const sel = document.getSelection();
      sel?.removeAllRanges();
      sel?.addRange(previous);
    }
    return ok;
  }

  async function copy() {
    let ok = false;
    try {
      // Optional-chained: in a non-secure context `navigator.clipboard` is
      // undefined, and reading `.writeText` off it throws before any copy is
      // attempted — which is exactly how this failed silently before.
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(content);
        ok = true;
      }
    } catch {
      ok = false; // permission denied, or a rejected write — fall through
    }
    if (!ok) ok = legacyCopy(content);

    // Report the outcome either way. A silent no-op on failure is what let the
    // original bug go unnoticed.
    setStatus(ok ? "copied" : "failed");
    window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => setStatus("idle"), COPIED_MS);
  }

  const label = status === "copied" ? "Copied" : status === "failed" ? "Copy failed" : "Copy message";
  return (
    <div
      data-testid="message-meta"
      className={cn(
        "mt-0.5 flex select-none items-center gap-1.5 px-1 text-[10px] leading-none text-muted-2",
        "opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100",
      )}
    >
      {createdAt && <span data-testid="message-time">{formatMessageTime(createdAt, timeZone)}</span>}
      <button
        type="button"
        aria-label={label}
        title={label}
        onClick={() => void copy()}
        className={cn(
          "rounded p-0.5 text-muted-2 hover:text-foreground",
          "focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
        )}
      >
        {status === "copied" ? <Check className="size-3" /> : status === "failed" ? <X className="size-3" /> : <Copy className="size-3" />}
      </button>
    </div>
  );
}
