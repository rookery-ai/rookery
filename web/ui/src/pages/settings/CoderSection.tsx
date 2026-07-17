import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { AlertTriangle, Check, ChevronDown, Loader2 } from "lucide-react";
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
}: {
  coder: CoderConfig | undefined;
  detectedCoders: DetectedCoder[];
  catalog: CoderCatalogEntry[];
}) {
  const [engine, setEngine] = useState<Engine>("local");
  const [bin, setBin] = useState("");
  const [timeoutS, setTimeoutS] = useState(120);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [baseURL, setBaseURL] = useState("");
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [baseURLError, setBaseURLError] = useState("");
  const [saved, setSaved] = useState(false);

  const save = useSaveCoder();
  const test = useTestCoder();

  useEffect(() => {
    if (!coder) return;
    setEngine(coder.kind === "api" ? "api" : "local");
    setBin(coder.bin);
    setTimeoutS(coder.timeout_s || 120);
    setProvider(coder.provider);
    setModel(coder.model);
    setBaseURL(coder.base_url);
  }, [coder]);

  const selectedEntry = catalog.find((c) => c.name === provider);
  const isCustom = selectedEntry?.custom ?? false;

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    setSaved(false);
    setBaseURLError("");
    if (engine === "api" && isCustom && !baseURL.trim()) {
      setBaseURLError("A base URL is required for a Custom (OpenAI-compatible) provider");
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
        api_key: "",
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

      <form onSubmit={(e) => void handleSave(e)} className="mt-4 max-w-lg space-y-4">
        <div className="grid grid-cols-2 gap-2" role="radiogroup" aria-label="Engine">
          {(["local", "api"] as const).map((eng) => (
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
              <p className="text-xs text-muted-2">No coder CLIs found on the server.</p>
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
                  const usable = c.hasKey || !c.requiresKey;
                  return (
                    <option key={c.name} value={c.name} disabled={!usable}>
                      {c.name}
                      {!usable ? " (add key above)" : ""}
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

            <div>
              <button
                type="button"
                onClick={() => setAdvancedOpen((v) => !v)}
                className="flex items-center gap-1 text-xs font-medium text-muted-2 hover:text-foreground"
              >
                <ChevronDown
                  className={cn("size-3.5 transition-transform", advancedOpen && "rotate-180")}
                />
                Advanced
              </button>
              {advancedOpen && (
                <div className="mt-2 space-y-1.5">
                  <Label htmlFor="coder_base_url">
                    Base URL {isCustom && <span className="text-danger">*</span>}
                  </Label>
                  <Input
                    id="coder_base_url"
                    value={baseURL}
                    onChange={(e) => setBaseURL(e.target.value)}
                    placeholder="https://api.example.com/v1"
                  />
                  {baseURLError && <p className="text-xs text-danger">{baseURLError}</p>}
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
            {save.isPending ? "Saving…" : "Save coder"}
          </Button>
          <Button type="button" variant="outline" onClick={() => test.mutate()} disabled={test.isPending}>
            {test.isPending && <Loader2 className="size-3.5 animate-spin" />}
            {test.isPending ? "Testing…" : "Test"}
          </Button>
        </div>

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
          <p className="text-sm text-danger">{test.data.error ?? "Test failed"}</p>
        )}
        {test.isError && <p className="text-sm text-danger">{errMsg(test.error)}</p>}
      </form>
    </section>
  );
}
