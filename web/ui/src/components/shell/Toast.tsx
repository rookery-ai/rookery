import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";

export type ToastVariant = "default" | "error";

export type ToastAction = { label: string; onClick: () => void };

export type ToastOptions = {
  message: string;
  action?: ToastAction;
  variant?: ToastVariant;
  durationMs?: number;
};

type ToastItem = ToastOptions & { id: number };

type ToastCtxValue = {
  toasts: ToastItem[];
  toast: (opts: ToastOptions) => () => void;
  dismiss: (id: number) => void;
};

const DEFAULT_DURATION_MS = 5000;

const ToastCtx = createContext<ToastCtxValue | null>(null);

// Module-level counter rather than useId/crypto.randomUUID: toasts are
// created outside render (event handlers), so a plain incrementing counter
// is simpler and collision-free within a page lifetime.
let nextToastId = 0;

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());

  const clearTimer = useCallback((id: number) => {
    const t = timers.current.get(id);
    if (t !== undefined) {
      clearTimeout(t);
      timers.current.delete(id);
    }
  }, []);

  const dismiss = useCallback(
    (id: number) => {
      clearTimer(id);
      setToasts((prev) => prev.filter((t) => t.id !== id));
    },
    [clearTimer],
  );

  const toast = useCallback(
    (opts: ToastOptions) => {
      const id = nextToastId++;
      setToasts((prev) => [...prev, { ...opts, id }]);
      const duration = opts.durationMs ?? DEFAULT_DURATION_MS;
      timers.current.set(
        id,
        setTimeout(() => dismiss(id), duration),
      );
      return () => dismiss(id);
    },
    [dismiss],
  );

  const value = useMemo(() => ({ toasts, toast, dismiss }), [toasts, toast, dismiss]);

  return <ToastCtx.Provider value={value}>{children}</ToastCtx.Provider>;
}

// Contract consumed by Task 3 (undoable deletes) — do not change this shape.
export function useToast() {
  const ctx = useContext(ToastCtx);
  if (!ctx) throw new Error("useToast must be used within a ToastProvider");
  return { toast: ctx.toast };
}

// The app's single aria-live region: mount this once (inside ToastProvider)
// regardless of whether any toast is currently showing. A region that only
// appears once there's content to announce is frequently missed by screen
// readers — it must already be in the accessibility tree before the content
// arrives.
export function ToastHost() {
  const ctx = useContext(ToastCtx);
  if (!ctx) throw new Error("ToastHost must be used within a ToastProvider");
  const { toasts, dismiss } = ctx;
  const latest = toasts[toasts.length - 1];

  return (
    <>
      <div aria-live="polite" aria-atomic="true" className="sr-only">
        {latest?.message ?? ""}
      </div>
      <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            // No live-region role here — role="status" is an IMPLICIT
            // aria-live="polite" region, which would double-announce this
            // toast (once here, once via the dedicated sr-only region
            // above). That sr-only region is the app's one live region by
            // design (see its comment) and is the only thing screen readers
            // should hear; this div is purely the sighted visual.
            className={cn(
              "pointer-events-auto flex max-w-sm items-center gap-3 rounded-md border px-3 py-2 text-sm shadow-lg",
              "motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-2",
              t.variant === "error"
                ? "border-destructive/40 bg-destructive text-white"
                : "border-border bg-chrome text-foreground",
            )}
          >
            <span className="flex-1">{t.message}</span>
            {t.action && (
              <button
                type="button"
                className="shrink-0 font-medium underline underline-offset-2 hover:no-underline"
                onClick={() => {
                  t.action!.onClick();
                  dismiss(t.id);
                }}
              >
                {t.action.label}
              </button>
            )}
            <button
              type="button"
              aria-label="Dismiss"
              className="shrink-0 text-current/70 hover:text-current"
              onClick={() => dismiss(t.id)}
            >
              <X className="size-3.5" />
            </button>
          </div>
        ))}
      </div>
    </>
  );
}
