import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { AlertTriangle } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { openSSE, type SSEHandle } from "@/lib/sse";
import { ChatScroll } from "@/components/chat/ChatScroll";
import { ChatMessageBubble, TypingIndicator } from "@/components/chat/Bubbles";
import { Composer } from "@/components/chat/Composer";
import { ActivityCard, type ActivityStatus } from "@/components/chat/ActivityCard";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Stepper } from "./Stepper";
import { SpecPanel } from "./SpecPanel";

// ── Binding interfaces (Task 8 — the skill creator — reuses this component
// via these exact shapes; see .superpowers/sdd/task-6-brief.md) ──────────────

export type DesignerEndpoints = {
  design: string; // POST {name?,message} → legacy {response,done,state?,building?,generation_failed?,can_keep_as_is?,agent_id?}
  cancel: string; // POST → {status}
  resume: string; // POST → {response,state,history,agent_id?,agent_name?,generation_failed?,can_keep_as_is?}
  dismiss: string; // POST → {status}
  progress: string; // GET SSE
  state?: string; // GET recovery — ABSENT for the skill designer
};

export type DesignerLabels = {
  steps: [string, string, string, string];
  buildButton: string;
  saveButton: string;
  entityName: string;
};

// Additive, optional props beyond the binding signature — Task 8 either
// passes its own equivalents or ignores them; none change the required
// (endpoints, labels, startPayload?, onDone) contract.
export type DesignerDraft = { name?: string } | null | undefined;

export type DesignerSurfaceProps = {
  endpoints: DesignerEndpoints;
  labels: DesignerLabels;
  startPayload?: Record<string, unknown>; // merged into the very first design POST (e.g. {name})
  onDone: (id?: string) => void;
  // The draft that would show a resume banner (agent case: useAgents().draft
  // from the caller's own query — kept OUT of this component so it stays
  // entity-agnostic; the skill creator will pass its own draft shape).
  draft?: DesignerDraft;
  // AgentNewPage's `?resume=1`: skip the banner and resume immediately.
  autoResume?: boolean;
  // Where the header Cancel button navigates. Required (not defaulted)
  // because the destination is entity-specific — the agent pages pass
  // "/agents"; Task 8's skill pages will pass their own.
  cancelTo: string;
  // Pre-fills (but does not send) the composer's very first message — e.g.
  // AgentNewPage's template picker seeding an editable starting brief.
  // Forwarded verbatim to Composer's own `initialText`, which already only
  // seeds an EMPTY composer, so this is purely additive: callers that don't
  // pass it (every existing one) see no behavior change.
  initialText?: string;
  // When true, `initialText` is SENT automatically as the first message once
  // the surface mounts on a fresh (non-resumed, empty) session — instead of
  // only pre-filling the composer. AgentNewPage sets this so clicking
  // "Continue" with a description actually starts the conversation with it. No
  // send happens when initialText is blank, a draft/resume is in play, or a
  // transcript already exists.
  autoSendInitial?: boolean;
  // Rendered in the transcript before the first message exists, so a fresh
  // session reads as "started, your turn" rather than a blank page with a
  // chatbox. Deliberately a ReactNode rendered OUTSIDE `messages` — it is not
  // a fabricated assistant turn: it never enters the transcript, is never
  // sent to the server, and vanishes the moment a real message lands. Left
  // out by callers that resume an existing transcript (agent EDIT mode).
  intro?: React.ReactNode;
};

type Role = "user" | "assistant";
type HistEntry = { role: Role; content: string };
type FsmState = "describing" | "designing" | "verifying" | "done" | null;

type DesignResponse = {
  response: string;
  done: boolean;
  state?: string;
  building?: boolean;
  generation_failed?: boolean;
  can_keep_as_is?: boolean;
  agent_id?: string;
  skill_id?: string; // forward-compatible: Task 8's completion id field
};

type StateSnapshot = {
  active: boolean;
  generating?: boolean;
  state?: string;
  history?: HistEntry[];
  name?: string;
  agent_id?: string;
  is_edit?: boolean;
  last_progress?: string;
  generation_failed?: boolean;
  can_keep_as_is?: boolean;
  // Present ONLY when active is true — omitted entirely (undefined, not {})
  // on the inactive-session branch. Every read of these two fields must go
  // through the `?? ""` / `?? {}` defaults below; never trust them present
  // just because the response parsed.
  pending_agent_md?: string;
  pending_tools?: Record<string, string>;
};

type ResumeResponse = {
  response: string;
  state?: string;
  history?: HistEntry[];
  agent_id?: string;
  agent_name?: string;
  generation_failed?: boolean;
  can_keep_as_is?: boolean;
};

const STATE_INDEX: Record<string, number> = { describing: 0, designing: 1, verifying: 3 };

const BUILD_PHRASE = "build it";
const SAVE_PHRASE = "save";
const KEEP_AS_IS_PHRASE = "keep it as-is";

function errMessage(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong";
}

export function DesignerSurface({
  endpoints,
  labels,
  startPayload,
  onDone,
  draft,
  autoResume,
  cancelTo,
  initialText,
  autoSendInitial,
  intro,
}: DesignerSurfaceProps) {
  const [messages, setMessages] = useState<HistEntry[]>([]);
  const [fsmState, setFsmState] = useState<FsmState>(null);
  const [generating, setGenerating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [recovering, setRecovering] = useState(!!endpoints.state);
  const [error, setError] = useState<string | null>(null);
  const [generationFailed, setGenerationFailed] = useState(false);
  const [canKeepAsIs, setCanKeepAsIs] = useState(false);
  const [resumeBanner, setResumeBanner] = useState<{ name?: string } | null>(null);
  const [sse, setSse] = useState<{ lines: string[]; status: ActivityStatus } | null>(null);
  const [focusSignal, setFocusSignal] = useState(0);
  const [view, setView] = useState<"transcript" | "spec">("transcript");
  const [pendingAgentMD, setPendingAgentMD] = useState("");
  const [pendingTools, setPendingTools] = useState<Record<string, string>>({});
  const navigate = useNavigate();

  const sseHandleRef = useRef<SSEHandle | null>(null);
  const sseStartedAtRef = useRef(0);
  const doneRef = useRef(false);
  const dismissedRef = useRef(false);
  const autoResumeTriedRef = useRef(false);
  // Guards state updates (and ensureSSE attachment) in async functions'
  // post-await continuations — handleSend's design POST and refetchState's
  // state GET both resolve well after the triggering event, and the user may
  // have navigated away by then. Reset to false at the start of the mount
  // effect (not just set true in its cleanup) so React 18 StrictMode's dev
  // double-invoke (setup→cleanup→setup) doesn't leave it stuck true.
  const unmountedRef = useRef(false);
  // How the currently-attached SSE stream got attached, decided once at the
  // moment of attachment (see ensureSSE's early-return guard — later calls
  // with a different source are no-ops while a stream is live):
  //   - "recovery": mount-recovery found a session already generating, with
  //     NO pending local POST. Nothing else will ever tell this tab the
  //     outcome, so onDone must resync via GET state.
  //   - "live": this tab itself is driving the generation (a "build it"
  //     click, or a "still building" placeholder response to a message this
  //     tab sent). The design POST that triggered/reported it is always the
  //     authoritative source of the eventual outcome — REGARDLESS of which
  //     resolves first, the POST or the SSE close (both orderings are
  //     possible: progressCh can close slightly before or after the POST
  //     that's blocked on the same generation returns). Refetching here
  //     would race that POST in either ordering and can silently replace
  //     locally-optimistic messages (e.g. the "build it" bubble) with a
  //     stale/incomplete server snapshot — so "live" NEVER refetches on
  //     done, it only clears the live-build UI state.
  const attachSourceRef = useRef<"recovery" | "live" | null>(null);
  // Set true when a design POST returns building:true — i.e. the build's real
  // outcome (the verifying transition + the generated spec) will arrive via the
  // SSE stream, NOT this POST's return value (a concurrent/detached build the
  // blocking POST didn't itself carry). When that's the case, the "live" SSE
  // onDone MUST refetch /state to pick up the result — otherwise the surface
  // never leaves "Build", the Spec panel stays empty, and a follow-up message
  // races a session the browser thinks is still mid-build ("name is required").
  // It stays false for a normal same-tab build, whose blocking POST returns the
  // verifying state directly — so that path still never refetches (the
  // zero-extra-/state-calls regression holds).
  const awaitingBuildResultRef = useRef(false);

  function focusComposer() {
    setFocusSignal((n) => n + 1);
  }

  function ensureSSE(source: "recovery" | "live", seedLine?: string) {
    if (sseHandleRef.current) return;
    attachSourceRef.current = source;
    sseStartedAtRef.current = Date.now();
    setSse({ lines: seedLine ? [seedLine] : [], status: "live" });
    const handle = openSSE(endpoints.progress, {
      onMessage: (line) => setSse((s) => (s ? { ...s, lines: [...s.lines, line] } : s)),
      onDone: () => {
        setSse((s) => (s ? { ...s, status: "done" } : s));
        sseHandleRef.current = null;
        setGenerating(false);
        // Refetch to pick up the finished build's result when nothing else
        // will deliver it: a "recovery" attach (a reload found a build already
        // running), OR a "live" build whose POST returned building:true (the
        // outcome comes via this stream, not that POST). A normal same-tab
        // build clears awaitingBuildResultRef, so it still never refetches.
        const src = attachSourceRef.current;
        const needsRefetch =
          src === "recovery" || (src === "live" && awaitingBuildResultRef.current);
        if (needsRefetch && !doneRef.current && endpoints.state) {
          awaitingBuildResultRef.current = false;
          void refetchState();
        }
      },
      onError: () => {
        setSse((s) => (s ? { ...s, status: "error" } : s));
        sseHandleRef.current = null;
        setGenerating(false);
      },
    });
    sseHandleRef.current = handle;
  }

  async function refetchState() {
    if (!endpoints.state) {
      // No state-recovery endpoint (the skill designer) — there's nothing to
      // GET, but a draft can still exist server-side (persisted every design
      // turn) and the caller can still pass `draft`/`autoResume`, so the
      // resume-banner/auto-resume behavior below must still run rather than
      // silently no-op.
      setRecovering(false);
      if (!dismissedRef.current && draft) {
        if (autoResume && !autoResumeTriedRef.current) {
          autoResumeTriedRef.current = true;
          await handleResume();
        } else {
          setResumeBanner({ name: draft.name });
        }
      }
      return;
    }
    try {
      const snap = await api.get<StateSnapshot>(endpoints.state);
      if (doneRef.current || unmountedRef.current) return;
      if (snap.active) {
        setResumeBanner(null);
        setMessages(snap.history ?? []);
        setFsmState((snap.state as FsmState) ?? null);
        setGenerationFailed(!!snap.generation_failed);
        setCanKeepAsIs(!!snap.can_keep_as_is);
        setGenerating(!!snap.generating);
        // pending_agent_md/pending_tools only exist on this branch of the
        // response (see StateSnapshot) — defended anyway, since an
        // undefined slipping through to SpecPanel's .trim()/Object.entries
        // is exactly the nil-slice-to-null-to-.length crash this codebase
        // already shipped once.
        setPendingAgentMD(snap.pending_agent_md ?? "");
        setPendingTools(snap.pending_tools ?? {});
        if (snap.generating) ensureSSE("recovery", snap.last_progress || undefined);
      } else {
        setGenerating(false);
        if (!dismissedRef.current && draft) {
          if (autoResume && !autoResumeTriedRef.current) {
            autoResumeTriedRef.current = true;
            await handleResume();
          } else {
            setResumeBanner({ name: draft.name });
          }
        }
      }
    } catch {
      // Non-critical — recovery failing just leaves a fresh composer.
    } finally {
      if (!unmountedRef.current) setRecovering(false);
    }
  }

  // Mount recovery — step 1 of the behavior spec. Runs once.
  useEffect(() => {
    unmountedRef.current = false;
    void refetchState();
    return () => {
      unmountedRef.current = true;
      sseHandleRef.current?.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Auto-send the initial description as the first message (AgentNewPage's
  // "Continue" flow). Fires exactly once, only for a genuinely fresh session:
  // recovery has settled, there's no resume banner or draft to restore, and the
  // transcript is still empty. When any of those don't hold we leave the text
  // as a composer pre-fill instead (the classic behavior).
  const autoSentRef = useRef(false);
  useEffect(() => {
    if (
      autoSendInitial &&
      initialText &&
      initialText.trim() &&
      !autoSentRef.current &&
      !recovering &&
      !resumeBanner &&
      messages.length === 0 &&
      !busy
    ) {
      autoSentRef.current = true;
      void handleSend(initialText.trim());
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoSendInitial, initialText, recovering, resumeBanner, busy]);

  async function handleResume() {
    setResumeBanner(null);
    setBusy(true);
    setError(null);
    try {
      const res = await api.post<ResumeResponse>(endpoints.resume);
      const hist = res.history ?? [];
      setMessages([...hist, { role: "assistant", content: res.response }]);
      setFsmState((res.state as FsmState) ?? null);
      setGenerationFailed(!!res.generation_failed);
      setCanKeepAsIs(!!res.can_keep_as_is);
    } catch (err) {
      setError(errMessage(err));
      setResumeBanner({ name: draft?.name });
    } finally {
      setBusy(false);
    }
  }

  async function handleDiscard() {
    dismissedRef.current = true;
    setResumeBanner(null);
    try {
      await api.post(endpoints.dismiss);
    } catch {
      // Best-effort — the banner is already gone locally either way.
    }
  }

  async function handleCancel() {
    try {
      await api.post(endpoints.cancel);
    } catch {
      // Ignore — we're navigating away regardless.
    }
    navigate(cancelTo);
  }

  async function handleSend(text: string) {
    setError(null);
    setMessages((m) => [...m, { role: "user", content: text }]);
    setBusy(true);
    // Set when this POST reports the generation is STILL running elsewhere
    // (the "still building" placeholder) — in that case a real build is
    // still in flight and `generating` must stay true until the live SSE
    // (attached below) reports completion; every other outcome is final as
    // of this POST resolving, so `generating` is cleared unconditionally.
    let stillBuilding = false;
    try {
      // Attach the start payload (the agent's name) whenever the transcript is
      // empty and we're not resuming — i.e. there's genuinely no conversation
      // yet, so this is a first message that must carry the name for the
      // backend to open a session. Keyed on an EMPTY transcript (not
      // fsmState===null) so it can never fire mid-conversation and start a
      // fresh session over an in-progress one — a backstop to the primary fix
      // (the surface staying in sync via the SSE-done refetch above).
      const isFirstMessage = messages.length === 0 && !resumeBanner;
      const body: Record<string, unknown> = { message: text };
      if (isFirstMessage && startPayload) Object.assign(body, startPayload);

      const res = await api.post<DesignResponse>(endpoints.design, body);
      if (unmountedRef.current) return;

      if (res.done) {
        doneRef.current = true;
        awaitingBuildResultRef.current = false;
        setMessages((m) => [...m, { role: "assistant", content: res.response }]);
        setFsmState("done");
        onDone(res.agent_id ?? res.skill_id);
        return;
      }

      setMessages((m) => [...m, { role: "assistant", content: res.response }]);
      if (res.state) setFsmState(res.state as FsmState);
      setGenerationFailed(!!res.generation_failed);
      setCanKeepAsIs(!!res.can_keep_as_is);
      // building:true means the real outcome arrives via the SSE stream, so the
      // live onDone must refetch; a terminal state (verifying/designing) was
      // delivered right here, so it must NOT (see awaitingBuildResultRef).
      awaitingBuildResultRef.current = !!res.building;
      if (res.building) {
        stillBuilding = true;
        ensureSSE("live"); // no-op if mount-recovery already attached it
        setGenerating(true); // stepper still shows "Build" while it runs
      }
    } catch (err) {
      if (!unmountedRef.current) setError(errMessage(err));
    } finally {
      if (!unmountedRef.current) {
        setBusy(false);
        if (!stillBuilding) setGenerating(false);
      }
    }
  }

  function handleBuildClick() {
    ensureSSE("live"); // attach BEFORE the POST resolves — generation runs long
    setGenerating(true); // stepper shows "Build" (index 2) while this POST is in flight
    void handleSend(BUILD_PHRASE);
  }

  // Deliberately NOT threaded through refetchState/ensureSSE: the "live"
  // build path's design POST is the sole authoritative source for the
  // transcript (see attachSourceRef's comment above) and a regression test
  // pins ZERO extra /state calls across a same-tab build. This is a fully
  // separate, user-triggered GET — fired only when the person opens the Spec
  // tab — that updates ONLY pendingAgentMD/pendingTools, never touching
  // messages/fsmState/generating, so it can't race or clobber anything that
  // logic protects. Also the one spot pending_agent_md/pending_tools cross
  // the JSON boundary — both are ABSENT (undefined) whenever `active` is
  // false, never `{}` — so this always gates on `snap.active` and defends
  // with `?? ""` / `?? {}` besides.
  async function openSpecView() {
    setView("spec");
    if (!endpoints.state) return;
    try {
      const snap = await api.get<StateSnapshot>(endpoints.state);
      if (unmountedRef.current) return;
      if (snap.active) {
        setPendingAgentMD(snap.pending_agent_md ?? "");
        setPendingTools(snap.pending_tools ?? {});
      }
    } catch {
      // Best-effort — the panel just keeps showing whatever it last had.
    }
  }

  // A build that finishes while the user is already sitting on the Spec tab
  // leaves it showing stale (or empty) content with no signal — nothing else
  // refetches pendingAgentMD/pendingTools once the click that first opened
  // the tab is done. Reacting to `generating` flipping false is the one
  // signal available that's orthogonal to the SSE/transcript machinery
  // above: it never touches attachSourceRef/doneRef/ensureSSE, and it only
  // fires when Spec is the active view, so the zero-extra-/state-calls
  // regression test (which never visits the Spec view) is unaffected.
  useEffect(() => {
    if (!generating && view === "spec" && endpoints.state) void openSpecView();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [generating]);

  const stepIndex = generating ? 2 : fsmState ? (STATE_INDEX[fsmState] ?? 0) : 0;
  const composerBusy = busy || recovering;
  const lastIsAssistant = messages.length > 0 && messages[messages.length - 1]!.role === "assistant";
  const showDesigningActions = fsmState === "designing" && !generating && !busy && lastIsAssistant;
  const showVerifyingActions = fsmState === "verifying" && !generating && !busy && lastIsAssistant;

  if (resumeBanner) {
    return (
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2.5">
          <Stepper steps={labels.steps} activeIndex={0} />
          <Button variant="ghost" size="sm" onClick={handleCancel}>
            Cancel
          </Button>
        </div>
        <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
          <p className="text-sm text-muted-2">
            You have an unfinished draft{resumeBanner.name ? `: ${resumeBanner.name}` : ""}
          </p>
          <div className="flex gap-2">
            <Button onClick={() => void handleResume()} disabled={busy}>
              Resume
            </Button>
            <Button variant="outline" onClick={() => void handleDiscard()}>
              Discard
            </Button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-2.5">
        <div className="flex items-center gap-4">
          <Stepper steps={labels.steps} activeIndex={stepIndex} />
          {/* The Spec tab depends on GET .../design/state, which the skill
              designer (Task 8's SkillNewPage) never wires up — endpoints.state
              is documented as "ABSENT for the skill designer". Without this
              gate a dead, permanently-empty tab would ship there too. */}
          {endpoints.state && (
            <div className="flex items-center gap-1 rounded-md border border-border p-0.5">
              <button
                type="button"
                onClick={() => setView("transcript")}
                className={cn(
                  "rounded px-2.5 py-1 text-xs",
                  view === "transcript" ? "bg-chrome font-medium text-foreground" : "text-muted-2",
                )}
              >
                Transcript
              </button>
              <button
                type="button"
                onClick={() => void openSpecView()}
                className={cn(
                  "rounded px-2.5 py-1 text-xs",
                  view === "spec" ? "bg-chrome font-medium text-foreground" : "text-muted-2",
                )}
              >
                Spec
              </button>
            </div>
          )}
        </div>
        <Button variant="ghost" size="sm" onClick={() => void handleCancel()}>
          Cancel
        </Button>
      </div>

      {view === "spec" ? (
        <div className="flex min-h-0 flex-1 flex-col">
          {generating && (
            <div className="border-b border-border bg-chrome px-4 py-2 text-xs text-muted-2">
              A new build is in progress — this will update automatically when it's done.
            </div>
          )}
          <div className="min-h-0 flex-1">
            <SpecPanel agentMD={pendingAgentMD} tools={pendingTools} />
          </div>
        </div>
      ) : (
        <ChatScroll>
          {/* Only while the transcript is genuinely empty AND nothing is in
              flight — mount recovery may still be about to populate it, and
              flashing a "start here" card in front of a session that's about
              to restore would be worse than the blank page it replaces. */}
          {intro && messages.length === 0 && !busy && !recovering && <>{intro}</>}

          {messages.map((m, i) => (
            <ChatMessageBubble key={i} role={m.role} content={m.content} />
          ))}

          {sse && (
            <div className="max-w-[78%] self-start">
              <ActivityCard
                title={`Building your ${labels.entityName}…`}
                lines={sse.lines}
                status={sse.status}
                startedAt={sseStartedAtRef.current}
                collapsible
              />
            </div>
          )}

          {busy && <TypingIndicator />}

          {showDesigningActions && (
            <div className="flex gap-2 pl-1">
              <Button size="sm" onClick={handleBuildClick}>
                {labels.buildButton}
              </Button>
              <Button size="sm" variant="outline" onClick={focusComposer}>
                Make changes
              </Button>
            </div>
          )}

          {showVerifyingActions && (
            <div className="flex gap-2 pl-1">
              <Button size="sm" onClick={() => void handleSend(SAVE_PHRASE)}>
                {labels.saveButton}
              </Button>
              <Button size="sm" variant="outline" onClick={focusComposer}>
                Request changes
              </Button>
            </div>
          )}
        </ChatScroll>
      )}

      {generationFailed && (
        <div className="flex items-center justify-between gap-2 border-t border-warn/30 bg-warn/10 px-4 py-2 text-xs text-warn">
          <span>The build hit a problem — describe a change or say &quot;try again&quot;.</span>
          {canKeepAsIs && (
            <Button size="xs" variant="outline" onClick={() => void handleSend(KEEP_AS_IS_PHRASE)}>
              Keep it as-is
            </Button>
          )}
        </div>
      )}

      {error && (
        <div className="flex items-center gap-2 border-t border-danger/30 bg-danger/10 px-4 py-1.5 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}

      <Composer
        onSend={(v) => void handleSend(v)}
        busy={composerBusy}
        focusSignal={focusSignal}
        // When auto-sending, the text becomes the first message — don't ALSO
        // seed it into the composer box (it would look like an unsent draft).
        initialText={autoSendInitial ? undefined : initialText}
      />
    </div>
  );
}

export default DesignerSurface;
