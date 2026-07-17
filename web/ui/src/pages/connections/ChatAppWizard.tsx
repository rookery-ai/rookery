import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { AlertTriangle, Check, Loader2 } from "lucide-react";
import { useSlideOver } from "@/components/shell/AppShell";
import { PanelBody } from "@/components/shell/PanelBody";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { Linkify } from "@/lib/linkify";
import {
  useSaveConnector,
  useTestConnector,
  useDeleteConnector,
  type ConnectorPlatform,
} from "@/lib/connections";

type ChatAppWizardProps = { platform: ConnectorPlatform };

// ── Step chips (1 Setup — 2 Credentials — 3 Test) ───────────────────────────

type Step = "setup" | "credentials" | "test";
const STEPS: Step[] = ["setup", "credentials", "test"];
const STEP_LABELS: Record<Step, string> = {
  setup: "Setup",
  credentials: "Credentials",
  test: "Test",
};

function StepChips({ step }: { step: Step }) {
  const activeIndex = STEPS.indexOf(step);
  return (
    <ol className="flex items-center gap-2 text-xs font-medium text-muted-2">
      {STEPS.map((s, i) => {
        const isActive = i === activeIndex;
        const isDone = i < activeIndex;
        return (
          <li key={s} className="flex items-center gap-2">
            <span
              className={cn(
                "flex size-5 shrink-0 items-center justify-center rounded-full border text-[10px]",
                isActive && "border-foreground bg-foreground text-background",
                isDone && "border-ok bg-ok text-white",
                !isActive && !isDone && "border-border text-muted-2",
              )}
            >
              {isDone ? <Check className="size-3" /> : i + 1}
            </span>
            <span className={cn(isActive && "text-foreground")}>{STEP_LABELS[s]}</span>
            {i < STEPS.length - 1 && <span aria-hidden className="mx-1 h-px w-4 bg-border" />}
          </li>
        );
      })}
    </ol>
  );
}

function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
      <AlertTriangle className="size-3.5 shrink-0" />
      {children}
    </div>
  );
}

function WarningNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">
      <AlertTriangle className="size-3.5 shrink-0" />
      {children}
    </div>
  );
}

function OkNote({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center gap-2 rounded-md bg-ok-soft px-3 py-2 text-sm font-medium text-ok">
      <Check className="size-4 shrink-0" />
      {children}
    </div>
  );
}

function Spinner({ text }: { text: string }) {
  return (
    <div className="flex items-center gap-2 text-sm text-muted-2">
      <Loader2 className="size-4 shrink-0 animate-spin" />
      {text}
    </div>
  );
}

// ── Test-connection result (shared by the wizard's step 3 and Manage) ──────

function TestResult({
  platform,
  pending,
  ok,
  identity,
  error,
}: {
  platform: string;
  pending: boolean;
  ok: boolean | null;
  identity?: string;
  error?: string;
}) {
  if (pending) return <Spinner text="Checking the connection…" />;
  if (ok === true) return <OkNote>Connected as {identity ?? platform} ✓</OkNote>;
  if (ok === false) return <ErrorNote>{error ?? "Connection failed"}</ErrorNote>;
  return null;
}

// ── Not-connected: 3-step guided wizard ─────────────────────────────────────

function ConnectWizard({ platform }: { platform: ConnectorPlatform }) {
  const { close } = useSlideOver();
  const [step, setStep] = useState<Step>("setup");
  const [values, setValues] = useState<Record<string, string>>({});
  const [saveError, setSaveError] = useState<string | null>(null);
  const [warning, setWarning] = useState<string | null>(null);

  const saveMutation = useSaveConnector();
  const testMutation = useTestConnector();

  // Auto-fire the live test the moment step 3 is entered.
  useEffect(() => {
    if (step === "test") testMutation.mutate(platform.platform);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  async function handleSave() {
    setSaveError(null);
    try {
      const res = await saveMutation.mutateAsync({ platform: platform.platform, values });
      if (res.warning) setWarning(res.warning);
      setStep("test");
    } catch (err) {
      setSaveError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  const testOk = testMutation.data ? testMutation.data.ok : null;

  return (
    <PanelBody>
      <StepChips step={step} />

      {step === "setup" && (
        <div className="space-y-3">
          {platform.blurb && <p className="text-sm text-muted-2">{platform.blurb}</p>}
          {platform.setup_steps.length > 0 && (
            <ol className="space-y-2">
              {platform.setup_steps.map((s, i) => (
                <li
                  key={i}
                  className="flex gap-3 rounded-lg border border-border bg-background p-3 text-sm"
                >
                  <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted text-[11px] font-semibold">
                    {i + 1}
                  </span>
                  <span className="leading-relaxed">
                    <Linkify text={s} />
                  </span>
                </li>
              ))}
            </ol>
          )}
          <div className="flex justify-end">
            <Button onClick={() => setStep("credentials")}>Next →</Button>
          </div>
        </div>
      )}

      {step === "credentials" && (
        <div className="space-y-3">
          {platform.fields.map((f) => (
            <div key={f.name} className="space-y-1">
              <Label htmlFor={`field-${f.name}`}>{f.label}</Label>
              <Input
                id={`field-${f.name}`}
                type={f.secret ? "password" : "text"}
                value={values[f.name] ?? ""}
                onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
                autoComplete="off"
              />
            </div>
          ))}
          {saveError && <ErrorNote>{saveError}</ErrorNote>}
          <div className="flex justify-between">
            <Button variant="outline" onClick={() => setStep("setup")}>
              ← Back
            </Button>
            <Button onClick={() => void handleSave()} disabled={saveMutation.isPending}>
              {saveMutation.isPending ? "Saving…" : "Save & continue"}
            </Button>
          </div>
        </div>
      )}

      {step === "test" && (
        <div className="space-y-3">
          {warning && <WarningNote>{warning}</WarningNote>}
          <TestResult
            platform={platform.label}
            pending={testMutation.isPending}
            ok={testOk}
            identity={testMutation.data?.identity}
            error={testMutation.data?.error}
          />
          {testMutation.isError && (
            <ErrorNote>
              {testMutation.error instanceof ApiError
                ? testMutation.error.message
                : "Something went wrong"}
            </ErrorNote>
          )}
          <div className="flex justify-end">
            {testOk === true ? (
              <Button onClick={() => close()}>Done</Button>
            ) : (
              !testMutation.isPending && (
                <Button
                  variant="outline"
                  onClick={() => testMutation.mutate(platform.platform)}
                >
                  Retry
                </Button>
              )
            )}
          </div>
        </div>
      )}
    </PanelBody>
  );
}

// ── Connected: Manage variant ───────────────────────────────────────────────

function ManageWizard({ platform }: { platform: ConnectorPlatform }) {
  const { close } = useSlideOver();
  const [confirming, setConfirming] = useState(false);
  const [disconnectError, setDisconnectError] = useState<string | null>(null);

  const testMutation = useTestConnector();
  const deleteMutation = useDeleteConnector();

  async function handleDisconnect() {
    setDisconnectError(null);
    try {
      await deleteMutation.mutateAsync(platform.platform);
      close();
    } catch (err) {
      setDisconnectError(err instanceof ApiError ? err.message : "Something went wrong");
    }
  }

  const testOk = testMutation.data ? testMutation.data.ok : null;

  return (
    <PanelBody>
      <div className="space-y-1">
        <div className="flex items-center gap-1.5 text-sm font-medium text-ok">
          <span className="size-1.5 rounded-full bg-ok" /> Connected
        </div>
        {platform.identity && <div className="text-xs text-muted-2">{platform.identity}</div>}
      </div>

      <Button
        variant="outline"
        onClick={() => testMutation.mutate(platform.platform)}
        disabled={testMutation.isPending}
      >
        {testMutation.isPending ? "Checking…" : "Test connection"}
      </Button>

      <TestResult
        platform={platform.label}
        pending={testMutation.isPending}
        ok={testOk}
        identity={testMutation.data?.identity}
        error={testMutation.data?.error}
      />

      <div className="border-t border-border pt-3">
        {!confirming ? (
          <Button variant="outline" className="text-danger" onClick={() => setConfirming(true)}>
            Disconnect…
          </Button>
        ) : (
          <div className="space-y-2 rounded-md border border-danger/30 bg-danger-soft p-3 text-xs text-danger">
            <p>
              Disconnect {platform.label}? You'll need to reconnect to use it again.
            </p>
            <div className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => setConfirming(false)}>
                Cancel
              </Button>
              <Button
                size="sm"
                variant="destructive"
                onClick={() => void handleDisconnect()}
                disabled={deleteMutation.isPending}
              >
                Yes, disconnect
              </Button>
            </div>
          </div>
        )}
        {disconnectError && <ErrorNote>{disconnectError}</ErrorNote>}
      </div>
    </PanelBody>
  );
}

// ── Entry point ──────────────────────────────────────────────────────────────

export function ChatAppWizard({ platform }: ChatAppWizardProps) {
  return platform.connected ? (
    <ManageWizard platform={platform} />
  ) : (
    <ConnectWizard platform={platform} />
  );
}

export default ChatAppWizard;
