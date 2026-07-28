# Agent designer: build-retry reliability

**Date:** 2026-07-27
**Status:** approved

## Problem

A user built a web-scraping agent on a weak tool-calling API coder. The build authored a
Python helper script the engine could not confirm ran, so the weak-backend verification gate
held it back. From there three separate defects compounded:

1. **The retry was unsteered.** The user had to type *"Dont build python script you can fetch
   the pages and figure out which are on discount"* by hand. The system knew the script was
   the problem and could not tell itself.
2. **The change request did not rebuild.** That message routed to a design-chat turn, which
   re-entered Q&A and asked for a URL already given three times. The user had to click
   **Build it** to apply the change.
3. **The failure banner never cleared.** It kept telling the user to do the thing that had
   just failed.

The agent built correctly only once the user manually forced the no-script approach and
clicked Build it — the exact two manual interventions this spec removes.

## Root causes

### 1. The steering note is advisory and offers an escape hatch

`reconcileBlockedOutcome` (`internal/agentdesigner/flow.go`) records, for the weak-backend
unverified-script case:

> the helper script it wrote was never confirmed to run. Next attempt: actually run the
> script and show its real output, **or** drop the script and do the task by reasoning directly.

Two problems. It is History prose, not a constraint — nothing in the next build's prompt
forbids a script. And the first of its two options is the one the model already failed at, so
a weak model re-picks it and regenerates the same unverifiable script.

`decideBuildOutcome`'s weak-backend branch sets no `recordFailNote` of its own; the note above
is supplied by `reconcileBlockedOutcome`, which takes priority for this case.

### 2. `stepDesigning` only rebuilds on approval-shaped input

`stepDesigning` re-runs generation for `isApproval` or (post-failure) `isRetryApproval`.
`isRetryApproval` explicitly excludes messages containing change cues — documented as
deliberate, so *"change X then try again"* refines the design rather than blindly re-running.

That reasoning holds during normal design. It is wrong immediately after a failed build: the
build is the thing being iterated on, the user's change request is the steering input, and
routing it to a Q&A-shaped design turn produces exactly the "paste the URL" regression seen
in the transcript.

### 3. `GenerationFailed` is only cleared inside `runGeneration`

Any chat turn leaves it set, so the banner persists indefinitely. `skilldesigner.Flow` mirrors
the same defect.

## Design

### A1 — Force TIER 1 on the retry after an unverified script

Turn the advisory note into an enforced constraint for one attempt.

- **Decision** stays in the pure function. `reconciledOutcome` gains `forceTier1 bool`, set in
  both weak-backend branches of `reconcileBlockedOutcome` (the `!presentable` branch and the
  `presentable && blocked != ""` branch). Keeping it there preserves unit-testability of the
  headline reconciliation.
- **Transport**: the flag lands on `DesignSession.ForceTier1`, is read by `runGeneration` into
  `prompts.ImplementationParams.ForceTier1`, and renders a block from `capabilitySpec()`.
- **The block** hard-forbids code files for that attempt: create ZERO code files; do the whole
  task with the direct tools (`web_fetch`, `web_search`, `search_files`, `glob`, `read_file`,
  `list_dir`) and reasoning; if a step genuinely cannot be done without a script, say so in
  plain language instead of writing one.
- **Lifecycle**: set when the gate fires; cleared at the start of `runGeneration` only after
  being consumed (read into the params first), and on finalize.
- **The note loses its escape hatch**: reworded to the single option that has not already
  failed — drop the script, do the task directly with the available tools.

**Known limitation.** `ForceTier1` is in-memory. A server restart mid-session resumes from the
draft, which carries the History note but not the flag, degrading to the pre-existing soft
steering. Adding a draft column was judged not worth it: the flag only needs to survive from
one build to the immediately following retry, and a restart in that window is rare.

### A2 — A change request after a failed build rebuilds

In `stepDesigning`, when `GenerationFailed` is set and the input matched none of
approval / keep-as-is / retry, route to `runGeneration` (after `appendUserHistory`, so the
instruction reaches the generation prompt) **unless** the message is a question.

`isDesignQuestion` deliberately **fails toward rebuilding** — a needless build costs minutes,
a misroute drops the user back into the trap this spec exists to remove. It returns true only
when all three hold:

- the trimmed input ends with `?`
- it opens with an interrogative lead (`why`, `what`, `what's`, `how`, `how come`, `which`,
  `who`, `when`, `where`, `did`, `does`, `is`, `was`, `can you explain`, `explain`)
- it contains no imperative change cue (`don't`, `do not`, `use `, `instead`, `change`,
  `add `, `remove`, `without`, `rather than`, `skip`, `make it`)

Checked against the reported transcript: *"Dont build python script you can fetch the pages…"*
has no `?` → rebuilds. *"why did that fail?"* → chat.

This reverses the exclusion documented on `isRetryApproval`. That function keeps its current
semantics (it still distinguishes a bare retry from a steered one, and the History capture
differs); the reversal is scoped to the new fall-through branch, which now catches what
`isRetryApproval` declines. The rationale is recorded here because a future reader will
otherwise reasonably restore the old behavior.

### A3 — Banner reflects reality

- `GenerationFailed` is cleared when a post-failure turn routes to the chat path, and on
  finalize. Both surfaces get it — the fix is in `Flow`, and Telegram steps the same FSM.
- `skilldesigner.Flow` gets the same clearing on its chat path.
- Banner copy in `DesignerSurface.tsx` changes from *"The build hit a problem — describe a
  change or say 'try again'."* (the instruction that did not work) to *"The last build didn't
  finish. Tell me what to change and I'll rebuild it."* The **Keep it as-is** button is
  unchanged.

## Testing

- `isDesignQuestion` — table test over the transcript's real messages plus question forms.
- `reconcileBlockedOutcome` — `forceTier1` set on both weak-backend branches, unset elsewhere;
  extends the existing `reconcile_blocked_test.go` table.
- `BuildImplementationPrompt` — contains the no-script clause when `ForceTier1`, absent when not.
- `stepDesigning` — post-failure change request triggers generation; a question does not; the
  failed flag clears on the chat path.

## Out of scope

Reminder/scheduler inbox delivery is a separate concern sharing no files — see
`2026-07-27-inbox-delivery-channel-design.md`.
