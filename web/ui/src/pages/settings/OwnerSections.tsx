import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { AlertTriangle, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { timeAgo } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { useSession } from "@/lib/session";
import { CreateWorkspaceDialog } from "@/pages/Workspaces";
import {
  useAdminSettings,
  useAuditLog,
  useDeleteWorkspaceAdmin,
} from "@/lib/settings";
import type { Workspace } from "@/lib/session";

function errMsg(err: unknown) {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

function ErrorNote({ message }: { message: string }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {message}
    </div>
  );
}

// ── Workspaces ───────────────────────────────────────────────────────────

function WorkspaceCard({ ws, activeID }: { ws: Workspace; activeID: string | undefined }) {
  const [confirming, setConfirming] = useState(false);
  const del = useDeleteWorkspaceAdmin();
  const isActive = ws.id === activeID;

  async function handleDelete() {
    try {
      await del.mutateAsync(ws.id);
      setConfirming(false);
    } catch {
      // surfaced via del.error
    }
  }

  return (
    <div className="rounded-lg border border-border bg-background p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-semibold">
            {ws.name}
            {isActive && <span className="ml-2 text-xs font-normal text-muted-2">(active)</span>}
          </div>
          {ws.about && <div className="truncate text-xs text-muted-2">{ws.about}</div>}
        </div>
      </div>

      <div className="mt-3">
        {!confirming ? (
          <Button size="sm" variant="outline" className="text-danger" onClick={() => setConfirming(true)}>
            Delete
          </Button>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-danger">
              Delete “{ws.name}”?{" "}
              {isActive && "This is your ACTIVE workspace — you'll be logged out of it."}
            </span>
            <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
            <Button size="sm" variant="destructive" onClick={() => void handleDelete()} disabled={del.isPending}>
              Yes, delete
            </Button>
          </div>
        )}
        {del.isError && <div className="mt-2"><ErrorNote message={errMsg(del.error)} /></div>}
      </div>
    </div>
  );
}

function WorkspacesSection() {
  const { data: session } = useSession();
  const [creating, setCreating] = useState(false);
  const workspaces = session?.workspaces ?? [];
  const activeID = session?.workspace?.id;

  return (
    <div>
      <h3 className="text-sm font-bold text-muted-2">Workspaces</h3>
      <div className="mt-2 space-y-3">
        {workspaces.map((ws) => (
          <WorkspaceCard key={ws.id} ws={ws} activeID={activeID} />
        ))}
      </div>
      <Button size="sm" variant="outline" className="mt-3" onClick={() => setCreating(true)}>
        + Create workspace
      </Button>
      <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
    </div>
  );
}

// ── System status ────────────────────────────────────────────────────────

// Runtime status only. This section used to be a form over claude_bin /
// coder_timeout / agent_timeout / memory_mb, all persisted to system_settings
// and none of them ever read back — the coder binary and timeout come from
// config.yaml, the per-workspace timeout from the workspace row, and the
// sandbox memory cap from the sandbox config. The inputs were removed rather
// than wired up, leaving the two indicators that report something real.
function SystemStatusSection() {
  const { data, isLoading, isError, error } = useAdminSettings();

  const sandboxOn = data?.sandbox_on ?? false;
  const landlockReady = data?.landlock_ready ?? false;

  return (
    <div>
      <h3 className="text-sm font-bold text-muted-2">System status</h3>
      <p className="mt-1 text-xs text-muted-2">
        Coder and sandbox settings come from <code>config.yaml</code> and each workspace's own
        coder configuration.
      </p>
      {isLoading && <p className="mt-2 text-xs text-muted-2">Loading…</p>}
      {isError && <div className="mt-2"><ErrorNote message={errMsg(error)} /></div>}
      {!isLoading && !isError && (
        <dl className="mt-3 flex flex-wrap gap-x-8 gap-y-2 text-xs">
          <div>
            <dt className="text-muted-2">Sandbox</dt>
            <dd className={sandboxOn ? "font-medium text-ok" : "font-medium text-muted-2"}>
              {sandboxOn ? "on" : "off"}
            </dd>
          </div>
          <div>
            <dt className="text-muted-2">Landlock</dt>
            <dd className={landlockReady ? "font-medium text-ok" : "font-medium text-muted-2"}>
              {landlockReady ? "ready" : "unavailable"}
            </dd>
          </div>
        </dl>
      )}
    </div>
  );
}

// ── Audit log ────────────────────────────────────────────────────────────

function AuditLogSection() {
  const { data: session } = useSession();
  const [action, setAction] = useState("");
  const [workspaceID, setWorkspaceID] = useState("");
  const [sinceDays, setSinceDays] = useState("");
  const [search, setSearch] = useState("");
  // The text box is debounced so typing doesn't fire a request per keystroke;
  // the dropdowns apply immediately because a single click is already the
  // user's final intent.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const { data, isLoading, isError, error } = useAuditLog({
    action: action || undefined,
    workspace_id: workspaceID || undefined,
    q: debouncedSearch || undefined,
    since_days: sinceDays ? Number(sinceDays) : undefined,
  });

  const logs = data?.logs ?? [];
  const workspaces = session?.workspaces ?? [];
  const workspaceNameById = new Map(workspaces.map((w) => [w.id, w.name] as const));
  const filtered = Boolean(action || workspaceID || sinceDays || debouncedSearch);

  function workspaceLabel(id: string) {
    if (!id) return "—";
    // A deleted workspace no longer appears in session.workspaces — fall
    // back to a short uuid rather than an empty/undefined cell.
    return workspaceNameById.get(id) ?? id.slice(0, 8);
  }

  function clearFilters() {
    setAction("");
    setWorkspaceID("");
    setSinceDays("");
    setSearch("");
  }

  const selectClass =
    "h-8 rounded-md border border-border bg-background px-2 text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring";

  return (
    <div>
      <h3 className="text-sm font-bold text-muted-2">Audit log</h3>
      <p className="mt-1 text-xs text-muted-2">
        Most recent first, up to 100 matching events.
      </p>

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Input
          aria-label="Search audit log"
          placeholder="Search target, detail or IP…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-8 w-56 text-xs"
        />
        <select
          aria-label="Filter by action"
          className={selectClass}
          value={action}
          onChange={(e) => setAction(e.target.value)}
        >
          <option value="">All actions</option>
          {(data?.actions ?? []).map((a) => (
            <option key={a} value={a}>{a}</option>
          ))}
        </select>
        <select
          aria-label="Filter by workspace"
          className={selectClass}
          value={workspaceID}
          onChange={(e) => setWorkspaceID(e.target.value)}
        >
          <option value="">All workspaces</option>
          {workspaces.map((w) => (
            <option key={w.id} value={w.id}>{w.name}</option>
          ))}
        </select>
        <select
          aria-label="Filter by time"
          className={selectClass}
          value={sinceDays}
          onChange={(e) => setSinceDays(e.target.value)}
        >
          <option value="">Any time</option>
          <option value="1">Last 24 hours</option>
          <option value="7">Last 7 days</option>
          <option value="30">Last 30 days</option>
        </select>
        {filtered && (
          <Button type="button" size="sm" variant="outline" onClick={clearFilters}>
            Clear
          </Button>
        )}
      </div>

      {isLoading && <p className="mt-2 text-xs text-muted-2">Loading…</p>}
      {isError && <div className="mt-2"><ErrorNote message={errMsg(error)} /></div>}
      {!isLoading && !isError && logs.length === 0 && (
        <p className="mt-2 text-xs text-muted-2">
          {filtered ? "No events match these filters." : "No audit events yet."}
        </p>
      )}
      {!isLoading && !isError && logs.length > 0 && (
        <div className="mt-2 overflow-x-auto rounded-md border border-border">
          <table className="w-full text-left text-xs">
            <thead className="bg-chrome text-muted-2">
              <tr>
                <th className="px-3 py-2 font-medium">Time</th>
                <th className="px-3 py-2 font-medium">Workspace</th>
                <th className="px-3 py-2 font-medium">Action</th>
                <th className="px-3 py-2 font-medium">Target</th>
              </tr>
            </thead>
            <tbody>
              {logs.map((l, i) => (
                <tr key={i} className="border-t border-border">
                  <td className="whitespace-nowrap px-3 py-2 text-muted-2">{timeAgo(l.created_at)}</td>
                  <td className="whitespace-nowrap px-3 py-2 font-mono text-muted-2">
                    {workspaceLabel(l.workspace_id)}
                  </td>
                  <td className="whitespace-nowrap px-3 py-2">{l.action}</td>
                  <td className="px-3 py-2 text-muted-2">{l.target}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

// ── Page ─────────────────────────────────────────────────────────────────

export function OwnerSections() {
  return (
    <section>
      <h2 className="text-lg font-bold">Owner</h2>
      <p className="mt-1 text-sm text-muted-2">Workspaces, system status, and the audit log.</p>

      <div className="mt-6 space-y-8">
        <WorkspacesSection />
        <SystemStatusSection />
        <AuditLogSection />
      </div>
    </section>
  );
}
