# Agent designer: the approval moment

The agent designer's review step is four separate defects wearing one coat. The
Build button offers itself while the designer is still asking questions; the
same act is called "approve" in chat and "Build it" in the browser; the plan the
user is approving is never shown as an artifact they can re-read; and the dry
run — the one screen where action is genuinely required — renders as an ordinary
chat bubble that scrolls past.

All four sit on one missing fact: **the server never tells the browser whether
the plan is settled.** Everything below follows from supplying it.

## The state of things

`DesignerSurface.tsx` decides whether to offer the Build button with:

```ts
const showDesigningActions =
  fsmState === "designing" && !generating && !busy && lastIsAssistant && !readOnly;
```

`fsmState === "designing"` covers the entire conversation — the first
clarifying question and the finished proposal are the same state. So the button
appears under "Which website should I watch?" exactly as it appears under a
complete plan, and clicking it launches a build against a half-specified agent.

There is no better signal available today. `stepDesigning` routes an approval
straight to `startGeneration`, and `callCoder` returns `result.Text` verbatim,
so nothing on the wire distinguishes a question from a proposal.

### The technical spec is almost certainly never written

`BuildDesignSystemPrompt` tells the designer:

> After the user approves, append this block (for the code generator only — NOT
> shown to the user): `[TECHNICAL SPEC] … [/TECHNICAL SPEC]`

The designer never gets that turn. `stepDesigning` matches `isApproval` and
calls `startGeneration` without another `callCoder`, so the turn in which the
model was supposed to write the block does not exist. Meanwhile
`BuildImplementationPrompt` reads it:

> The design's `[TECHNICAL SPEC]` proposed a Tier:. Match it, or override toward
> the LOWER …

— against a History that does not contain one. The implementation prompt has
been running tier-blind. `generationPreviewFallback` strips the marker from
*generation* output, which is a different code path and is why this survived.

This is not a separate bug to file. Moving spec emission onto the **proposal**
turn is what supplies the plan-ready signal, so one change fixes both.

## Design

### 1. `[TECHNICAL SPEC]` moves to the proposal turn

Change `<your_job>` in both the create and edit branches of
`BuildDesignSystemPrompt`: the designer appends the `[TECHNICAL SPEC]` block to
the **message that proposes the plan**, in the same turn, immediately after
telling the user to type approve. Not after approval — there is no such turn.

The block is machine-facing and must never render. Handling mirrors `roleNote`
exactly, which already solves this shape:

- **History stores the full text**, block included. `dbMessagesToPrompt` feeds
  History to `BuildImplementationPrompt`, so the generator finally receives the
  tier, schedule, secrets and KB-write declarations it has always asked for.
- **The user-facing text is stripped**, once, in `callCoder`'s return value and
  in `designHistoryDTO`. The same helper serves both, so the transcript and a
  resumed transcript cannot disagree.

Storing the raw text and stripping at the edges — rather than stripping before
storage — is the load-bearing choice. Strip-before-store would re-break the
implementation prompt in the same invisible way it is broken now.

### 2. `plan_ready` is derived, not stored

`Flow.Snapshot` gains `PlanReady bool` and `PendingSpec string`, computed by
scanning the **last assistant turn** in History for a well-formed
`[TECHNICAL SPEC] … [/TECHNICAL SPEC]` block.

Derivation rather than a session field is deliberate: `agent_drafts` has fixed
columns and a stored flag would need a migration, while History is already
persisted on every turn by `saveDraft`. A resumed draft therefore recovers
plan-readiness for free, and the flag can never drift from the artifact it
describes.

Scanning the **last** assistant turn (not any) is what makes the signal
retract. A user who answers a settled proposal with "actually, make it hourly"
gets a fresh assistant turn; if that turn is another question, it carries no
block, `PlanReady` goes false, and the Build button withdraws. A flag that only
ever latched true would be a worse bug than the one being fixed.

`plan_ready` and `pending_spec` ship on **every** path that returns a design
body: `designTurnResponse` (which covers `handleDesignChat`, its `IsGenerating`
branch, and `handleStartEditDesign`), `handleDesignState`, and
`handleResumeDraft`. The SPA coerces a missing field to false, and this codebase
has already shipped one bug of exactly that shape — a mid-build message
silently clearing the failure banner because a hand-rolled body omitted a field.

### 3. The button appears when the plan is ready — the typed word never stops working

```ts
const showDesigningActions =
  fsmState === "designing" && planReady && !generating && !busy && lastIsAssistant && !readOnly;
```

`isApproval` in `stepDesigning` is **unchanged**. It remains the gate; the
button is only the affordance. If a weak model forgets the block, the button
never appears — and typing `approve` still builds. A hard server-side gate would
strand that user with no way forward at all, which is strictly worse than the
defect being fixed.

While `fsmState === "designing"` and `planReady` is false, the composer is the
whole interface, as it should be during a Q&A.

### 4. One name for one act: "Approve & build"

The web button is labelled **Approve & build**; chat's `<your_job>` copy becomes
*"type **approve** when you're happy with this plan and I'll build it"*, so both
surfaces describe the same act in the same words.

The phrase the button *sends* is a separate question from the phrase it
*displays*. `isApproval` is exact-match on a closed list, so sending
`"approve and build it"` today would fall through to an ordinary design turn and
silently do nothing. Both `"approve and build"` and `"approve and build it"` are
added to `isApproval`'s trigger list (and therefore inherit into
`isVerifyApproval`, which shares the same openers). `DesignerSurface`'s
`BUILD_PHRASE` becomes `"approve and build it"` so the transcript bubble reads
as what the user did.

Save stays `"save"` — approving a *built* agent is a different act from
approving a *plan*, and collapsing them would be the same conflation in reverse.

### 5. View spec — one artifact, two moments

`SpecPanel` today renders only `pendingAgentMD`/`pendingTools`, which exist only
**after** a build, and parses `# Suggested schedule:`, `# Skills:` and
`# Connections:` out of AGENT.md. It does not know about `# MCP:`.

The panel gains a second source and a third parser:

- **Before the build** (`planReady && !pendingAgentMD`) it renders the
  `[TECHNICAL SPEC]` block from `pending_spec`, parsed into labelled rows —
  tier, schedule, notifies, KB writes, secrets, external services — plus the
  plain-English plan from the transcript's last assistant turn.
- **After the build** it renders what it renders today, plus MCP servers.
- `parseMCP` joins `parseSkills`/`parseConnections`, matching
  `internal/agentdesigner/parse_mcp.go`'s `# MCP:` / `# MCP servers:` heading.

Parsing the technical-spec block is a small tolerant line reader in
`SpecPanel.tsx` (`parseTechnicalSpec`), following `parseSchedule`'s established
policy: render only what it can prove, fall back to the raw block otherwise. A
plausible-but-wrong summary of what an agent is about to do is worse than the
raw text, because the user cannot tell it is wrong.

A **View spec** button joins the action row at both moments — beside
*Approve & build* when the plan settles, and beside *Save* after the build. It
switches the existing header toggle to the Spec view rather than opening a
second surface, so there is exactly one place the spec lives. The header toggle
stays; the button is the discoverable entry point to it, because a user who has
never noticed the toggle is precisely the user who forgets what they approved.

### 6. The dry run stops being a chat bubble

In `StateVerifying` the last assistant turn is the build's dry run — sample
output, and a decision the user must make. Today it renders as
`ChatMessageBubble` with a `size="sm"` button row at `pl-1`, indistinguishable
from every other turn, and it scrolls.

When `fsmState === "verifying"` and the last turn is that assistant message, it
renders instead inside a **ReviewCard**: full-width within the 10% gutter, a
bordered `bg-chrome` container with an accent left edge, a "Dry run — your
agent ran and produced this" heading, the output, and a centered action row of
`size="default"` buttons — *Save*, *View spec*, *Request changes*.

This is presentation only. No FSM state, no endpoint, and no response field
changes; `DesignerSurface` already knows `fsmState` and which turn is last.
Reaching for a structured response field here would add a wire contract to
solve a styling problem.

The card is not sticky and does not trap focus. It is the last thing in the
transcript and `ChatScroll` already pins to the bottom; a sticky overlay would
fight the scroll container the KB pane's `overscroll-contain` fix exists to
keep well-behaved.

## Files

| File | Change |
|---|---|
| `internal/prompts/prompts.go` | `[TECHNICAL SPEC]` emitted with the proposal (both branches); "type approve … and I'll build it" copy |
| `internal/agentdesigner/flow.go` | `stripTechnicalSpec` + `extractTechnicalSpec`; strip in `callCoder`'s return; `PlanReady`/`PendingSpec` on `DesignSnapshot`; `isApproval` triggers |
| `web/handlers_agents.go` | `plan_ready`/`pending_spec` on `designTurnResponse`, `handleDesignState`, `handleResumeDraft`; strip in `designHistoryDTO` |
| `web/ui/src/components/designer/DesignerSurface.tsx` | `planReady`/`pendingSpec` state; gate `showDesigningActions`; `BUILD_PHRASE`; View-spec buttons; `ReviewCard` |
| `web/ui/src/components/designer/SpecPanel.tsx` | `parseMCP`, `parseTechnicalSpec`, pre-build rendering |
| `web/ui/src/components/designer/ReviewCard.tsx` | new |

## Testing

Go:

- `[TECHNICAL SPEC]` is stripped from the user-facing return of `callCoder` and
  from `designHistoryDTO`, and **retained** in `History` — the retention
  assertion is the one that matters, since strip-before-store is the tempting
  wrong fix.
- `PlanReady` is true after a proposal turn carrying the block, false after a
  following question turn (the retraction case), and false for an unterminated
  block.
- `isApproval("approve and build it")` and `isApproval("approve and build")` are
  true; the existing exact-match cases still pass.
- Every design-body path carries `plan_ready` — asserted on **raw response
  bytes**, per the `flattenRequires`/`null` precedent, since decoding into a
  struct erases a missing field.
- A prompt test pins that the spec block is requested alongside the proposal,
  not after approval.

Vitest:

- Build button absent while `plan_ready` is false, present when true, absent
  again when a later turn retracts it.
- `plan_ready` missing from the body ⇒ button absent (the coerce-to-false path).
- The verifying turn renders as `ReviewCard`, not `ChatMessageBubble`; its Save
  sends `"save"`.
- View spec switches the header toggle to Spec at both moments.
- `parseTechnicalSpec` on a well-formed block, a partial block, and garbage
  (falls back to raw).
- `parseMCP` mirrors `parseSkills`/`parseConnections`.

## Not doing

- **Server-side gating of `isApproval` on `PlanReady`.** A model that forgets
  the marker would lock the user out of building at all.
- **A `plan_ready` column on `agent_drafts`.** Derivation from History needs no
  migration and cannot drift.
- **Showing the technical spec in the transcript.** It is machine-facing; the
  Spec view is where a user who wants it goes.
- **Restructuring the skill designer's surface.** It shares `DesignerSurface`
  but has no `endpoints.state`, so the Spec view is already gated off there;
  `plan_ready` simply arrives absent and the button behaves as today.
