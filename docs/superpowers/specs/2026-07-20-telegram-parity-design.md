# Telegram Parity Design (SP9)

**Status:** proposed
**Supersedes:** nothing. **Depends on:** SP8 (`2026-07-20-everyday-feel`) being merged — no code overlap, but the branch is sequential.

## Why this, now

SP1–SP8 were eight consecutive sub-plans of web-SPA work. Over that arc the web UI gained a
conversational skill creator, an inbox, undoable deletes, a resizable context pane. Telegram gained
nothing. The gap is no longer cosmetic:

> **Platform parity is a standing rule for this project: the web UI and Telegram must offer the same
> experience, and divergence is a bug — not a backlog item.**

CLAUDE.md's "Known gaps" records two live violations of that rule:

| Gap | Recorded status |
|---|---|
| Skill creator via Telegram | `internal/skilldesigner.Flow` supports the Telegram states but the router has no `/skill` route — "web-only for now (platform-parity gap)" |
| `/remind` list/delete via Telegram | "only create is wired" |

SP9 closes both. It is deliberately a **wiring** sub-plan, not a feature sub-plan: nearly all the
machinery exists and is already tested. That is what makes it a good single increment.

## Evidence the framework is ready (and one dead branch)

`internal/skilldesigner.Flow` already exposes the full Telegram-facing surface, matching
`agentdesigner.Flow` method-for-method — which is what `handleAgent` consumes today:

| Need | `agentdesigner.Flow` | `skilldesigner.Flow` |
|---|---|---|
| Start a session | `Start` | `StartDesign` |
| Advance a turn | `Step` | `Step` (same 4-tuple, deliberately) |
| Cancel | `Cancel` | `Cancel` |
| Draft exists? | `HasDraft` | `HasDraft` |
| Offer resume | `OfferDraftResume` | `OfferDraftResume` / `ResumeDraft` / `DismissDraft` |
| Stream build progress | `SetProgressHandler` | `SetProgressHandler` |
| Active session? | `GetSession` | `GetSession` |

`Step`'s doc comment states the 4-tuple return "mirrors agentdesigner.Flow.Step for a consistent
handler shape" — the parity was designed in and then never consumed.

**One genuine defect surfaces here.** `StateDescribing` (`flow.go:40`) is commented
`// Telegram: waiting for a description after /skill create <name>`. `Step` dispatches it at
`flow.go:190`. **No code path ever assigns it** — `newSession` is only ever called with
`StateDesigning` (from `StartDesign`) or constructed directly as `StateAwaitingResume` (from
`OfferDraftResume`). It is unreachable today. SP9 makes it reachable; that is the point.

## The real design problem: two flows, one text stream

This is the only part of SP9 that is design rather than transcription, so it gets decided here.

`handleText` currently routes *all* plain text to the agent design session when one is active:

```go
if r.designFlow != nil && r.designFlow.GetSession(msg.WorkspaceID) != nil { … Step … }
```

Add a second conversational flow and that becomes ambiguous: a user with both an agent session and a
skill session live has no way to say which one their next message is for. Three options:

1. **Route to both** — incoherent, rejected without further thought.
2. **Priority order** (agent first, then skill) — the losing session silently swallows nothing, and
   the user cannot reach it at all until they cancel the winner. Confusing failure mode.
3. **Mutual exclusion** — at most one conversational design session per workspace. Starting one
   while the other is live is refused with a message naming the live session and how to end it.

**Decision: option 3.** Rationale: it makes the ambiguity *impossible* instead of resolving it
arbitrarily, and it matches the mental model the web UI already enforces implicitly (the design FSMs
are per-workspace singletons — `StartDesign` itself already returns
`"a skill design session is already active; cancel it first"` for its own kind). SP9 extends that
existing refusal across the two flows rather than inventing a new concept.

The refusal message must name the *other* kind, e.g.:

> You're in the middle of building the agent **daily-digest**. Finish it, or send `/agent cancel`, then try `/skill create` again.

### Consequence: `pendingCancel` becomes flow-aware

`handleText` resolves a pending cancel save/discard choice via `resolveCancelChoice`, which is
hard-wired to `designFlow`. With `/skill cancel` offering the same save/discard choice, the pending
state must record *which* flow asked. `r.pendingCancel` changes from `map[string]bool` to a map
carrying the kind. This is small but easy to miss, and getting it wrong means `/skill cancel` →
`discard` silently deletes the user's *agent* draft — a data-loss bug of exactly the shape SP7 spent
its review budget on.

## `/remind list` and `/remind delete <n>`

`db.ListReminders(workspaceID)` and `db.DeleteReminder(id)` already exist. The pattern to mirror is
`handleMemory`, which does numbered-list + `delete <n>` correctly (list to get numbers, index into
the same ordering, report what was deleted).

**The one hazard is disambiguation.** `handleRemind` currently treats its entire argument as
"when + what", including an LLM fallback. `/remind list the groceries` is a legitimate reminder
today. So the subcommand test must be **exact**, not prefix-based:

- `/remind list` (argument is exactly `list`, nothing after) → list.
- `/remind delete <n>` (argument is `delete` + one integer, nothing else) → delete.
- Anything else, including `/remind list the groceries` and `/remind delete the old note`, falls
  through to the existing reminder-creation path unchanged.

This is a behaviour-preserving addition: no input that creates a reminder today may stop doing so.
That property gets its own test.

Reminders are displayed in the workspace's timezone via `profile.LoadLocation` — the same call
`handleRemind` already makes. A list rendered in UTC would be a bug on a UTC+2 install.

## Scope

**In:**
- `/skill` command: `list`, `create <name>`, `cancel`; draft resume; build-progress streaming.
- `/remind list`, `/remind delete <n>`.
- Cross-flow mutual exclusion + flow-aware pending-cancel.
- `helpText()` updated to include both.
- Router wiring in `cmd/simple-agents/main.go`.

**Out:**
- `/skill edit` — the skill designer has no edit mode at all yet (unlike `agentdesigner.StartEdit`).
  Adding one is a designer feature, not a parity fix, and belongs in its own sub-plan.
- Skill *import* (ZIP / pasted SKILL.md) via Telegram — file-upload handling is per-adapter work
  across Telegram/Discord/Slack and is its own increment.
- Discord and Slack are unaffected: `Router` is platform-neutral, so both commands appear on every
  adapter for free. No per-adapter code.

## Risks

| Risk | Mitigation |
|---|---|
| `/skill cancel` → `discard` deletes the agent draft | Flow-aware `pendingCancel`; test that asserts the agent draft survives a skill cancel |
| A reminder that used to be created now lists instead | Exact-match subcommand test; a test per fall-through phrase |
| Skill build blocks the Telegram handler | Same shape as `/agent create` — already streams via `SetProgressHandler`; reuse verbatim |
| `StateDescribing` is untested dead code being switched on | It gets its first test in this sub-plan; treat it as new code, not existing code |

## Follow-on candidates (not SP9)

- **Local-coder Model field** in coder settings — CLAUDE.md flags it as blocking OpenCode out of the
  box (no model → 401 against its OpenRouter default). Two clean fixes already analysed there.
  Small, valuable, unrelated to parity.
- `/skill edit`, skill import via chat.
