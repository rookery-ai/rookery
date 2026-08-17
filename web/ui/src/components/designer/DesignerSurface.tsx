import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import {
  AlertTriangle,
  Check,
  FileText,
  Hammer,
  MessageSquare,
  Pencil,
  Play,
  Save,
  Undo2,
} from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { openSSE, type SSEHandle } from "@/lib/sse";
import { ChatScroll } from "@/components/chat/ChatScroll";
import { ChatMessageBubble, TypingIndicator } from "@/components/chat/Bubbles";
import { Composer } from "@/components/chat/Composer";
import {
  ActivityCard,
  type ActivityStatus,
} from "@/components/chat/ActivityCard";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Stepper } from "./Stepper";
import { SpecPanel } from "./SpecPanel";
import { ReviewCard } from "./ReviewCard";

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
  // POST target for the VERY FIRST message of a genuinely fresh session, instead
  // of endpoints.design — the agent editor's /agents/:id/edit/start, which
  // creates the session server-side. Every later message goes to
  // endpoints.design, because once created an edit session is indistinguishable
  // from a create session. Body is {message} only: startPayload is the OTHER way
  // to open a session and is deliberately not merged here.
  //
  // This prop is why the agent editor no longer needs a second chat surface of
  // its own — the reason the edit chat used to open full-width and then jump to
  // this one's 10% gutter once the first reply landed.
  startEndpoint?: string;
  // Vetoes a recovered session. The design session is a per-workspace SINGLETON,
  // so mount recovery would otherwise adopt whatever is live — showing an
  // unrelated create conversation on an agent's edit page and offering to save
  // the wrong entity, which the edit page's own draft gate used to prevent.
  // Returning false makes the surface treat the session as inactive. Omitted
  // (every caller but AgentEditPage) accepts everything, which is the
  // pre-existing behavior.
  acceptRecoveredSession?: (info: {
    isEdit: boolean;
    agentId: string;
  }) => boolean;
  // Withhold the build button until the server says the plan is settled
  // (`plan_ready`). `fsmState === "designing"` covers the whole conversation —
  // a clarifying question and a finished proposal are the same state — which is
  // why the button used to offer itself under "Which page should I watch?".
  //
  // An explicit opt-in rather than "gate whenever the flag is absent": the
  // SKILL designer shares this component, returns its own response body, and
  // has no plan-ready signal of its own yet. Coercing its missing field to
  // false would hide its build button entirely. Agent pages pass true; the
  // skill page does not, and behaves exactly as before.
  gateBuildOnPlanReady?: boolean;
};

type Role = "user" | "assistant";
type HistEntry = { role: Role; content: string; created_at?: string };

// Turns appended locally — the optimistic user bubble, each assistant reply from
// a design POST, and the resume message — carry no server time: the design
// endpoints return prose, not a transcript row. Stamping in the browser is
// accurate to within the round-trip and keeps every bubble's footer consistent
// with the chats page, where the time comes from the DB. Restored history keeps
// the server's own `created_at`.
function nowStamp(): string {
  return new Date().toISOString();
}

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
  plan_ready?: boolean;
  pending_spec?: string;
};

type StateSnapshot = {
  active: boolean;
  generating?: boolean;
  state?: string;
  history?: HistEntry[];
  name?: string;
  agent_id?: string;
  is_edit?: boolean;
  // The surface that OWNS this session. Absent on the inactive-session branch
  // and on a server predating ownership; both read as "we own it", which keeps
  // the surface usable rather than locking it read-only on a stale response.
  origin?: string;
  last_progress?: string;
  generation_failed?: boolean;
  can_keep_as_is?: boolean;
  // Present ONLY when active is true — omitted entirely (undefined, not {})
  // on the inactive-session branch. Every read of these two fields must go
  // through the `?? ""` / `?? {}` defaults below; never trust them present
  // just because the response parsed.
  pending_agent_md?: string;
  pending_tools?: Record<string, string>;
  plan_ready?: boolean;
  pending_spec?: string;
};

type ResumeResponse = {
  response: string;
  state?: string;
  history?: HistEntry[];
  agent_id?: string;
  agent_name?: string;
  generation_failed?: boolean;
  can_keep_as_is?: boolean;
  plan_ready?: boolean;
  pending_spec?: string;
};

const STATE_INDEX: Record<string, number> = {
  describing: 0,
  designing: 1,
  verifying: 3,
};

// Both surfaces name the act the same way now, so the phrase the button SENDS
// must be one the server's isApproval accepts. That test is exact-match, so a
// label change alone would send a phrase that falls through to an ordinary
// design turn and the button would silently do nothing.
const BUILD_PHRASE = "approve and build it";
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
  startEndpoint,
  acceptRecoveredSession,
  gateBuildOnPlanReady,
}: DesignerSurfaceProps) {
  const [messages, setMessages] = useState<HistEntry[]>([]);
  const [fsmState, setFsmState] = useState<FsmState>(null);
  const [generating, setGenerating] = useState(false);
  const [busy, setBusy] = useState(false);
  const [recovering, setRecovering] = useState(!!endpoints.state);
  const [error, setError] = useState<string | null>(null);
  const [generationFailed, setGenerationFailed] = useState(false);
  const [canKeepAsIs, setCanKeepAsIs] = useState(false);
  // Set by "Request changes", which is the only way to type during the review
  // step. Cleared whenever a new review begins (see the effect below), so each
  // finished build starts locked again rather than inheriting the last one's
  // unlocked composer.
  const [changesRequested, setChangesRequested] = useState(false);
  // Which surface owns the live session. "" means unowned, or ours. Anything
  // else means another surface is driving and this one is a read-only mirror:
  // the session is a per-workspace singleton, so a mirror that thinks it drives
  // can cancel someone else's in-flight build.
  const [ownerSurface, setOwnerSurface] = useState("");
  const readOnly = ownerSurface !== "" && ownerSurface !== "web";
  const [resumeBanner, setResumeBanner] = useState<{ name?: string } | null>(
    null,
  );
  const [sse, setSse] = useState<{
    lines: string[];
    status: ActivityStatus;
  } | null>(null);
  const [focusSignal, setFocusSignal] = useState(0);
  const [view, setView] = useState<"transcript" | "spec">("transcript");
  const [pendingAgentMD, setPendingAgentMD] = useState("");
  const [pendingTools, setPendingTools] = useState<Record<string, string>>({});
  // Derived server-side from the [TECHNICAL SPEC] block the designer appends to
  // its proposal turn (internal/agentdesigner/technicalspec.go). It RETRACTS: a
  // follow-up question carries no block, so the button withdraws.
  const [planReady, setPlanReady] = useState(false);
  const [pendingSpec, setPendingSpec] = useState("");
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
  // True once this surface owns a session: mount recovery ACCEPTED a live one, a
  // resume succeeded, or the user sent a message. handleCancel POSTs
  // endpoints.cancel, and the design session is a per-workspace singleton — so
  // without this, opening an agent's edit page while an unrelated build is
  // running and hitting Cancel would kill that build. An untouched surface has
  // nothing of its own to cancel anyway.
  const sessionTouchedRef = useRef(false);
  // True once a session PROVABLY exists server-side: recovery adopted one, a
  // resume succeeded, or an opening POST returned without throwing. This — not
  // `messages.length === 0` — decides whether the next send is an OPENING one
  // (startEndpoint / startPayload) or an ordinary turn. The transcript is the
  // wrong signal because the optimistic user bubble is appended BEFORE the POST:
  // if that POST fails (the editor's "design session already active; cancel it
  // first" is an expected outcome, not an exotic one), a transcript-keyed test
  // would treat the retry as an ordinary turn, send it to endpoints.design with
  // no session to step, and dead-end on "name is required to start a new
  // session" until the user reloaded.
  const sessionOpenedRef = useRef(false);

  function focusComposer() {
    setFocusSignal((n) => n + 1);
  }

  function ensureSSE(source: "recovery" | "live", seedLine?: string) {
    if (sseHandleRef.current) return;
    attachSourceRef.current = source;
    sseStartedAtRef.current = Date.now();
    setSse({ lines: seedLine ? [seedLine] : [], status: "live" });
    const handle = openSSE(endpoints.progress, {
      onMessage: (line) =>
        setSse((s) => (s ? { ...s, lines: [...s.lines, line] } : s)),
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
          src === "recovery" ||
          (src === "live" && awaitingBuildResultRef.current);
        if (needsRefetch && !doneRef.current && endpoints.state) {
          awaitingBuildResultRef.current = false;
          void refetchState();
        }
      },
      onError: () => {
        setSse((s) => (s ? { ...s, status: "error" } : s));
        sseHandleRef.current = null;
        setGenerating(false);
        // A dropped or never-opened stream used to end here, stranding a build
        // whose result was already committed to History server-side — the dead
        // spinner with no result. This is the second of three independent
        // completion signals; the others are the server's `done` event and the
        // poll below.
        if (!doneRef.current && endpoints.state) {
          awaitingBuildResultRef.current = false;
          void refetchState();
        }
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
      // A vetoed session is treated as if none were live — including NOT
      // attaching its SSE stream below, which would otherwise pipe another
      // entity's build log into this page.
      const accepted =
        snap.active &&
        (!acceptRecoveredSession ||
          acceptRecoveredSession({
            isEdit: !!snap.is_edit,
            agentId: snap.agent_id ?? "",
          }));
      if (accepted) {
        sessionTouchedRef.current = true;
        sessionOpenedRef.current = true;
        setOwnerSurface(snap.origin ?? "");
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
        setPlanReady(!!snap.plan_ready);
        setPendingSpec(snap.pending_spec ?? "");
        if (snap.generating)
          ensureSSE("recovery", snap.last_progress || undefined);
      } else {
        setOwnerSurface("");
        setGenerating(false);
        setPlanReady(false);
        setPendingSpec("");
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

  // Re-lock the composer whenever the transcript advances. Each new turn is a new
  // decision point — a fresh plan or a fresh build — and should be met with the
  // buttons again rather than inheriting the unlock from the previous one.
  //
  // Keyed on the turn COUNT, not on fsmState: the surface now locks during design
  // as well as review, so a state-keyed reset would fire immediately on the very
  // state the user just unlocked and re-lock the box under their cursor.
  useEffect(() => {
    setChangesRequested(false);
  }, [messages.length]);

  // Third completion signal: a slow poll for as long as a build is running.
  // The `done` event and the error refetch both depend on the SSE stream
  // existing at all; a proxy that swallows it entirely leaves neither, and the
  // surface sits on a spinner while the result waits in History. Five seconds
  // is invisible against a multi-minute build and bounds how stuck it can look.
  useEffect(() => {
    if (!generating || !endpoints.state) return;
    const url = endpoints.state;
    const id = setInterval(() => {
      if (doneRef.current || unmountedRef.current) return;
      void (async () => {
        try {
          const snap = await api.get<StateSnapshot>(url);
          if (doneRef.current || unmountedRef.current) return;
          // Adopt the server transcript ONLY once the build is over. Mid-build
          // the server History has nothing new — the outcome is written at the
          // end — while the local transcript holds the approve turn and the
          // "Building…" placeholder, neither of which startGeneration records.
          // Applying a mid-build snapshot would erase both and leave the user
          // watching their own message disappear.
          if (!snap.generating) void refetchState();
        } catch {
          // A failed poll is not worth surfacing — the next tick retries, and
          // the SSE stream and its error refetch are still in play.
        }
      })();
    }, 5000);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [generating, endpoints.state]);

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
      sessionTouchedRef.current = true;
      sessionOpenedRef.current = true;
      const hist = res.history ?? [];
      setMessages([
        ...hist,
        { role: "assistant", content: res.response, created_at: nowStamp() },
      ]);
      setFsmState((res.state as FsmState) ?? null);
      setGenerationFailed(!!res.generation_failed);
      setCanKeepAsIs(!!res.can_keep_as_is);
      setPlanReady(!!res.plan_ready);
      setPendingSpec(res.pending_spec ?? "");
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
    // Nothing was ever started here, so there is nothing of ours to cancel — and
    // the session is a per-workspace singleton, so cancelling blindly could kill
    // someone else's in-flight build. See sessionTouchedRef.
    // A read-only mirror has nothing of its own to cancel, and the session is a
    // per-workspace singleton — POSTing here would kill the OTHER surface's live
    // build. Mount recovery sets sessionTouchedRef when it ADOPTS a session, so
    // that flag alone never guarded this case. (The server refuses it too; this
    // keeps the UI honest rather than relying on the round trip.)
    if (sessionTouchedRef.current && !readOnly) {
      try {
        await api.post(endpoints.cancel);
      } catch {
        // Ignore — we're navigating away regardless.
      }
    }
    navigate(cancelTo);
  }

  async function handleSend(text: string) {
    // Defence in depth: the composer and the action buttons are already hidden
    // in a read-only mirror, and the server refuses a non-owner turn anyway.
    if (readOnly) return;
    setError(null);
    setMessages((m) => [
      ...m,
      { role: "user", content: text, created_at: nowStamp() },
    ]);
    sessionTouchedRef.current = true;
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
      const isFirstMessage = !sessionOpenedRef.current && !resumeBanner;
      const body: Record<string, unknown> = { message: text };
      // startEndpoint and startPayload are alternative ways to OPEN a session
      // (the editor's edit/start creates it from the agent id in the URL; the
      // create pages pass a name). No caller needs both, so the payload is never
      // merged into a start POST.
      const url =
        isFirstMessage && startEndpoint ? startEndpoint : endpoints.design;
      if (isFirstMessage && startPayload && !startEndpoint)
        Object.assign(body, startPayload);

      const res = await api.post<DesignResponse>(url, body);
      sessionOpenedRef.current = true;
      if (unmountedRef.current) return;

      if (res.done) {
        doneRef.current = true;
        awaitingBuildResultRef.current = false;
        setMessages((m) => [
          ...m,
          { role: "assistant", content: res.response, created_at: nowStamp() },
        ]);
        setFsmState("done");
        onDone(res.agent_id ?? res.skill_id);
        return;
      }

      setMessages((m) => [
        ...m,
        { role: "assistant", content: res.response, created_at: nowStamp() },
      ]);
      if (res.state) setFsmState(res.state as FsmState);
      setGenerationFailed(!!res.generation_failed);
      setCanKeepAsIs(!!res.can_keep_as_is);
      // Retracts as well as arms: a follow-up clarifying question carries no
      // [TECHNICAL SPEC] block, so the server reports false and the build
      // button withdraws until the plan settles again.
      setPlanReady(!!res.plan_ready);
      setPendingSpec(res.pending_spec ?? "");
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
      if (!unmountedRef.current) {
        setError(errMessage(err));
        // An opening POST that failed created nothing, so its bubble would sit
        // in front of an empty session and be duplicated by the retry. Drop it
        // and leave the user where they started — what the agent editor's old
        // pre-screen did by simply not having a transcript yet.
        if (!sessionOpenedRef.current) setMessages((m) => m.slice(0, -1));
      }
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
        setPendingSpec(snap.pending_spec ?? "");
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

  const stepIndex = generating
    ? 2
    : fsmState
      ? (STATE_INDEX[fsmState] ?? 0)
      : 0;
  const composerBusy = busy || recovering;
  const lastIsAssistant =
    messages.length > 0 && messages[messages.length - 1]!.role === "assistant";
  // The reported bug: this used to be true for EVERY assistant turn in
  // "designing", so the build button appeared under clarifying questions.
  // planReady is the server's "the plan is settled" signal; the typed word
  // "approve" is unaffected either way, so a model that forgets the marker
  // costs discoverability, never the ability to build.
  // A settled plan almost always ends by inviting approval in so many words
  // ("Type approve and I'll build it"). That sentence is the fallback signal for
  // plan-readiness, and it exists because the server's flag is derived from a
  // [TECHNICAL SPEC] marker a weak model frequently never emits at all — so the
  // gate never opened, no action row was offered, and the user was left with a
  // finished plan and nothing to press. Reported from a real session.
  //
  // Deliberately narrow: it matches an explicit invitation to approve or build,
  // never a clarifying question ("Which page should I watch?"), which is the case
  // gateBuildOnPlanReady was introduced to protect and which must keep working.
  const lastAssistantText =
    [...messages].reverse().find((m) => m.role === "assistant")?.content ?? "";
  const planInvitesApproval =
    /\btype\s+`?approve|\bapprove\b[^.]{0,40}\bbuild\b|\bready to build\b/i.test(lastAssistantText);
  const buildOffered = !gateBuildOnPlanReady || planReady || planInvitesApproval;
  const showDesigningActions =
    fsmState === "designing" && buildOffered && !generating && !busy && lastIsAssistant && !readOnly;
  // Which transcript turn is the dry run: the LAST ASSISTANT turn, not the last
  // turn. Requiring the dry run to be last meant anything landing after it —
  // most easily a turn that FAILED, which leaves the user's own message last and
  // clears busy — hid the finished build completely: no output, no Save, no
  // Request changes. The build was still on the server the whole time, so the
  // only remaining move was to guess a word the server accepts, and guessing
  // wrong drops the FSM back to designing and silently rebuilds the agent.
  //
  // Deliberately NOT gated on !readOnly: a mirror must still SEE the review, it
  // just gets no action row.
  const lastAssistantIndex = (() => {
    if (fsmState !== "verifying" || generating || busy) return -1;
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i]!.role === "assistant") return i;
    }
    return -1;
  })();
  const reviewTurnIndex = lastAssistantIndex;
  const showVerifyingActions = reviewTurnIndex >= 0 && !readOnly;
  // ── The action bar ───────────────────────────────────────────────────────────
  // Accepting a plan or a build goes through a button rather than a guess at which
  // words the server treats as approval, so the composer is closed while the bar is
  // up. "Make changes" / "Request changes" is the key back to typing.
  //
  // THE BAR RENDERS OUTSIDE THE TRANSCRIPT, and that is not cosmetic — it is the
  // fix for a defect that survived two attempts at patching conditions. The rows
  // used to sit inside ChatScroll, which is stick-to-bottom only while the reader
  // is already within 80px of the bottom (STICK_THRESHOLD). Scrolling up during a
  // five-minute build — to re-read the plan, or watch the tool calls — clears that
  // flag, so when the review card finally rendered the view never moved to it. The
  // buttons existed, in the DOM, below the fold, while the composer sat locked by
  // actions the user could not see and had no way to reach. Twice diagnosed as a
  // logic problem; it was a layout problem all along.
  //
  // Outside the scroll container the bar cannot be scrolled away, which also makes
  // it visible on the Spec tab (the transcript's subtree is replaced there, the bar
  // is not) — so the composer can now be closed on that tab too, as asked, without
  // reintroducing a dead end. `deadend.test.tsx` asserts the buttons are NOT
  // descendants of the scrollable element, because that is the property that keeps
  // this fixed; jsdom has no layout, so nothing else can catch a regression here.
  //
  // SHOWING the bar and CLOSING the box are separate decisions, and collapsing them
  // is a mistake worth naming: gating the bar on a settled plan removed the build
  // button from the skill designer entirely, since it passes no
  // gateBuildOnPlanReady and has no plan-ready signal of its own.
  //
  //   - The bar follows the same rule the inline row always did, so every surface
  //     keeps the button it had.
  //   - The box closes only at a SETTLED plan (or a finished build), because a
  //     surface without plan-readiness shows a bar on every assistant turn, and
  //     closing on that would block the ordinary back-and-forth of answering the
  //     designer's own questions.
  const planSettled = planReady || planInvitesApproval;
  const showBuildBar = showDesigningActions;
  const showSaveBar = showVerifyingActions;
  const actionBarUp = showBuildBar || showSaveBar;
  const decisionPending = showSaveBar || (showBuildBar && planSettled);
  // `generating` closes the box too: during a build there is nothing useful to say
  // to the designer, and leaving it open invited exactly the mid-build typing the
  // user reported. That state has no action bar by design — the header's Cancel is
  // the escape, which is why deadend.test.tsx counts Cancel as an action.
  const composerLocked = (decisionPending && !changesRequested) || generating;

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
            You have an unfinished draft
            {resumeBanner.name ? `: ${resumeBanner.name}` : ""}
          </p>
          <div className="flex gap-2">
            <Button onClick={() => void handleResume()} disabled={busy}>
              <Play />
              Resume
            </Button>
            <Button variant="outline" onClick={() => void handleDiscard()}>
              <Undo2 />
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
                  view === "transcript"
                    ? "bg-chrome font-medium text-foreground"
                    : "text-muted-2",
                )}
              >
                Transcript
              </button>
              <button
                type="button"
                onClick={() => void openSpecView()}
                className={cn(
                  "rounded px-2.5 py-1 text-xs",
                  view === "spec"
                    ? "bg-chrome font-medium text-foreground"
                    : "text-muted-2",
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

      {readOnly && (
        <div className="border-b border-border bg-chrome px-4 py-2 text-xs text-muted-2">
          This design session is running in your chat app — follow along here,
          and continue there to make changes.
        </div>
      )}

      {view === "spec" ? (
        <div className="flex min-h-0 flex-1 flex-col">
          {generating && (
            <div className="border-b border-border bg-chrome px-4 py-2 text-xs text-muted-2">
              A new build is in progress — this will update automatically when
              it's done.
            </div>
          )}
          <div className="min-h-0 flex-1">
            <SpecPanel agentMD={pendingAgentMD} tools={pendingTools} spec={pendingSpec} />
          </div>
        </div>
      ) : (
        <ChatScroll className="px-[10%]">
          {/* Only while the transcript is genuinely empty AND nothing is in
              flight — mount recovery may still be about to populate it, and
              flashing a "start here" card in front of a session that's about
              to restore would be worse than the blank page it replaces. */}
          {intro && messages.length === 0 && !busy && !recovering && (
            <>{intro}</>
          )}

          {messages.map((m, i) =>
            i === reviewTurnIndex ? null : (
              <ChatMessageBubble
                key={i}
                role={m.role}
                content={m.content}
                createdAt={m.created_at}
              />
            ),
          )}

          {/* The dry run is promoted out of the bubble stream: it is the one
              turn where action is required, and as an ordinary bubble it was
              indistinguishable from the questions above it and scrolled past. */}
          {reviewTurnIndex >= 0 && (
            <ReviewCard
              title="Dry run — review before saving"
              subtitle={`Your ${labels.entityName} ran and produced this. Save it, or tell me what to change.`}
              content={messages[reviewTurnIndex]!.content}
              createdAt={messages[reviewTurnIndex]!.created_at}
            />
          )}

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

        </ChatScroll>
      )}

      {/* The action bar. OUTSIDE both views and outside ChatScroll, so it can
          neither be scrolled past nor replaced by the Spec tab — see the comment
          on showBuildBar for the defect that made this structural rather than a
          matter of taste. */}
      {actionBarUp && (
        <div
          data-testid="designer-actions"
          className="flex flex-wrap items-center justify-center gap-2 border-t border-border px-4 py-2.5"
        >
          {showSaveBar ? (
            <Button size="sm" onClick={() => void handleSend(SAVE_PHRASE)}>
              <Save />
              {labels.saveButton}
            </Button>
          ) : (
            <Button size="sm" onClick={handleBuildClick}>
              <Hammer />
              {labels.buildButton}
            </Button>
          )}
          {/* The plan and the built agent are both real artifacts worth re-reading,
              and a user who has not noticed the header toggle is precisely the one
              who forgets what they approved. Gated on endpoints.state for the same
              reason the toggle is: the skill designer has none. */}
          {endpoints.state && (
            <Button size="sm" variant="outline" onClick={() => void openSpecView()}>
              <FileText />
              View spec
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              setChangesRequested(true);
              focusComposer();
            }}
          >
            {showSaveBar ? <MessageSquare /> : <Pencil />}
            {showSaveBar ? "Request changes" : "Make changes"}
          </Button>
        </div>
      )}

      {generationFailed && (
        <div className="flex items-center justify-between gap-2 border-t border-warn/30 bg-warn/10 px-4 py-2 text-xs text-warn">
          {/* Copy states what happens next, not what the user must type. The old wording
              ("describe a change or say 'try again'") named the exact action that did NOT
              work: a described change was routed to a chat turn instead of a rebuild. The
              flow now rebuilds on any non-question message, so this just says so. */}
          <span>
            The last build didn&apos;t finish. Tell me what to change and
            I&apos;ll rebuild it.
          </span>
          {canKeepAsIs && !readOnly && (
            <Button
              size="xs"
              variant="outline"
              onClick={() => void handleSend(KEEP_AS_IS_PHRASE)}
            >
              <Check />
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

      {readOnly ? (
        // Not a disabled Composer: a greyed-out input still reads as "type here
        // and it will send eventually". Replacing it states plainly that this
        // surface cannot drive the session and where the driver is.
        <div className="border-t border-border px-4 py-3 text-center text-xs text-muted-2">
          Read-only — this session is being driven from your chat app.
        </div>
      ) : (
        <Composer
          onSend={(v) => void handleSend(v)}
          busy={composerBusy || composerLocked}
          placeholder={
            composerLocked
              ? "Use the buttons above — or Request changes to type here"
              : undefined
          }
          focusSignal={focusSignal}
          // When auto-sending, the text becomes the first message — don't ALSO
          // seed it into the composer box (it would look like an unsent draft).
          initialText={autoSendInitial ? undefined : initialText}
          gutter
        />
      )}
    </div>
  );
}

export default DesignerSurface;
