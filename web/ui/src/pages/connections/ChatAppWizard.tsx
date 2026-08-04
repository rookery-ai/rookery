import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { AlertTriangle, ArrowLeft, ArrowRight, Check, Link2, Loader2, RotateCcw, Save, Unlink } from "lucide-react";
import { useSlideOver } from "@/components/shell/AppShell";
import { PanelBody } from "@/components/shell/PanelBody";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ApiError } from "@/lib/api";
import { Linkify } from "@/lib/linkify";
import {
  useConnectors,
  useSaveConnector,
  useTestConnector,
  useDeleteConnector,
  type ConnectorPlatform,
} from "@/lib/connections";
import { ConnectorCredentialsFields } from "./ConnectorCredentialsFields";

type ChatAppWizardProps = { platform: ConnectorPlatform };

// ── Step chips (1 Setup — 2 Credentials — 3 Test — 4 Link) ─────────────────

type Step = "setup" | "credentials" | "test" | "link";
const STEPS: Step[] = ["setup", "credentials", "test", "link"];
const STEP_LABELS: Record<Step, string> = {
  setup: "Setup",
  credentials: "Credentials",
  test: "Test",
  link: "Link",
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
                "flex size-5 shrink-0 items-center justify-center rounded-full border text-xs",
                isActive && "border-foreground bg-foreground text-background",
                isDone && "border-ok bg-ok text-white",
                !isActive && !isDone && "border-border text-muted-2",
              )}
            >
              {isDone ? <Check className="size-3" /> : i + 1}
            </span>
            <span className={cn(isActive && "text-foreground")}>
              {STEP_LABELS[s]}
            </span>
            {i < STEPS.length - 1 && (
              <span aria-hidden className="mx-1 h-px w-4 bg-border" />
            )}
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
  if (ok === true)
    return <OkNote>Connected as {identity ?? platform} ✓</OkNote>;
  if (ok === false)
    return <ErrorNote>{error ?? "Connection failed"}</ErrorNote>;
  return null;
}

// ── Step 4: Link your account ───────────────────────────────────────────────
//
// The identity row is created only when the operator's /start actually reaches
// the bot, so its presence proves the inbound path end to end — which a token
// check cannot. Until it lands there is deliberately no Done button and no
// green state: the product must never signal completion it has not verified.
function LinkStep({
  platform,
  onFinishLater,
  onDone,
}: {
  platform: ConnectorPlatform;
  onFinishLater: () => void;
  onDone: () => void;
}) {
  // `linked` starts from the platform snapshot the wizard opened with and is
  // latched true the moment the poll confirms it — so the poll can actually
  // stop once linked, rather than running for the rest of the panel's life.
  const [linked, setLinked] = useState(platform.linked);
  const { data } = useConnectors({ refetchInterval: linked ? false : 2000 });
  const live =
    data?.platforms?.find((p) => p.platform === platform.platform) ?? platform;

  useEffect(() => {
    if (live.linked && !linked) setLinked(true);
  }, [live.linked, linked]);

  if (live.linked) {
    return (
      <div className="space-y-3">
        <OkNote>Linked as {live.linked_identity}</OkNote>
        <div className="flex justify-end">
          <Button onClick={onDone}>Done</Button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {live.invite_url && (
        <div className="space-y-2 rounded-lg border border-border bg-background p-3 text-sm">
          <p className="font-medium">First, add the bot to a server</p>
          <p className="text-muted-2">
            Discord only allows a direct message between accounts that share a
            server. Afterwards, check the server's Privacy Settings and make sure
            Direct Messages are allowed.
          </p>
          <Button asChild variant="outline" size="sm">
            <a href={live.invite_url} target="_blank" rel="noreferrer">
              <Link2 />
              Invite to a server
            </a>
          </Button>
        </div>
      )}

      <div className="space-y-2 rounded-lg border border-border bg-background p-3 text-sm">
        <p className="font-medium">Then send the bot a message</p>
        <p className="text-muted-2">
          Open a direct message with{" "}
          <b className="text-foreground">{live.identity || live.label}</b> and
          send:
        </p>
        <code className="block rounded bg-muted-surface px-2 py-1 font-mono">/start</code>
        {live.dm_url && (
          <Button asChild variant="outline" size="sm">
            <a href={live.dm_url} target="_blank" rel="noreferrer">
              <ArrowRight />
              Open {live.label}
            </a>
          </Button>
        )}
      </div>

      <Spinner text="Waiting for you to send /start…" />

      <div className="flex justify-end">
        <Button variant="link" onClick={onFinishLater}>
          Finish later — I'm not linked yet
        </Button>
      </div>
    </div>
  );
}

// ── Not-connected: 4-step guided wizard ─────────────────────────────────────

function ConnectWizard({
  platform,
  initialStep,
}: {
  platform: ConnectorPlatform;
  initialStep?: Step;
}) {
  const { close } = useSlideOver();
  const [step, setStep] = useState<Step>(initialStep ?? "setup");
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

  // A token check is not proof the integration works — only advance past it
  // automatically into the step that waits for the real /start handshake.
  useEffect(() => {
    if (step === "test" && testMutation.data?.ok) setStep("link");
  }, [step, testMutation.data]);

  async function handleSave() {
    setSaveError(null);
    try {
      const res = await saveMutation.mutateAsync({
        platform: platform.platform,
        values,
      });
      if (res.warning) setWarning(res.warning);
      setStep("test");
    } catch (err) {
      setSaveError(
        err instanceof ApiError ? err.message : "Something went wrong",
      );
    }
  }

  const testOk = testMutation.data ? testMutation.data.ok : null;

  return (
    <PanelBody>
      <StepChips step={step} />
      {warning && <WarningNote>{warning}</WarningNote>}

      {step === "setup" && (
        <div className="space-y-3">
          {platform.blurb && (
            <p className="text-sm text-muted-2">{platform.blurb}</p>
          )}
          {platform.setup_steps.length > 0 && (
            <ol className="space-y-2">
              {platform.setup_steps.map((s, i) => (
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
          <div className="flex justify-end">
            <Button onClick={() => setStep("credentials")}>
              <ArrowRight />
              Next
            </Button>
          </div>
        </div>
      )}

      {step === "credentials" && (
        <div className="space-y-3">
          <ConnectorCredentialsFields
            fields={platform.fields}
            values={values}
            onChange={(name, value) =>
              setValues((v) => ({ ...v, [name]: value }))
            }
          />
          {saveError && <ErrorNote>{saveError}</ErrorNote>}
          <div className="flex justify-between">
            <Button variant="outline" onClick={() => setStep("setup")}>
              <ArrowLeft />
              Back
            </Button>
            <Button
              onClick={() => void handleSave()}
              disabled={saveMutation.isPending}
            >
              <Save />
              {saveMutation.isPending ? "Saving…" : "Save & continue"}
            </Button>
          </div>
        </div>
      )}

      {step === "test" && (
        <div className="space-y-3">
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
          {/* testOk === true never lingers here — the effect above advances
              to the link step the moment the mutation resolves, so a green
              "Connected" state is never the final word. */}
          {testOk === false && !testMutation.isPending && (
            <div className="flex justify-end">
              <Button
                variant="outline"
                onClick={() => testMutation.mutate(platform.platform)}
              >
                <RotateCcw />
                Retry
              </Button>
            </div>
          )}
        </div>
      )}

      {step === "link" && (
        <LinkStep
          platform={platform}
          onFinishLater={() => close()}
          onDone={() => close()}
        />
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
      setDisconnectError(
        err instanceof ApiError ? err.message : "Something went wrong",
      );
    }
  }

  const testOk = testMutation.data ? testMutation.data.ok : null;

  return (
    <PanelBody>
      <div className="space-y-1">
        <div className="flex items-center gap-1.5 text-sm font-medium text-ok">
          <span className="size-1.5 rounded-full bg-ok" /> Connected
        </div>
        {platform.identity && (
          <div className="text-xs text-muted-2">{platform.identity}</div>
        )}
        {platform.linked && (
          <div className="flex items-center gap-1.5 text-sm font-medium text-ok">
            <Check className="size-4" /> Linked as {platform.linked_identity}
          </div>
        )}
      </div>

      <Button
        variant="outline"
        onClick={() => testMutation.mutate(platform.platform)}
        disabled={testMutation.isPending}
      >
        <Link2 />
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
          <Button
            variant="outline"
            className="text-danger"
            onClick={() => setConfirming(true)}
          >
            <Unlink />
            Disconnect…
          </Button>
        ) : (
          <div className="space-y-2 rounded-md border border-danger/30 bg-danger-soft p-3 text-xs text-danger">
            <p>
              Disconnect {platform.label}? You'll need to reconnect to use it
              again.
            </p>
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={() => setConfirming(false)}
              >
                Cancel
              </Button>
              <Button
                size="sm"
                variant="destructive"
                onClick={() => void handleDisconnect()}
                disabled={deleteMutation.isPending}
              >
                <Unlink />
                Yes, disconnect
              </Button>
            </div>
          </div>
        )}
        {disconnectError && <ErrorNote>{disconnectError}</ErrorNote>}
      </div>

      <div className="flex justify-end">
        <Button onClick={() => close()}>Done</Button>
      </div>
    </PanelBody>
  );
}

// ── Entry point ──────────────────────────────────────────────────────────────
//
// `connected` only proves the token authenticates. A connected-but-unlinked
// platform still routes back into the wizard's link step, not Manage — Manage
// (and its Done button) is reserved for a platform the operator has actually
// linked, so a stale/never-linked connection can never present as green.
export function ChatAppWizard({ platform }: ChatAppWizardProps) {
  if (!platform.connected) return <ConnectWizard platform={platform} />;
  if (!platform.linked) return <ConnectWizard platform={platform} initialStep="link" />;
  return <ManageWizard platform={platform} />;
}

export default ChatAppWizard;
