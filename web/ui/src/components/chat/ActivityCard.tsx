import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";

export type ActivityStatus = "live" | "done" | "error";

type ActivityCardProps = {
  title: string;
  lines: string[];
  status: ActivityStatus;
  startedAt: number; // Date.now() at attach
  collapsible?: boolean;
};

function formatElapsed(ms: number): string {
  const totalSec = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

// Live build/run progress card fed by SSE milestones (design + run
// endpoints). Reused for both agent builds and agent runs — spec §7.
export function ActivityCard({ title, lines, status, startedAt, collapsible }: ActivityCardProps) {
  const [now, setNow] = useState(() => Date.now());
  // Elapsed stops advancing once the card leaves "live" — captured once,
  // the moment status changes away from live, rather than continuing to
  // tick against the wall clock.
  const [frozenElapsed, setFrozenElapsed] = useState<number | null>(null);
  const [collapsed, setCollapsed] = useState(false);

  useEffect(() => {
    if (status !== "live") {
      setFrozenElapsed((prev) => prev ?? Date.now() - startedAt);
      return;
    }
    setFrozenElapsed(null);
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [status, startedAt]);

  const elapsedMs = status === "live" ? now - startedAt : (frozenElapsed ?? now - startedAt);
  const elapsed = formatElapsed(elapsedMs);

  const lastLine = lines[lines.length - 1];
  const visibleLines = collapsible && collapsed ? (lastLine !== undefined ? [lastLine] : []) : lines;

  return (
    <div
      data-testid="activity-card"
      className={cn(
        // Width is the placing container's job (chat stream bubble vs. run
        // panel) — this card just fills whatever wrapper it's given.
        "w-full overflow-hidden rounded-xl border",
        status === "error" ? "border-danger" : "border-border",
      )}
    >
      <div
        className={cn(
          "flex items-center gap-2.5 px-3.5 py-2.5",
          status === "error" ? "bg-danger-soft text-danger" : "bg-chrome text-foreground",
        )}
      >
        <span
          data-testid="activity-status-dot"
          className={cn(
            "h-2 w-2 shrink-0 rounded-full",
            status === "error" && "bg-danger",
            status === "done" && "bg-ok",
            status === "live" && "bg-ok animate-pulse",
          )}
        />
        <b className="text-sm">{title}</b>
        <span data-testid="activity-elapsed" className="text-[11.5px] text-muted-2">
          {elapsed}
        </span>
        <div className="flex-1" />
        {collapsible && (
          <button
            type="button"
            data-testid="activity-toggle"
            onClick={() => setCollapsed((c) => !c)}
            className="text-[11px] text-muted-2 hover:text-muted"
          >
            {collapsed ? "▸ expand" : "▾ collapse"}
          </button>
        )}
      </div>

      {status === "live" && (
        <div className="h-1 bg-border">
          <div className="h-1 w-1/2 animate-pulse rounded-r-sm bg-ok" />
        </div>
      )}

      <div className="max-h-48 overflow-y-auto px-3.5 py-3 font-mono text-xs leading-6 text-muted">
        {visibleLines.map((line, i) => (
          <div key={i} className={cn(line.startsWith("✓") && "text-ok")}>
            {line}
          </div>
        ))}
      </div>
    </div>
  );
}
