import { cn } from "@/lib/utils";
import type { Agent } from "@/lib/agents";

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
