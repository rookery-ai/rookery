import { useState } from "react";
import type { FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Check, ExternalLink, Plus, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ProviderLogo } from "@/components/brand/ProviderLogo";
import { ApiError, api } from "@/lib/api";
import type { APIProvider, CoderCatalogEntry } from "@/lib/settings";

function errMsg(err: unknown) {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

// The secret-name convention every save posts under — mirrors
// coder.CoderKeySecretName (Go). Kept in one place so a future backend-name
// tweak only needs updating here.
export function coderKeySecretName(provider: string) {
  return "CODER_KEY_" + provider.toUpperCase();
}

function ProviderCard({
  entry,
  label,
}: {
  entry: CoderCatalogEntry;
  label: string;
}) {
  const [expanded, setExpanded] = useState(false);
  const [value, setValue] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const qc = useQueryClient();

  const noKeyNeeded = entry.requiresKey === false;
  const canAddKey = !noKeyNeeded;

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    if (!value.trim()) return;
    setError("");
    setBusy(true);
    try {
      await api.post<{ ok: boolean }>("/api/v1/secrets", {
        name: coderKeySecretName(entry.name),
        value: value.trim(),
      });
      await qc.invalidateQueries({ queryKey: ["settings"] });
      setValue("");
      setExpanded(false);
      setJustSaved(true);
      window.setTimeout(() => setJustSaved(false), 2000);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rounded-lg border border-border bg-background p-4">
      <button
        type="button"
        disabled={!canAddKey}
        onClick={() => canAddKey && setExpanded((v) => !v)}
        className="flex w-full items-center gap-3 text-left disabled:cursor-default"
      >
        <ProviderLogo name={entry.name} size={32} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">{label}</div>
          {entry.docs && (
            <a
              href={entry.docs}
              target="_blank"
              rel="noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="inline-flex items-center gap-1 text-xs text-muted-2 hover:text-foreground hover:underline"
            >
              Docs <ExternalLink className="size-3" />
            </a>
          )}
        </div>
        {justSaved ? (
          <span className="flex shrink-0 items-center gap-1 text-xs font-medium text-ok">
            <Check className="size-3" /> Saved
          </span>
        ) : noKeyNeeded ? (
          <span className="shrink-0 text-xs text-muted-2">No key needed</span>
        ) : entry.hasKey ? (
          <span className="flex shrink-0 items-center gap-1 text-xs font-medium text-ok">
            <span className="size-1.5 rounded-full bg-ok" /> Key saved
          </span>
        ) : (
          <span className="flex shrink-0 items-center gap-1 text-xs font-medium text-primary">
            <Plus className="size-3" /> Add key
          </span>
        )}
      </button>

      {expanded && canAddKey && (
        <form
          onSubmit={(e) => void handleSave(e)}
          className="mt-3 space-y-2 border-t border-border pt-3"
        >
          {entry.hasKey && (
            <p className="text-xs text-muted-2">
              Already set — paste a new key to override.
            </p>
          )}
          {entry.custom && (
            <p className="text-xs text-muted-2">
              This provider also needs a base URL — set it under Coder below.
            </p>
          )}
          <Input
            type="password"
            autoFocus
            placeholder="Paste API key"
            aria-label={`${label} API key`}
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
          {error && <p className="text-xs text-danger">{error}</p>}
          <div className="flex items-center gap-2">
            <Button type="submit" size="sm" disabled={busy || !value.trim()}>
              <Save />
              {busy ? "Saving…" : "Save key"}
            </Button>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => setExpanded(false)}
            >
              Cancel
            </Button>
          </div>
        </form>
      )}
    </div>
  );
}

export function ProviderCards({
  catalog,
  providers,
}: {
  catalog: CoderCatalogEntry[];
  providers: APIProvider[];
}) {
  if (catalog.length === 0) {
    return <p className="text-sm text-muted-2">No providers available.</p>;
  }

  const labelFor = (name: string) =>
    providers.find((p) => p.name === name)?.label ?? name;

  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {catalog.map((entry) => (
        <ProviderCard
          key={entry.name}
          entry={entry}
          label={labelFor(entry.name)}
        />
      ))}
    </div>
  );
}
