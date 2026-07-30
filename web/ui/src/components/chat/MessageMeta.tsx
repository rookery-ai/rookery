import { useEffect, useRef, useState } from "react";
import { Check, Copy, X } from "lucide-react";
import { cn, formatMessageTime } from "@/lib/utils";
import { useTimeZone } from "@/lib/timezone";

import { copyText } from "@/lib/copyText";

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

  async function copy() {
    // The clipboard write (including the non-secure-context fallback that this
    // app depends on over plain HTTP) lives in lib/copyText, shared with the
    // connections wizard's redirect-URI copy button.
    const ok = await copyText(content);

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
        "mt-0.5 flex select-none items-center gap-1.5 px-1 text-xs leading-none text-muted-2",
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
