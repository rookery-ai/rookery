import { useState } from "react";
import { LogIn } from "lucide-react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RookeryTile } from "@/components/brand/RookeryMark";

export default function Login() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const nav = useNavigate();
  const qc = useQueryClient();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await api.post<{
        ok: boolean;
        must_change_password: boolean;
      }>("/api/v1/auth/login", { username, password });
      await qc.invalidateQueries({ queryKey: ["session"] });
      nav(res.must_change_password ? "/change-password" : "/", {
        replace: true,
      });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="min-h-screen bg-chrome flex items-center justify-center">
      <form
        onSubmit={submit}
        className="bg-background border border-border rounded-xl p-8 w-full max-w-sm shadow-sm"
      >
        {/* The mark carries the identity, so the heading below it drops to the
            role the page actually plays. Two "Rookery"s stacked would be one
            more than the screen needs. */}
        <div className="mb-6 flex flex-col items-center text-center">
          <RookeryTile className="size-12 rounded-xl" id="login-mark" />
          <h1 className="mt-3 text-xl font-bold">Rookery</h1>
          <p className="text-muted-2 text-sm">Sign in to your server</p>
        </div>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="username">Username</Label>
            <Input
              id="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          {error && <p className="text-danger text-sm">{error}</p>}
          <Button type="submit" className="w-full" disabled={busy}>
            <LogIn />
            {busy ? "Signing in…" : "Log in"}
          </Button>
        </div>
      </form>
    </div>
  );
}
