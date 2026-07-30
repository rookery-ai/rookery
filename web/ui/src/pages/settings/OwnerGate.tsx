import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api, ApiError } from "@/lib/api";

const GATE_CODE = "owner_verification_required";

/**
 * Wraps the Owner settings tab.
 *
 * Install-level endpoints 403 with `owner_verification_required` until the owner
 * re-enters their password, so this probes one of them and renders a prompt in
 * place of the body. The gate is enforced on the server — this component is the
 * affordance, not the protection.
 *
 * There is deliberately no client-side TTL timer: the server owns expiry, and a
 * timer here could only disagree with it. When the stamp lapses, the next
 * request 403s and the prompt comes back on its own.
 */
export function OwnerGate({ children }: { children: React.ReactNode }) {
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // The cheapest install-level endpoint; its 403 is the gate signal.
  const probe = useQuery({
    queryKey: ["admin", "overview"],
    queryFn: () => api.get<unknown>("/api/v1/admin/overview"),
    retry: false,
  });

  const gated =
    probe.error instanceof ApiError &&
    probe.error.status === 403 &&
    probe.error.code === GATE_CODE;

  async function verify(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api.post("/api/v1/auth/owner-verify", { password });
      setPassword("");
      await qc.invalidateQueries();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  if (probe.isLoading) return <div className="text-muted-2">Loading…</div>;
  if (!gated) return <>{children}</>;

  return (
    <section className="max-w-sm">
      <div className="flex items-center gap-2">
        <ShieldCheck className="size-5" />
        <h2 className="text-lg font-bold">Owner settings</h2>
      </div>
      <p className="mt-1 text-sm text-muted-2">
        These settings cover your whole install — every workspace, and your backups. Confirm
        your owner password to continue.
      </p>
      {error && (
        <div className="mt-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}
      <form onSubmit={(e) => void verify(e)} className="mt-4 space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="owner_password">Owner password</Label>
          <Input
            id="owner_password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <Button type="submit" disabled={busy || !password}>
          {busy ? "Checking…" : "Unlock"}
        </Button>
      </form>
    </section>
  );
}
