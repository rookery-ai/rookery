import { useEffect, useState } from "react";
import { DoorOpen, Plus, ShieldCheck } from "lucide-react";
import { useNavigate } from "react-router";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { useOwnerVerify } from "@/lib/ownerVerify";
import { useSession, type Workspace } from "@/lib/session";
import { WorkspaceAvatar } from "@/lib/workspaceIcons";
import { PageTitle } from "@/components/shell/PageTitle";
import { SignOutButton } from "@/components/shell/SignOutButton";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RookeryTile } from "@/components/brand/RookeryMark";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

// Every tenant-scoped query key in the app (["agents"], ["skills"],
// ["kb-tree", …], ["secrets"], …) is keyed by RESOURCE, not by workspace — so
// after switching workspaces the previous tenant's rows stay in cache and
// render until each query happens to refetch. Drop them all on the way in.
//
// Deliberately NOT qc.clear(): that also evicts ["session"], and RequireAuth
// (router.tsx) renders a full-page loading fallback whenever the session query
// is pending — clearing it flashes the entire app. The session is refreshed by
// invalidation instead, so the guard keeps its last-known-good value while the
// new one lands.
export async function resetWorkspaceScopedCache(qc: QueryClient) {
  qc.removeQueries({ predicate: (query) => query.queryKey[0] !== "session" });
  await qc.invalidateQueries({ queryKey: ["session"] });
}

export function EnterWorkspaceDialog({
  ws,
  onClose,
}: {
  ws: Workspace | null;
  onClose: () => void;
}) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const nav = useNavigate();
  const qc = useQueryClient();

  // Reset stale password/error when the target workspace changes (including
  // reopening for a different workspace after a failed attempt).
  useEffect(() => {
    setPassword("");
    setError("");
  }, [ws?.id]);

  async function enter(e: React.FormEvent) {
    e.preventDefault();
    if (!ws) return;
    setBusy(true);
    setError("");
    try {
      await api.post<{ ok: boolean; needs_setup: boolean }>(
        `/api/v1/workspaces/${ws.id}/enter`,
        { master_password: password },
      );
      await resetWorkspaceScopedCache(qc);
      // Close BEFORE navigating. This dialog is also rendered by WorkspaceMenu,
      // which lives in the always-mounted icon rail — there, nav("/") doesn't
      // unmount it, so without this the master-password modal stayed on screen
      // over the workspace the user just entered.
      onClose();
      nav("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={ws !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Enter “{ws?.name}”</DialogTitle>
        </DialogHeader>
        <form onSubmit={enter} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="master_password">Master password</Label>
            <Input
              id="master_password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoFocus
            />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full" disabled={busy}>
            <DoorOpen />
            {busy ? "Entering…" : "Enter workspace"}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Rendered by both the workspace picker and the icon rail's workspace menu, so
 * the owner-password gate below reaches every entry point at once.
 *
 * A workspace is a TENANT, and the create endpoint sits behind
 * requireOwnerVerified for that reason. This dialog never predicts whether the
 * confirmation is still fresh — it submits, and only if the server refuses does
 * it swap in a password step, keeping the typed name. See useOwnerVerify for
 * why a client-side check of session.owner_verified is the wrong shape.
 */
export function CreateWorkspaceDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const nav = useNavigate();
  const qc = useQueryClient();
  const gate = useOwnerVerify();

  // A reopened dialog starts clean — including dropping any action the gate is
  // still holding from a dismissed attempt.
  useEffect(() => {
    if (open) return;
    setName("");
    setPassword("");
    setError("");
    gate.reset();
    // gate.reset is stable (useCallback with no deps); listing it would re-run
    // this on every render of the hook's state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  async function createWorkspace() {
    // Name only. "What is this workspace about?" is asked once, by the
    // setup wizard the new workspace lands in — it was previously collected
    // here as well, so the same question appeared twice in a row.
    await api.post<Workspace>("/api/v1/workspaces", { name });
    // A freshly created workspace is auto-activated — same tenant switch as
    // entering one, so the previous workspace's cached rows must go too.
    await resetWorkspaceScopedCache(qc);
    // A freshly created workspace is auto-activated and needs_setup — just
    // navigate home and let RequireAuth route to /setup.
    nav("/", { replace: true });
    onClose();
  }

  async function submitName(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await gate.run(createWorkspace);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  async function submitPassword(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    try {
      // Confirms, then retries the create with the name already typed.
      await gate.verify(password);
      setPassword("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>
            {gate.needsPassword ? "Confirm it’s you" : "Create workspace"}
          </DialogTitle>
        </DialogHeader>
        {gate.needsPassword ? (
          <form onSubmit={submitPassword} className="space-y-4">
            <p className="text-muted-2 text-sm">
              A workspace is a separate tenant with its own data. Confirm your
              owner password to create “{name}”.
            </p>
            <div className="space-y-1.5">
              <Label htmlFor="ws-owner-password">Owner password</Label>
              <Input
                id="ws-owner-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoFocus
              />
            </div>
            {error && <p className="text-danger text-sm">{error}</p>}
            <Button
              type="submit"
              className="w-full"
              disabled={gate.busy || !password}
            >
              <ShieldCheck />
              {gate.busy ? "Creating…" : "Confirm and create"}
            </Button>
          </form>
        ) : (
          <form onSubmit={submitName} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="ws-name">Name</Label>
              <Input
                id="ws-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
              />
            </div>
            {error && <p className="text-danger text-sm">{error}</p>}
            <Button type="submit" className="w-full" disabled={gate.busy}>
              <Plus />
              {gate.busy ? "Creating…" : "Create"}
            </Button>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

export default function Workspaces() {
  const { data: session } = useSession();
  const [entering, setEntering] = useState<Workspace | null>(null);
  const [creating, setCreating] = useState(false);
  const [directEnterError, setDirectEnterError] = useState("");
  const nav = useNavigate();
  const qc = useQueryClient();

  const list = session?.workspaces ?? [];

  // needs_setup workspaces skip the master-password dialog (they don't have
  // one yet) — enter directly, then let RequireAuth route to /setup once the
  // session reflects the newly active workspace.
  async function directEnter(ws: Workspace) {
    setDirectEnterError("");
    try {
      await api.post(`/api/v1/workspaces/${ws.id}/enter`, {});
      await resetWorkspaceScopedCache(qc);
      nav("/", { replace: true });
    } catch (err) {
      setDirectEnterError(
        err instanceof ApiError ? err.message : "Something went wrong",
      );
    }
  }

  return (
    <div className="min-h-screen bg-chrome flex items-center justify-center p-4">
      {/* The app's only sign-out. Leaving a workspace lands here, which is what
          makes one button on this screen reach the whole app. */}
      <SignOutButton />
      <div className="bg-background border border-border rounded-xl p-8 w-full max-w-md shadow-sm">
        {/* This screen and the sign-in screen are the two the owner sees
            outside the app shell, where the rail's branding is absent. Marking
            both is what keeps the product recognisable before there is any
            product on screen. */}
        <div className="mb-4 flex items-center gap-2 border-b border-border pb-4">
          <RookeryTile className="size-7 rounded-md" id="workspaces-mark" />
          <span className="text-lg font-semibold tracking-tight lowercase">
            rookery
          </span>
        </div>
        <div className="mb-1">
          <PageTitle icon="owner-workspaces" title="Workspaces" />
        </div>
        <p className="text-muted-2 text-sm mb-6">
          Pick a workspace to enter — its master password is required every
          time.
        </p>
        {directEnterError && (
          <p className="text-danger text-sm mb-4">{directEnterError}</p>
        )}
        <ul className="space-y-2">
          {list.map((ws) => (
            <li key={ws.id}>
              <button
                onClick={() =>
                  ws.needs_setup ? void directEnter(ws) : setEntering(ws)
                }
                className="flex w-full items-center gap-3 text-left border border-border rounded-lg px-4 py-3 hover:bg-chrome transition-colors"
              >
                {/* Same avatar the rail's switcher uses, so a workspace is
                    recognised by the same picture in both places. `shrink-0`
                    keeps it square: it is a flex item next to a text column
                    that can wrap, and without it a long name compresses the
                    avatar into an ellipse. */}
                <WorkspaceAvatar
                  name={ws.name}
                  icon={ws.icon}
                  className="size-9 shrink-0 text-base"
                />
                {/* min-w-0 lets the text column shrink below its content width;
                    a flex item's automatic minimum size is content-based, so an
                    unbroken `about` line would otherwise push the row wider than
                    the card. */}
                <span className="min-w-0 flex-1">
                  <span className="font-semibold">{ws.name}</span>
                  {ws.needs_setup && (
                    <span className="text-warn text-xs ml-2">needs setup</span>
                  )}
                  {ws.about && (
                    <span className="block text-muted-2 text-xs mt-0.5">
                      {ws.about}
                    </span>
                  )}
                </span>
              </button>
            </li>
          ))}
        </ul>
        <Button
          variant="outline"
          className="w-full mt-4"
          onClick={() => setCreating(true)}
        >
          <Plus />
          Create workspace
        </Button>
        <EnterWorkspaceDialog ws={entering} onClose={() => setEntering(null)} />
        <CreateWorkspaceDialog
          open={creating}
          onClose={() => setCreating(false)}
        />
      </div>
    </div>
  );
}
