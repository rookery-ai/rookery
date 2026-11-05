# Chat-App Tool Trace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Edit the chat-app placeholder into a live, plain-language list of tool steps for chat turns and `/run`.

**Architecture:** `coder.FriendlyStep` translates a milestone line beside the code that produces it. `gateway.stepTrace` owns the list, throttling and the flush barrier. The router wires it between the handlers and the existing `send`/`sendProgress`.

**Tech Stack:** Go; `internal/coder`, `internal/gateway`, `internal/agentrunner`, `cmd/rookery`.

**Spec:** `docs/superpowers/specs/2026-11-05-chat-app-tool-trace-design.md`

## Global Constraints

- At most 5 steps visible, one edit per 3 seconds.
- Labels follow the spec's Privacy section; an unknown tool shows its name only.
- No change to `sendProgress`, agent or skill builds, or scheduled runs.

## Review Focus

- A slow flush racing the final answer: nothing may be posted after it (Task 2).
- Markdown punctuation in a query or path: must not break MarkdownV2 or show stray backslashes (Task 1).
- A milestone whose detail is the raw-JSON argument fallback: must not reach chat (Task 1).
- A turn with no tool calls: output byte-identical to today (Task 3).
- The stored assistant message: must not contain the summary (Task 3).

---

### Task 1: Plain-language step labels

**Files:** Create `internal/coder/stepnames.go`, `internal/coder/stepnames_test.go`

**Interfaces:** Produces `func FriendlyStep(line string) (doing, did string)`. A `🔧` line is translated; any other line returns itself for both.

- [ ] Write tests driving real `toolMilestone`/`browserMilestone` output: `TestFriendlyStepNamesKnownTools`, `TestFriendlyStepHidesSensitiveDetail` (bash text, URL path and query, fill value, connector JSON args absent), `TestFriendlyStepPassesOtherLinesThrough`.
- [ ] Run, expect undefined `FriendlyStep`.
- [ ] Implement with a table of tool name → icon, doing, did, detail kind (none, text, host). Details go in a code span with backticks replaced by `'`.
- [ ] Run `GOTOOLCHAIN=auto go test ./internal/coder/ -run FriendlyStep`, expect PASS.
- [ ] Commit `feat(coder): plain-language labels for tool milestones`.

### Task 2: The step trace

**Files:** Create `internal/gateway/steptrace.go`, `internal/gateway/steptrace_test.go`

**Interfaces:** Consumes `coder.FriendlyStep`. Produces `newStepTrace(header string, edit func(string)) *stepTrace` with nil-safe `Step(line string)`, `Summary() string` (stops pending flushes, waits for an in-flight one, returns the `✓ N steps · m:ss` line or `""` when no step is new since the last call) and `Close()`.

- [ ] Write tests: `TestStepTraceRendersRunningList`, `TestStepTraceCollapsesOlderSteps`, `TestStepTraceCoalescesWithinInterval`, `TestStepDoesNotBlockOnSlowEdit`, `TestSummaryWaitsForInFlightFlushAndNothingFollows`, `TestStepAfterSummaryResumes`, `TestNoStepsMeansNoSummary`, `TestTraceSurvivesEveryRenderer` (hostile query through telegram, discord, slack).
- [ ] Run, expect undefined.
- [ ] Implement. `flushMu` is held across `edit`; `mu` guards state. `Summary` sets `paused` under `mu`, stops the timer, then takes and releases `flushMu`. A flush re-checks `paused` after taking `flushMu`. The timer callback recovers panics.
- [ ] Run with `-race`, expect PASS.
- [ ] Commit `feat(gateway): live step trace for chat-app replies`.

### Task 3: Wire chat, /run and the runner

**Files:** Modify `internal/gateway/router.go`, `internal/agentrunner/runner.go`, `cmd/rookery/main.go`, `internal/gateway/chat_session_test.go`, `internal/gateway/run_completion_test.go`. Add `internal/gateway/steptrace_router_test.go`; add `internal/agentrunner/onstep_test.go`.

**Interfaces:** `TextHandler` and `AgentRunHandler` each gain a final `onStep func(string)`. `RunInput.OnStep SendFunc`. `RunByName` gains `onStep SendFunc`.

- [ ] Write tests: `TestChatTurnShowsStepsThenTheAnswer` (nothing posted after the answer, fake `TypingGateway`), `TestChatTraceSummaryIsNotStored`, `TestChatWithNoStepsIsUnchanged`, `TestRunTraceKeepsHeaderAndSummarises`, `TestSilentRunWithStepsSummarisesInPlace`, `TestOnStepReceivesOnlyMilestones`.
- [ ] Run, expect signature failures.
- [ ] In `handleText`, place the summary wrapper between `wrappedSend` and `send`. In `handleRun`, wrap `track`, and take the summary before the stopped and finished lines. `defer trace.Close()` in both.
- [ ] Chain `OnStep` into the coder sink only, and pass it to child runs. Wire `main.go`'s chat coder with `WithProgress(onStep)`.
- [ ] Run `GOTOOLCHAIN=auto go test ./internal/gateway/... ./internal/agentrunner/... ./cmd/...`, expect PASS.
- [ ] Commit `feat(gateway): show tool steps on chat turns and /run`.

### Task 4: Docs, PR, deploy

- [ ] Use the `docs-sync` skill. Add the design to `CLAUDE.md` under the inbound pipeline.
- [ ] Run `make ci-fmt ci-vet ci-docs`, expect clean. Search the diff for local-setup details.
- [ ] Push, open the PR.
- [ ] Deploy the branch from the live checkout, and assert `/healthz` reports the branch commit.
