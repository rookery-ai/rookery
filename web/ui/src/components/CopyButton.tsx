import { useEffect, useRef, useState } from "react";

import { copyText } from "@/lib/copyText";

const COPIED_MS = 1500;

/**
 * CopyButton copies a value and reports the outcome.
 *
 * It always reports — including failure. A silent no-op is what let the
 * original broken copy button go unnoticed for so long.
 */
export function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  const timerRef = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timerRef.current), []);

  async function onClick() {
    const ok = await copyText(value);
    setStatus(ok ? "copied" : "failed");
    window.clearTimeout(timerRef.current);
    timerRef.current = window.setTimeout(() => setStatus("idle"), COPIED_MS);
  }

  return (
    <button
      type="button"
      onClick={onClick}
      className="shrink-0 rounded border border-border px-1.5 py-0.5 text-[11px] text-muted-2 transition-colors hover:border-primary/40 hover:text-foreground"
    >
      {status === "copied" ? "Copied" : status === "failed" ? "Copy failed" : label}
    </button>
  );
}
