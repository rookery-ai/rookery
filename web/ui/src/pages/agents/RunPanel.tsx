import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ActivityCard, type ActivityStatus } from "@/components/chat/ActivityCard";
import { openSSE, type SSEHandle } from "@/lib/sse";
import { ApiError } from "@/lib/api";
import { useAgentActions } from "@/lib/agents";

type RunPanelProps = {
  agentId: string;
  agentName: string;
  // AgentDetail.live_run — a run started elsewhere (another tab, or the
  // manual-run POST that kicked this one off) is already streaming; attach
  // to it on mount instead of waiting for a click.
  liveRun: boolean;
};

// "▶ Run now" + the live SSE activity card for a manual run. useAgentActions'
// run() mutation already invalidates ["agent", id] on the 202 POST response
// (run just started, status flipped to running); this component invalidates
// again when the SSE stream reports done so the run-history list picks up
// the finished run without a manual refresh.
export function RunPanel({ agentId, agentName, liveRun }: RunPanelProps) {
  const { run } = useAgentActions();
  const qc = useQueryClient();
  const [isRunning, setIsRunning] = useState(false);
  const [sse, setSse] = useState<{ lines: string[]; status: ActivityStatus } | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Distinct from `error`: an "already running" response is informational
  // (this tab attaches to the live run), not a failure — it must not render
  // the red AlertTriangle banner.
  const [note, setNote] = useState<string | null>(null);

  const sseHandleRef = useRef<SSEHandle | null>(null);
  const startedAtRef = useRef(0);
  // Which situation the CURRENTLY-attaching SSE stream is for. "collision"
  // means attach() was called right after an already_running response — the
  // in-flight run may be a scheduled (cron) one, which has no in-memory SSE
  // producer (see web/run_tracker.go's isLiveRun vs isAgentRunning split), so
  // the stream 404s. That 404 isn't a real failure; onError below reads this
  // ref to render a muted informational note instead of the ActivityCard's
  // red error state.
  const attachContextRef = useRef<"own" | "collision">("own");
  // Guards state updates in handleRun's post-await continuation so a run
  // kicked off just before navigating away doesn't setState on an unmounted
  // component. Reset to false at the start of the mount effect (not just set
  // true in its cleanup) so React 18 StrictMode's dev double-invoke
  // (setup→cleanup→setup) doesn't leave it stuck true after the second setup.
  const unmountedRef = useRef(false);

  function attach(context: "own" | "collision" = "own") {
    if (sseHandleRef.current) return;
    attachContextRef.current = context;
    startedAtRef.current = Date.now();
    setSse({ lines: [], status: "live" });
    const handle = openSSE(`/api/v1/agents/${agentId}/run/progress`, {
      onMessage: (line) => setSse((s) => (s ? { ...s, lines: [...s.lines, line] } : s)),
      onDone: () => {
        setSse((s) => (s ? { ...s, status: "done" } : s));
        sseHandleRef.current = null;
        qc.invalidateQueries({ queryKey: ["agent", agentId] });
      },
      onError: () => {
        sseHandleRef.current = null;
        if (attachContextRef.current === "collision") {
          // No in-memory producer for this run (it was started by the
          // scheduler, not this tab) — the endpoint 404s, which is expected,
          // not a failure. Drop the activity card and show a plain note
          // instead of an error card under an "in progress" note that would
          // otherwise contradict each other. No polling here (simplest
          // option) — the run-history list picks up the finished run on the
          // next manual refresh/navigation, same as any other out-of-band run.
          setSse(null);
          setNote(
            "Another run is in progress — live progress isn't available for it. Refresh the page to check its status.",
          );
        } else {
          setSse((s) => (s ? { ...s, status: "error" } : s));
        }
      },
    });
    sseHandleRef.current = handle;
  }

  // Mount-only auto-attach (empty deps — deliberately does NOT react to later
  // `liveRun` flips, e.g. from the run-POST's own query invalidation right
  // after handleRun's own attach() already opened a stream: re-running this
  // effect on every liveRun change would tear that live stream down via the
  // cleanup below, right as it starts). If a run is live elsewhere at the
  // moment this page mounts, attach; the click path handles everything else.
  useEffect(() => {
    unmountedRef.current = false;
    if (liveRun) attach();
    return () => {
      unmountedRef.current = true;
      // Also null out the ref (not just close()) — StrictMode double-invokes
      // mount effects in dev, and this synchronous attach() would otherwise
      // see a stale non-null ref on the second setup and no-op, leaving the
      // card bound to the already-closed first stream.
      sseHandleRef.current?.close();
      sseHandleRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function handleRun() {
    setError(null);
    setNote(null);
    setIsRunning(true);
    try {
      const res = await run(agentId);
      if (unmountedRef.current) return;
      if (res.status === "already_running") {
        setNote("A run is already in progress");
        attach("collision");
      } else {
        attach("own");
      }
    } catch (err) {
      if (unmountedRef.current) return;
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      if (!unmountedRef.current) setIsRunning(false);
    }
  }

  const busy = isRunning || sse?.status === "live";

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-background p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold">Run</h2>
        <Button size="sm" onClick={() => void handleRun()} disabled={busy}>
          ▶ Run now
        </Button>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      {note && (
        <div className="rounded-md bg-muted px-3 py-2 text-xs text-muted-2">{note}</div>
      )}

      {sse && (
        <ActivityCard
          title={`Running ${agentName}…`}
          lines={sse.lines}
          status={sse.status}
          startedAt={startedAtRef.current}
          collapsible
        />
      )}
    </div>
  );
}

export default RunPanel;
