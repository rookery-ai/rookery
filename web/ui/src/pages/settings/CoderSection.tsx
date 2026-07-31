import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import type { UseMutationResult } from "@tanstack/react-query";
import { AlertTriangle, Check, ChevronDown, Loader2, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import {
  useSaveCoder,
  useTestCoder,
  type CoderCatalogEntry,
  type CoderConfig,
  type DetectedCoder,
  type SaveCoderInput,
} from "@/lib/settings";

function errMsg(err: unknown) {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

type Engine = "local" | "api";

function summarize(coder: CoderConfig | undefined): string {
  if (!coder) return "Not configured";
  if (coder.kind === "api") {
    return `API · ${coder.provider || "—"} / ${coder.model || "—"}`;
  }
  return `Local CLI · ${coder.bin || "—"}`;
}

export function CoderSection({
  coder,
  detectedCoders,
  catalog,
  saveOverride,
  hideTest = false,
  showApiKeyInput = false,
  coderMode = "full",
}: {
  coder: CoderConfig | undefined;
  detectedCoders: DetectedCoder[];
  catalog: CoderCatalogEntry[];
  // Wizard-mode overrides (all optional; omitted ⇒ identical to the
  // settings-page behavior). saveOverride lets a caller point Save at a
  // different endpoint (the setup wizard posts through /api/v1/setup, not
  // /api/v1/settings/coder — see SetupWizard's coderSaveMutation) while
  // reusing this component's form + validation. showApiKeyInput renders an
  // inline API-key field next to Provider/Model — the setup wizard can't
  // reach ProviderCards' /api/v1/secrets endpoint (blocked while
  // needs_setup is true), so the key has to travel in the same POST as the
  // rest of the coder config, exactly like the classic template wizard does.
  // `any` for TData/TError is deliberate: the setup wizard's mutation
  // resolves a different response shape ({ok, next_step}) than the
  // settings-page save ({ok}) — this prop only needs mutateAsync/isPending/
  // isError/error, so pinning those two type params would just fight duck
  // typing for no benefit.
  saveOverride?: UseMutationResult<any, any, SaveCoderInput>;
  hideTest?: boolean;
  showApiKeyInput?: boolean;
  // Build policy from /api/v1/settings: a "slim" build ships no CLI coder
  // binary at all, so the local engine is not an option the user can pick.
  // Distinct from detectedCoders being empty, which only means none is
  // installed right now on a build that does support them.
  coderMode?: "full" | "slim";
}) {
  const [engine, setEngine] = useState<Engine>(
    coderMode === "slim" ? "api" : "local",
  );
  const [bin, setBin] = useState("");
  const [timeoutS, setTimeoutS] = useState(120);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [baseURLError, setBaseURLError] = useState("");
  const [saved, setSaved] = useState(false);

  const internalSave = useSaveCoder();
  const save = saveOverride ?? internalSave;
  const test = useTestCoder();

  useEffect(() => {
    if (!coder) return;
    setEngine(coder.kind === "api" || coderMode === "slim" ? "api" : "local");
    setBin(coder.bin);
    setTimeoutS(coder.timeout_s || 120);
    setProvider(coder.provider);
    setModel(coder.model);
    setBaseURL(coder.base_url);
  }, [coder, coderMode]);

  // A slim build ships no CLI coder binary, so "local" is not offered. This
  // mirrors the server-side guard in rejectLocalInSlim — the UI is the
  // convenience, the API is the enforcement.
  const engines: Engine[] = coderMode === "slim" ? ["api"] : ["local", "api"];

  const selectedEntry = catalog.find((c) => c.name === provider);
  const isCustom = selectedEntry?.custom ?? false;

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    setSaved(false);
    setBaseURLError("");
    if (engine === "api" && isCustom && !baseURL.trim()) {
      setBaseURLError(
        "A base URL is required for a Custom (OpenAI-compatible) provider",
      );
      setAdvancedOpen(true);
      return;
    }
    try {
      await save.mutateAsync({
        kind: engine,
        bin: engine === "local" ? bin : "",
        timeout_s: timeoutS,
        provider: engine === "api" ? provider : "",
        model: engine === "api" ? model : "",
        base_url: engine === "api" ? baseURL : "",
        api_key: showApiKeyInput && engine === "api" ? apiKey.trim() : "",
      });
      setSaved(true);
    } catch {
      // surfaced below via save.error
    }
  }

  return (
    <section>
      <div className="flex items-center gap-2">
        <h2 className="text-lg font-bold">Coder</h2>
        {saved && (
          <span className="inline-flex items-center gap-1 rounded-full bg-ok-soft px-2 py-0.5 text-xs font-medium text-ok">
            <Check className="size-3" /> Saved
          </span>
        )}
      </div>
      <p className="mt-1 text-sm text-muted-2">
        Pick which engine and model runs your agents. Currently:{" "}
        <span className="font-medium text-foreground">{summarize(coder)}</span>
      </p>

      {save.isError && (
        <div className="mt-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" /> {errMsg(save.error)}
        </div>
      )}

      <form
        onSubmit={(e) => void handleSave(e)}
        className="mt-4 max-w-lg space-y-4"
      >
        <div
          className="grid grid-cols-2 gap-2"
          role="radiogroup"
          aria-label="Engine"
        >
          {engines.map((eng) => (
            <label
              key={eng}
              className={cn(
                "flex cursor-pointer items-center justify-center gap-2 rounded-lg border p-3 text-sm font-medium transition-colors",
                engine === eng
                  ? "border-primary bg-primary/5 text-foreground"
                  : "border-border text-muted-2 hover:border-primary/40",
              )}
            >
              <input
                type="radio"
                name="engine"
                value={eng}
                checked={engine === eng}
                onChange={() => setEngine(eng)}
                className="sr-only"
              />
              {eng === "local" ? "Local CLI" : "API"}
            </label>
          ))}
        </div>

        {engine === "local" ? (
          <div className="space-y-1.5">
            <Label htmlFor="coder_bin">Coder CLI</Label>
            {detectedCoders.length === 0 ? (
              <p className="text-xs text-muted-2">
                No coder CLIs found on the server.
              </p>
            ) : (
              <select
                id="coder_bin"
                value={bin}
                onChange={(e) => setBin(e.target.value)}
                className="w-full rounded-md border border-border bg-background p-2 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
              >
                <option value="">Select a coder…</option>
                {detectedCoders.map((d) => (
                  <option key={d.bin} value={d.bin}>
                    {d.name} — {d.bin}
                  </option>
                ))}
              </select>
            )}
          </div>
        ) : (
          <>
            <div className="space-y-1.5">
              <Label htmlFor="coder_provider">Provider</Label>
              <select
                id="coder_provider"
                value={provider}
                onChange={(e) => setProvider(e.target.value)}
                className="w-full rounded-md border border-border bg-background p-2 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
              >
                <option value="">Select a provider…</option>
                {catalog.map((c) => {
                  // In wizard mode (showApiKeyInput) there's no ProviderCards
                  // section to "add key above" — the inline API-key field
                  // below supplies it, so every provider stays selectable
                  // regardless of hasKey.
                  const usable = c.hasKey || !c.requiresKey || showApiKeyInput;
                  return (
                    <option key={c.name} value={c.name} disabled={!usable}>
                      {c.name}
                      {!usable
                        ? showApiKeyInput
                          ? " (enter your API key below)"
                          : " (add key above)"
                        : ""}
                    </option>
                  );
                })}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="coder_model">Model</Label>
              <Input
                id="coder_model"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder={selectedEntry?.model || "model name"}
              />
            </div>

            {showApiKeyInput && (selectedEntry?.requiresKey ?? true) && (
              <div className="space-y-1.5">
                <Label htmlFor="coder_api_key">API key</Label>
                <Input
                  id="coder_api_key"
                  type="password"
                  autoComplete="off"
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                  placeholder={
                    selectedEntry?.hasKey
                      ? "leave blank to keep current key"
                      : "paste your provider API key"
                  }
                />
                <p className="text-xs text-muted-2">
                  Stored securely as a secret.
                  {selectedEntry?.hasKey &&
                    " Already set — leave blank to keep it."}
                  {selectedEntry?.docs && (
                    <>
                      {" "}
                      <a
                        href={selectedEntry.docs}
                        target="_blank"
                        rel="noreferrer"
                        className="underline"
                      >
                        Get a key ↗
                      </a>
                    </>
                  )}
                </p>
              </div>
            )}

            <div>
              <button
                type="button"
                onClick={() => setAdvancedOpen((v) => !v)}
                className="flex items-center gap-1 text-xs font-medium text-muted-2 hover:text-foreground"
              >
                <ChevronDown
                  className={cn(
                    "size-3.5 transition-transform",
                    advancedOpen && "rotate-180",
                  )}
                />
                Advanced
              </button>
              {advancedOpen && (
                <div className="mt-2 space-y-1.5">
                  <Label htmlFor="coder_base_url">
                    Base URL{" "}
                    {isCustom && <span className="text-danger">*</span>}
                  </Label>
                  <Input
                    id="coder_base_url"
                    value={baseURL}
                    onChange={(e) => setBaseURL(e.target.value)}
                    placeholder="https://api.example.com/v1"
                  />
                  {baseURLError && (
                    <p className="text-xs text-danger">{baseURLError}</p>
                  )}
                </div>
              )}
            </div>
          </>
        )}

        <div className="space-y-1.5">
          <Label htmlFor="coder_timeout">Timeout (seconds)</Label>
          <Input
            id="coder_timeout"
            type="number"
            value={timeoutS}
            onChange={(e) => setTimeoutS(Number(e.target.value))}
          />
        </div>

        <div className="flex items-center gap-3">
          <Button type="submit" disabled={save.isPending}>
            <Save />
            {save.isPending ? "Saving…" : "Save coder"}
          </Button>
          {!hideTest && (
            <Button
              type="button"
              variant="outline"
              onClick={() => test.mutate()}
              disabled={test.isPending}
            >
              {test.isPending && <Loader2 className="size-3.5 animate-spin" />}
              {test.isPending ? "Testing…" : "Test"}
            </Button>
          )}
        </div>

        {!hideTest && (
          <>
            <p className="text-xs text-muted-2">
              Tests the last saved configuration.
            </p>
            {test.isPending && (
              <p className="text-xs text-muted-2">
                Running a live test call — this can take up to a minute…
              </p>
            )}
            {test.data && test.data.ok && (
              <p className="flex items-center gap-1 text-sm text-ok">
                <Check className="size-3.5" /> {test.data.reply ?? "OK"}
              </p>
            )}
            {test.data && !test.data.ok && (
              <p className="text-sm text-danger">
                {test.data.error ?? "Test failed"}
              </p>
            )}
            {test.isError && (
              <p className="text-sm text-danger">{errMsg(test.error)}</p>
            )}
          </>
        )}
      </form>
    </section>
  );
}
