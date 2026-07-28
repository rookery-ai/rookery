# Designer chat parity: one surface for agent editing, timestamps on every bubble

**Date:** 2026-07-28
**Status:** approved

## Problem

Clicking **Edit** on an agent lands on `AgentEditPage`, which renders a bespoke
pre-screen — a header, an empty body, and a full-width `<Composer>` — until the
first message round-trips. Only then does it swap in `DesignerSurface`, whose
message column and composer are inset 10% (`ChatScroll className="px-[10%]"` +
`<Composer gutter>`). Three defects follow from that:

1. **Layout jump.** The edit chat opens in the pre-redesign full-width chrome and
   snaps to the 10%-gutter chrome once the agent replies.
2. **Invisible first turn.** The pre-screen has no transcript, so the user's first
   message produces no bubble and no typing indicator. They stare at a disabled
   composer for the length of a full coder round-trip, then the page swaps.
3. **No timestamps.** `DesignerSurface` renders every turn through
   `ChatMessageBubble` without `createdAt`, so the hover footer shows only a copy
   button. `ChatsPage` passes `createdAt={m.created_at}` and shows `Day HH:MM`.
   Two chat surfaces, two different footers.

(1) and (2) share one root cause: the edit flow has a second chat surface that
exists solely to route the first message to a different endpoint. (3) is
independent and affects the agent designer, the agent editor, and the skill
designer equally.

## Design

### Part 1 — Delete the pre-screen; one surface owns the edit chat

`AgentEditPage` mounts `DesignerSurface` unconditionally. The only thing the
pre-screen did that the surface cannot is POST the first message to
`/api/v1/agents/:id/edit/start` instead of `/api/v1/agents/design`. That becomes
an optional prop.

**`startEndpoint?: string`** — when set, the *very first* message of a genuinely
fresh session POSTs `{message}` here instead of `endpoints.design`. Every later
message goes to `endpoints.design`, exactly as today (once created, an edit
session is indistinguishable from a create session server-side). "Fresh" reuses
the existing `isFirstMessage` test: an empty transcript and no resume banner.
`startPayload` is *not* merged into a `startEndpoint` POST — the two are
alternative ways to open a session and no caller needs both.

The intro card carries the context the deleted header had:
`<DesignerIntro title="What would you like to change about <name>?" …>`.

**`acceptRecoveredSession?: (info) => boolean`** — the design session is a
per-workspace singleton, and `DesignerSurface`'s mount recovery adopts whatever
`GET /design/state` reports as active. Today the edit page is shielded from that
by its own `hasMatchingDraft` gate; without a replacement, opening an agent's edit
page while an unrelated *create* session is live would show that conversation and
offer to save the wrong entity. The prop is called with `{ isEdit, agentId }` from
the snapshot; returning false makes the surface treat the session as inactive
(fresh composer, no history, `setGenerating(false)`, **no SSE attach** — a
rejected mid-build session must not stream another entity's build log into this
page). `AgentEditPage` passes `(s) => s.isEdit && s.agentId === id`. Omitted
everywhere else, so `AgentNewPage` and `SkillNewPage` are unchanged.

This preserves today's semantics precisely: with an unrelated session live, the
first message hits `edit/start` → `StartEditDesign` → `"design session already
active; cancel it first"`, surfaced in the surface's own error banner — the same
error the pre-screen showed.

**Cancel must not kill a session this page never owned.** The pre-screen's Cancel
was a plain `<Link>`; `DesignerSurface.handleCancel` POSTs `endpoints.cancel`
first. Guard it with a `sessionTouchedRef`, set when recovery *accepts* an active
session, when a resume succeeds, or when a message is sent. Untouched + empty
transcript → navigate without the POST. On the create/skill pages this only skips
a POST that had nothing to cancel.

**Server: `handleStartEditDesign` must return the session state.** It currently
returns `{response, done}` only. That was harmless while the pre-screen unmounted
and `DesignerSurface` remounted into its own `GET /design/state` recovery. With no
remount, `fsmState` would stay `null`, `showDesigningActions` false, and the
**"🔨 Build it" button would never appear after the first edit turn** — the user
would have to send a throwaway second message to unlock it. Add `snap :=
s.designFlow.Snapshot(u.ID)` and emit `state` / `generation_failed` /
`can_keep_as_is`, matching `handleDesignChat`'s response shape exactly.
`StartEditDesign` sets `State: StateDesigning` before calling the coder, so the
snapshot is already correct at that point.

`DismissDraft` deliberately leaves the in-memory session alone, but the Discard
button is only reachable from the resume banner, which only renders when
`snap.active` is false — so there is no live session to strand. No change needed.

### Part 2 — Timestamps on designer turns

`db.ChatMessage` already carries `CreatedAt`; the designers simply never set it.

- **Stamp on append.** Every `db.ChatMessage{Role:…, Content:…}` literal in
  `internal/agentdesigner/flow.go` (5 sites) and `internal/skilldesigner/flow.go`
  (5 sites) gains `CreatedAt: time.Now().UTC()`. Draft persistence is
  `json.Marshal(sess.History)`, so this round-trips through `agent_drafts` /
  `skill_drafts` with no schema change.
- **Emit from the DTOs.** The three inline `histEntry` structs
  (`handlers_agents.go` resume + state, `handlers_skill_design.go` resume) gain
  `CreatedAt string \`json:"created_at,omitempty"\``, populated with
  `m.CreatedAt.Format(time.RFC3339Nano)` only when `!m.CreatedAt.IsZero()`. It is
  a **string**, not a `time.Time`: `omitempty` is a no-op on a struct, so a
  `time.Time` field would emit `"0001-01-01T00:00:00Z"` for pre-existing drafts
  and render a bubble stamped year 1. RFC3339Nano matches what
  `/api/v1/chats/:id/messages` emits for `created_at`, so both chat surfaces feed
  `formatMessageTime` identical input.
- **Thread through the client.** `HistEntry` gains `created_at?: string`;
  `ChatMessageBubble` receives `createdAt={m.created_at}`. Messages appended
  locally (the optimistic user turn, each assistant reply from a design POST, and
  the trailing resume message that is *not* part of the returned history) are
  stamped `new Date().toISOString()` client-side — the server does not echo them
  back with a time, and a locally-stamped bubble is accurate to within the
  round-trip.

Backward compatibility is by omission: a draft written before this change has a
zero `CreatedAt`, the field is left off the JSON, and the footer degrades to the
copy-button-only form it has today. No migration.

## Out of scope

`SkillNewPage`'s name gate and `AgentNewPage`'s name/template gate are separate
pre-chat *forms*, not chat surfaces — they collect a field the API requires before
a session can exist. They do not exhibit the reported bug and are untouched.

## Testing

- **Unit (Go):** `handleStartEditDesign` returns `state:"designing"`; history DTOs
  emit `created_at` when set and omit it when zero.
- **Unit (Vitest):** the edit page mounts `DesignerSurface` with no pre-screen;
  the first message POSTs `startEndpoint` and later messages POST
  `endpoints.design`; a rejected recovered session yields an empty transcript and
  no SSE attach; bubbles render a `message-time` element.
- **Regression:** `web/api_parity_test.go` (no routes added or removed) and the
  existing `designer.test.tsx` zero-extra-`/state`-calls assertions.
- **Manual:** click Edit on an agent — the chat opens already inset 10% with the
  stepper, the first message appears as a bubble with a typing indicator, and the
  Build button appears on the first reply.
