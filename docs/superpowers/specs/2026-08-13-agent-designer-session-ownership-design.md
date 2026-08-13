# Agent designer: session ownership, durable outcomes, and traceability

**Date:** 2026-08-13
**Status:** design approved, implementation pending

## The incident

A user built an agent from the web UI in the `Ilija Personal` workspace. The build
completed, the dry-run result was delivered **to Telegram**, and the web UI showed
nothing. After the user approved on Telegram, the agent was created. On a second
workspace with no chat platform connected, the same flow left the web UI with a
stopped progress indicator and no result at all.

The cause is not one bug. It is that the build's user-facing outcome has **four**
delivery paths — in-memory `DesignSession.History`, `runGeneration`'s return value,
the `OnBuildComplete` chat hook, and the SSE progress stream — two of which are
lossy, and none of which knows which surface the user is actually on.

### Evidence

Agent `461ce34d-4508-4cde-8c35-7562bf1f6535` ("wefw") was created at `2026-08-13
10:45:35Z`. There is **no `create_agent` row in `audit_logs`** for it. The web
handler always writes one (`web/handlers_agents.go:206`); the chat router does not.
The approval that saved the agent therefore came from Telegram, not the web.

`logs/server.log` for the same window:

```
POST /api/v1/agents/design 200        ← the approve that started the build
GET  /api/v1/agents/design/progress 200   ← SSE attached, streamed, server closed it
GET  /api/v1/agents/design/progress 404   ← reconnect; 404 only after a 30s poll
GET  /api/v1/agents/design/state 200      ← the refetch finally happened
POST /api/v1/agents/design 400            ← "name is required to start a new session"
```

Not one `agentdesigner` log line appears across the entire build.

`agent_drafts` is empty: `saveAndFinish` deletes the session
(`internal/agentdesigner/flow.go:2317`) and the draft. The Telegram approval landed
during the 30-second SSE stall, so by the time the web refetched, the session it
needed was gone — and the user's next web message hit the bare `400`.

## Root causes

1. **Delivery is origin-blind.** `DesignSession` carries no notion of which surface
   started it. `cmd/rookery/main.go:588` registers a single global `OnBuildComplete`
   hook that unconditionally calls `gwManager.SendToUser(...)`. A build started in
   the web pushes its result to Telegram.

2. **The user-facing message is lost on the failure path.** `runGeneration` returns
   `outcome.message` (which reaches chat) but `recordGenerationFailure` appends a
   *different*, generic note to `History` (which is what the web renders). On a
   workspace with no chat platform, `SendToUser` errors and the failure is logged at
   `slog.Debug` — invisible at the default level. The real explanation reaches nobody.

3. **The web has exactly one way to learn a build finished, and it is fragile.**
   `handleDesignProgress` never emits an `event: done` (unlike
   `web/run_tracker.go:187`), so the browser's `EventSource` silently reconnects on a
   clean server close and only discovers the build ended when the next attach 404s —
   after the handler's 30-second poll. `DesignerSurface.tsx:304`'s `onError` stops the
   spinner without refetching, so a stream that never opens is a permanent dead end.

4. **A non-owner surface can destroy a live session.** The design session is a
   per-workspace singleton. Web mount recovery adopts whatever session exists and sets
   `sessionTouchedRef.current = true` (`DesignerSurface.tsx:345`); the Cancel button is
   wired directly to `handleCancel`, which POSTs `/design/cancel` whenever that flag is
   set. Opening the web page during a Telegram build and clicking Cancel kills that
   build. The comment at `DesignerSurface.tsx:449` states this hazard as the reason the
   flag exists — but adoption sets the flag, so it guards only a surface that recovered
   nothing, never one that adopted another surface's session.

5. **No traceability.** A full build produces zero structured log lines. There is no
   correlation id, no record of which surface a result was sent to, and no record of
   whether the send succeeded.

## Design

The spine: **one owner per session, one durable record of what the user should see,
and three independent ways for the web to notice a build finished.**

### 1. Exclusive session ownership

A new `Origin` type (`OriginWeb` | `OriginChat`) is set when a `DesignSession` is
created and **never changes**. Ownership does not follow activity; it is fixed for the
life of the session.

It is threaded as a **parameter** on every public entry point a surface calls —
`Start`, `StartDesign`, `StartEdit`, `StartEditDesign`, `ResumeDraft` — so the compiler
catches a missed call site rather than letting one silently default. `Step` takes the
calling surface too, not to reassign ownership but to **reject** a non-owner turn.

**Chat owns.** The web may open the page and watch: transcript, progress milestones and
the final result all mirror in through `GET /design/state` and the progress stream, both
plain reads that cannot interfere. Composer, Build, Approve, Keep-as-is and Cancel are
disabled, behind a banner naming Telegram as the active surface. The build result is
pushed to Telegram; the web mirror displays it because it reads the same durable
`History`.

**Web owns.** A design message on Telegram is refused with a message pointing at the web
app, following the shape `otherSessionBlock` already uses
(`internal/gateway/router.go:428`). Web turns are **not** mirrored to chat — pushing
every turn to Telegram is precisely the notification noise this change exists to remove.

**Starting a second session already fails** — `Start` and `StartDesign` both refuse
when one exists (`flow.go:428`). Their messages gain the owning surface, so
`/agent create` on Telegram during a web-owned session says the session is active in
the web app rather than the current bare "you already have an active design session".

**The escape hatch is `/agent cancel`, and it always works from chat**, including
against a web-owned session. Without it, a web-owned session whose browser is gone
locks Telegram out until the 7-day draft TTL. The refusal message names the command
explicitly. Cancel is the *only* action a non-owner may take.

**Restart.** The in-memory session dies and only the draft survives. Ownership is
deliberately **not** persisted (that would need a column, and this change adds no
migration), so whoever resumes the draft becomes the owner of the new session. This is
correct rather than a compromise: after a restart there is no in-flight build and no
surface holds a live view.

### 2. One durable record of the user-facing outcome

`History` currently does double duty — the user's transcript *and* the coder's retry
context — and on the failure path those two purposes conflict.

The two are separated without changing coder behaviour:

- The real user-facing message (`outcome.message`) is appended as `role: "assistant"`,
  on **both** the success and the soft-failure branch.
- The coder's steering note (`outcome.recordFailNote`) is appended as `role: "note"`.
- `dbMessagesToPrompt` maps `note` → `assistant`, so **what the coder receives is
  byte-identical to today**.
- `designHistoryDTO` drops `note` turns, so the UI stops rendering the internal note
  and starts rendering the real explanation.

The draft already persists `History`, so a reload, a returning tab or a read-only mirror
always finds the outcome. This is what makes the web independent of the SSE stream
having behaved.

### 3. Three independent completion signals for the web

- `handleDesignProgress` emits `event: done` before closing, matching
  `run_tracker.go:187`. Completion becomes deterministic instead of inferred from a 404
  after a 30-second poll.
- `onError` refetches `/design/state` instead of silently giving up.
- A 5-second `/design/state` poll runs while `generating` is true, so a proxy that
  swallows the stream entirely still surfaces the result.

Any one of the three delivers the outcome.

### 4. Two correctness fixes on the same path

- `handlers_agents.go:162`'s `IsGenerating` branch returns no `state`,
  `generation_failed` or `can_keep_as_is`, so a mid-build turn clears the failure banner
  client-side. It will return `designTurnResponse` like every other path.
- The bare `400 "name is required to start a new session"` becomes an honest message
  stating the session has ended and may have been completed from another surface.

### 5. Traceability

A `build_id` is minted in `startGeneration`, stored on the session, and stamped on
`slog.Info` lines at five points: build start (with origin, agent, edit flag, backend),
coder returned (duration), decision (advance/saveable/script_verified), outcome (state,
message size), and delivery (target surface, whether chat was suppressed, send result).

`grep build_id=<id> logs/server.log` yields the whole lifecycle. The chat-send failure
at `main.go:597` moves from `slog.Debug` to `slog.Warn` — a workspace that cannot
receive its own build result is a real signal, not a debug detail.

## Files touched

| File | Change |
|---|---|
| `internal/agentdesigner/flow.go` | `Origin` type; `Origin` field on `DesignSession`; origin params on the five entry points; caller-surface param + non-owner rejection on `Step`; `note` role on the failure path; `outcome.message` into `History` on both branches; `build_id` + lifecycle logging |
| `internal/agentdesigner/session_origin.go` *(new)* | `Origin` type, `String()`, and the ownership predicate, kept out of the 2900-line `flow.go` |
| `cmd/rookery/main.go` | `OnBuildComplete` gains origin; delivers to chat only when `origin == OriginChat`; `Warn` on send failure |
| `internal/gateway/router.go` | Pass `OriginChat` on chat entry points; refuse non-owner design turns with a pointer to the web app; keep `/agent cancel` unconditional |
| `web/handlers_agents.go` | Pass `OriginWeb`; `event: done`; `designTurnResponse` on the `IsGenerating` branch; `origin` in the `/design/state` payload; honest ended-session error |
| `web/ui/src/components/designer/DesignerSurface.tsx` | Read-only mode when `origin !== "web"`; cancel gated on ownership; `onError` refetch; 5s poll while generating |
`web/ui/src/lib/sse.ts` needs **no** change: `openSSE` registers its `done` listener
unconditionally in `connect()`, so emitting the event server-side is enough for the
design stream to pick it up.

No schema change, no migration.

## Testing

**Go**

- A web-origin build never calls the chat sender; a chat-origin build does.
- A chat send failure on a chat-origin build logs at `Warn`.
- Both the success and the soft-failure branch append the user-facing message to
  `History`.
- A `note` turn reaches `dbMessagesToPrompt` as `assistant` and is absent from
  `designHistoryDTO`.
- `handleDesignProgress` emits `event: done` before closing.
- A non-owner `Step` is refused and does not mutate the session.
- `/agent cancel` cancels a web-owned session from chat.
- `Start`'s refusal names the owning surface.
- The `IsGenerating` branch returns `state`, `generation_failed` and `can_keep_as_is`.

**TSX**

- `onError` triggers a `/design/state` refetch.
- The poll fires while generating and stops when it ends.
- `origin: "chat"` renders the surface read-only and Cancel does not POST.
- The existing `designer.test.tsx` SSE contract still passes.

## Explicitly not built

- **Replaying a session completed from another surface.** A persisted `design_builds`
  row was considered and declined in favour of no schema change. When a chat-owned
  session is saved, the web says the session ended rather than showing the transcript.
- **`/agent takeover`.** Considered as a non-destructive alternative to cancel and
  declined: it needs a rule for a takeover landing mid-build, which cancel does not.
- **Mirroring web turns into chat.** Deliberate — it would reintroduce the
  notification noise this change removes.
- **Ownership persistence across restart.** Needs a column; resume-claims-ownership is
  correct without one.
