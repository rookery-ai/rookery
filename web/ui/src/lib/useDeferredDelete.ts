import { useCallback, useEffect, useRef, useState } from "react";
import { useToast } from "@/components/shell/Toast";

// §6.2 of the everyday-feel design: deferred-commit undo. The API call is
// NOT made when the user clicks delete — it's made when the 5s undo window
// expires. Undo means the call is never made at all. This needs no
// soft-delete schema change: the "delete" is just a pending timer that
// either fires (commit) or is cancelled (undo) client-side.
const UNDO_WINDOW_MS = 5000;

export type UseDeferredDeleteOptions = {
  /** Performs the real delete. Called only on expiry or flush, never on schedule. */
  commit: (id: string) => Promise<unknown>;
  /** Called when a committed delete fails, so the caller can bring the row back
   *  (e.g. re-invalidate the query it came from). */
  onRestore: (id: string) => void;
};

export function useDeferredDelete({ commit, onRestore }: UseDeferredDeleteOptions) {
  const { toast } = useToast();
  const [pending, setPending] = useState<Set<string>>(new Set());
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>());

  // commit/onRestore are typically fresh closures every render (mutation
  // objects, query invalidators). Timers scheduled with the closure captured
  // at schedule() time would call a stale one; refs keep run() calling
  // whatever the caller most recently passed in.
  const commitRef = useRef(commit);
  const onRestoreRef = useRef(onRestore);
  commitRef.current = commit;
  onRestoreRef.current = onRestore;

  // run() is idempotent: if the id isn't in the timers map anymore (already
  // committed, or cancelled via Undo) this is a no-op. That's what makes
  // expiry racing a flush (or a flush racing a second flush) safe.
  const run = useCallback(
    async (id: string) => {
      const timer = timers.current.get(id);
      if (timer === undefined) return;
      clearTimeout(timer);
      timers.current.delete(id);

      try {
        await commitRef.current(id);
        // Success: leave `id` in `pending`. The caller's list source (a
        // react-query cache, typically) will drop the row once its own
        // invalidation lands; removing it from `pending` first would just
        // flash the row back for a frame before that refetch resolves.
      } catch {
        setPending((prev) => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
        onRestoreRef.current(id);
        toast({ variant: "error", message: "Couldn't delete — it's back in the list." });
      }
    },
    [toast],
  );

  const cancel = useCallback((id: string) => {
    const timer = timers.current.get(id);
    if (timer !== undefined) clearTimeout(timer);
    timers.current.delete(id);
    setPending((prev) => {
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
  }, []);

  const schedule = useCallback(
    (id: string, label: string) => {
      setPending((prev) => new Set(prev).add(id));
      toast({
        message: label,
        action: { label: "Undo", onClick: () => cancel(id) },
      });
      const timer = setTimeout(() => {
        void run(id);
      }, UNDO_WINDOW_MS);
      timers.current.set(id, timer);
    },
    [toast, cancel, run],
  );

  const flushAll = useCallback(() => {
    for (const id of timers.current.keys()) {
      void run(id);
    }
  }, [run]);

  // Commit every pending delete on navigation away or tab close. A pending
  // delete is never silently dropped: `beforeunload` covers the tab-close
  // case, and the cleanup (fired on unmount — e.g. a route change away from
  // this page) covers in-app navigation.
  //
  // Deliberately `[]`: this must run exactly once on mount / once on
  // unmount, not on every re-render. flushAll/run close over refs
  // (commitRef/onRestoreRef) and the stable toast function, so they don't
  // need to be in the dep array — and putting them there would make the
  // cleanup (which calls flushAll) fire on every re-render, committing
  // pending deletes well before the 5s window is up.
  useEffect(() => {
    window.addEventListener("beforeunload", flushAll);
    return () => {
      window.removeEventListener("beforeunload", flushAll);
      flushAll();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { schedule, flushAll, pending };
}
