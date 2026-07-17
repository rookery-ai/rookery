import { useMemo, useState } from "react";
import { AlertTriangle, Check, KeyRound, Search, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ApiError } from "@/lib/api";
import { useSecrets, useAddSecret, useDeleteSecret, type Secret } from "@/lib/secrets";

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="mb-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {message}
    </div>
  );
}

// ── Add secret ───────────────────────────────────────────────────────────

function AddSecretCard() {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [saved, setSaved] = useState(false);
  const add = useAddSecret();

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSaved(false);
    try {
      await add.mutateAsync({ name: name.trim(), value });
      setName("");
      setValue("");
      setSaved(true);
    } catch {
      // surfaced below via add.error
    }
  }

  return (
    <section className="rounded-lg border border-border bg-background p-4">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-semibold">Add secret</h2>
        {saved && (
          <span className="inline-flex items-center gap-1 rounded-full bg-ok-soft px-2 py-0.5 text-xs font-medium text-ok">
            <Check className="size-3" /> Saved ✓
          </span>
        )}
      </div>

      {add.isError && (
        <div className="mt-3">
          <ErrorBanner message={errMessage(add.error)} />
        </div>
      )}

      <form onSubmit={(e) => void handleSubmit(e)} className="mt-3 flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="flex-1 space-y-1.5">
          <Label htmlFor="secret-name">Name</Label>
          <Input
            id="secret-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="OPENAI_API_KEY"
          />
          <p className="text-xs text-muted-2">
            UPPER_SNAKE_CASE recommended — agents see these as environment variables.
          </p>
        </div>
        <div className="flex-1 space-y-1.5">
          <Label htmlFor="secret-value">Value</Label>
          <Input
            id="secret-value"
            type="password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoComplete="off"
          />
          <p className="text-xs text-muted-2">
            Write-only: values are never displayed after saving.
          </p>
        </div>
        <Button type="submit" disabled={!name.trim() || !value || add.isPending}>
          {add.isPending ? "Adding…" : "Add"}
        </Button>
      </form>
    </section>
  );
}

// ── Delete confirmation ──────────────────────────────────────────────────

function DeleteSecretDialog({
  secret,
  onClose,
}: {
  secret: Secret | null;
  onClose: () => void;
}) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const del = useDeleteSecret();

  async function handleDelete() {
    if (!secret) return;
    setError("");
    try {
      await del.mutateAsync({ name: secret.name, masterPassword: password });
      setPassword("");
      onClose();
    } catch (err) {
      setError(errMessage(err));
    }
  }

  function handleOpenChange(open: boolean) {
    if (!open) {
      setPassword("");
      setError("");
      onClose();
    }
  }

  return (
    <Dialog open={secret !== null} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>
            Deleting &ldquo;{secret?.name}&rdquo; — enter your master password to confirm
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label htmlFor="delete-secret-password">Master password</Label>
          <Input
            id="delete-secret-password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
          />
        </div>
        {error && <ErrorBanner message={error} />}
        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)} disabled={del.isPending}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={() => void handleDelete()}
            disabled={!password || del.isPending}
          >
            {del.isPending ? "Deleting…" : "Delete"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ── List ─────────────────────────────────────────────────────────────────

function EmptyState() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center text-muted-2">
      <KeyRound className="size-10" />
      <p className="max-w-sm text-sm">
        No secrets yet — agents use these for API keys and tokens.
      </p>
    </div>
  );
}

export default function SecretsPage() {
  const { data } = useSecrets();
  const [query, setQuery] = useState("");
  const [pendingDelete, setPendingDelete] = useState<Secret | null>(null);

  const secrets = data?.secrets ?? [];

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return secrets;
    return secrets.filter((s) => s.name.toLowerCase().includes(q));
  }, [secrets, query]);

  const showEmpty = secrets.length === 0;

  return (
    <div className="flex h-full flex-col overflow-y-auto p-6">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-bold">Secrets</h1>
          {secrets.length > 0 && (
            <p className="mt-0.5 text-sm text-muted-2">
              {secrets.length} secret{secrets.length > 1 ? "s" : ""} stored
            </p>
          )}
        </div>
        {secrets.length > 0 && (
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-2" />
            <Input
              aria-label="Search secrets"
              placeholder="Search secrets…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="w-56 pl-8"
            />
          </div>
        )}
      </div>

      <div className="mb-6">
        <AddSecretCard />
      </div>

      {showEmpty ? (
        <EmptyState />
      ) : (
        <ul className="divide-y divide-border rounded-lg border border-border bg-background">
          {filtered.map((s) => (
            <li key={s.name} className="flex items-center justify-between gap-3 px-4 py-3">
              <div className="flex items-center gap-2 min-w-0">
                <KeyRound className="size-4 shrink-0 text-muted-2" />
                <span className="truncate font-mono text-sm">{s.name}</span>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="text-danger"
                onClick={() => setPendingDelete(s)}
              >
                <Trash2 className="size-4" /> Delete
              </Button>
            </li>
          ))}
        </ul>
      )}

      <DeleteSecretDialog secret={pendingDelete} onClose={() => setPendingDelete(null)} />
    </div>
  );
}
