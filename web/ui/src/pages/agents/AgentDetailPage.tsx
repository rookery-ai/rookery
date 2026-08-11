import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { MoreHorizontal, Trash2 } from "lucide-react";
import { PageTitle } from "@/components/shell/PageTitle";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn, timeAgo } from "@/lib/utils";
import { useAgentActions, useAgentDetail, type AgentRun } from "@/lib/agents";
import { StatusChip } from "./StatusChip";
import { RunPanel } from "./RunPanel";
import { AgentMDCard } from "./AgentMDCard";
import { ScheduleCard } from "./ScheduleCard";
import { SkillsCard, ConnectionsCard, MCPServersCard } from "./AttachmentCards";

function RunStatusChip({ status }: { status: AgentRun["status"] }) {
  if (status === "running") {
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
        status === "success"
          ? "bg-ok-soft text-ok"
          : "bg-danger-soft text-danger",
      )}
    >
      {status === "success" ? "OK" : "Failed"}
    </span>
  );
}

// mm:ss between two ISO timestamps, or "" if either is unparsable — a run's
// duration is only shown once it has a finished_at.
function formatDuration(startedAt: string, finishedAt: string): string {
  const ms = new Date(finishedAt).getTime() - new Date(startedAt).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "";
  const totalSec = Math.floor(ms / 1000);
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function RunRow({ run }: { run: AgentRun }) {
  const [expanded, setExpanded] = useState(false);
  const output = [run.stdout, run.stderr].filter(Boolean).join("\n\n");
  const duration = run.finished_at
    ? formatDuration(run.started_at, run.finished_at)
    : "";

  return (
    <li className="flex flex-col gap-1.5 py-2.5">
      <div className="flex items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <RunStatusChip status={run.status} />
          <span className="text-xs text-muted-2">{run.trigger}</span>
          <span className="text-xs text-muted-2">
            {timeAgo(run.started_at)}
          </span>
          {duration && <span className="text-xs text-muted-2">{duration}</span>}
        </div>
        {output && (
          <button
            type="button"
            className="shrink-0 text-xs text-muted-2 hover:text-foreground"
            onClick={() => setExpanded((e) => !e)}
          >
            {expanded ? "Hide output" : "Show output"}
          </button>
        )}
      </div>
      {expanded && output && (
        <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-md bg-chrome p-2 font-mono text-xs">
          {output}
        </pre>
      )}
    </li>
  );
}

export default function AgentDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const { data: detail } = useAgentDetail(id);
  const { del } = useAgentActions();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);

  if (!detail) return <div className="p-8 text-muted-2">Loading…</div>;

  const {
    agent,
    schedule,
    runs,
    agent_md,
    missing_secrets,
    live_run,
    attached_skills,
    core_skills,
    all_skills,
    workspace_connections,
    attached_connection_ids,
  } = detail;

  async function handleDelete() {
    setDeleting(true);
    try {
      await del(id);
      navigate("/agents");
    } finally {
      setDeleting(false);
      setDeleteOpen(false);
    }
  }

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-1 flex items-start justify-between gap-3">
        <div className="flex items-center gap-3">
          <PageTitle icon="agents" title={agent.name} />
          <StatusChip agent={agent} />
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button asChild variant="outline" size="sm">
            <Link to={`/agents/${id}/edit`}>Edit</Link>
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label="Agent actions"
                className="shrink-0 rounded p-1.5 hover:bg-border"
              >
                <MoreHorizontal className="size-4" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                variant="destructive"
                onSelect={() => setDeleteOpen(true)}
              >
                Delete…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {agent.description && (
        <p className="mb-6 max-w-2xl text-sm text-muted-2">
          {agent.description}
        </p>
      )}

      <div className="grid grid-cols-1 gap-6 md:grid-cols-[minmax(0,1fr)_320px] md:mt-6">
        <div className="flex min-w-0 flex-col gap-6">
          <RunPanel agentId={id} agentName={agent.name} liveRun={live_run} />

          <div className="flex flex-col gap-2 rounded-lg border border-border bg-background p-4">
            <h2 className="text-sm font-semibold">Run history</h2>
            {runs.length === 0 ? (
              <p className="text-xs text-muted-2">No runs yet.</p>
            ) : (
              <ul className="flex flex-col divide-y divide-border">
                {runs.map((r) => (
                  <RunRow key={r.id} run={r} />
                ))}
              </ul>
            )}
          </div>

          <AgentMDCard
            agentId={id}
            content={agent_md}
            missingSecrets={missing_secrets}
          />
        </div>

        <div className="flex flex-col gap-6">
          <ScheduleCard agentId={id} schedule={schedule} />
          <SkillsCard
            agentId={id}
            attachedSkills={attached_skills}
            coreSkills={core_skills}
            allSkills={all_skills}
          />
          <ConnectionsCard
            agentId={id}
            attachedConnectionIds={attached_connection_ids}
            connections={workspace_connections}
          />
          <MCPServersCard agentId={id} />
        </div>
      </div>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>Delete &ldquo;{agent.name}&rdquo;?</DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-2">This can&rsquo;t be undone.</p>
          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setDeleteOpen(false)}
              disabled={deleting}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => void handleDelete()}
              disabled={deleting}
            >
              <Trash2 />
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
