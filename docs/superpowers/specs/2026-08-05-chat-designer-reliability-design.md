# Agent designer reliability over chat platforms

**Date:** 2026-08-05
**Status:** Design approved, implementation not started

## Summary

The agent designer works well in the web UI and badly over chat: messages go
missing, a build that outlives its deadline is lost along with the session, and
the user is silently dropped into one-off chat with no indication the designer
has gone.

The root cause is not the designer. It is that the gateway silently discards
any message longer than the platform's limit, and that chat has no channel
through which a build result can arrive after the request that started it has
returned. This spec fixes both, plus three smaller defects the same session
exposed.

## Evidence

A real Discord session, reproduced in full by the reporter:

| Time | Message | Length | Delivered |
|---|---|---|---|
| 10:15 | The design plan | ~1400 chars | yes |
| 10:16 | `Design session error: coder: coder api error: context deadline exceeded` | short | yes |
| 10:21 | "I built this, but on your configured model I couldn't confirm…" | short | yes |
| 10:23 | "🔍 Validating agent safety checks…" | short | yes |
| — | The agent overview and approval prompt | long | **no** |
| 10:28 | `/run hackernews` → `agent "hackernews" not found` | short | yes |

Every short message arrived. The one long message did not. The session then
behaved as though the designer had ended, and the agent existed only as an
unsaved draft directory.

## Problem

### 1. Messages over the platform limit are silently discarded

There is no message-length handling anywhere in `internal/gateway`. No
splitting, truncation, or retry function exists.

`DiscordGateway.Send` (`discord.go:332-339`) passes the rendered text straight
to `ChannelMessageSend`. Discord rejects any message over **2000 characters**
with HTTP 400. Telegram's limit is 4096.

The failure path in `GatewayManager.dispatch` (`gateway.go:472-490`) hides it
completely:

1. `send()` tries `EditMessage` on the placeholder → 400.
2. The error is discarded, `placeholderID` is cleared.
3. `m.Send` is called → 400 again.
4. `fmt.Printf("gateway: send error: %v\n", err)` — a bare stdout write, not
   even `slog`.

The user receives nothing and the operator sees nothing. An agent overview —
the message the user must read in order to approve — is exactly the content
that exceeds 2000 characters.

### 2. Chat has no channel for a result that arrives late

`runGeneration` is invoked synchronously from `Flow.Step` (`flow.go:999`,
`:1034`, `:1052`). On chat the only delivery is the `send()` closure when
`Step` returns, so if the coder's deadline fires first, the build's outcome is
unreachable — there is nowhere else for it to go.

The web does not have this problem. The build is detached from the request
context (`flow.go:1234-1243`), and the result reaches the browser through the
SSE progress stream and `GET /design/state` regardless of what happened to the
POST. Chat has neither.

### 3. No concurrency guard on the chat path

`handleDesignChat` rejects a design turn while a build is running
(`handlers_agents.go:152`). `router.handleText` (`router.go:992-1001`) has no
equivalent, so a message sent mid-build steps the FSM concurrently with the
build that is still writing to the same session.

### 4. Raw Go errors, and a session that appears to vanish

`router.go:1003` sends `"Design session error: " + err.Error()`, producing
`coder: coder api error: context deadline exceeded`. It says nothing about
what to do, and nothing about whether the session survived. In the transcript
the user's following messages were answered as ordinary chat, so from the
user's side the designer had silently disappeared.

### 5. An unsaved build is indistinguishable from a nonexistent agent

A build that never reaches approval leaves `agents/draft_<slug>/` on disk with
no `agents` row. `/run <name>` reports `agent "<name>" not found`, directly
contradicting the designer's own claim to have built it.

## Design

### Component 1 — Platform-aware message splitting

Splitting operates on the **rendered** text, not the neutral CommonMark the
router emits: the limit applies to what is actually transmitted, and each
platform's renderer produces different output.

A shared splitter takes rendered text and a limit and returns ordered chunks:

- Prefers paragraph boundaries, then line boundaries, then a hard split.
- Never splits inside a fenced code block; when a fence must span a boundary
  it is closed at the end of one chunk and reopened at the start of the next.
- Never emits an empty chunk.

**Length is counted in UTF-16 code units, not runes or bytes.** Both Discord
and Telegram count that way, and designer output is dense with emoji (`🔧`,
`⏳`, `✅`, `⚠️`) that occupy two units each. Rune counting would under-count and
still produce a 400 on emoji-heavy text — which is most of the progress and
result messages this spec exists to deliver.

Limits: Discord 2000, Telegram 4096, Slack 40000. Each adapter declares its own
and is covered by a test.

Adapter behaviour:

- `Send` transmits chunks **sequentially**. Neither platform guarantees
  ordering across concurrent calls, so these must not be parallelised.
- `SendMessageGetID` returns the id of the first chunk, which remains the
  placeholder anchor.
- `EditMessage` edits the placeholder with the first chunk and sends the
  remainder as follow-up messages.

`gateway.go:488`'s `fmt.Printf` becomes `slog.Error` carrying platform, user
and message length, so the next delivery failure is diagnosable instead of
invisible.

### Component 2 — Always-detached generation with completion delivery

`runGeneration` returns immediately with the building placeholder and lets the
build proceed on the detached `genCtx` goroutine that already exists.

**This requires no frontend change.** The SPA already implements exactly this
contract: `DesignerSurface.tsx:518-522` sets `awaitingBuildResultRef` on
`building: true`, attaches the SSE stream, and refetches `/state` when the
stream completes. `designer.test.tsx:232` pins the behaviour —
*"a building:true build refetches /state on SSE done and surfaces the verifying
result"*. Making generation always-detached therefore unifies the two surfaces
rather than forking chat away from web.

`Flow` gains a completion hook, registered **once at wiring time in
`main.go`** rather than per message, invoked when a detached build finishes
with the workspace id, the response text, and the done/agent-id pair.

The chat hook delivers through `GatewayManager.SendToUser`, which is durable
and independent of the request goroutine — so the result arrives even though
the handler that started the build returned minutes earlier. This is the
recovery channel chat has never had, and it is the direct counterpart of the
web's SSE-plus-state-endpoint path.

Session state (`StateVerifying`, `PendingAgentMD`, draft persistence) is
already written inside `runGeneration`, so a user who misses the delivered
message can re-issue `/agent create <name>` and be offered the draft to resume.

### Component 3 — Concurrency guard on chat

`router.handleText` checks `IsGenerating` for both the agent and skill flows
before stepping, and replies that the build is still running and its result
will follow. This mirrors `handlers_agents.go:152` and prevents a mid-build
message from stepping the FSM.

### Component 4 — Readable errors that state the session survived

The raw error at `router.go:1003` is replaced by a mapper over the same
sentinels `agentrunner.friendlyRunError` already handles — `coder.ErrUsageLimit`,
`coder.ErrRateLimited`, `coder.ErrAPIAuth`, `coder.ErrMaxTurns`, and
`context.DeadlineExceeded` — each producing text that says what happened and
what to do next.

Every message must also state explicitly whether the design session is still
open. The most damaging part of the reported transcript was not the error
itself but the four messages after it, which were answered as ordinary chat
while the user believed they were still talking to the designer.

### Component 5 — Draft visibility

`/run <name>` distinguishes three cases rather than two: a saved agent, no such
name, and a build that exists as an unsaved draft. The third reports that a
draft exists and points at `/agent create <name>` to resume it.

`/agent list` lists unfinished drafts in a separate section, so a draft is
visible without having to guess its name.

## Testing

- **Splitter:** exact-limit, over-limit and empty input; code fences preserved
  across a boundary; emoji-heavy text measured in UTF-16 code units; chunk
  order stable.
- **Adapters:** a 2500-character reply produces two sequential Discord sends;
  the placeholder edit carries the first chunk and follow-ups carry the rest;
  each adapter's declared limit matches the platform's.
- **Flow:** `runGeneration` returns immediately with the building flag; the
  completion hook fires exactly once with the final response; a `Step` issued
  during generation is rejected rather than running concurrently.
- **Router:** the `IsGenerating` guard replies without stepping; each error
  sentinel maps to its friendly text and leaves the session present.
- **Draft visibility:** `/run` on a draft name reports the draft;
  `/agent list` includes it.

## Risks

**The always-detached change alters the design POST's contract** from
*returns the verify result* to *returns building*. The SPA already implements
and tests that path and the JSON API has no other consumer, but this is the
change most capable of breaking the web UI, so the existing designer tests must
be green before merge.

**Chunk ordering is not guaranteed by the platforms.** Sends must be
sequential; parallelising them would interleave a long agent overview into
nonsense.

**Splitting changes message identity.** A reply that was one editable
placeholder becomes several messages, so the progress-edit UX degrades for long
results: the placeholder holds only the first chunk. This is accepted —
delivering the whole overview in several messages is strictly better than
delivering none.

## Out of scope

The weak-model verification loop — *"on your configured model I couldn't
confirm the helper it wrote actually runs"* — is a coder-model capability
issue, not a chat defect, and reproduces identically in the web UI. The
workspace's configured model deserves separate attention; the behaviour of
`decideBuildOutcome` is unchanged by this spec.
