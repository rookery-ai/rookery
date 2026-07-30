import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { AlertTriangle, Check } from "lucide-react";
import { CuratedSelect } from "@/components/profile/CuratedSelect";
import {
  timezoneOptions, countryOptions, LANGUAGE_OPTIONS, TONE_OPTIONS,
} from "@/components/profile/options";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ProviderLogo } from "@/components/brand/ProviderLogo";
import { cn } from "@/lib/utils";
import { api, ApiError } from "@/lib/api";
import { useSession } from "@/lib/session";
import { useSetupQuery, type SetupResponse, type SetupStepResponse } from "@/lib/setup";
import type { APIProvider, CoderCatalogEntry, DetectedCoder, SaveCoderInput } from "@/lib/settings";
import type { ConnectorPlatform } from "@/lib/connections";
import { CoderSection } from "@/pages/settings/CoderSection";
import { ConnectorCredentialsFields } from "@/pages/connections/ConnectorCredentialsFields";
import { Linkify } from "@/lib/linkify";

function errMsg(err: unknown) {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

function ErrorNote({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {children}
    </div>
  );
}

// ── Step chip header (Basics → Master password → Coder → Profile → Chat app) ─

const CHIP_STEPS = [1, 2, 3, 4, 5] as const;
const CHIP_LABELS: Record<(typeof CHIP_STEPS)[number], string> = {
  1: "Basics",
  2: "Master password",
  3: "Coder",
  4: "Profile",
  5: "Chat app",
};

function StepChips({ step }: { step: number }) {
  const activeIndex = CHIP_STEPS.indexOf(step as (typeof CHIP_STEPS)[number]);
  return (
    <ol className="mb-6 flex flex-wrap items-center gap-x-1 gap-y-2 text-xs font-medium text-muted-2">
      {CHIP_STEPS.map((s, i) => {
        const isActive = i === activeIndex;
        const isDone = activeIndex >= 0 && i < activeIndex;
        return (
          <li key={s} className="flex items-center gap-1.5">
            <span
              className={cn(
                "flex size-5 shrink-0 items-center justify-center rounded-full border text-xs",
                isActive && "border-foreground bg-foreground text-background",
                isDone && "border-ok bg-ok text-white",
                !isActive && !isDone && "border-border text-muted-2",
              )}
            >
              {isDone ? <Check className="size-3" /> : i + 1}
            </span>
            <span className={cn(isActive && "text-foreground")}>{CHIP_LABELS[s]}</span>
            {i < CHIP_STEPS.length - 1 && <span aria-hidden className="mx-1 h-px w-4 bg-border" />}
          </li>
        );
      })}
    </ol>
  );
}

function BackBar({ onBack }: { onBack: () => void }) {
  return (
    <button
      type="button"
      onClick={onBack}
      className="mb-3 text-xs font-medium text-muted-2 hover:text-foreground"
    >
      ← Back
    </button>
  );
}

// ── Step 1: Basics ───────────────────────────────────────────────────────

function BasicsStep({
  initialName,
  onNext,
}: {
  initialName: string;
  onNext: (next: number) => void;
}) {
  const [name, setName] = useState(initialName);
  const [about, setAbout] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await api.post<SetupStepResponse>("/api/v1/setup", { step: 1, name, about });
      onNext(res.next_step);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="space-y-4">
      <h2 className="text-lg font-bold">Workspace basics</h2>
      <p className="text-sm text-muted-2">
        Your agents read this to understand what the workspace is for, so it's worth a
        sentence or two.
      </p>
      <div className="space-y-1.5">
        <Label htmlFor="setup_name">Workspace name</Label>
        <Input id="setup_name" value={name} onChange={(e) => setName(e.target.value)} required />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="setup_about">What is this workspace about?</Label>
        <textarea
          id="setup_about"
          value={about}
          onChange={(e) => setAbout(e.target.value)}
          // The name arrives pre-filled from workspace creation, so the
          // description is the field actually being asked for here.
          autoFocus
          rows={3}
          placeholder="Its purpose, domain, goals…"
          className="min-h-20 w-full resize-y rounded-md border border-border bg-background p-3 text-sm outline-none focus:border-ring focus:ring-[3px] focus:ring-ring/50"
        />
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <Button type="submit" className="w-full" disabled={busy}>
        {busy ? "Saving…" : "Continue →"}
      </Button>
    </form>
  );
}

// ── Step 2: Master password ──────────────────────────────────────────────

function MasterPasswordStep({ onBack, onNext }: { onBack: () => void; onNext: (next: number) => void }) {
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [mismatch, setMismatch] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setMismatch("");
    setError("");
    if (password.length < 8) {
      setMismatch("Master password must be at least 8 characters");
      return;
    }
    if (password !== confirm) {
      setMismatch("Passwords do not match");
      return;
    }
    setBusy(true);
    try {
      const res = await api.post<SetupStepResponse>("/api/v1/setup", {
        step: 2,
        master_password: password,
        confirm,
      });
      onNext(res.next_step);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="space-y-4">
      <BackBar onBack={onBack} />
      <h2 className="text-lg font-bold">Set the master password</h2>
      <p className="text-sm text-muted-2">
        This password encrypts this workspace's secrets (API keys, connector tokens) and is
        required every time you switch into this workspace. It cannot be recovered if lost.
      </p>
      <div className="space-y-1.5">
        <Label htmlFor="setup_master_password">Master password</Label>
        <Input
          id="setup_master_password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          autoFocus
          required
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="setup_master_confirm">Confirm master password</Label>
        <Input
          id="setup_master_confirm"
          type="password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          autoComplete="new-password"
          required
        />
      </div>
      {mismatch && <ErrorNote>{mismatch}</ErrorNote>}
      {error && !mismatch && <ErrorNote>{error}</ErrorNote>}
      <Button type="submit" className="w-full" disabled={busy}>
        {busy ? "Saving…" : "Set master password →"}
      </Button>
    </form>
  );
}

// ── Step 3: Coder ────────────────────────────────────────────────────────

type CoderStepData = {
  detected: DetectedCoder[];
  providers: APIProvider[];
  catalog: CoderCatalogEntry[];
  coderMode: "full" | "slim";
};

function CoderStep({
  data,
  onBack,
  onNext,
}: {
  data: CoderStepData | null;
  onBack: () => void;
  onNext: (next: number) => void;
}) {
  const mutation = useMutation({
    mutationFn: (input: SaveCoderInput) =>
      api.post<SetupStepResponse>("/api/v1/setup", {
        step: 3,
        coder_kind: input.kind,
        coder_bin: input.bin,
        coder_timeout_s: input.timeout_s,
        coder_provider: input.provider,
        coder_model: input.model,
        coder_base_url: input.base_url,
        coder_api_key: input.api_key,
      }),
    onSuccess: (res) => onNext(res.next_step),
  });

  return (
    <div className="space-y-4">
      <BackBar onBack={onBack} />
      <h2 className="text-lg font-bold">Choose a coder</h2>
      <p className="text-sm text-muted-2">
        Pick the engine that runs your agents — a local CLI already on this host, or a direct LLM
        provider API.
      </p>
      {!data ? (
        <div className="text-sm text-muted-2">Loading…</div>
      ) : (
        <CoderSection
          coder={undefined}
          detectedCoders={data.detected}
          catalog={data.catalog}
          coderMode={data.coderMode}
          saveOverride={mutation}
          hideTest
          showApiKeyInput
        />
      )}
    </div>
  );
}

// ── Step 4: Profile ──────────────────────────────────────────────────────

function ProfileStep({
  onBack,
  onNext,
  onSkip,
}: {
  onBack: () => void;
  onNext: (next: number) => void;
  onSkip: () => void;
}) {
  const [displayName, setDisplayName] = useState("");
  const [timezone, setTimezone] = useState("");
  const [language, setLanguage] = useState("");
  const [location, setLocation] = useState("");
  const [tone, setTone] = useState("");
  const timezones = useMemo(() => timezoneOptions(), []);
  const countries = useMemo(() => countryOptions(), []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await api.post<SetupStepResponse>("/api/v1/setup", {
        step: 4,
        display_name: displayName,
        timezone,
        language,
        location,
        tone,
      });
      onNext(res.next_step);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} className="space-y-4">
      <BackBar onBack={onBack} />
      <h2 className="text-lg font-bold">Workspace profile</h2>
      <p className="text-sm text-muted-2">
        Tell your agents who they act for so replies feel personal. All optional and editable
        later from Settings.
      </p>
      <div className="space-y-1.5">
        <Label htmlFor="setup_display_name">What should we call you?</Label>
        <Input
          id="setup_display_name"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          placeholder="Your name"
          autoFocus
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="setup_location">Location</Label>
        <CuratedSelect
          id="setup_location"
          value={location}
          onChange={setLocation}
          options={countries}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="setup_timezone">Timezone</Label>
        <CuratedSelect
          id="setup_timezone"
          value={timezone}
          onChange={setTimezone}
          options={timezones}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="setup_language">Preferred language</Label>
        <CuratedSelect
          id="setup_language"
          value={language}
          onChange={setLanguage}
          options={LANGUAGE_OPTIONS}
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="setup_tone">Tone</Label>
        <CuratedSelect
          id="setup_tone"
          value={tone}
          onChange={setTone}
          options={TONE_OPTIONS}
        />
      </div>
      {error && <ErrorNote>{error}</ErrorNote>}
      <Button type="submit" className="w-full" disabled={busy}>
        {busy ? "Saving…" : "Save and continue →"}
      </Button>
      <Button type="button" variant="ghost" className="w-full text-muted-2" onClick={onSkip}>
        Skip for now — I'll fill this in later
      </Button>
    </form>
  );
}

// ── Step 5: Chat app ─────────────────────────────────────────────────────

function ChatAppStep({
  data,
  onBack,
  onNext,
  onSkip,
}: {
  data: { platforms: ConnectorPlatform[] } | null;
  onBack: () => void;
  onNext: (next: number) => void;
  onSkip: () => void;
}) {
  const [selected, setSelected] = useState<ConnectorPlatform | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function pick(p: ConnectorPlatform) {
    setSelected(p);
    setValues({});
    setError("");
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!selected) return;
    setBusy(true);
    setError("");
    try {
      const res = await api.post<SetupStepResponse>("/api/v1/setup", {
        step: 5,
        platform: selected.platform,
        fields: values,
      });
      onNext(res.next_step);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  }

  const allFilled =
    !!selected && selected.fields.length > 0 && selected.fields.every((f) => (values[f.name] ?? "").trim() !== "");

  return (
    <div className="space-y-4">
      <BackBar onBack={onBack} />
      <h2 className="text-lg font-bold">Connect a chat app</h2>
      <p className="text-sm text-muted-2">
        Talk to this workspace from Telegram, Discord, or Slack. You can skip and set this up
        later from Connections.
      </p>

      {!data ? (
        <div className="text-sm text-muted-2">Loading…</div>
      ) : !selected ? (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            {data.platforms.map((p) => (
              <button
                key={p.platform}
                type="button"
                onClick={() => pick(p)}
                className="flex flex-col items-center gap-2 rounded-lg border border-border bg-background p-4 text-sm font-medium hover:border-primary/40"
              >
                <ProviderLogo name={p.platform} size={32} />
                {p.label}
              </button>
            ))}
          </div>
          <Button type="button" variant="ghost" className="w-full text-muted-2" onClick={onSkip}>
            Skip for now — I'll set this up later
          </Button>
        </>
      ) : (
        <form onSubmit={(e) => void submit(e)} className="space-y-3">
          <button
            type="button"
            onClick={() => setSelected(null)}
            className="text-xs font-medium text-muted-2 hover:text-foreground"
          >
            ← choose a different app
          </button>
          {selected.blurb && <p className="text-sm text-muted-2">{selected.blurb}</p>}
          {selected.setup_steps.length > 0 && (
            <ol className="space-y-2">
              {selected.setup_steps.map((s, i) => (
                <li
                  key={i}
                  className="flex gap-3 rounded-lg border border-border bg-background p-3 text-sm"
                >
                  <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted-surface text-xs font-semibold">
                    {i + 1}
                  </span>
                  <span className="leading-relaxed">
                    <Linkify text={s} />
                  </span>
                </li>
              ))}
            </ol>
          )}
          <ConnectorCredentialsFields
            fields={selected.fields}
            values={values}
            onChange={(name, value) => setValues((v) => ({ ...v, [name]: value }))}
          />
          {error && <ErrorNote>{error}</ErrorNote>}
          <Button type="submit" className="w-full" disabled={busy || !allFilled}>
            {busy ? "Connecting…" : "Connect"}
          </Button>
          <Button type="button" variant="ghost" className="w-full text-muted-2" onClick={onSkip}>
            Skip for now — I'll set this up later
          </Button>
        </form>
      )}
    </div>
  );
}

// ── Done ─────────────────────────────────────────────────────────────────

function DoneScreen({
  botUsername,
  onFinish,
}: {
  botUsername: string;
  onFinish: (target: string) => void;
}) {
  return (
    <div className="space-y-4 py-4 text-center">
      <div className="text-5xl">🎉</div>
      <h1 className="text-2xl font-bold">You're set up</h1>
      <p className="text-sm text-muted-2">This workspace is configured and ready to use.</p>
      {botUsername && (
        <div className="rounded-lg border border-primary/30 bg-primary/10 p-4 text-left text-sm">
          <p className="font-semibold text-primary">✅ Bot connected: {botUsername}</p>
          <p className="mt-1 text-muted-2">
            Open Telegram, find <strong>{botUsername}</strong>, and send <code>/start</code> to
            link this workspace.
          </p>
        </div>
      )}
      <div className="flex flex-col gap-2 pt-2">
        <Button onClick={() => onFinish("/agents/new")}>Create your first agent</Button>
        <Button variant="outline" onClick={() => onFinish("/kb")}>
          Explore the knowledge base
        </Button>
      </div>
    </div>
  );
}

// ── Wizard ───────────────────────────────────────────────────────────────

export default function SetupWizard() {
  const { data: session } = useSession();
  const setupQuery = useSetupQuery();
  const qc = useQueryClient();
  const nav = useNavigate();

  const [step, setStep] = useState<number | null>(null);
  const [coderData, setCoderData] = useState<CoderStepData | null>(null);
  const [connectorData, setConnectorData] = useState<{ platforms: ConnectorPlatform[] } | null>(null);
  const [botUsername, setBotUsername] = useState<string | null>(null);
  const [finishError, setFinishError] = useState("");
  const [finishing, setFinishing] = useState(false);

  function applyExtras(d: SetupResponse) {
    if (d.step === 3 && !coderData) {
      setCoderData({
        detected: d.detected_coders ?? [],
        providers: d.api_providers ?? [],
        catalog: d.coder_catalog ?? [],
        coderMode: d.coder_mode ?? "full",
      });
    }
    if (d.step === 5 && !connectorData) {
      setConnectorData({ platforms: d.platforms ?? [] });
    }
    if (d.step === 7 && botUsername === null) {
      setBotUsername(d.bot_username ?? "");
    }
  }

  // Seed the initial step + whichever step-conditional payload the first GET
  // carried (e.g. a resumed wizard landing directly on step 3 or 5).
  useEffect(() => {
    const d = setupQuery.data;
    if (!d) return;
    if (step === null) setStep(d.step);
    applyExtras(d);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [setupQuery.data]);

  // Advancing forward keeps the client in sync with the server's computed
  // step, so a plain re-GET at the moment we land on 3/5/7 for the first
  // time carries that step's payload. This does NOT refire on Back
  // navigation (coderData/connectorData/botUsername stay cached once set),
  // which is deliberate — the server's computed step has moved past 3/5 by
  // then, so a re-GET would no longer include that step's payload.
  async function advance(next: number) {
    setStep(next);
    if ((next === 3 && !coderData) || (next === 5 && !connectorData) || (next === 7 && botUsername === null)) {
      try {
        const d = await api.get<SetupResponse>("/api/v1/setup");
        applyExtras(d);
      } catch {
        // The step will just show its own "Loading…" state; the user can
        // still navigate Back and forward again to retry.
      }
    }
  }

  async function finish(target: string) {
    setFinishing(true);
    setFinishError("");
    try {
      await api.post("/api/v1/setup", { step: 7 });
      await qc.invalidateQueries({ queryKey: ["session"] });
      nav(target);
    } catch (err) {
      setFinishError(errMsg(err));
    } finally {
      setFinishing(false);
    }
  }

  const workspaceName = session?.workspace?.name ?? "";

  return (
    <div className="flex min-h-screen items-center justify-center bg-chrome p-4">
      <div className="w-full max-w-xl rounded-xl border border-border bg-background p-8 shadow-sm">
        {step !== null && step !== 7 && <StepChips step={step} />}

        {setupQuery.isLoading && step === null && (
          <div className="text-sm text-muted-2">Loading…</div>
        )}
        {setupQuery.isError && step === null && (
          <ErrorNote>{errMsg(setupQuery.error)}</ErrorNote>
        )}

        {step === 1 && <BasicsStep initialName={workspaceName} onNext={(n) => void advance(n)} />}
        {step === 2 && (
          <MasterPasswordStep onBack={() => setStep(1)} onNext={(n) => void advance(n)} />
        )}
        {step === 3 && (
          <CoderStep data={coderData} onBack={() => setStep(2)} onNext={(n) => void advance(n)} />
        )}
        {step === 4 && (
          <ProfileStep
            onBack={() => setStep(3)}
            onNext={(n) => void advance(n)}
            onSkip={() =>
              void (async () => {
                const res = await api.post<SetupStepResponse>("/api/v1/setup", { step: 4, skip: true });
                void advance(res.next_step);
              })()
            }
          />
        )}
        {step === 5 && (
          <ChatAppStep
            data={connectorData}
            onBack={() => setStep(4)}
            onNext={(n) => void advance(n)}
            onSkip={() =>
              void (async () => {
                const res = await api.post<SetupStepResponse>("/api/v1/setup", { step: 5, skip: true });
                void advance(res.next_step);
              })()
            }
          />
        )}
        {step === 7 && (
          <>
            <DoneScreen botUsername={botUsername ?? ""} onFinish={(t) => void finish(t)} />
            {finishError && <ErrorNote>{finishError}</ErrorNote>}
            {finishing && <p className="mt-2 text-center text-xs text-muted-2">Finishing up…</p>}
          </>
        )}
      </div>
    </div>
  );
}
