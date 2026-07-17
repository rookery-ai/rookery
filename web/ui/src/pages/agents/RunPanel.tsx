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

  const sseHandleRef = useRef<SSEHandle | null>(null);
  const startedAtRef = useRef(0);

  function attach() {
    if (sseHandleRef.current) return;
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
        setSse((s) => (s ? { ...s, status: "error" } : s));
        sseHandleRef.current = null;
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
    if (liveRun) attach();
    return () => {
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
    setIsRunning(true);
    try {
      await run(agentId);
      attach();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setIsRunning(false);
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
