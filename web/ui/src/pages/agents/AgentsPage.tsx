import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bot, Plus, Search } from "lucide-react";
import { PageTitle } from "@/components/shell/PageTitle";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { timeAgo } from "@/lib/utils";
import { useAgents, type Agent, type AgentDraft } from "@/lib/agents";
import { StatusChip } from "./StatusChip";

function AgentCard({ agent }: { agent: Agent }) {
  return (
    <Link
      to={`/agents/${agent.id}`}
      className="flex flex-col gap-2 rounded-lg border border-border bg-background p-4 text-left transition-colors hover:border-primary/40 hover:shadow-sm"
    >
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-semibold leading-tight">{agent.name}</h3>
        <StatusChip agent={agent} />
      </div>
      <p className="line-clamp-2 flex-1 text-sm leading-relaxed text-muted-2">
        {agent.description || <em className="text-muted-2/70">No description</em>}
      </p>
      <p className="text-xs text-muted-2/80">Created {timeAgo(agent.created_at)}</p>
    </Link>
  );
}

// Draft discard isn't part of useAgentActions' binding contract (Task 6/7
// import those 7 methods by name) — it's a standalone mutation local to the
// list page.
function useDismissDraft() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ status: string }>("/api/v1/agents/design/dismiss"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agents"] }),
  });
}

function DraftCard({ draft }: { draft: AgentDraft }) {
  const dismiss = useDismissDraft();
  if (!draft) return null;
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-dashed border-warn/50 bg-background p-4">
      <div className="flex items-start justify-between gap-2">
        <h3 className="font-semibold leading-tight">{draft.agent_name || "Untitled draft"}</h3>
        <span className="shrink-0 rounded-full bg-warn-soft px-2 py-0.5 text-xs font-medium text-warn">
          Draft
        </span>
      </div>
      <p className="flex-1 text-sm leading-relaxed text-muted-2">
        {draft.state === "verifying"
          ? "Ready to approve — generated and tested, awaiting your final OK."
          : "In progress — describe what to change, or resume to continue."}
        {draft.is_edit && <span className="block text-muted-2/70">Editing an existing agent.</span>}
      </p>
      {draft.updated_at && (
        <p className="text-xs text-muted-2/80">Last edited {timeAgo(draft.updated_at)}</p>
      )}
      <div className="mt-1 flex items-center justify-between gap-2">
        <Button
          variant="ghost"
          size="sm"
          className="text-danger"
          disabled={dismiss.isPending}
          onClick={() => dismiss.mutate()}
        >
          Discard
        </Button>
        <Button asChild size="sm">
          <Link to="/agents/new?resume=1">Resume</Link>
        </Button>
      </div>
    </div>
  );
}

function EmptyState() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center text-muted-2">
      <Bot className="size-10" />
      <h2 className="text-lg font-bold text-foreground">No agents yet</h2>
      <p className="max-w-sm text-sm">
        Create your first agent by describing what you want it to do in plain language.
      </p>
      <Button asChild>
        <Link to="/agents/new">
          <Plus /> Create your first agent
        </Link>
      </Button>
    </div>
  );
}

export default function AgentsPage() {
  const { data } = useAgents();
  const [query, setQuery] = useState("");

  const agents = data?.agents ?? [];
  const draft = data?.draft ?? null;

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return agents;
    return agents.filter(
      (a) => a.name.toLowerCase().includes(q) || a.description.toLowerCase().includes(q),
    );
  }, [agents, query]);

  const showEmpty = agents.length === 0 && !draft;

  return (
    <div className="flex h-full flex-col p-6">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <PageTitle
          icon="agents"
          title="Agents"
          subtitle={
            agents.length > 0
              ? `${agents.length} agent${agents.length > 1 ? "s" : ""} configured`
              : undefined
          }
        />
        <div className="flex items-center gap-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-2" />
            <Input
              aria-label="Search agents"
              placeholder="Search agents…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="w-56 pl-8"
            />
          </div>
          <Button asChild>
            <Link to="/agents/new">
              <Plus /> New agent
            </Link>
          </Button>
        </div>
      </div>

      {showEmpty ? (
        <EmptyState />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {draft && <DraftCard draft={draft} />}
          {filtered.map((a) => (
            <AgentCard key={a.id} agent={a} />
          ))}
        </div>
      )}
    </div>
  );
}
