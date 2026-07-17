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
  useSaveAdminSettings,
  useAuditLog,
  useWorkspacePermissions,
  useSaveWorkspacePermissions,
  useDeleteWorkspaceAdmin,
  type AdminSettings,
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

function PermissionsEditor({ workspaceID }: { workspaceID: string }) {
  const { data, isLoading, isError, error } = useWorkspacePermissions(workspaceID);
  const save = useSaveWorkspacePermissions();
  const [granted, setGranted] = useState<Record<string, boolean>>({});
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!data) return;
    const m: Record<string, boolean> = {};
    for (const p of data.permissions) m[p.name] = p.granted;
    setGranted(m);
  }, [data]);

  async function handleSave() {
    setSaved(false);
    const grant = Object.entries(granted).filter(([, v]) => v).map(([n]) => n);
    const revoke = Object.entries(granted).filter(([, v]) => !v).map(([n]) => n);
    try {
      await save.mutateAsync({ id: workspaceID, grant, revoke });
      setSaved(true);
    } catch {
      // surfaced via save.error
    }
  }

  if (isLoading) return <p className="text-xs text-muted-2">Loading permissions…</p>;
  if (isError) return <ErrorNote message={errMsg(error)} />;

  return (
    <div className="mt-2 space-y-2 rounded-md border border-border p-3">
      <div className="flex flex-wrap gap-3">
        {(data?.permissions ?? []).map((p) => (
          <label key={p.name} className="flex items-center gap-1.5 text-xs">
            <input
              type="checkbox"
              checked={granted[p.name] ?? false}
              onChange={(e) => {
                setGranted((g) => ({ ...g, [p.name]: e.target.checked }));
                setSaved(false);
              }}
            />
            {p.name}
          </label>
        ))}
      </div>
      <div className="flex items-center gap-2">
        <Button type="button" size="sm" variant="outline" onClick={() => void handleSave()} disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save permissions"}
        </Button>
        {saved && (
          <span className="flex items-center gap-1 text-xs text-ok">
            <Check className="size-3" /> Saved
          </span>
        )}
      </div>
      {save.isError && <ErrorNote message={errMsg(save.error)} />}
    </div>
  );
}

function WorkspaceCard({ ws, activeID }: { ws: Workspace; activeID: string | undefined }) {
  const [expandedPerms, setExpandedPerms] = useState(false);
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
        <Button size="sm" variant="outline" onClick={() => setExpandedPerms((v) => !v)}>
          Permissions
        </Button>
      </div>

      {expandedPerms && <PermissionsEditor workspaceID={ws.id} />}

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

// ── System settings ──────────────────────────────────────────────────────

const EMPTY_ADMIN: AdminSettings = {
  claude_bin: "",
  coder_timeout: "",
  agent_timeout: "",
  memory_mb: "",
  sandbox_on: false,
  landlock_ready: false,
};

function SystemSettingsSection() {
  const { data, isLoading, isError, error } = useAdminSettings();
  const save = useSaveAdminSettings();
  const [form, setForm] = useState<AdminSettings>(EMPTY_ADMIN);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (data) setForm(data);
  }, [data]);

  function set<K extends keyof AdminSettings>(key: K, value: AdminSettings[K]) {
    setForm((f) => ({ ...f, [key]: value }));
    setSaved(false);
  }

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    setSaved(false);
    try {
      await save.mutateAsync({
        claude_bin: form.claude_bin,
        coder_timeout: form.coder_timeout,
        agent_timeout: form.agent_timeout,
        memory_mb: form.memory_mb,
      });
      setSaved(true);
    } catch {
      // surfaced via save.error
    }
  }

  return (
    <div>
      <h3 className="text-sm font-bold text-muted-2">System settings</h3>
      {isLoading && <p className="mt-2 text-xs text-muted-2">Loading…</p>}
      {isError && <div className="mt-2"><ErrorNote message={errMsg(error)} /></div>}
      {!isLoading && !isError && (
        <>
          <div className="mt-2 flex flex-wrap gap-4 text-xs text-muted-2">
            <span>
              Sandbox: <span className={form.sandbox_on ? "text-ok" : "text-muted-2"}>{form.sandbox_on ? "on" : "off"}</span>
            </span>
            <span>
              Landlock: <span className={form.landlock_ready ? "text-ok" : "text-muted-2"}>{form.landlock_ready ? "ready" : "unavailable"}</span>
            </span>
          </div>
          {save.isError && <div className="mt-3"><ErrorNote message={errMsg(save.error)} /></div>}
          <form onSubmit={(e) => void handleSave(e)} className="mt-3 max-w-lg space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="claude_bin">Claude binary path</Label>
              <Input id="claude_bin" value={form.claude_bin} onChange={(e) => set("claude_bin", e.target.value)} />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="coder_timeout">Coder timeout (s)</Label>
                <Input id="coder_timeout" value={form.coder_timeout} onChange={(e) => set("coder_timeout", e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="agent_timeout">Agent timeout (s)</Label>
                <Input id="agent_timeout" value={form.agent_timeout} onChange={(e) => set("agent_timeout", e.target.value)} />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="memory_mb">Memory limit (MB)</Label>
              <Input id="memory_mb" value={form.memory_mb} onChange={(e) => set("memory_mb", e.target.value)} />
            </div>
            <div className="flex items-center gap-3">
              <Button type="submit" size="sm" disabled={save.isPending}>
                {save.isPending ? "Saving…" : "Save"}
              </Button>
              {saved && (
                <span className="flex items-center gap-1 text-xs text-ok">
                  <Check className="size-3" /> Saved
                </span>
              )}
            </div>
          </form>
        </>
      )}
    </div>
  );
}

// ── Audit log ────────────────────────────────────────────────────────────

function AuditLogSection() {
  const { data, isLoading, isError, error } = useAuditLog(100);
  const logs = data?.logs ?? [];

  return (
    <div>
      <h3 className="text-sm font-bold text-muted-2">Audit log</h3>
      <p className="mt-1 text-xs text-muted-2">Last 100 events, most recent first.</p>
      {isLoading && <p className="mt-2 text-xs text-muted-2">Loading…</p>}
      {isError && <div className="mt-2"><ErrorNote message={errMsg(error)} /></div>}
      {!isLoading && !isError && logs.length === 0 && (
        <p className="mt-2 text-xs text-muted-2">No audit events yet.</p>
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
                    {l.workspace_id ? l.workspace_id.slice(0, 8) : "—"}
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
      <p className="mt-1 text-sm text-muted-2">Workspaces, audit log, and system settings.</p>

      <div className="mt-6 space-y-8">
        <WorkspacesSection />
        <SystemSettingsSection />
        <AuditLogSection />
      </div>
    </section>
  );
}
