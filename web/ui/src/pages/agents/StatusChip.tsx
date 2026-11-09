import { cn } from "@/lib/utils";
import type { Agent, AgentRun } from "@/lib/agents";

// Shared between AgentsPage (card grid) and AgentDetailPage (header) — kept
// in its own file so neither page owns the other's import.
export function StatusChip({ agent }: { agent: Agent }) {
  if (agent.running) {
    return (
      <span className="flex shrink-0 items-center gap-1 rounded-full bg-warn-soft px-2 py-0.5 text-xs font-medium text-warn">
        <span className="size-1.5 animate-pulse rounded-full bg-warn" />
        Running
      </span>
    );
  }
  return (
    <span
      className={cn(
        "shrink-0 rounded-full px-2 py-0.5 text-xs font-medium",
        agent.active ? "bg-ok-soft text-ok" : "bg-muted-surface text-foreground",
      )}
    >
      {agent.active ? "Active" : "Paused"}
    </span>
  );
}

// A run's outcome. "Limited" is warn-coloured rather than red: a spending limit
// did exactly what the owner configured, so nothing is broken. The tooltip
// carries the sentence naming WHICH limit.
export function RunStatusChip({
  status,
  reason,
}: {
  status: AgentRun["status"];
  reason?: string;
}) {
  if (status === "running") {
    return (
      <span className="flex shrink-0 items-center gap-1 rounded-full bg-warn-soft px-2 py-0.5 text-xs font-medium text-warn">
        <span className="size-1.5 animate-pulse rounded-full bg-warn" />
        Running
      </span>
    );
  }
  if (status === "limited") {
    return (
      <span
        title={reason || "A spending limit stopped this run."}
        className="shrink-0 rounded-full bg-warn-soft px-2 py-0.5 text-xs font-medium text-warn"
      >
        Limited
      </span>
    );
  }
  // Neutral, not red: the owner chose this, and nothing went wrong.
  if (status === "stopped") {
    return (
      <span className="shrink-0 rounded-full bg-muted-surface px-2 py-0.5 text-xs font-medium text-foreground">
        Stopped
      </span>
    );
  }
  return (
    <span
      className={cn(
        "shrink-0 rounded-full px-2 py-0.5 text-xs font-medium",
        status === "success" ? "bg-ok-soft text-ok" : "bg-danger-soft text-danger",
      )}
    >
      {status === "success" ? "OK" : "Failed"}
    </span>
  );
}
