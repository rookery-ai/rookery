import { useEffect, useRef, useState } from "react";
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

// The same field means different things per coder, so the placeholder shows a
// real example for the selected one. Illustrative only — never validated, since
// the valid model set is the coder's business and changes constantly.
function localModelPlaceholder(bin: string): string {
  const b = bin.toLowerCase();
  if (b.includes("opencode")) return "ollama-cloud/glm-5.2";
  if (b.includes("codex")) return "gpt-5.5-codex";
  if (b.includes("gemini")) return "gemini-2.5-pro";
  if (b.includes("cursor")) return "sonnet-4.5";
  if (b.includes("claude")) return "inherits your login";
  return "model name";
}

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
  hideTimeout = false,
  defaultTimeoutS = 1800,
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
  // Wizard-mode: hide the timeout field entirely. A new owner cannot judge the
  // number, and showing it was worse than not — the value on screen was this
  // component's own hardcoded 120, which every save then wrote to the database.
  hideTimeout?: boolean;
  // The server's effective default, shown as the placeholder so an empty field
  // states what it will do rather than looking unset.
  defaultTimeoutS?: number;
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
  // A STRING, and empty means "follow the server default" — not a number with a
  // hardcoded fallback. It was `useState(120)`, loaded as `coder.timeout_s || 120`
  // and posted on every save, so a stored 0 (which is exactly how the column
  // spells "use the default") rendered as 120 and saved back as 120. Merely
  // opening this page and pressing Save converted a workspace from following the
  // 30-minute default to a hard 2-minute cap — long enough to look deliberate,
  // short enough to cut agent builds off mid-repair.
  const [timeoutS, setTimeoutS] = useState("");
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [baseURL, setBaseURL] = useState("");
  // True once the base URL holds a value the user chose — either typed here or
  // loaded from a saved config. The prefill must never overwrite one of those,
  // and a plain equality check against the default cannot tell them apart,
  // because a user may legitimately type the default back in.
  const baseURLTouched = useRef(false);
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
    setTimeoutS(coder.timeout_s > 0 ? String(coder.timeout_s) : "");
    setProvider(coder.provider);
    setModel(coder.model);
    const entry = catalog.find((c) => c.name === coder.provider);
    if (coder.base_url) {
      setBaseURL(coder.base_url);
      baseURLTouched.current = true;
    } else {
      // Empty means "follow the registry default" — show that default rather
      // than a blank box, so the value is editable instead of merely absent.
      setBaseURL(entry?.base ?? "");
    }
    if (entry?.group === "local") setAdvancedOpen(true);
  }, [coder, coderMode, catalog]);

  // A slim build ships no CLI coder binary, so "local" is not offered. This
  // mirrors the server-side guard in rejectLocalInSlim — the UI is the
  // convenience, the API is the enforcement.
  const engines: Engine[] = coderMode === "slim" ? ["api"] : ["local", "api"];

  const selectedEntry = catalog.find((c) => c.name === provider);
  const isCustom = selectedEntry?.custom ?? false;

  function handleProviderChange(name: string) {
    setProvider(name);
    const entry = catalog.find((c) => c.name === name);
    if (!baseURLTouched.current) setBaseURL(entry?.base ?? "");
    // A self-hosted server is the case where the URL routinely needs editing —
    // a different port, a different host. Open the section rather than making
    // the user discover it.
    if (entry?.group === "local") setAdvancedOpen(true);
  }

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
    // An unmodified prefill is not an override. Posting it verbatim would pin
    // this workspace to today's URL forever; empty keeps it following the
    // registry default, exactly as before this field was prefilled.
    const trimmedBase = baseURL.trim();
    const effectiveBase =
      trimmedBase === (selectedEntry?.base ?? "") ? "" : trimmedBase;
    try {
      await save.mutateAsync({
        kind: engine,
        bin: engine === "local" ? bin : "",
        // 0 means "follow the server default" on the write path, so an empty
        // field posts 0 rather than a number this component invented.
        timeout_s: timeoutS.trim() === "" ? 0 : Number(timeoutS),
        provider: engine === "api" ? provider : "",
        // Sent for BOTH engines: a local CLI coder takes a model too, and
        // blanking it here is half of why OpenCode could not be configured.
        model: model,
        base_url: engine === "api" ? effectiveBase : "",
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
            <div className="space-y-1.5 pt-2">
              <Label htmlFor="coder_local_model">Model (optional)</Label>
              <Input
                id="coder_local_model"
                value={model}
                onChange={(e) => setModel(e.target.value)}
                placeholder={localModelPlaceholder(bin)}
              />
              {/* Stated as help, not enforced as validation: a user relying on a
                  host-level default should not be blocked, and a hard rule here
                  would be wrong the moment OpenCode ships a default of its own. */}
              <p className="text-xs text-muted-2">
                {bin.includes("opencode")
                  ? "OpenCode has no default model — without one it fails with a 401 that looks like broken authentication."
                  : "Leave blank to use the coder's own default."}
              </p>
            </div>
          </div>
        ) : (
          <>
            <div className="space-y-1.5">
              <Label htmlFor="coder_provider">Provider</Label>
              <select
                id="coder_provider"
                value={provider}
                onChange={(e) => handleProviderChange(e.target.value)}
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
                      {c.label || c.name}
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
                  autoComplete="new-password"
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
                {!advancedOpen && baseURL && (
                  // The effective URL, visible without opening the section —
                  // this is what makes a Bedrock region or a self-hosted port
                  // discoverable rather than hidden one click away.
                  <span className="truncate font-normal text-muted-2">
                    · {baseURL}
                  </span>
                )}
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
                    onChange={(e) => {
                      baseURLTouched.current = true;
                      setBaseURL(e.target.value);
                    }}
                    placeholder={
                      selectedEntry?.base || "https://api.example.com/v1"
                    }
                  />
                  {baseURLError && (
                    <p className="text-xs text-danger">{baseURLError}</p>
                  )}
                  <p className="text-xs text-muted-2">
                    Change the port here if your server listens somewhere else.
                  </p>
                </div>
              )}
            </div>
          </>
        )}

        {/* Hidden during setup: a brand-new owner has no way to judge this
            number, and the one they were shown (120) was actively harmful. The
            field stays here on the settings page, where someone changing it has
            context for why. */}
        {!hideTimeout && (
          <div className="space-y-1.5">
            <Label htmlFor="coder_timeout">Timeout (seconds)</Label>
            <Input
              id="coder_timeout"
              type="number"
              min={1}
              value={timeoutS}
              placeholder={String(defaultTimeoutS)}
              onChange={(e) => setTimeoutS(e.target.value)}
            />
            <p className="text-xs text-muted-2">
              How long one coder call may run. Leave empty to follow the default
              of {defaultTimeoutS} seconds. Building an agent is the longest
              call — the coder writes files, runs them against live services and
              fixes what fails — so a short timeout here shows up as a failed
              build rather than a slow one.
            </p>
          </div>
        )}

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
