import { useEffect, useState } from "react";
import { AlertTriangle, Eraser, FlaskConical, LogIn, Plus, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SearchInput } from "@/components/ui/search-input";
import { timeAgo } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { entityIcon } from "@/lib/entityIcons";
import { useSession } from "@/lib/session";
import { WorkspaceAvatar } from "@/lib/workspaceIcons";
import { CreateWorkspaceDialog, EnterWorkspaceDialog } from "@/pages/Workspaces";
import { BackupWarningBanner } from "./BackupWarningBanner";
import {
  useAdminSettings,
  useAuditLog,
  useDeleteWorkspaceAdmin,
  usePublicURL,
  useSavePublicURL,
  useTestPublicURL,
} from "@/lib/settings";
import type { Workspace } from "@/lib/session";

// Each owner section is its own settings page now, so it carries a page-level
// heading with the same icon its nav entry uses (one shared map, so the two
// cannot disagree).
export function OwnerIcon({ slug }: { slug: string }) {
  const Icon = entityIcon(slug);
  return <Icon className="size-5 shrink-0 text-muted" />;
}

function errMsg(err: unknown) {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

function ErrorNote({ message }: { message: string }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-sm text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {message}
    </div>
  );
}

// ── Workspaces ───────────────────────────────────────────────────────────

function WorkspaceCard({
  ws,
  activeID,
}: {
  ws: Workspace;
  activeID: string | undefined;
}) {
  const [confirming, setConfirming] = useState(false);
  const [entering, setEntering] = useState(false);
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
        <div className="flex min-w-0 items-start gap-2.5">
          {/* WorkspaceAvatar already distinguishes unset (the Rookery mark) from
              an UNKNOWN slug (the initial) — an unknown value means a workspace
              configured by a NEWER build, where rendering the default would
              present that build's choice as the user's own. */}
          <WorkspaceAvatar
            name={ws.name}
            icon={ws.icon}
            className="mt-0.5 size-8 shrink-0"
          />
          <div className="min-w-0">
            <div className="truncate font-semibold">
              {ws.name}
              {isActive && (
                <span className="ml-2 text-xs font-normal text-muted-2">
                  (active)
                </span>
              )}
            </div>
            {ws.about && (
              <div className="truncate text-sm text-muted-2">{ws.about}</div>
            )}
          </div>
        </div>
      </div>

      <div className="mt-3">
        {!confirming ? (
          <div className="flex flex-wrap items-center gap-2">
            {/* Entering was reachable only from the workspace picker and the
                shell menu, so this page listed workspaces it could not open —
                and with Delete as its sole control, the one thing it offered
                was the destructive one. */}
            {!isActive && (
              <Button size="sm" onClick={() => setEntering(true)}>
                <LogIn />
                Enter
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              className="text-danger"
              onClick={() => setConfirming(true)}
            >
              <Trash2 />
              Delete
            </Button>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm text-danger">
              Delete “{ws.name}”?{" "}
              {isActive &&
                "This is your ACTIVE workspace — you'll be logged out of it."}
            </span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => setConfirming(false)}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => void handleDelete()}
              disabled={del.isPending}
            >
              <Trash2 />
              Yes, delete
            </Button>
          </div>
        )}
        {del.isError && (
          <div className="mt-2">
            <ErrorNote message={errMsg(del.error)} />
          </div>
        )}
        <EnterWorkspaceDialog
          ws={entering ? ws : null}
          onClose={() => setEntering(false)}
        />
      </div>
    </div>
  );
}

export function WorkspacesSection() {
  const { data: session } = useSession();
  const [creating, setCreating] = useState(false);
  const workspaces = session?.workspaces ?? [];
  const activeID = session?.workspace?.id;

  return (
    <div>
      <BackupWarningBanner />
      <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-workspaces" />
        <h2 className="text-lg font-bold">Workspaces</h2>
      </div>
      <div className="mt-2 space-y-3">
        {workspaces.map((ws) => (
          <WorkspaceCard key={ws.id} ws={ws} activeID={activeID} />
        ))}
      </div>
      {/* Primary, not outline. Every control on this page used to be a small
          outline button — a white box with a #dcd8d2 hairline on a white page
          — so the section read as having no buttons at all. */}
      <Button size="sm" className="mt-3" onClick={() => setCreating(true)}>
        <Plus />
        Create workspace
      </Button>
      <CreateWorkspaceDialog
        open={creating}
        onClose={() => setCreating(false)}
      />
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
export function SystemStatusSection() {
  const { data, isLoading, isError, error } = useAdminSettings();

  const sandboxOn = data?.sandbox_on ?? false;
  const landlockReady = data?.landlock_ready ?? false;

  return (
    <div>
      <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-system" />
        <h2 className="text-lg font-bold">System status</h2>
      </div>
      <p className="mt-1 text-sm text-muted-2">
        Coder and sandbox settings come from <code>config.yaml</code> and each
        workspace's own coder configuration.
      </p>
      {isLoading && <p className="mt-2 text-sm text-muted-2">Loading…</p>}
      {isError && (
        <div className="mt-2">
          <ErrorNote message={errMsg(error)} />
        </div>
      )}
      {!isLoading && !isError && (
        <>
          {/* Warnings first: these are the states that change how the install
              behaves. Without python3 the agent-tool AST guardrail silently
              self-skips, and until now only /healthz said so. */}
          {(data?.warnings ?? []).length > 0 && (
            <ul className="mt-3 space-y-2">
              {(data?.warnings ?? []).map((w) => (
                <li
                  key={w}
                  className="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-sm text-warn"
                >
                  <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                  <span>{w}</span>
                </li>
              ))}
            </ul>
          )}
          <dl className="mt-3 flex flex-wrap gap-x-8 gap-y-2 text-sm">
            <div>
              <dt className="text-muted-2">Version</dt>
              <dd className="font-medium">{data?.version || "unknown"}</dd>
            </div>
            <div>
              <dt className="text-muted-2">Commit</dt>
              <dd className="font-mono font-medium">
                {data?.commit || "unknown"}
              </dd>
            </div>
            <div>
              <dt className="text-muted-2">Coder mode</dt>
              <dd className="font-medium">{data?.coder_mode || "unknown"}</dd>
            </div>
            <div>
              <dt className="text-muted-2">Python 3</dt>
              <dd
                className={
                  data?.tools?.python3
                    ? "font-medium text-ok"
                    : "font-medium text-warn"
                }
              >
                {data?.tools?.python3 ? "present" : "missing"}
              </dd>
            </div>
            <div>
              <dt className="text-muted-2">ripgrep</dt>
              <dd
                className={
                  data?.tools?.rg ? "font-medium text-ok" : "font-medium text-muted-2"
                }
              >
                {data?.tools?.rg ? "present" : "missing"}
              </dd>
            </div>
            <div>
              <dt className="text-muted-2">pdftotext</dt>
              <dd
                className={
                  data?.tools?.pdftotext
                    ? "font-medium text-ok"
                    : "font-medium text-muted-2"
                }
              >
                {data?.tools?.pdftotext ? "present" : "missing"}
              </dd>
            </div>
            <div>
              <dt className="text-muted-2">tesseract</dt>
              <dd
                className={
                  data?.tools?.tesseract
                    ? "font-medium text-ok"
                    : "font-medium text-muted-2"
                }
              >
                {data?.tools?.tesseract ? "present" : "missing"}
              </dd>
            </div>
            <div>
              <dt className="text-muted-2">Sandbox</dt>
              <dd
                className={
                  sandboxOn ? "font-medium text-ok" : "font-medium text-warn"
                }
              >
                {sandboxOn ? "on" : "off"}
              </dd>
            </div>
            <div>
              <dt className="text-muted-2">Landlock</dt>
              <dd
                className={
                  landlockReady
                    ? "font-medium text-ok"
                    : "font-medium text-warn"
                }
              >
                {landlockReady
                  ? `ready (ABI ${data?.landlock_abi ?? 0})`
                  : "unavailable"}
              </dd>
            </div>
          </dl>
        </>
      )}
    </div>
  );
}

// ── Instance URL ─────────────────────────────────────────────────────────

// Every OAuth redirect URI is built from this value, so it is the single most
// consequential setting for connecting a service. Left unset it is detected from
// the browser's request, which is why the redirect URI used to change depending
// on how the operator reached the page.
export function InstanceURLSection() {
  const { data, isLoading } = usePublicURL();
  const save = useSavePublicURL();
  const test = useTestPublicURL();
  const [value, setValue] = useState("");
  const [touched, setTouched] = useState(false);

  // Seed from the server once, then leave the field alone so typing is not
  // clobbered by a refetch.
  useEffect(() => {
    if (data && !touched) setValue(data.public_url);
  }, [data, touched]);

  const source = data?.public_url_source;
  return (
    <div>
      <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-instance-url" />
        <h2 className="text-lg font-bold">Instance URL</h2>
      </div>
      <p className="mt-1 text-sm text-muted-2">
        The address this server is reached at. Every service connection's
        redirect URI is built from it, so providers must be able to accept it.
        Leave it empty to detect it from your browser.
      </p>

      {isLoading && <p className="mt-2 text-sm text-muted-2">Loading…</p>}

      {!isLoading && (
        <>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Input
              value={value}
              placeholder="https://agents.example.com"
              onChange={(e) => {
                setTouched(true);
                setValue(e.target.value);
              }}
              className="min-w-64 flex-1"
              aria-label="Instance URL"
            />
            <Button
              onClick={() => save.mutate(value.trim())}
              disabled={save.isPending}
            >
              <Save />
              {save.isPending ? "Saving…" : "Save"}
            </Button>
            <Button
              variant="secondary"
              onClick={() =>
                test.mutate((value.trim() || data?.public_url_actual) ?? "")
              }
              disabled={test.isPending}
            >
            <FlaskConical />
              {test.isPending ? "Testing…" : "Test this URL"}
            </Button>
          </div>

          <p className="mt-2 text-sm text-muted-2">
            Currently using <code>{data?.public_url_actual}</code>{" "}
            {source === "configured"
              ? "(configured here)"
              : source === "env"
                ? "(from ROOKERY_PUBLIC_URL)"
                : "(detected from your browser)"}
          </p>

          {save.isError && (
            <div className="mt-2">
              <ErrorNote message={errMsg(save.error)} />
            </div>
          )}
          {test.isError && (
            <div className="mt-2">
              <ErrorNote message={errMsg(test.error)} />
            </div>
          )}
          {test.data && (
            <p
              className={
                test.data.ok && !test.data.warning
                  ? "mt-2 text-sm font-medium text-ok"
                  : "mt-2 text-sm text-muted-2"
              }
            >
              {test.data.ok && !test.data.warning
                ? "Reached this server successfully."
                : test.data.error}
            </p>
          )}
        </>
      )}
    </div>
  );
}

// ── Audit log ────────────────────────────────────────────────────────────

export function AuditLogSection() {
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
  const workspaceNameById = new Map(
    workspaces.map((w) => [w.id, w.name] as const),
  );
  const filtered = Boolean(
    action || workspaceID || sinceDays || debouncedSearch,
  );

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
    "h-8 rounded-md border border-border bg-background px-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring";

  return (
    <div>
      <div className="flex items-center gap-2.5">
        <OwnerIcon slug="owner-audit" />
        <h2 className="text-lg font-bold">Audit log</h2>
      </div>
      <p className="mt-1 text-sm text-muted-2">
        Most recent first, up to 100 matching events.
      </p>

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <SearchInput
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
            <option key={a} value={a}>
              {a}
            </option>
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
            <option key={w.id} value={w.id}>
              {w.name}
            </option>
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
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={clearFilters}
          >
            <Eraser />
            Clear
          </Button>
        )}
      </div>

      {isLoading && <p className="mt-2 text-sm text-muted-2">Loading…</p>}
      {isError && (
        <div className="mt-2">
          <ErrorNote message={errMsg(error)} />
        </div>
      )}
      {!isLoading && !isError && logs.length === 0 && (
        <p className="mt-2 text-sm text-muted-2">
          {filtered ? "No events match these filters." : "No audit events yet."}
        </p>
      )}
      {!isLoading && !isError && logs.length > 0 && (
        <div className="mt-2 overflow-x-auto rounded-md border border-border">
          <table className="w-full text-left text-sm">
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
                  <td className="whitespace-nowrap px-3 py-2 text-muted-2">
                    {timeAgo(l.created_at)}
                  </td>
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
