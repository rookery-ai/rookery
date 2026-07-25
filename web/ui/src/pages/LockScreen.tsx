import { useState } from "react";
import type { FormEvent } from "react";
import { Lock } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { useSession } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// LockScreen covers the whole app while the session is locked.
//
// It is not the security boundary — the server is (every guarded route 423s
// while locked). This is the surface that lets the owner get back in, and it
// deliberately keeps the workspace name visible: the lock's purpose is to
// blank the screen WITHOUT giving up the entered workspace, so showing which
// one is still entered is the reassurance that logging out would have cost.
export default function LockScreen() {
  const { data: session } = useSession();
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const workspace = session?.workspace;

  async function unlock(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api.post<{ ok: boolean }>("/api/v1/auth/unlock", { master_password: password });
      setPassword("");
      // Refetch the session first so `locked` is false before anything else
      // re-runs; the rest of the app's queries were 423ing until now and must
      // be refetched, not served from a stale error state.
      await qc.invalidateQueries({ queryKey: ["session"] });
      await qc.invalidateQueries();
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 401
          ? "Wrong master password"
          : err instanceof ApiError
            ? err.message
            : "Something went wrong",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background p-6">
      <div className="w-full max-w-sm text-center">
        <div className="mx-auto flex size-12 items-center justify-center rounded-full bg-chrome">
          <Lock className="size-5 text-muted-2" aria-hidden="true" />
        </div>
        <h1 className="mt-4 text-lg font-bold">Locked</h1>
        <p className="mt-1 text-sm text-muted-2">
          {workspace
            ? <>Enter the master password for <b className="text-foreground">{workspace.name}</b> to continue.</>
            : "Enter your master password to continue."}
        </p>

        <form onSubmit={(e) => void unlock(e)} className="mt-6 space-y-3 text-left">
          <div className="space-y-1.5">
            <Label htmlFor="unlock_password">Master password</Label>
            <Input
              id="unlock_password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoFocus
              autoComplete="current-password"
            />
          </div>
          {error && <p className="text-sm text-danger">{error}</p>}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Unlocking…" : "Unlock"}
          </Button>
        </form>
      </div>
    </div>
  );
}
