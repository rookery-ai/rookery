import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { useSession, type Workspace } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";

export function EnterWorkspaceDialog({
  ws, onClose,
}: { ws: Workspace | null; onClose: () => void }) {
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
      await qc.invalidateQueries({ queryKey: ["session"] });
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
              id="master_password" type="password" value={password}
              onChange={(e) => setPassword(e.target.value)} autoFocus
            />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Entering…" : "Enter workspace"}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function CreateWorkspaceDialog({
  open, onClose,
}: { open: boolean; onClose: () => void }) {
  const [name, setName] = useState("");
  const [about, setAbout] = useState("");
  const [error, setError] = useState("");
  const nav = useNavigate();
  const qc = useQueryClient();

  async function create(e: React.FormEvent) {
    e.preventDefault();
    try {
      await api.post<Workspace>("/api/v1/workspaces", { name, about });
      await qc.invalidateQueries({ queryKey: ["session"] });
      // A freshly created workspace is auto-activated and needs_setup — just
      // navigate home and let RequireAuth route to /setup.
      nav("/", { replace: true });
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Create workspace</DialogTitle>
        </DialogHeader>
        <form onSubmit={create} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="ws-name">Name</Label>
            <Input id="ws-name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ws-about">About (optional)</Label>
            <Input id="ws-about" value={about} onChange={(e) => setAbout(e.target.value)} />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full">Create</Button>
        </form>
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
      await qc.invalidateQueries({ queryKey: ["session"] });
      nav("/", { replace: true });
    } catch (err) {
      setDirectEnterError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  return (
    <div className="min-h-screen bg-chrome flex items-center justify-center p-4">
      <div className="bg-background border border-border rounded-xl p-8 w-full max-w-md shadow-sm">
        <h1 className="text-xl font-bold mb-1">Workspaces</h1>
        <p className="text-muted-2 text-sm mb-6">
          Pick a workspace to enter — its master password is required every time.
        </p>
        {directEnterError && (
          <p className="text-danger text-sm mb-4">{directEnterError}</p>
        )}
        <ul className="space-y-2">
          {list.map((ws) => (
            <li key={ws.id}>
              <button
                onClick={() => (ws.needs_setup ? void directEnter(ws) : setEntering(ws))}
                className="w-full text-left border border-border rounded-lg px-4 py-3 hover:bg-chrome transition-colors"
              >
                <span className="font-semibold">{ws.name}</span>
                {ws.needs_setup && <span className="text-warn text-xs ml-2">needs setup</span>}
                {ws.about && <span className="block text-muted-2 text-xs mt-0.5">{ws.about}</span>}
              </button>
            </li>
          ))}
        </ul>
        <Button variant="outline" className="w-full mt-4" onClick={() => setCreating(true)}>
          + Create workspace
        </Button>
        <EnterWorkspaceDialog ws={entering} onClose={() => setEntering(null)} />
        <CreateWorkspaceDialog open={creating} onClose={() => setCreating(false)} />
      </div>
    </div>
  );
}
