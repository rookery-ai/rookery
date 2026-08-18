# Build Review Honesty Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The build review shows what the agent actually did, the schedule fires at the hour the user asked for, and a failed build always leaves a trace.

**Architecture:** Five independent changes. The largest executes the built agent once after a create build, on its own narrow path that writes no run row, inbox message or vault note, and uses the real output as the review sample. The rest are a labelling correction, a prompt clause, a failure-recording fix, and a UI guard.

**Tech Stack:** Go 1.26.6 (`internal/agentdesigner`, `internal/prompts`), React/TypeScript (`web/ui`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-18-build-review-honesty-design.md`

## Global Constraints

- Build and test with `GOTOOLCHAIN=auto` — host Go is 1.26.5, `go.mod` requires 1.26.6.
- Do NOT run `make ci` locally; the pipeline is the gate. Run targeted package tests only.
- Conventional Commits (`type(scope): summary`). Types: `feat fix refactor docs test chore perf build ci`. A commit-msg hook rejects anything else.
- Never commit to `main`; work on the existing feature branch.
- The dry run is **create-only** and **best-effort**: it must never fail a build.
- The dry run must write **no** `agent_runs` row, **no** inbox message and **no** vault reflection.
- `internal/agentdesigner/flow.go` is large and carefully commented. Make minimal, local edits; do not restructure.

## File Structure

| File | Responsibility |
|---|---|
| `internal/agentdesigner/dryrun.go` *(new)* | The post-build execution: build the runtime prompt, call the coder in the draft dir, capture `[CHAT]` output. Its own file so it can be read and tested without `flow.go`'s bulk. |
| `internal/agentdesigner/flow.go` | Call the dry run after a successful create build; record the hard-error failure. |
| `internal/agentdesigner/buildoutcome_message.go` *(new)* | The review message wording, moved out of `decideBuildOutcome` so honesty rules are testable in isolation. |
| `internal/prompts/prompts.go` | The cron local-time clause. |
| `web/ui/src/pages/agents/AgentNewPage.tsx` | Reflect an in-flight build instead of offering a fresh form. |

---

### Task 1: The review message stops calling prose a test run

**Files:**
- Create: `internal/agentdesigner/buildoutcome_message.go`
- Create: `internal/agentdesigner/buildoutcome_message_test.go`
- Modify: `internal/agentdesigner/flow.go:2247-2260` (the `var message string` block in `decideBuildOutcome`)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func reviewMessage(sample string, executed bool) string` — `executed` true when `sample` came from something that actually ran.

- [ ] **Step 1: Write the failing test**

```go
package agentdesigner

import (
	"strings"
	"testing"
)

// The review step is where a user decides whether to trust an agent. Telling them
// "here's what a test run produces" when nothing ran teaches them not to trust the
// step at all — and for a TIER 1 agent (no script) nothing DID run, which is the
// common case rather than the exotic one.
func TestReviewMessageOnlyClaimsATestRunWhenSomethingRan(t *testing.T) {
	executed := reviewMessage("3 files changed", true)
	if !strings.Contains(executed, "test run") {
		t.Errorf("an executed sample should be presented as a test run: %q", executed)
	}

	notExecuted := reviewMessage("I will list the files and summarise each.", false)
	if strings.Contains(notExecuted, "test run produces") {
		t.Errorf("prose was presented as a test run: %q", notExecuted)
	}
	if !strings.Contains(strings.ToLower(notExecuted), "didn't run") &&
		!strings.Contains(strings.ToLower(notExecuted), "did not run") &&
		!strings.Contains(strings.ToLower(notExecuted), "couldn't run") {
		t.Errorf("a non-executed sample must say so plainly: %q", notExecuted)
	}
}

// Both forms must still tell the user how to proceed — the message is the only
// place the next action is named.
func TestReviewMessageAlwaysOffersTheNextStep(t *testing.T) {
	for _, executed := range []bool{true, false} {
		got := reviewMessage("sample", executed)
		if !strings.Contains(got, "approve") {
			t.Errorf("reviewMessage(executed=%v) does not tell the user how to save: %q", executed, got)
		}
		if !strings.Contains(got, "sample") {
			t.Errorf("reviewMessage(executed=%v) dropped the sample: %q", executed, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -run TestReviewMessage -count=1`
Expected: FAIL — `reviewMessage undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/agentdesigner/buildoutcome_message.go`:

```go
package agentdesigner

import "fmt"

// reviewMessage wraps the review sample shown at the end of a build.
//
// `executed` is the whole point. The sample has three possible origins — a
// [TEST_OUTPUT] marker, a verified script's captured stdout, or (when neither
// exists) a preview of the model's own prose — and only the first two are evidence
// that anything ran. A TIER 1 agent has no script at all, which is the correct tier
// for "call an API, compare, notify" and therefore the common case, so the prose
// branch is reached often.
//
// Presenting that prose as "here's what a test run produces" is the review step
// lying about the one thing the user is there to check. When nothing ran, say so.
func reviewMessage(sample string, executed bool) string {
	if executed {
		return fmt.Sprintf(
			"Here's what a test run produces:\n\n---\n%s\n---\n\nDoes this look right? Type **approve** to save the agent, or tell me what to change.",
			sample,
		)
	}
	return fmt.Sprintf(
		"I built the assistant and it passed the safety checks, but it didn't run — so this is its own description of what it will do, not real output:\n\n---\n%s\n---\n\nPlease look it over. Type **approve** to save it, or tell me what to change.",
		sample,
	)
}
```

Replace the `var message string` / `if thinProof { … } else { … }` block in
`decideBuildOutcome` (`flow.go:2247-2260`) with:

```go
	message := reviewMessage(testOut, !thinProof)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -count=1`
Expected: PASS. Other tests in the package assert on the old wording; update any that
break to the new strings, keeping their original intent.

- [ ] **Step 5: Commit**

```bash
git add internal/agentdesigner/buildoutcome_message.go internal/agentdesigner/buildoutcome_message_test.go internal/agentdesigner/flow.go
git commit -m "fix(agentdesigner): stop presenting model prose as a test run"
```

---

### Task 2: The cron expression is written in local time

**Files:**
- Modify: `internal/prompts/prompts.go:678-695` (the SCHEDULE DECISION block)
- Test: `internal/prompts/prompts_test.go`

**Interfaces:**
- Consumes: nothing. Produces: nothing code-facing — a prompt clause plus a test pinning it.

- [ ] **Step 1: Write the failing test**

```go
// The scheduler evaluates cron against time.Now() in the SERVER's local zone
// (internal/scheduler: cron.NewParser(Minute|Hour|Dom|Month|Dow), schedule.Next).
// The prompt used to say nothing about this while the profile block handed the model
// the user's timezone — so the model converted to UTC, and an agent asked for "Monday
// at 8" was scheduled 0 6 * * 1 and fired two hours early. Twice, on two builds.
//
// This pins the instruction because the failure is silent: a wrong hour looks like a
// working agent until someone notices the timing.
func TestSchedulePromptForbidsUTCConversion(t *testing.T) {
	p := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x"})
	low := strings.ToLower(p)
	for _, want := range []string{"local time", "do not convert", "utc"} {
		if !strings.Contains(low, want) {
			t.Errorf("the schedule guidance does not mention %q — the model will convert to UTC", want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/prompts/ -run TestSchedulePromptForbidsUTCConversion -count=1`
Expected: FAIL — the prompt contains none of those phrases.

- [ ] **Step 3: Write minimal implementation**

In `internal/prompts/prompts.go`, immediately after the
`YES → First line of AGENT.md: # Suggested schedule: <5-part cron expression>` line
in the SCHEDULE DECISION block, add:

```
  The cron expression is evaluated in the user's OWN LOCAL TIME. Write the hour the
  user said, exactly as they said it. Do NOT convert to UTC — you are told the user's
  timezone elsewhere in this prompt, and converting with it is the single most common
  way this goes wrong: "every morning at 8" for a user in Skopje is `0 8 * * *`, never
  `0 6 * * *`.
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=auto go test ./internal/prompts/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/prompts/prompts.go internal/prompts/prompts_test.go
git commit -m "fix(prompts): state that cron is evaluated in the user's local time"
```

---

### Task 3: A hard build failure leaves a trace

**Files:**
- Modify: `internal/agentdesigner/flow.go:1810-1817` (the unknown-hard-error branch)
- Test: `internal/agentdesigner/hard_failure_test.go` (create)

**Interfaces:**
- Consumes: nothing. Produces: `func hardFailureMessage(err error) string`.

- [ ] **Step 1: Write the failing test**

```go
package agentdesigner

import (
	"errors"
	"strings"
	"testing"
)

// A build that dies on a provider error used to return a raw error and delete its
// working directory, calling neither recordGenerationFailure nor saveDraft. Observed:
// a build ran 488 seconds, failed, and left the draft's updated_at ELEVEN SECONDS
// OLDER than the build's own start — so the user watched eight minutes of "building",
// landed back on the plan, and was told nothing at all.
//
// The message must name the likely cause without echoing the provider's error text:
// buildErrClass exists precisely because a provider error can quote back the request
// that produced it, which CodeQL traced to the workspace's API key.
func TestHardFailureMessageIsActionableAndLeaksNothing(t *testing.T) {
	secret := "sk-workspace-secret-key"
	err := errors.New("coder api error: 502 from provider, request={\"key\":\"" + secret + "\"}")

	got := hardFailureMessage(err)

	if strings.Contains(got, secret) {
		t.Fatalf("the provider's error text leaked into a user-facing message: %q", got)
	}
	if got == "" {
		t.Fatal("a hard failure must always produce a message — silence is the bug")
	}
	if !strings.Contains(strings.ToLower(got), "try") && !strings.Contains(strings.ToLower(got), "again") {
		t.Errorf("the message does not tell the user what to do next: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -run TestHardFailureMessage -count=1`
Expected: FAIL — `hardFailureMessage undefined`.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/agentdesigner/flow.go`, beside `buildErrClass`'s call sites:

```go
// hardFailureMessage is the user-facing account of a build that died on an
// unrecognised error — in practice a provider dropping the connection.
//
// It deliberately does NOT include err.Error(). A provider error can echo back the
// request that produced it, and that dataflow was traced to the workspace's API key
// (go/clear-text-logging), which is why buildErrClass reports a class rather than the
// text. The same reasoning applies with more force to something shown to a user.
func hardFailureMessage(err error) string {
	return "⚠️ The build stopped unexpectedly — the model provider dropped the " +
		"connection. Nothing was saved. Type **approve** to try again, or tell me what " +
		"to change first."
}
```

Then change the unknown-hard-error branch (`flow.go:1814-1816`) from:

```go
		cleanupOnFail()
		return "", false, "", fmt.Errorf("coder: %w", err)
```

to:

```go
		// Record it like every other failure. This branch used to return a raw error
		// having called neither recordGenerationFailure nor saveDraft, so an eight-minute
		// provider drop left the user back on the plan with no explanation at all.
		msg := hardFailureMessage(err)
		f.recordGenerationFailure(workspaceID, msg,
			"the build stopped on an unrecognised coder error (likely a provider drop). "+
				"Next attempt: retry as-is; if it recurs, simplify the agent.", false)
		cleanupOnFail()
		return msg, false, "", nil
```

Note the return is now a soft failure (`nil` error), matching every other branch in
this switch, so the caller's normal outcome flow records and displays it.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentdesigner/flow.go internal/agentdesigner/hard_failure_test.go
git commit -m "fix(agentdesigner): record hard build failures instead of failing silently"
```

---

### Task 4: A real dry run after a create build

**Files:**
- Create: `internal/agentdesigner/dryrun.go`
- Create: `internal/agentdesigner/dryrun_test.go`
- Modify: `internal/agentdesigner/flow.go` (after `decision := decideBuildOutcome(...)` at `:1786`)

**Interfaces:**
- Consumes: `reviewMessage(sample string, executed bool) string` (Task 1).
- Produces: `func (f *Flow) dryRun(ctx context.Context, workspaceID, workDir, agentMD string) (output string, ok bool)` — `ok` false means no usable output; the caller falls back to the existing sample.

- [ ] **Step 1: Write the failing test**

```go
package agentdesigner

import (
	"strings"
	"testing"
)

// A dry run's only job is to produce something the user can look at. The parser has
// to pull the agent's own [CHAT] output back out of the coder's reply and drop the
// protocol scaffolding, exactly as a real run does — otherwise the review shows
// markers instead of a message.
func TestDryRunOutputExtractsChatAndDropsMarkers(t *testing.T) {
	raw := "[STATE]{\"seen\":[\"a.com\"]}[/STATE]\n[CHAT] 3 files changed: notes.md, plan.md, budget.xlsx\n"

	got, ok := dryRunOutput(raw)
	if !ok {
		t.Fatal("a reply containing [CHAT] must yield output")
	}
	if strings.Contains(got, "[STATE]") || strings.Contains(got, "[CHAT]") {
		t.Errorf("protocol markers leaked into the review sample: %q", got)
	}
	if !strings.Contains(got, "3 files changed") {
		t.Errorf("the message was lost: %q", got)
	}
}

// A silent agent is behaving correctly, and the review must say so rather than
// showing an empty box — "it ran and chose to say nothing" is a real, useful result.
func TestDryRunOutputReportsAnIntentionallySilentRun(t *testing.T) {
	got, ok := dryRunOutput("[STATE]{}[/STATE]\n[SILENT]\n")
	if !ok {
		t.Fatal("a silent run is a successful run and must produce a sample")
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("a silent run's sample must not be empty")
	}
	if !strings.Contains(strings.ToLower(got), "nothing to report") {
		t.Errorf("a silent run should be described plainly: %q", got)
	}
}

// Nothing usable means the caller falls back rather than showing an empty review.
func TestDryRunOutputRejectsAnEmptyReply(t *testing.T) {
	if _, ok := dryRunOutput("   \n  "); ok {
		t.Error("an empty reply must not be offered as a dry run result")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -run TestDryRunOutput -count=1`
Expected: FAIL — `dryRunOutput undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/agentdesigner/dryrun.go`:

```go
package agentdesigner

import (
	"context"
	"regexp"
	"strings"

	"github.com/rookery-ai/rookery/internal/buildphase"
	"github.com/rookery-ai/rookery/internal/prompts"
)

// dryRun executes a freshly built agent ONCE so the review step can show what it
// actually does, rather than what the model says it will do.
//
// Why this exists: decideBuildOutcome only has executed evidence when the build
// authored a script and the engine confirmed it ran. A TIER 1 agent has no script —
// the right tier for "call an API, compare, notify", and therefore the common case —
// so the review sample fell back to a preview of the model's own prose, presented as
// "here's what a test run produces". Nothing had run.
//
// Deliberately NOT agentrunner.Run: that requires a db.Agent row (a draft has none)
// and writes an agent_runs row, an inbox message and a vault reflection. A build must
// produce none of those. So this borrows only the runtime PROMPT and the output
// protocol, and throws the rest away.
//
// Best-effort by contract: ok=false on any failure, and the caller keeps the sample it
// already had. A dry run must never fail a build that is already on disk and has
// passed its guardrails.
//
// Cost is real: this is one extra agent run per create build, and a real one measured
// over 1.5M tokens. That is the price of the review step showing something true, and
// it is why the caller invokes this for CREATE builds only.
func (f *Flow) dryRun(ctx context.Context, workspaceID, workDir, agentMD string) (string, bool) {
	coderSvc := f.coderFor(workspaceID)
	if coderSvc == nil {
		return "", false
	}

	// The build-phase marker is NOT optional here, and it is the single most important
	// line in this function. It is what makes connectors.Execute refuse mutating actions.
	// Without it a "dry run" would really post to the user's spreadsheet, really send the
	// email, really publish — a test run with live side effects. WithExtraEnv REPLACES
	// rather than merges, so the whole map is built once, exactly as the build call does.
	extraEnv := map[string]string{buildphase.EnvVar: buildphase.Generation}
	if f.secretsLoader != nil {
		if env, err := f.secretsLoader(ctx, workspaceID); err == nil {
			for k, v := range env {
				extraEnv[k] = v
			}
		}
	}

	run := coderSvc.
		WithDir(workDir).
		WithAllowedTools("Bash,WebFetch,Read,Write,Edit").
		WithExtraEnv(extraEnv)

	// Without connectors the agent has no tools and the dry run proves nothing — the
	// whole point is to exercise the same surface a real run gets. The build-time guard
	// above is what keeps that safe.
	if bound := f.buildBoundConns(ctx, workspaceID); len(bound) > 0 {
		run = run.WithConnectors(f.connReg, f.connStore, bound)
	}
	if boundMCP := f.buildBoundMCP(ctx, workspaceID); len(boundMCP) > 0 {
		run = run.WithMCP(f.mcpCaller, boundMCP)
	}

	res, err := run.Generate(ctx, workspaceID, prompts.BuildCoderPrompt(prompts.CoderPromptParams{
		AgentMD: agentMD,
	}))
	if err != nil || res == nil {
		return "", false
	}
	return dryRunOutput(res.Text)
}

var dryRunStateRE = regexp.MustCompile(`(?s)\[STATE\].*?\[/STATE\]`)

// dryRunOutput turns a coder reply into the sample shown in the review, using the
// same output protocol a real run reads. Returns ok=false when there is nothing
// worth showing.
func dryRunOutput(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if isDryRunSilent(s) {
		// A silent run is a CORRECT outcome for an agent built to stay quiet, and the
		// review must say so — an empty box reads as a broken build.
		return "(The agent ran and had nothing to report — it would stay silent.)", true
	}
	s = dryRunStateRE.ReplaceAllString(s, "")

	var out []string
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "", strings.HasPrefix(t, "[CALL:"):
			continue
		case strings.HasPrefix(t, "[CHAT]"):
			t = strings.TrimSpace(strings.TrimPrefix(t, "[CHAT]"))
			if t == "" {
				continue
			}
		}
		out = append(out, t)
	}
	joined := strings.TrimSpace(strings.Join(out, "\n"))
	if joined == "" {
		return "", false
	}
	return joined, true
}

// isDryRunSilent recognises the [SILENT] marker with the decoration models add.
func isDryRunSilent(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		t := strings.Trim(strings.TrimSpace(line), "*_`\"' \t")
		t = strings.TrimRight(t, ".!?,;:")
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "[silent]", "[/silent]", "silent":
			return true
		}
	}
	return false
}
```

Then wire it in `flow.go`, immediately after
`decision := decideBuildOutcome(workDir, resultText, backendType, scriptVerified, scriptOutput)`
at `:1786`:

```go
	// A create build's review sample should be REAL output. decideBuildOutcome can only
	// produce that when an authored script ran, so a TIER 1 agent (no script) fell back
	// to the model's prose. Run the built agent once and use what it actually says.
	//
	// Create-only: an edit already has a live agent the user has seen work. Best-effort:
	// a failed dry run leaves decision.message exactly as it was.
	if decision.presentable && !isEdit {
		notify("🧪 Running it once to show you real output…")
		if sample, ok := f.dryRun(ctx, workspaceID, workDir, decision.agentMD); ok {
			decision.message = reviewMessage(sample, true)
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentdesigner/dryrun.go internal/agentdesigner/dryrun_test.go internal/agentdesigner/flow.go
git commit -m "feat(agentdesigner): run a created agent once so the review shows real output"
```

---

### Task 5: New Agent reflects an in-flight build

**Files:**
- Modify: `web/ui/src/pages/agents/AgentNewPage.tsx`
- Test: `web/ui/src/pages/agents/newpage-building.test.tsx` (create)

**Interfaces:**
- Consumes: `GET /api/v1/agents/design/state` → `{ active, generating, name? }` (already served).
- Produces: UI behaviour only.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import AgentNewPage from "./AgentNewPage";

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} } as never);
});
afterEach(() => vi.unstubAllGlobals());

// The design session is a per-workspace SINGLETON, so opening New Agent in a second
// tab while a build runs adopts the in-flight session rather than starting fresh.
// Presenting an apparently-blank form is what made that read as broken: the user
// filled it in and nothing they typed could start anything.
test("New Agent says a build is already running instead of offering a fresh form", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/design/state")) {
        return Promise.resolve(jsonResponse({ active: true, generating: true, name: "drive checker" }));
      }
      return Promise.resolve(jsonResponse({}));
    }),
  );

  render(
    <MemoryRouter>
      <AgentNewPage />
    </MemoryRouter>,
  );

  await waitFor(() => {
    expect(screen.getByText(/already building/i)).toBeInTheDocument();
  });
  expect(screen.getByRole("button", { name: /open|view/i })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/agents/newpage-building.test.tsx`
Expected: FAIL — no such text is rendered.

- [ ] **Step 3: Write minimal implementation**

In `AgentNewPage.tsx`, the page already queries `/design/state` for its draft banner.
Read `generating` from that same response and, when true, render a notice in place of
the creation form:

```tsx
{buildInProgress ? (
  <div className="flex flex-col items-center gap-3 p-8 text-center">
    <p className="text-sm text-muted-2">
      A build is already running{buildingName ? ` for “${buildingName}”` : ""}.
      Only one can run at a time.
    </p>
    <Button onClick={() => navigate("/agents/new?resume=1")}>Open it</Button>
  </div>
) : (
  /* the existing form, unchanged */
)}
```

Keep the existing form's markup untouched — this wraps it, it does not rewrite it.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web/ui && npx vitest run src/pages/agents/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/pages/agents/AgentNewPage.tsx web/ui/src/pages/agents/newpage-building.test.tsx
git commit -m "fix(web/agents): show an in-flight build instead of an unusable new form"
```

---

### Task 6: Documentation

**Files:**
- Modify: `CLAUDE.md` (the agent-designer build section)

- [ ] **Step 1: Record the dry run and the honesty rule**

Add to the agent-designer section, after the paragraph describing `[TEST_OUTPUT]`:

```markdown
**A create build RUNS the agent once before showing it to you** (`dryrun.go`).
`decideBuildOutcome` only has executed evidence when the build authored a script and
the engine confirmed it ran — so a TIER 1 agent, which is the correct tier for "call
an API, compare, notify" and therefore the common case, fell back to a preview of the
model's own prose presented as "here's what a test run produces". Nothing had run.
The dry run borrows only the runtime prompt and the output protocol: deliberately NOT
`agentrunner.Run`, which needs a `db.Agent` row a draft has none of and writes a run
row, an inbox message and a vault reflection that a build must not produce. It is
create-only (an edit has a live agent the user has seen work) and best-effort (a
failure leaves the previous sample intact), and it costs one extra agent run per
build — a real one measured over 1.5M tokens. When nothing executed, `reviewMessage`
says so rather than calling prose a test run.
```

- [ ] **Step 2: Record the cron timezone rule**

Add to the same section:

```markdown
**Cron is evaluated in the SERVER's local time, and the prompt now says so.** The
SCHEDULE DECISION block used to name the format and stay silent on the zone, while the
profile block handed the model the user's timezone — so it converted, and an agent
asked for "Monday at 8" was scheduled `0 6 * * 1` and fired two hours early, twice.
Known limitation: the scheduler has no per-workspace timezone, so this is correct only
while the host's zone matches the owner's — true for the single-owner self-hosted case,
and it would need a timezone column on `agent_schedules` to fix properly.
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: record the post-build dry run and the cron timezone rule"
```

---

## Self-Review

**Spec coverage.** §1 dry run → Task 4. §2 honest labelling → Task 1. §3 cron timezone → Task 2. §4 hard failure trace → Task 3. §5 concurrent build → Task 5. Docs → Task 6. Out-of-scope items (history compaction, per-workspace scheduler timezone, dry run for edits) are recorded in Task 6's CLAUDE.md text rather than implemented.

**Placeholders.** None: every step carries code and a command.

**Type consistency.** `reviewMessage(sample string, executed bool) string` is defined in Task 1 and called in Task 4. `dryRunOutput(raw string) (string, bool)` is defined and tested in Task 4. `hardFailureMessage(err error) string` is defined and tested in Task 3. Task 4's `decision.presentable`, `decision.agentMD` and `decision.message` are existing `buildDecision` fields.

**Ordering constraint for the executor.** Task 1 must land before Task 4 — Task 4's wiring calls `reviewMessage`. Tasks 2, 3 and 5 are independent of both and of each other.

**Known risk, resolved in the design rather than left to the executor.** Task 4 makes a SECOND coder call inside `runGeneration`. The first draft of this plan wrote it as a bare `coderFor(...).WithDir(...).Generate(...)`, which would have been genuinely unsafe: the build-phase marker lives in the build call's own `WithExtraEnv` map (`flow.go:1707`), not in the coder object, so a fresh call inherits **neither** the guard **nor** the secrets **nor** the connectors.

The consequences, in order of severity: with no `buildphase.EnvVar`, `connectors.Execute` permits mutating actions, so a "dry run" would really append to the user's spreadsheet and really send mail — a test with live side effects. With no secrets it cannot authenticate. With no connectors it has no tools and proves nothing. The corrected implementation sets all three, building the env map once because `WithExtraEnv` replaces rather than merges.

Two smaller constraints for the executor: pass the SAME `ctx`, so `Cancel()` still stops a dry run mid-flight — never `context.Background()`; and place the call after `decideBuildOutcome` but before `cleanupOnSuccess()`, since the dry run needs the working directory to still exist.

**Both of those were wrong as written, and execution corrected them — recorded here so this plan is not read as the account of what shipped.** `ctx` inside `runGeneration` IS `context.Background()` (the build is deliberately detached), so following the first instruction literally would have made the dry run uncancellable; it uses `genCtx`. And the placement is not merely "before `cleanupOnSuccess()`": that function is a no-op for create builds, which are the only builds that dry-run. The call belongs after `reconcileBlockedOutcome`'s `!outcome.advance` early return — otherwise a blocked build pays for a full rehearsal whose output is then discarded — and before `caveatTruncatedBuild`, which prepends. See `CLAUDE.md` for the shipped rationale.
