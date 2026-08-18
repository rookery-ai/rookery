# Agent Run Reliability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An agent run either completes its work or fails in a way that says so — and never delivers raw model scaffolding to a user.

**Architecture:** Three independent changes to the API tool-calling engine and the runner's delivery path. The engine gains a progress-based turn budget (a turn that made real progress does not spend base budget) and stops relying on a failed model to narrate its own failure. The runner refuses to deliver text that names the run's own tools inside markup — a rule keyed on our tool registry rather than on any provider's dialect.

**Tech Stack:** Go 1.26.6, `internal/coder` (API engine + host tools), `internal/agentrunner` (delivery). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-18-agent-run-reliability-design.md`

## Global Constraints

- Build and test with `GOTOOLCHAIN=auto` — the host Go is 1.26.5 and `go.mod` requires 1.26.6.
- Do NOT run `make ci` locally; the pipeline is the gate. Run targeted package tests only.
- Conventional Commits (`type(scope): summary`). Types: `feat fix refactor docs test chore perf build ci`.
- Never commit to `main`; work on a feature branch and open a PR.
- Turn budget values, fixed by the spec: base **30** runs/chat, base **50** builds, hard ceiling **150**, unproductive streak stop at **6**.
- History compaction and cumulative token ceilings are OUT of scope.

## File Structure

| File | Responsibility |
|---|---|
| `internal/coder/hosttools.go` | Per-run call statistics. Already owns success-vs-error detection. |
| `internal/coder/turnbudget.go` *(new)* | Pure turn-budget state machine. Its own file so the policy is testable with no provider, no vault, no HTTP. |
| `internal/coder/scaffolding.go` *(new)* | Pure `LooksLikeToolScaffolding` predicate, exported for the runner. Its own file because two packages depend on it. |
| `internal/coder/api_engine.go` | Wire the budget into `runToolLoop`; make the grace turn best-effort. |
| `internal/coder/coder.go` | `Result` gains `OfferedTools` so the runner can apply the registry rule. |
| `internal/agentrunner/runner.go` | Delivery refuses scaffolding before the prose fallback sends. |

---

### Task 1: Per-run call statistics

**Files:**
- Modify: `internal/coder/hosttools.go` (struct fields near `consecutiveFails`, and `executeOrNudge`)
- Test: `internal/coder/hosttools_stats_test.go` (create)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `type callStats struct { Productive, Total, Failed int; SucceededTools []string }`, `func (h *hostToolSet) noteCall(name string, failed bool)`, `func (h *hostToolSet) callStats() callStats`.

- [ ] **Step 1: Write the failing test**

```go
package coder

import "testing"

// A turn is "productive" only when a tool actually did something. The engine uses
// this to decide whether a turn spends base budget, so the distinction has to be
// exact: a repeat that is short-circuited never reached a tool, and an error result
// means the tool ran and failed. Neither is progress.
func TestCallStatsCountsOnlyRealProgress(t *testing.T) {
	h := &hostToolSet{}

	h.noteCall("adguard_query_log", false) // succeeded
	h.noteCall("write_file", true)         // failed
	h.noteCall("adguard_query_log", false) // succeeded again

	got := h.callStats()
	if got.Productive != 2 {
		t.Errorf("Productive = %d, want 2", got.Productive)
	}
	if got.Total != 3 {
		t.Errorf("Total = %d, want 3", got.Total)
	}
	if got.Failed != 1 {
		t.Errorf("Failed = %d, want 1", got.Failed)
	}
	// Names feed the human-readable exhaustion summary, so they are deduped: a list
	// repeating one tool nine times tells the reader nothing.
	if len(got.SucceededTools) != 1 || got.SucceededTools[0] != "adguard_query_log" {
		t.Errorf("SucceededTools = %v, want [adguard_query_log]", got.SucceededTools)
	}
}

func TestCallStatsStartsEmpty(t *testing.T) {
	got := (&hostToolSet{}).callStats()
	if got.Productive != 0 || got.Total != 0 || got.Failed != 0 || len(got.SucceededTools) != 0 {
		t.Fatalf("fresh stats = %+v, want zeroes", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestCallStats -count=1`
Expected: FAIL — `h.noteCall undefined`, `h.callStats undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to the `hostToolSet` struct in `internal/coder/hosttools.go`, immediately after the `consecutiveFails int` field:

```go
	// Per-run call statistics. productiveCalls drives the turn budget (turnbudget.go):
	// a turn that produced no successful, non-repeated call did not advance the run
	// and must spend budget. The rest feed the deterministic exhaustion summary,
	// which never asks a failing model to narrate its own failure.
	productiveCalls int
	totalCalls      int
	failedCalls     int
	succeededTools  []string
```

Add near the bottom of `internal/coder/hosttools.go`:

```go
// callStats is a snapshot of what a run's tool calls actually did.
type callStats struct {
	Productive     int
	Total          int
	Failed         int
	SucceededTools []string
}

// noteCall records one EXECUTED tool call. A repeat that executeOrNudge
// short-circuits never reaches here on purpose: it ran no tool, so counting it as
// either progress or a tool failure would misreport the run.
func (h *hostToolSet) noteCall(name string, failed bool) {
	h.totalCalls++
	if failed {
		h.failedCalls++
		return
	}
	h.productiveCalls++
	for _, seen := range h.succeededTools {
		if seen == name {
			return
		}
	}
	h.succeededTools = append(h.succeededTools, name)
}

func (h *hostToolSet) callStats() callStats {
	return callStats{
		Productive:     h.productiveCalls,
		Total:          h.totalCalls,
		Failed:         h.failedCalls,
		SucceededTools: h.succeededTools,
	}
}
```

In `executeOrNudge`, insert `h.noteCall(call.Name, isErr)` immediately after the existing `h.trackScriptProgress(call, result, isErr)` line, so it reads:

```go
	result := h.execute(ctx, call)
	isErr := strings.HasPrefix(result, "error:")
	h.trackScriptProgress(call, result, isErr)
	h.noteCall(call.Name, isErr)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestCallStats -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/coder/hosttools.go internal/coder/hosttools_stats_test.go
git commit -m "feat(coder): record per-run tool call statistics"
```

---

### Task 2: Progress-based turn budget

**Files:**
- Create: `internal/coder/turnbudget.go`
- Create: `internal/coder/turnbudget_test.go`
- Modify: `internal/coder/api_engine.go` (constants at lines 18-28; loop setup at 95-108)

**Interfaces:**
- Consumes: `hostToolSet.callStats()` (Task 1).
- Produces: `func newTurnBudget(isBuild bool) *turnBudget`, `func (b *turnBudget) next(productive bool) (stop bool, reason string)`. `reason` is `""` while running, else `"budget"`, `"unproductive"` or `"hard-ceiling"`. Fields `base` and `turns` are read by tests.

- [ ] **Step 1: Write the failing test**

```go
package coder

import "testing"

// The fixed cap conflated two situations identical by turn count and completely
// different by behaviour: a runaway loop and legitimately long work. Budget is spent
// only by turns that achieved nothing, so real work runs on while a model spinning
// on one failing call still dies quickly.
func TestTurnBudgetProductiveWorkRunsPastTheBase(t *testing.T) {
	b := newTurnBudget(false) // run/chat: base 30
	for i := 0; i < 100; i++ {
		if stop, reason := b.next(true); stop {
			t.Fatalf("productive turn %d stopped early (%s)", i, reason)
		}
	}
}

func TestTurnBudgetStopsOnUnproductiveStreak(t *testing.T) {
	b := newTurnBudget(false)

	// Interleaving keeps the streak broken, so the run continues.
	for i := 0; i < 20; i++ {
		if stop, _ := b.next(i%2 == 0); stop {
			t.Fatalf("interleaved turn %d stopped early", i)
		}
	}

	// Six dead turns in a row is a model going nowhere. Stop sooner than the base
	// budget would — waiting out 30 turns of failure helps nobody and costs money.
	var stop bool
	var reason string
	for i := 0; i < 6; i++ {
		stop, reason = b.next(false)
	}
	if !stop || reason != "unproductive" {
		t.Fatalf("after 6 dead turns: stop=%v reason=%q, want true/unproductive", stop, reason)
	}
}

func TestTurnBudgetSpendsBaseOnUnproductiveTurnsOnly(t *testing.T) {
	b := newTurnBudget(false) // base 30
	spent := 0
	for spent < 29 {
		b.next(false)
		spent++
		if spent%5 == 0 {
			b.next(true) // resets the streak, spends no base budget
		}
	}
	stop, reason := b.next(false) // the 30th unproductive turn
	if !stop || reason != "budget" {
		t.Fatalf("stop=%v reason=%q, want true/budget", stop, reason)
	}
}

func TestTurnBudgetHardCeilingIsNeverExtended(t *testing.T) {
	b := newTurnBudget(true) // build: base 50
	var stop bool
	var reason string
	for i := 0; i < 500 && !stop; i++ {
		if stop, reason = b.iterate(); stop {
			break
		}
		stop, reason = b.next(true) // always productive — only the ceiling can stop this
	}
	if reason != "hard-ceiling" {
		t.Fatalf("reason = %q, want hard-ceiling", reason)
	}
}

// The ceiling must bind even when a turn never reaches next() — the shape of the
// tools-unsupported degrade and the verify-finish nudge, both of which `continue`.
// Without iterate() counting at the top, such a path would spin forever.
func TestTurnBudgetCeilingBindsWithoutOutcomes(t *testing.T) {
	b := newTurnBudget(false)
	var stop bool
	var reason string
	for i := 0; i < 1000 && !stop; i++ {
		stop, reason = b.iterate() // never calls next(), as a `continue` path would not
	}
	if !stop || reason != "hard-ceiling" {
		t.Fatalf("stop=%v reason=%q, want true/hard-ceiling — an outcome-free loop must still terminate", stop, reason)
	}
}

func TestTurnBudgetBuildBaseExceedsRunBase(t *testing.T) {
	// A build carries the same work PLUS verify nudges and the grace turn, so its
	// base must never be the smaller of the two.
	if newTurnBudget(true).base <= newTurnBudget(false).base {
		t.Fatal("build base budget must exceed the run base budget")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestTurnBudget -count=1`
Expected: FAIL — `newTurnBudget undefined`, `maxHardTurns undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/coder/turnbudget.go`:

```go
package coder

// turnBudget decides when a tool-calling loop has gone on long enough.
//
// A fixed turn cap cannot tell a runaway loop from legitimately long work — they
// look identical by turn count. It also caused a real incident: an agent that
// genuinely needed more than 25 turns hit the cap, the grace turn stripped its
// tools, and the model expressed a still-pending tool call as raw text which was
// then delivered to the user. Budgeting on PROGRESS separates the two cases.
//
// Three limits, in the order they usually bite:
//
//   - unproductive streak — a model going nowhere stops in 6 turns, far sooner than
//     any base budget would allow.
//   - base budget — spent only by turns that achieved nothing, so honest work does
//     not consume it.
//   - hard ceiling — pure runaway protection, never extended by anything.
type turnBudget struct {
	base        int
	spent       int
	streak      int
	turns       int
	hardCeiling int
}

func newTurnBudget(isBuild bool) *turnBudget {
	base := maxAPITurns
	if isBuild {
		base = maxBuildAPITurns
	}
	return &turnBudget{base: base, hardCeiling: maxHardTurns}
}

// iterate is called at the TOP of every loop iteration and counts it, whatever the
// iteration goes on to do.
//
// It is separate from next() so the loop is bounded BY CONSTRUCTION. Two paths in
// runToolLoop `continue` without ever executing a tool call — the tools-unsupported
// degrade and the verify-finish nudge — and so never reach next(). Both are bounded
// by their own counters today, but relying on that would make an unbounded `for`
// safe only by coincidence, and a future third path would spin forever.
func (b *turnBudget) iterate() (stop bool, reason string) {
	b.turns++
	if b.turns > b.hardCeiling {
		return true, "hard-ceiling"
	}
	return false, ""
}

// next records the OUTCOME of a turn that actually ran tool calls, and reports
// whether the loop must stop. productive means the turn executed at least one tool
// call that succeeded and was not a short-circuited repeat.
func (b *turnBudget) next(productive bool) (stop bool, reason string) {
	if productive {
		b.streak = 0
	} else {
		b.streak++
		b.spent++
	}

	switch {
	case b.streak >= maxUnproductiveStreak:
		return true, "unproductive"
	case b.spent >= b.base:
		return true, "budget"
	}
	return false, ""
}
```

Replace the constants at `internal/coder/api_engine.go:18-28` with:

```go
// maxAPITurns is the BASE turn budget for agent runs and one-off chat. It is spent
// only by turns that achieved nothing (see turnbudget.go) — a run making real
// progress is not stopped by it. Surfaced as ErrMaxTurns.
const maxAPITurns = 30

// maxBuildAPITurns is the base budget during an agent BUILD, which shares its turns
// between the actual work, up to maxVerifyNudges finish-verification nudges, and the
// grace turn. It must stay larger than maxAPITurns for that reason.
const maxBuildAPITurns = 50

// maxHardTurns is runaway protection and is NEVER extended, however productive the
// loop claims to be.
//
// It is not reachable in practice on most models today: nothing trims req.Messages,
// so a 128k-context model exceeds its window somewhere around turn 45-50 and the
// provider errors first. History compaction is the prerequisite for this ceiling to
// mean what it says; see the design doc.
const maxHardTurns = 150

// maxUnproductiveStreak stops a model that is going nowhere long before the base
// budget would. Six consecutive turns with no successful tool call is not a slow
// run, it is a stuck one.
const maxUnproductiveStreak = 6
```

In `runToolLoop`, replace the `turnBudget := maxAPITurns` / `if tools.verifyBuild {...}` block and the `for turn := 0; turn < turnBudget; turn++ {` header with:

```go
	budget := newTurnBudget(tools.verifyBuild)
	offered := toolNames(req.Tools)
	var stopReason string
	for {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w after %s", ErrTimeout, c.timeout)
		}
		// Counted at the top so paths that `continue` without running a tool still
		// consume the hard ceiling — the loop is bounded by construction, not by the
		// two `continue` sites happening to have counters of their own.
		if stop, reason := budget.iterate(); stop {
			stopReason = reason
			slog.Info("coder: tool loop stopped", "reason", reason, "turns", budget.turns)
			break
		}
```

Immediately BEFORE the `for _, tc := range resp.ToolCalls {` line, capture the baseline:

```go
		productiveBefore := tools.callStats().Productive
```

and immediately AFTER that inner loop's closing brace, add the budget check as the last statement in the outer loop body:

```go
		// A turn counts as progress only if some tool actually succeeded on it.
		if stop, reason := budget.next(tools.callStats().Productive > productiveBefore); stop {
			stopReason = reason
			slog.Info("coder: tool loop stopped", "reason", reason, "turns", budget.turns)
			break
		}
	}
```

Add `"log/slog"` to the imports if absent. `offered` and `stopReason` are consumed in Task 4.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestTurnBudget -count=1`
Expected: PASS

Then the whole package, to catch anything depending on the old constants:
Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -count=1`
Expected: PASS (`toolNames` and `stopReason` are unused until Task 4 — if the compiler objects, complete Task 4 before re-running.)

- [ ] **Step 5: Commit**

```bash
git add internal/coder/turnbudget.go internal/coder/turnbudget_test.go internal/coder/api_engine.go
git commit -m "feat(coder): budget tool-loop turns on progress rather than a fixed cap"
```

---

### Task 3: Scaffolding predicate

**Files:**
- Create: `internal/coder/scaffolding.go`
- Create: `internal/coder/scaffolding_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func LooksLikeToolScaffolding(text string, offeredTools []string) bool` — exported, used by Tasks 4 and 5.

- [ ] **Step 1: Write the failing test**

```go
package coder

import "testing"

// The rule is keyed on OUR tool registry, not on any provider's markup dialect. We
// know exactly which tools were offered on a given run, so "this text names one of
// our tools where prose would not put it" is decisive — and it does not go stale
// when the next provider invents new syntax. Matching dialects is an unwinnable
// blacklist.
func TestLooksLikeToolScaffolding(t *testing.T) {
	tools := []string{"adguard_query_log", "write_file", "web_search"}

	scaffolding := []string{
		// The exact payload from the incident: delivered to a user's phone as if it
		// were a notification.
		"<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"adguard_query_log\">\n" +
			"<｜DSML｜parameter name=\"limit\" string=\"false\">10</｜DSML｜parameter>\n" +
			"</｜DSML｜invoke>\n</｜DSML｜tool_calls>",
		// Other dialects, so the rule is not fitted to one vendor.
		"<tool_call>{\"name\": \"web_search\", \"arguments\": {\"query\": \"x\"}}</tool_call>",
		"<function=write_file>{\"path\": \"notes/a.md\"}</function>",
	}
	for _, s := range scaffolding {
		if !LooksLikeToolScaffolding(s, tools) {
			t.Errorf("LooksLikeToolScaffolding(%.40q) = false, want true — this would reach the user", s)
		}
	}

	prose := []string{
		// The case the prose fallback exists for: a real message whose [CHAT] marker
		// the model forgot. Suppressing this would be worse than the bug being fixed.
		"3 new blocked domains overnight: doubleclick.net, app-measurement.com.",
		"I checked AdGuard and nothing new was blocked since yesterday.",
		// Naming a tool in a sentence is prose, not a call.
		"I used adguard_query_log to fetch the overnight entries and found nothing new.",
		// Angle brackets in ordinary writing must not trip the markup heuristic.
		"Latency was <100ms for every request, so nothing looked wrong.",
		"",
	}
	for _, s := range prose {
		if LooksLikeToolScaffolding(s, tools) {
			t.Errorf("LooksLikeToolScaffolding(%.40q) = true, want false — a real message was suppressed", s)
		}
	}
}

// With no registry (a CLI coder reports none) the name rule is inert and only the
// markup-density backstop applies.
func TestLooksLikeToolScaffoldingWithoutRegistry(t *testing.T) {
	if !LooksLikeToolScaffolding("<tool_call><invoke name=\"x\"><parameter/></invoke></tool_call>", nil) {
		t.Error("dense markup with no registry should still be refused")
	}
	if LooksLikeToolScaffolding("Nothing new to report today.", nil) {
		t.Error("plain prose with no registry must be delivered")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestLooksLikeToolScaffolding -count=1`
Expected: FAIL — `LooksLikeToolScaffolding undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/coder/scaffolding.go`:

```go
package coder

import (
	"regexp"
	"strings"
)

// markupToken matches a tag-like construct: an ASCII <…> tag, or one using the
// full-width bars some providers wrap their markup in (｜DSML｜ and relatives).
// Deliberately loose — it is only ever the SECOND half of the decision.
var markupToken = regexp.MustCompile(`<[^<>\n]{1,120}>|｜[^｜\n]{1,60}｜`)

// LooksLikeToolScaffolding reports whether text is a model's tool-call machinery
// rather than a message meant for a person.
//
// It exists because a model with no structured tool channel will sometimes express
// the intent as text instead — and our prose fallback, meant to rescue a forgotten
// [CHAT] marker, faithfully delivered that markup to a user's phone. The trigger is
// our own grace turn: it strips the tools field while the model still has work
// queued, removing the well-formed way to express an intent without removing the
// intent.
//
// The test is keyed on OUR registry rather than on provider dialects:
//
//  1. The text names one of the tools we offered on this run, inside a markup-like
//     construct. Decisive, and it cannot go stale — we do not have to recognise
//     DeepSeek's syntax, only our own tool names appearing where prose would not put
//     them.
//  2. The text is mostly markup rather than sentences. A backstop for scaffolding
//     that happens to name no tool.
//
// offeredTools may be empty (a CLI coder reports none), in which case only rule 2
// applies. When in doubt this returns false: withholding a real message is worse
// than the warning a suppressed one produces.
func LooksLikeToolScaffolding(text string, offeredTools []string) bool {
	s := strings.TrimSpace(text)
	if s == "" {
		return false
	}
	tokens := markupToken.FindAllString(s, -1)
	if len(tokens) == 0 {
		return false // no markup at all: it is prose, whatever it mentions
	}

	// Rule 1 — one of our own tools named inside markup.
	inMarkup := strings.Join(tokens, "\n")
	for _, name := range offeredTools {
		if name != "" && strings.Contains(inMarkup, name) {
			return true
		}
	}

	// Rule 2 — predominantly markup. Measured as a share of the text, so a sentence
	// containing one bracketed aside stays deliverable.
	var markupLen int
	for _, t := range tokens {
		markupLen += len(t)
	}
	return markupLen*2 >= len(s)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestLooksLikeToolScaffolding -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/coder/scaffolding.go internal/coder/scaffolding_test.go
git commit -m "feat(coder): add a registry-keyed tool-scaffolding predicate"
```

---

### Task 4: Deterministic exhaustion, best-effort grace turn

**Files:**
- Modify: `internal/coder/api_engine.go` (`graceTurnOnBudgetExhausted` and its call site)
- Create: `internal/coder/exhaustion_test.go`

**Interfaces:**
- Consumes: `callStats` (Task 1), `offered`/`stopReason` (Task 2), `LooksLikeToolScaffolding` (Task 3).
- Produces: `func exhaustionSummary(stats callStats, reason string) string`, `func toolNames(tools []llm.Tool) []string`. `graceTurnOnBudgetExhausted` gains `stats callStats, offeredTools []string, reason string`.

- [ ] **Step 1: Write the failing test**

```go
package coder

import (
	"strings"
	"testing"
)

// At exhaustion we already know every fact worth reporting, so we do not need — and
// after the incident that produced this code, cannot trust — the model to narrate
// its own failure.
func TestExhaustionSummaryStatesWhatHappened(t *testing.T) {
	got := exhaustionSummary(callStats{
		Productive:     12,
		Total:          17,
		Failed:         5,
		SucceededTools: []string{"adguard_query_log", "write_file"},
	}, "budget")

	for _, want := range []string{"12", "adguard_query_log", "write_file"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "[BLOCKED]") || strings.Contains(got, "[CHAT]") {
		t.Errorf("summary leaked a protocol marker to the user: %q", got)
	}
}

// A run that achieved nothing must not imply that it did.
func TestExhaustionSummaryWithNoProgress(t *testing.T) {
	got := exhaustionSummary(callStats{Total: 3, Failed: 3}, "unproductive")
	if strings.Contains(got, "Completed:") {
		t.Errorf("summary claims completed work where there was none: %q", got)
	}
	if got == "" {
		t.Fatal("summary must never be empty — it is the user's only account of the run")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestExhaustionSummary -count=1`
Expected: FAIL — `exhaustionSummary undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/coder/api_engine.go`, beside the grace-turn constants:

```go
// exhaustionSummary composes the user-facing account of a run that ran out of steps,
// from facts the engine already holds.
//
// The alternative — asking the model to explain its own failure and trusting the
// reply — is what delivered raw tool-call markup to a user. A model that has just
// failed to finish is the last thing that should narrate the outcome, so the grace
// turn became optional garnish and this became the source of truth.
func exhaustionSummary(stats callStats, reason string) string {
	var b strings.Builder
	switch reason {
	case "unproductive":
		b.WriteString("⚠️ Stopped early: several tool calls in a row achieved nothing.")
	case "hard-ceiling":
		b.WriteString("⚠️ Stopped: the run hit its hard limit on tool calls.")
	default:
		b.WriteString("⚠️ Ran out of steps before finishing.")
	}
	if stats.Productive > 0 {
		b.WriteString(" Completed: ")
		b.WriteString(strconv.Itoa(stats.Productive))
		b.WriteString(" successful tool calls (")
		b.WriteString(strings.Join(stats.SucceededTools, ", "))
		b.WriteString(").")
	}
	if stats.Failed > 0 {
		b.WriteString(" ")
		b.WriteString(strconv.Itoa(stats.Failed))
		b.WriteString(" failed.")
	}
	b.WriteString(" See the run log for detail.")
	return b.String()
}

// toolNames lists the tools offered on this run, for the scaffolding predicate. It
// must be evaluated BEFORE graceTurnOnBudgetExhausted, which nils req.Tools.
func toolNames(tools []llm.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}
```

Add `"strconv"` to the imports.

Replace `graceTurnOnBudgetExhausted` with:

```go
func (c *Coder) graceTurnOnBudgetExhausted(ctx context.Context, prov llm.Provider, req llm.Request, total llm.Usage, start time.Time, isBuild bool, stats callStats, offeredTools []string, reason string) (*Result, error) {
	fallback := exhaustionSummary(stats, reason)
	req.Tools = nil
	nudge := graceTurnWrapUpNudge
	if isBuild {
		nudge = graceTurnBudgetNudge
	}
	req.Messages = append(req.Messages, llm.Message{Role: "user", Content: nudge})
	resp, err := prov.Complete(ctx, req)
	if err != nil {
		return &Result{Text: fallback, Duration: time.Since(start), Usage: total}, nil
	}
	total = addUsage(total, resp.Usage)
	text := strings.TrimSpace(resp.Content)
	// Best-effort garnish. We removed the model's structured tool channel a moment
	// ago while it still had work queued, so a reply expressing that work as raw
	// markup is close to expected — and must never be forwarded.
	if text == "" || LooksLikeToolScaffolding(text, offeredTools) {
		text = fallback
	}
	return &Result{Text: text, Duration: time.Since(start), Usage: total}, nil
}
```

Update the call site after the loop:

```go
	res, err := c.graceTurnOnBudgetExhausted(ctx, prov, req, total, start, tools.verifyBuild, tools.callStats(), offered, stopReason)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/coder/api_engine.go internal/coder/exhaustion_test.go
git commit -m "feat(coder): compose exhaustion messages from run facts, not from the model"
```

---

### Task 5: Carry offered tools on Result; refuse scaffolding at delivery

**Files:**
- Modify: `internal/coder/coder.go` (`Result` struct)
- Modify: `internal/coder/api_engine.go` (populate `OfferedTools` on both return paths)
- Modify: `internal/agentrunner/runner.go` (prose-fallback branch)
- Create: `internal/agentrunner/scaffolding_delivery_test.go`

**Interfaces:**
- Consumes: `LooksLikeToolScaffolding` (Task 3), `offered` (Task 2).
- Produces: `Result.OfferedTools []string`; `func deliverableProse(raw string, offeredTools []string) string`.

- [ ] **Step 1: Write the failing test**

```go
package agentrunner

import (
	"strings"
	"testing"
)

// The prose fallback exists to rescue a forgotten [CHAT] marker. It must not also
// forward the model's tool-call machinery — which is exactly what reached a user's
// phone in the incident this guards.
func TestDeliverableProseRefusesScaffolding(t *testing.T) {
	tools := []string{"adguard_query_log", "write_file"}

	scaffolding := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"adguard_query_log\">\n" +
		"<｜DSML｜parameter name=\"limit\" string=\"false\">10</｜DSML｜parameter>\n" +
		"</｜DSML｜invoke>\n</｜DSML｜tool_calls>"
	if got := deliverableProse(scaffolding, tools); got != "" {
		t.Errorf("deliverableProse returned %q, want \"\" — this reached a real user's phone", got)
	}

	real := "3 new blocked domains overnight: doubleclick.net and app-measurement.com."
	if got := deliverableProse(real, tools); got != real {
		t.Errorf("deliverableProse suppressed a real message: got %q", got)
	}
}

// Protocol markers are still stripped — the new check is a floor under that
// behaviour, not a replacement for it.
func TestDeliverableProseStillStripsMarkers(t *testing.T) {
	got := deliverableProse("[STATE]{\"a\":1}[/STATE]\nAll quiet overnight.", nil)
	if !strings.Contains(got, "All quiet overnight.") {
		t.Fatalf("prose lost: %q", got)
	}
	if strings.Contains(got, "[STATE]") {
		t.Fatalf("marker leaked: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/agentrunner/ -run TestDeliverableProse -count=1`
Expected: FAIL — `deliverableProse undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to the `Result` struct in `internal/coder/coder.go`, beside the other engine-reported fields:

```go
	// OfferedTools names the tools this run offered the model. Empty for a CLI coder.
	// The runner uses it to recognise the model's own tool-call machinery leaking into
	// a message, without needing to know any provider's markup dialect.
	OfferedTools []string
```

Populate it on BOTH return paths in `runToolLoop`. In the final-answer branch, beside the other `res.*` assignments:

```go
			res.OfferedTools = offered
```

and in the post-loop block:

```go
		res.OfferedTools = offered
```

Add to `internal/agentrunner/runner.go`, next to `extractProseMessage`:

```go
// deliverableProse is the floor under the prose fallback: the message to deliver, or
// "" when there is nothing safe to send.
//
// The fallback's job is to rescue a run whose model forgot the [CHAT] marker. It is
// NOT to forward whatever the model happened to emit — a distinction that cost a real
// user, who received DeepSeek's raw tool-call markup as a notification. Keyed on the
// tools the run itself offered, so it needs no knowledge of any provider's dialect.
func deliverableProse(raw string, offeredTools []string) string {
	prose := extractProseMessage(raw)
	if prose == "" {
		return ""
	}
	if coder.LooksLikeToolScaffolding(prose, offeredTools) {
		return ""
	}
	return prose
}
```

Add an `offeredTools []string` field to `coderRunContext` (beside `lastRaw`), assign it where the `*coder.Result` fields are copied in `runCoderTurns`, and change the prose-fallback branch in `runCoderAgent`:

```go
	if finalOutput == "" && !rctx.silentSignaled {
		if prose := deliverableProse(rctx.lastRaw, rctx.offeredTools); prose != "" {
			rctx.warnings = append(rctx.warnings, "no [CHAT] marker emitted; delivered prose as fallback")
			finalOutput = prose
		} else if strings.TrimSpace(rctx.lastRaw) != "" {
			rctx.warnings = append(rctx.warnings,
				"suppressed a non-prose reply (tool-call scaffolding) rather than delivering it")
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/agentrunner/ ./internal/coder/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/coder/coder.go internal/coder/api_engine.go internal/agentrunner/runner.go internal/agentrunner/scaffolding_delivery_test.go
git commit -m "fix(agentrunner): never deliver tool-call scaffolding as a notification"
```

---

### Task 6: Documentation

**Files:**
- Modify: `CLAUDE.md` (API coder engine turn-budget bullet; "Reliable delivery" list)

**Interfaces:**
- Consumes: everything above. Produces nothing code-facing.

- [ ] **Step 1: Replace the turn-budget description**

In the `### API coder engine` section, replace the `**Turn budgets**` bullet with:

```markdown
- **Turn budgets are spent by UNPRODUCTIVE turns only** (`internal/coder/turnbudget.go`):
  base `maxAPITurns` (30) for runs/chat, `maxBuildAPITurns` (50) for builds, a
  `maxHardTurns` (150) ceiling never extended by anything, and a
  `maxUnproductiveStreak` (6) that stops a stuck model far sooner than any base
  budget would. A turn is productive when it executed at least one tool call that
  succeeded and was not a short-circuited repeat. The fixed cap this replaced could
  not tell a runaway loop from legitimately long work — they are identical by turn
  count, and an agent that genuinely needed more turns hit the cap, had its tools
  stripped by the grace turn, and emitted a pending tool call as raw text.
  **The 150 ceiling is not reachable in practice today**: nothing trims
  `req.Messages`, so a 128k-context model exceeds its window around turn 45-50 and
  the provider errors first. History compaction is the prerequisite.
```

- [ ] **Step 2: Add the delivery-contract rule**

Append to the "Reliable delivery" numbered list:

```markdown
6. **Tool-call scaffolding is never delivered** (`coder.LooksLikeToolScaffolding`,
   `agentrunner.deliverableProse`). A model with no structured tool channel will
   sometimes express a pending call as raw text, and the prose fallback — built to
   rescue a forgotten `[CHAT]` — forwarded DeepSeek's `｜DSML｜` markup to a real
   user's phone. The check is keyed on the tools the run OFFERED, never on a
   provider dialect: our own tool name inside a markup construct is decisive, with
   markup density as a backstop. The trigger was our own grace turn, which strips
   `req.Tools` while the model still has work queued. That grace turn is now
   best-effort — its reply is used only if it passes this same check — and
   `exhaustionSummary` composes the real message from run facts instead.
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: record the progress-based turn budget and delivery contract"
```

---

## Self-Review

**Spec coverage.** Turn budget → Task 2. Deterministic exhaustion → Task 4. Delivery contract → Tasks 3 and 5. The statistics both depend on → Task 1. Documentation → Task 6. Out-of-scope items (compaction, cumulative token ceiling) are recorded in Task 2's constant comment and Task 6's CLAUDE.md text rather than implemented.

**Placeholders.** None: every step carries the code to write and the command to run.

**Type consistency.** `callStats` is defined in Task 1 and consumed in Tasks 2 and 4 under that name. `LooksLikeToolScaffolding(text string, offeredTools []string) bool` is defined in Task 3 and called with that signature in Tasks 4 and 5. `newTurnBudget(isBuild bool)` / `next(productive bool) (bool, string)` are consistent. `toolNames` is defined in Task 4 but its result (`offered`) is first captured in Task 2 — Task 2's package test may not compile until Task 4 lands, which Task 2 Step 4 states explicitly.

**Correctness note, resolved in the design rather than left to the executor.** Task 2 replaces `for turn := 0; turn < turnBudget; turn++` with an unbounded `for`. Two existing `continue` statements — the `ErrToolsUnsupported` degrade and the `verifyFinishNudge` path — skip the outcome call, since neither executed a tool. Had the hard ceiling lived only inside `next()`, the loop would have been bounded only by those two paths happening to carry counters of their own, and a third such path added later would spin forever.

Hence the split: `iterate()` counts every pass at the top of the loop and owns the hard ceiling; `next(productive)` records the outcome and owns the base budget and the unproductive streak. The loop is bounded by construction. `TestTurnBudgetCeilingBindsWithoutOutcomes` pins exactly this — a loop that only ever calls `iterate()` must still terminate.
