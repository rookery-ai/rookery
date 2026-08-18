# Build review honesty: a real dry run, a true schedule, and failures that leave a trace

**Status:** design, awaiting implementation
**Scope:** `internal/agentdesigner`, `internal/prompts`, `web/ui`

## What the user saw

Across three builds of the same agent on 2026-08-18:

| build | kind | `msg_bytes` | dry run visible? |
|---|---|---|---|
| `9260ea6f` | create | 632 | no |
| `a84adda1` | edit | 786 | no |
| `f6cccf10` | edit | 322 | **yes** |

The *smallest* message is the one that showed a dry run, which rules out a
rendering fault immediately. Separately, the saved agent was scheduled `0 6 * * 1`
after the user asked for 8am, and an earlier build failed after 488 seconds leaving
no trace at all — the user returned to the Design step with no error and no
explanation.

Four defects, four distinct causes.

## 1. The dry run is not a dry run

`decideBuildOutcome` picks its review sample from three sources in order
(`flow.go:2209-2224`):

```go
testOut := parseTestOutput(resultText)                    // the model's [TEST_OUTPUT] marker
if testOut == "" && scriptVerified { testOut = scriptOutput }   // a real script's stdout
else if testOut == "" { testOut = generationPreviewFallback(resultText); thinProof = true }
```

Only the middle branch is evidence of anything executing, and it requires an
authored script. **All three builds above were TIER 1 — no script at all**
(`script_verified=false`), which is the correct tier for "call an API, compare,
notify". So the second branch is unavailable by construction, and when the model
also omits `[TEST_OUTPUT]`, the "dry run" degrades to a preview of the model's own
prose.

The message then presents that prose as *"Here's what a test run produces"*
(`flow.go:2257`). Nothing ran. For a whole tier of agents — the simplest and most
common — the review step has been showing the model's description of a run rather
than a run.

### The fix: actually run it

After a successful **create** build, execute the built agent once and use its real
output as the review sample.

- **Create builds only.** An edit already has a live agent the user has seen work,
  and the user asked for this on real builds specifically. Edits get honest
  labelling (§2) rather than a second execution.
- **Its own narrow path, not `agentrunner.Run`.** `Run` requires a `db.Agent` row
  (`runner.go:202`), which a draft has none of, and it writes an `agent_runs` row,
  an inbox message and a vault reflection — none of which a build should produce.
  The dry run therefore builds the same runtime prompt
  (`prompts.BuildCoderPrompt`), calls the coder with the draft dir as its working
  directory, and captures the result. No run row, no inbox, no vault note.
- **Build-phase guarded.** `internal/buildphase`'s marker stays set, so
  `connectors.Execute` refuses mutating actions exactly as it does during the build
  itself. The agent may read; it may not post, send or append.
- **Best-effort.** A dry run that errors or times out must not fail the build. The
  build is already on disk and guardrail-checked; the review falls back to §2's
  honest labelling and says the dry run did not complete.

**The cost is real and is stated rather than hidden:** this is one additional agent
run per create build. The user's own run measured **1,555,481 tokens**. Builds will
roughly double in tokens and wall-clock. That is the price of the review step
showing something true, and it is why this is create-only and best-effort.

## 2. Never present prose as a test run

Independent of §1, and applying to edits too: when nothing executed, the message
must not say *"Here's what a test run produces"*.

`thinProof` already tracks exactly this condition and already selects a more honest
sentence — but that sentence still introduces the prose with *"Here's what it
produced"*, which reads as output. When there is no executed sample at all, say so
plainly, and show the model's summary labelled as a summary.

This is the difference between a review step the user can trust and one that
teaches them not to.

## 3. The schedule is written in the wrong timezone

`# Suggested schedule: 0 6 * * 1` for a request of "Monday at 8". The scheduler
evaluates cron against `time.Now()` in **server local time**
(`scheduler.go`, `cron.NewParser(Minute|Hour|Dom|Month|Dow)`), so this fires at
06:00, two hours early.

**The cause is a prompt gap, not model weakness.** The SCHEDULE DECISION block
(`prompts.go:678-695`) tells the model to emit a 5-part cron expression and says
nothing about which timezone it is read in. Meanwhile `UserProfile` hands the model
the user's timezone. Given a timezone and no instruction, converting to UTC is a
reasonable thing for a model to do — and it did, on two separate builds.

Fix: state it. The cron expression is evaluated in the user's local time; write the
local hour; never convert to UTC.

**Known limitation, recorded:** the scheduler has no per-workspace timezone — it
uses the server's. This fix is correct when the server's timezone matches the
owner's, which is the single-owner self-hosted case the product is built around. A
workspace whose owner is in a different timezone from the host would still be
wrong, and fixing that means giving `agent_schedules` a timezone and parsing
against it. Out of scope here; named so it is not mistaken for solved.

## 4. A hard build failure leaves no trace

`runGeneration` has one branch that returns a raw error (`flow.go:1816`):

```go
cleanupOnFail()
return "", false, "", fmt.Errorf("coder: %w", err)
```

Every other failure returns a user-facing message with a `nil` error and calls
`recordGenerationFailure`, which appends to History and saves the draft. This
branch does neither — and additionally deletes the working directory.

Observed: build `7231384b` ran 488 seconds, failed `err_class=other` (a provider
drop — not a timeout, the workspace's coder timeout is 3000s), and wrote nothing.
The draft's `updated_at` was eleven seconds *older* than the build's start. The user
watched eight minutes of "building", then landed back at the Design step with the
plan, the buttons, and no explanation whatsoever.

Fix: record it like every other failure. Append a user-facing note to History and
save the draft, so a reload shows what happened and the user can retry knowingly.
The error class is already computed (`buildErrClass`) — the message should say the
provider dropped the connection rather than exposing the raw error, which
`buildErrClass` exists to avoid logging for exactly the reason it should not be
shown: a provider error can echo the request that produced it.

## 5. Starting a second build while one runs

The design session is a **per-workspace singleton**. Opening a second tab and
pressing *New Agent* adopts the in-flight session rather than starting fresh, which
reads as the new form being broken.

Fix: the server already knows (`Flow.IsGenerating`, surfaced as `generating` on
`GET /api/v1/agents/design/state`). The New Agent entry point should reflect it —
tell the user a build is running and offer to open it, rather than presenting a form
that cannot do what it appears to offer.

Deliberately **not** a server-side refusal: `startGeneration` already guards against
a concurrent build, and a UI that explains beats an endpoint that rejects.

## Not in scope

- **History compaction** — still the prerequisite for the 150-turn ceiling to mean
  what it says. Unchanged from the previous design doc.
- **Per-workspace scheduler timezone** — see §3.
- **A dry run for edits** — §1 is create-only by decision.

## Testing

- **Dry run:** a create build with a TIER 1 agent produces a review sample from an
  actual execution; a dry run that errors still presents the build, labelled as
  unverified; no `agent_runs` row, inbox message or vault note is written by a dry
  run; mutating connector actions are refused while it runs.
- **Honesty:** when nothing executed, the review message does not claim a test run.
- **Schedule:** the prompt states the local-time rule; a test pins the sentence, as
  the codebase already does for other load-bearing prompt clauses.
- **Hard failure:** the unknown-error branch appends to History and saves the draft;
  a reload shows the failure.
- **Concurrent build:** the New Agent surface reflects `generating`.
