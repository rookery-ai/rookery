import { useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  Plus,
  RefreshCw,
  Server,
  Settings,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  statusLabel,
  useDeleteMCPServer,
  useMCPServers,
  useMCPTools,
  useSyncMCPServer,
  useUpdateMCPTool,
  type MCPServer,
} from "@/lib/mcp";
import { MCPServerWizard } from "./MCPServerWizard";

// MCPSection is the third top-level block on the connections page.
//
// Structurally it sits where "Chat apps" sits rather than among the service
// categories: those are DERIVED from the vendored provider registry
// (groupByCategory), while MCP servers are rows the owner created. There is no
// catalog to group by — which is the whole point of MCP.
export function MCPSection() {
  const serversQuery = useMCPServers();
  const [wizardFor, setWizardFor] = useState<MCPServer | "new" | null>(null);
  const servers = serversQuery.data ?? [];

  return (
    <>
      <div className="mb-4 flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-bold">MCP servers</h2>
          <p className="text-sm text-muted-2">
            Connect a Model Context Protocol server by URL and its tools become
            available to your agents and chat. Nothing ships with Rookery — the
            server tells us what it can do.
          </p>
        </div>
        <Button onClick={() => setWizardFor("new")} className="shrink-0">
          <Plus className="size-4" aria-hidden="true" />
          Add server
        </Button>
      </div>

      {serversQuery.isLoading && (
        <p className="text-sm text-muted-2">Loading MCP servers…</p>
      )}

      {!serversQuery.isLoading && servers.length === 0 && (
        <div className="rounded-lg border border-dashed border-chrome px-4 py-6 text-sm text-muted-2">
          No MCP servers yet. Add one with its URL and a token if it needs one —
          self-hosted servers on your own network work fine.
        </div>
      )}

      <div className="space-y-3">
        {servers.map((s) => (
          <ServerCard key={s.id} server={s} onEdit={() => setWizardFor(s)} />
        ))}
      </div>

      {wizardFor && (
        <MCPServerWizard
          server={wizardFor === "new" ? null : wizardFor}
          onClose={() => setWizardFor(null)}
        />
      )}
    </>
  );
}

function ServerCard({
  server,
  onEdit,
}: {
  server: MCPServer;
  onEdit: () => void;
}) {
  const [open, setOpen] = useState(false);
  const sync = useSyncMCPServer();
  const del = useDeleteMCPServer();
  const status = statusLabel(server.status);
  const [syncNote, setSyncNote] = useState<string | null>(null);

  async function runSync() {
    setSyncNote(null);
    const r = await sync.mutateAsync(server.id);
    if (r.error) {
      setSyncNote(r.error);
      return;
    }
    // Held-back tools are reported explicitly. A silent truncation reads as
    // "that is all the server had", which is exactly the wrong conclusion.
    const bits = [`${r.discovered ?? 0} tools`];
    if (r.added) bits.push(`${r.added} new`);
    if (r.missing) bits.push(`${r.missing} gone`);
    if (r.held_back)
      bits.push(`${r.held_back} left off — the per-server limit was reached`);
    setSyncNote(bits.join(", "));
  }

  return (
    <div className="rounded-lg border border-chrome">
      <div className="flex items-center gap-3 px-4 py-3">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="flex min-w-0 flex-1 items-center gap-3 text-left"
          aria-expanded={open}
        >
          {open ? (
            <ChevronDown className="size-4 shrink-0 text-muted-2" aria-hidden="true" />
          ) : (
            <ChevronRight className="size-4 shrink-0 text-muted-2" aria-hidden="true" />
          )}
          <Server className="size-4 shrink-0 text-muted-2" aria-hidden="true" />
          <span className="min-w-0">
            <span className="block truncate font-medium">{server.name}</span>
            <span className="block truncate text-xs text-muted-2">
              {server.url}
            </span>
          </span>
        </button>

        <span
          className={cn(
            "shrink-0 rounded-full px-2 py-0.5 text-xs font-medium",
            status.tone === "ok" && "bg-ok-soft text-ok",
            status.tone === "warn" && "bg-warn-soft text-warn",
            status.tone === "danger" && "bg-danger-soft text-danger",
          )}
        >
          {status.text}
        </span>
        <span className="shrink-0 text-xs text-muted-2">
          {server.active_tools}/{server.tool_count} tools
        </span>

        <Button
          variant="outline"
          size="sm"
          onClick={runSync}
          disabled={sync.isPending}
        >
          <RefreshCw
            className={cn("size-4", sync.isPending && "animate-spin")}
            aria-hidden="true"
          />
          {server.tool_count === 0 ? "Test & sync" : "Re-sync"}
        </Button>
        <Button variant="ghost" size="sm" onClick={onEdit} aria-label="Edit server">
          <Settings className="size-4" aria-hidden="true" />
        </Button>
        <Button
          variant="ghost"
          size="sm"
          aria-label="Delete server"
          onClick={() => {
            if (confirm(`Remove “${server.name}” and its cached tools?`))
              del.mutate(server.id);
          }}
        >
          <Trash2 className="size-4 text-danger" aria-hidden="true" />
        </Button>
      </div>

      {(syncNote || server.last_error) && (
        <p
          className={cn(
            "border-t border-chrome px-4 py-2 text-xs",
            syncNote && !server.last_error ? "text-muted-2" : "text-danger",
          )}
        >
          {syncNote ?? server.last_error}
        </p>
      )}

      {open && <ToolTable serverId={server.id} />}
    </div>
  );
}

// ToolTable is where the owner grants trust, one tool at a time.
//
// The descriptions shown here are UNTRUSTED remote text: they come from whoever
// runs the server, not from vendored YAML. Reading them before enabling a tool is
// the review step, which is why the table shows the description rather than only a
// name.
function ToolTable({ serverId }: { serverId: string }) {
  const toolsQuery = useMCPTools(serverId);
  const update = useUpdateMCPTool();
  const tools = toolsQuery.data?.tools ?? [];

  if (toolsQuery.isLoading)
    return <p className="border-t border-chrome px-4 py-3 text-sm text-muted-2">Loading tools…</p>;

  if (tools.length === 0)
    return (
      <p className="border-t border-chrome px-4 py-3 text-sm text-muted-2">
        No tools discovered yet. Run Test &amp; sync to ask the server what it can
        do.
      </p>
    );

  return (
    <div className="border-t border-chrome">
      <div className="grid grid-cols-[1fr_auto_auto_auto] items-center gap-3 px-4 py-2 text-xs font-semibold uppercase tracking-wide text-muted-2">
        <span>Tool</span>
        <span title="Offer this tool to agents and chat">On</span>
        <span title="Read-only tools may run during an agent build">Read-only</span>
        <span title="Ask before this tool runs">Approval</span>
      </div>
      {tools.map((t) => (
        <div
          key={t.id}
          className={cn(
            "grid grid-cols-[1fr_auto_auto_auto] items-center gap-3 border-t border-chrome px-4 py-2",
            t.missing && "opacity-60",
          )}
        >
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">
              {t.name}
              {t.missing && (
                <span className="ml-2 text-xs font-normal text-warn">
                  no longer offered by the server
                </span>
              )}
            </p>
            <p className="truncate text-xs text-muted-2">{t.description}</p>
          </div>
          <input
            type="checkbox"
            aria-label={`Enable ${t.name}`}
            checked={t.enabled}
            disabled={t.missing}
            onChange={(e) =>
              update.mutate({ serverId, toolId: t.id, enabled: e.target.checked })
            }
          />
          <input
            type="checkbox"
            aria-label={`Mark ${t.name} read-only`}
            checked={t.read_only}
            onChange={(e) =>
              update.mutate({
                serverId,
                toolId: t.id,
                read_only: e.target.checked,
              })
            }
          />
          <input
            type="checkbox"
            aria-label={`Require approval for ${t.name}`}
            checked={t.approval_mode === "approve"}
            onChange={(e) =>
              update.mutate({
                serverId,
                toolId: t.id,
                approval_mode: e.target.checked ? "approve" : "auto",
              })
            }
          />
        </div>
      ))}
      <p className="border-t border-chrome px-4 py-2 text-xs text-muted-2">
        Read-only tools may run while an agent is being built. Everything else is
        blocked during a build and runs only when the agent executes for real.
      </p>
    </div>
  );
}
