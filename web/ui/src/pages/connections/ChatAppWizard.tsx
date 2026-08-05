import { useEffect, useState } from "react";
import { ArrowLeft, ArrowRight, Check, Link2, RotateCcw, Save, Unlink } from "lucide-react";
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
  useUnlinkConnector,
  type ConnectorPlatform,
} from "@/lib/connections";
import { ErrorNote, TestResult, WarningNote } from "@/components/chat-connect/notes";
import { LinkStep } from "@/components/chat-connect/LinkStep";
import { connectorsSource } from "@/components/chat-connect/source";
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

// ── Not-connected: 4-step guided wizard ─────────────────────────────────────

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
          <div className="flex justify-end">
            {testOk === true ? (
              <Button onClick={() => setStep("link")}>
                <ArrowRight />
                Next
              </Button>
            ) : (
              !testMutation.isPending && (
                <Button
                  variant="outline"
                  onClick={() => testMutation.mutate(platform.platform)}
                >
                  <RotateCcw />
                  Retry
                </Button>
              )
            )}
          </div>
        </div>
      )}

      {step === "link" && (
        <LinkStep
          platform={platform}
          source={connectorsSource}
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
  const [unlinkError, setUnlinkError] = useState<string | null>(null);

  // `AppShell`'s slide-over is `useState<{node: ReactNode}>`, so the element
  // ConnectionsPage passes to `open()` is created once and never re-created —
  // this component's `platform` prop is a frozen snapshot from the moment the
  // panel opened. Unlink flips `linked` server-side and invalidates the
  // `["connectors"]` query, but a prop can't observe that; without reading the
  // live query result here, the green header and Done button below would keep
  // rendering after a successful Unlink. Mirrors `LinkStep`'s own `live` read.
  const { data } = useConnectors();
  const live =
    data?.platforms?.find((p) => p.platform === platform.platform) ?? platform;

  const testMutation = useTestConnector();
  const deleteMutation = useDeleteConnector();
  const unlinkMutation = useUnlinkConnector();

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

  async function handleUnlink() {
    setUnlinkError(null);
    try {
      await unlinkMutation.mutateAsync(platform.platform);
    } catch (err) {
      setUnlinkError(
        err instanceof ApiError ? err.message : "Something went wrong",
      );
    }
  }

  const testOk = testMutation.data ? testMutation.data.ok : null;

  return (
    <PanelBody>
      {/* A connected-but-unlinked platform must never show this green header
          — that's the same "Connected ✓" claim the link step exists to
          withhold, just reachable from Manage instead of the connect flow.
          Disconnect stays reachable in BOTH branches below: a token that
          authenticates as the wrong bot still has to be removable. */}
      {live.linked ? (
        <>
          <div className="space-y-1">
            <div className="flex items-center gap-1.5 text-sm font-medium text-ok">
              <span className="size-1.5 rounded-full bg-ok" /> Connected
            </div>
            {live.identity && (
              <div className="text-xs text-muted-2">{live.identity}</div>
            )}
            <div className="flex items-center gap-1.5 text-sm font-medium text-ok">
              <Check className="size-4" /> Linked as {live.linked_identity}
            </div>
          </div>

          {/* Unlink drops the operator's /start link but keeps the saved bot
              credentials — the platform falls back to connected-but-unlinked,
              not disconnected. This is what makes a wrong link
              self-serviceable instead of a dead end ("contact your
              administrator" in a single-owner product). Distinct from
              Disconnect below, which does remove credentials. */}
          <Button
            variant="outline"
            size="sm"
            onClick={() => void handleUnlink()}
            disabled={unlinkMutation.isPending}
          >
            <Unlink />
            {unlinkMutation.isPending ? "Unlinking…" : "Unlink this account"}
          </Button>
          {unlinkError && <ErrorNote>{unlinkError}</ErrorNote>}

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
        </>
      ) : (
        <LinkStep
          platform={live}
          source={connectorsSource}
          onFinishLater={() => close()}
          onDone={() => close()}
        />
      )}

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

      {live.linked && (
        <div className="flex justify-end">
          <Button onClick={() => close()}>Done</Button>
        </div>
      )}
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
