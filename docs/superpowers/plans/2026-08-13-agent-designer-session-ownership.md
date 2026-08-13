# Agent Designer Session Ownership Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make an agent build report its result to the surface that owns the session — and only that surface — with a durable record the web can always read, and correlated logs to trace any future failure.

**Architecture:** A `DesignSession` gains an `Origin` (`web` | `chat`) fixed at creation. The non-owner surface is read-only; `Step` refuses it. The build's user-facing message is written to `History` on *both* the success and failure branches, with the coder's technical steering note carried in a separate `note` role that the coder still sees but the UI filters out. The web gains two completion signals beyond the SSE stream so a dropped stream can no longer strand it.

**Tech Stack:** Go 1.x (`log/slog`, Echo v4), React + TypeScript (Vite, vitest), SQLite via `modernc.org/sqlite`. No new dependencies, no schema change, no migration.

**Spec:** `docs/superpowers/specs/2026-08-13-agent-designer-session-ownership-design.md`

## Global Constraints

- **No schema change and no migration.** Ownership is deliberately not persisted; a resumed draft is owned by whoever resumed it.
- **The coder's view of `History` must not regress.** The steering note still reaches the generation prompt. Verified by an explicit test, not by inspection.
- **`/agent cancel` from chat always works**, including against a web-owned session. It is the only action a non-owner may take.
- **Conventional Commits** (`type(scope): summary`) on every commit. Branch is `worktree-agent-designer-session-ownership`; never commit to `main`.
- **`make ci` must pass** before the PR. `go test ./... -count=1` with a 900s allowance for `-race`.
- Web turns are never mirrored to chat. Chat-owned sessions ARE mirrored read-only to web.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/agentdesigner/session_origin.go` *(new)* | The `Origin` type, its labels, and the ownership predicate. Kept out of the 2900-line `flow.go`. |
| `internal/agentdesigner/session_origin_test.go` *(new)* | Origin unit tests. |
| `internal/agentdesigner/flow.go` | `Origin` field + `buildID` on `DesignSession`; origin params on the six session-creation entry points; caller-surface param and non-owner refusal on `Step`; the `note` role; lifecycle logging. |
| `internal/agentdesigner/ownership_test.go` *(new)* | Ownership routing and refusal tests. |
| `internal/agentdesigner/history_roles_test.go` *(new)* | `note`-role tests: coder sees it, DTO does not. |
| `cmd/rookery/main.go` | Origin-aware `OnBuildComplete`; `Warn` on chat-send failure. |
| `internal/gateway/router.go` | Pass `OriginChat`; unconditional `/agent cancel`. |
| `web/handlers_agents.go` | Pass `OriginWeb`; `event: done`; `designTurnResponse` on the `IsGenerating` branch; `origin` in `/design/state`; ownership-gated cancel; honest ended-session error. |
| `web/api_agents_ownership_test.go` *(new)* | Handler-level ownership + SSE `done` tests. |
| `web/ui/src/components/designer/DesignerSurface.tsx` | Read-only mirror mode; `onError` refetch; 5s poll. |
| `web/ui/src/components/designer/ownership.test.tsx` *(new)* | SPA read-only + resilience tests. |

---

### Task 1: The `Origin` type

**Files:**
- Create: `internal/agentdesigner/session_origin.go`
- Test: `internal/agentdesigner/session_origin_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Origin string`; constants `OriginWeb Origin = "web"`, `OriginChat Origin = "chat"`; methods `func (o Origin) String() string` and `func (o Origin) Label() string`.

- [ ] **Step 1: Write the failing test**

```go
package agentdesigner

import "testing"

func TestOriginLabels(t *testing.T) {
	cases := []struct {
		origin Origin
		label  string
	}{
		{OriginWeb, "the web app"},
		{OriginChat, "your chat app"},
		{Origin(""), "another surface"},
	}
	for _, c := range cases {
		if got := c.origin.Label(); got != c.label {
			t.Errorf("Origin(%q).Label() = %q, want %q", c.origin, got, c.label)
		}
	}
}

// The wire form is what /design/state sends and what the SPA compares against,
// so it must be the bare word, not a Go-ish rendering.
func TestOriginStringIsTheWireForm(t *testing.T) {
	if OriginWeb.String() != "web" || OriginChat.String() != "chat" {
		t.Errorf("String() = %q/%q, want web/chat", OriginWeb, OriginChat)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run TestOrigin -v`
Expected: FAIL — `undefined: Origin`

- [ ] **Step 3: Write minimal implementation**

```go
// Package agentdesigner: session ownership.
//
// A design session is a per-workspace singleton that BOTH the web UI and a chat
// adapter can reach. Before this type existed, neither the session nor the
// build-completion hook knew which of them the user was actually using, so a
// build started in the web pushed its dry-run result to Telegram and left the
// browser blank. Origin is fixed when the session is created and never moves:
// the owning surface drives, the other one may read.
package agentdesigner

// Origin identifies the surface that owns a design session.
type Origin string

const (
	// OriginWeb is a session created from the SPA.
	OriginWeb Origin = "web"
	// OriginChat is a session created from a chat adapter (Telegram, Discord, Slack).
	OriginChat Origin = "chat"
)

// String is the wire form sent on /design/state and compared by the SPA.
func (o Origin) String() string { return string(o) }

// Label names the surface in a user-facing refusal. Deliberately generic for
// chat: a workspace may have several adapters linked and Origin does not record
// which one, so naming the wrong app would be worse than naming none.
func (o Origin) Label() string {
	switch o {
	case OriginWeb:
		return "the web app"
	case OriginChat:
		return "your chat app"
	default:
		return "another surface"
	}
}

// Owns reports whether a session with this origin may be driven from `from`.
// A zero origin (a session built by a test, or one predating this field) is
// owned by everyone — failing open here keeps an unknown session usable rather
// than bricking it.
func (o Origin) Owns(from Origin) bool {
	return o == "" || from == "" || o == from
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/agentdesigner/ -run TestOrigin -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentdesigner/session_origin.go internal/agentdesigner/session_origin_test.go
git commit -m "feat(agentdesigner): add Origin type for session ownership"
```

---

### Task 2: Record the origin on every session-creation path

**Files:**
- Modify: `internal/agentdesigner/flow.go` (six creation sites + `Step`)
- Modify: `internal/gateway/router.go:351,359,385,1105`
- Modify: `web/handlers_agents.go:103,177,195,345`
- Test: `internal/agentdesigner/ownership_test.go` (create)

**Interfaces:**
- Consumes: `Origin`, `OriginWeb`, `OriginChat` from Task 1.
- Produces: these exact signatures, which Tasks 3–7 call:
  - `func (f *Flow) Start(workspaceID, agentName string, origin Origin) (string, error)`
  - `func (f *Flow) StartDesign(ctx context.Context, workspaceID, agentName, firstMessage string, origin Origin) (string, error)`
  - `func (f *Flow) StartEdit(workspaceID, agentID string, origin Origin) (string, error)`
  - `func (f *Flow) StartEditDesign(ctx context.Context, workspaceID, agentID, firstMessage string, origin Origin) (string, error)`
  - `func (f *Flow) ResumeDraft(ctx context.Context, workspaceID string, origin Origin) (string, error)`
  - `func (f *Flow) OfferDraftResume(workspaceID, pendingAgentName string, draft *db.AgentDraft, origin Origin) (string, error)`
  - `func (f *Flow) Step(ctx context.Context, workspaceID, input string, from Origin) (string, bool, string, error)`
  - `DesignSession.Origin Origin` (exported field, read by `Snapshot`)

- [ ] **Step 1: Write the failing test**

Create `internal/agentdesigner/ownership_test.go`. `newGenFlow`, `newFakeCoder`,
`slowCoderScript` and `startedSession` already exist in `detached_build_test.go`
(same package) — reuse them rather than building a second harness:

```go
package agentdesigner

import (
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

func TestStartStampsChatOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))

	if _, err := flow.Start(workspaceID, "price-tracker", OriginChat); err != nil {
		t.Fatalf("Start: %v", err)
	}
	sess := flow.GetSession(workspaceID)
	if sess == nil {
		t.Fatal("no session created")
	}
	if sess.Origin != OriginChat {
		t.Errorf("Origin = %q, want %q", sess.Origin, OriginChat)
	}
}

func TestOfferDraftResumeStampsOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))

	draft := &db.AgentDraft{AgentID: "a1", AgentName: "price-tracker"}
	if _, err := flow.OfferDraftResume(workspaceID, "price-tracker", draft, OriginChat); err != nil {
		t.Fatalf("OfferDraftResume: %v", err)
	}
	if got := flow.GetSession(workspaceID).Origin; got != OriginChat {
		t.Errorf("Origin = %q, want %q", got, OriginChat)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run 'StampsOrigin|StampsChatOrigin' -v`
Expected: FAIL — `too many arguments in call to flow.Start`

- [ ] **Step 3: Add the field and thread the parameter**

In `internal/agentdesigner/flow.go`, add to `DesignSession` (after `CreatedAt`, around line 70):

```go
	// Origin is the surface that created this session. Fixed at creation and
	// never reassigned: the owner drives, the other surface may read. See
	// session_origin.go for why this exists.
	Origin Origin
```

Then add the parameter to each of the six creation entry points and set
`Origin: origin` in the `&DesignSession{...}` literal each one builds:

| Function | Line (approx) | Literal to extend |
|---|---|---|
| `Start` | 424 | `f.sessions[workspaceID] = &DesignSession{...}` at 436 |
| `StartDesign` | 456 | its session literal |
| `StartEdit` | 489 | its session literal |
| `StartEditDesign` | 532 | its session literal |
| `ResumeDraft` | 891 | `sess := &DesignSession{...}` at 900 |
| `OfferDraftResume` | 1020 | `f.sessions[workspaceID] = &DesignSession{...}` at 1023 |

`Step` gains a trailing `from Origin` parameter. For this task it is accepted and
ignored — Task 3 adds the refusal. Add it now so the call-site churn happens once:

```go
func (f *Flow) Step(ctx context.Context, workspaceID, input string, from Origin) (string, bool, string, error) {
```

`stepAwaitingResume` calls `ResumeDraft` internally (line 1056). It must pass the
session's OWN origin, not a literal — a resumed draft keeps the surface it was
offered on:

```go
func (f *Flow) stepAwaitingResume(ctx context.Context, workspaceID, msg string) (string, bool, string, error) {
	f.mu.Lock()
	sess := f.sessions[workspaceID]
	pendingName := ""
	origin := Origin("")
	if sess != nil {
		pendingName = sess.pendingName
		origin = sess.Origin
	}
	f.mu.Unlock()

	lower := strings.TrimSpace(strings.ToLower(msg))
	if lower == "resume" {
		resp, err := f.ResumeDraft(ctx, workspaceID, origin)
```

The `"new"` branch of `stepAwaitingResume` calls `f.Start(...)` — pass `origin`
there too.

Add `Origin` to `DesignSnapshot` and populate it in `Snapshot` (Task 6 sends it
on the wire):

```go
	// in DesignSnapshot, after IsEdit:
	Origin Origin

	// in Snapshot's returned literal, after IsEdit:
	Origin: sess.Origin,
```

- [ ] **Step 4: Update the non-test call sites**

`internal/gateway/router.go` — chat is always `OriginChat`:

```go
// line ~351
response, err := r.designFlow.OfferDraftResume(msg.WorkspaceID, name, draft, agentdesigner.OriginChat)
// line ~359
response, err := r.designFlow.Start(msg.WorkspaceID, name, agentdesigner.OriginChat)
// line ~385
response, err := r.designFlow.StartEdit(msg.WorkspaceID, agent.ID, agentdesigner.OriginChat)
// line ~1105
response, _, _, err := r.designFlow.Step(ctx, msg.WorkspaceID, msg.Text, agentdesigner.OriginChat)
```

`web/handlers_agents.go` — web is always `OriginWeb`:

```go
// line ~103
resp, err := s.designFlow.ResumeDraft(c.Request().Context(), u.ID, agentdesigner.OriginWeb)
// line ~177
response, err := s.designFlow.StartDesign(ctx, u.ID, req.Name, req.Message, agentdesigner.OriginWeb)
// line ~195
response, isDone, agentID, err := s.designFlow.Step(ctx, u.ID, req.Message, agentdesigner.OriginWeb)
// line ~345
response, err := s.designFlow.StartEditDesign(c.Request().Context(), u.ID, id, req.Message, agentdesigner.OriginWeb)
```

`internal/skilldesigner/flow.go:321` calls its OWN `ResumeDraft` on the skill
flow — a different type. **Do not touch it.**

- [ ] **Step 5: Fix the compile errors in existing tests**

Run: `go build ./... && go vet ./...`
Then: `go test ./internal/agentdesigner/ ./internal/gateway/ ./web/ -count=1 2>&1 | head -40`

Existing tests call these functions with the old arity. Pass the origin matching
the surface each test is simulating — `OriginChat` for gateway/router tests,
`OriginWeb` for web handler tests, and for `internal/agentdesigner` tests pick
whichever the test's scenario describes (`OriginWeb` when in doubt; the zero
origin also works because `Owns` fails open, but an explicit value documents the
scenario).

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/agentdesigner/ ./internal/gateway/ ./web/ -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agentdesigner/ internal/gateway/router.go web/handlers_agents.go
git commit -m "feat(agentdesigner): stamp session origin at every creation path"
```

---

### Task 3: Refuse a non-owner turn, and name the owner in the already-active refusals

**Files:**
- Modify: `internal/agentdesigner/flow.go` (`Step`, `Start`, `StartDesign`, `StartEdit`, `StartEditDesign`)
- Test: `internal/agentdesigner/ownership_test.go`

**Interfaces:**
- Consumes: `Origin.Owns`, `Origin.Label` (Task 1); `Step`'s `from` param (Task 2).
- Produces: `Step` returns a plain refusal string (not an error) when `from` is not the owner. `errSessionActiveElsewhere(owner Origin) error` for the creation paths.

- [ ] **Step 1: Write the failing test**

Append to `internal/agentdesigner/ownership_test.go`:

```go
// A chat message aimed at a web-owned session must be refused WITHOUT touching
// the session: the whole point is that two surfaces cannot drive one FSM.
func TestStepRefusesNonOwnerAndLeavesSessionAlone(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
		Origin:    OriginWeb,
		History:   []db.ChatMessage{{Role: "assistant", Content: "hello"}},
	}
	flow.mu.Unlock()

	resp, isDone, agentID, err := flow.Step(context.Background(), workspaceID, "approve", OriginChat)
	if err != nil {
		t.Fatalf("a refusal is a normal answer, not an error: %v", err)
	}
	if isDone || agentID != "" {
		t.Errorf("refused turn must not finish anything: (%v, %q)", isDone, agentID)
	}
	if !strings.Contains(resp, "the web app") {
		t.Errorf("refusal = %q, want it to name the owning surface", resp)
	}
	if !strings.Contains(resp, "/agent cancel") {
		t.Errorf("refusal = %q, want it to name the escape hatch", resp)
	}
	sess := flow.GetSession(workspaceID)
	if sess.State != StateDesigning {
		t.Errorf("state = %v, want it untouched", sess.State)
	}
	if len(sess.History) != 1 {
		t.Errorf("history len = %d, want the refused turn NOT recorded", len(sess.History))
	}
}

// The owner is unaffected.
func TestStepAllowsOwner(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
		Origin:    OriginWeb,
	}
	flow.mu.Unlock()

	resp, _, _, err := flow.Step(context.Background(), workspaceID, "approve", OriginWeb)
	if err != nil {
		t.Fatalf("owner turn: %v", err)
	}
	if strings.Contains(resp, "active in") {
		t.Errorf("owner was refused: %q", resp)
	}
}

// A session created by a test or predating the field is owned by everyone —
// failing open keeps it usable rather than bricking it.
func TestStepAllowsZeroOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID) // no Origin set

	if _, _, _, err := flow.Step(context.Background(), workspaceID, "approve", OriginChat); err != nil {
		t.Fatalf("zero-origin session must stay drivable: %v", err)
	}
}

// Starting a second session must say WHERE the first one lives.
func TestStartNamesTheOwningSurface(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	if _, err := flow.StartDesign(context.Background(), workspaceID, "a", "hi", OriginWeb); err != nil {
		t.Fatalf("StartDesign: %v", err)
	}

	_, err := flow.Start(workspaceID, "b", OriginChat)
	if err == nil {
		t.Fatal("want a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "the web app") {
		t.Errorf("err = %q, want it to name the web app", err)
	}
}
```

Add `"context"`, `"strings"` and the `db` import to the file's import block.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run 'Step(Refuses|Allows)|StartNames' -v`
Expected: FAIL — the refusal string is absent; `Step` currently drives the FSM for anyone.

- [ ] **Step 3: Write the implementation**

In `flow.go`, add the shared refusal near `Step`:

```go
// errSessionActiveElsewhere is the refusal used by every creation entry point
// when a session already exists. It names the owning surface, because "you
// already have an active design session" left the user with no idea where to go
// and no idea how to get out.
func errSessionActiveElsewhere(owner Origin) error {
	return fmt.Errorf(
		"you already have an active design session in %s; continue there, or send /agent cancel to discard it",
		owner.Label(),
	)
}

// nonOwnerRefusal is what a design turn from the wrong surface gets back. It is
// a normal response string rather than an error: chat renders it verbatim, and
// an error would be reported as a failure when nothing failed.
func nonOwnerRefusal(owner Origin) string {
	return fmt.Sprintf(
		"This design session is active in %s — please continue there.\n\n"+
			"If you'd rather start over here, send `/agent cancel` to discard it.",
		owner.Label(),
	)
}
```

Add the ownership gate at the top of `Step`, after the session lookup and before
the state dispatch:

```go
func (f *Flow) Step(ctx context.Context, workspaceID, input string, from Origin) (string, bool, string, error) {
	f.mu.Lock()
	sess, ok := f.sessions[workspaceID]
	if !ok {
		f.mu.Unlock()
		return "", false, "", fmt.Errorf("no active design session; use /agent create <name> to start one")
	}
	owner := sess.Origin
	state := sess.State
	f.mu.Unlock()

	// Exclusive ownership: only the surface that created the session may drive
	// it. Returning BEFORE any state dispatch is what makes this safe — a
	// refused turn must not append history, start a build, or advance the FSM.
	if !owner.Owns(from) {
		slog.Info("agentdesigner: refused non-owner design turn",
			"workspace_id", workspaceID, "owner", owner.String(), "from", from.String())
		return nonOwnerRefusal(owner), false, "", nil
	}

	switch state {
	// ... unchanged
	}
}
```

Replace the four `fmt.Errorf("you already have an active design session; send /agent cancel to start over")`
occurrences in `Start`, `StartDesign`, `StartEdit` and `StartEditDesign` with
`errSessionActiveElsewhere(existing.Origin)`. Each currently discards the
existing session with `if _, exists := f.sessions[workspaceID]; exists` — bind it
instead:

```go
	if existing, exists := f.sessions[workspaceID]; exists {
		f.mu.Unlock()   // omit this line in Start, which uses `defer f.mu.Unlock()`
		return "", errSessionActiveElsewhere(existing.Origin)
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agentdesigner/ -count=1`
Expected: PASS

- [ ] **Step 5: Verify `/agent cancel` is still unconditional**

Read `internal/gateway/router.go` around line 393-405. The `cancel` case calls
`r.designFlow.Cancel(msg.WorkspaceID)` with no ownership check. **Leave it that
way** and add a comment stating why:

```go
	case "cancel":
		if r.designFlow == nil {
			send("Agent creation is not yet available.")
			return nil
		}
		// Deliberately NOT ownership-gated. A web-owned session whose browser is
		// gone would otherwise lock chat out until the 7-day draft TTL expires,
		// and cancel is the only action a non-owner may take. See
		// session_origin.go and the ownership section of the design spec.
		r.designFlow.Cancel(msg.WorkspaceID)
```

- [ ] **Step 6: Commit**

```bash
git add internal/agentdesigner/ internal/gateway/router.go
git commit -m "feat(agentdesigner): refuse design turns from the non-owning surface"
```

---

### Task 4: Route the build result to the owning surface only

**Files:**
- Modify: `internal/agentdesigner/flow.go` (`BuildCompleteFunc`, `startGeneration`)
- Modify: `cmd/rookery/main.go:588-600`
- Test: `internal/agentdesigner/ownership_test.go`

**Interfaces:**
- Consumes: `DesignSession.Origin` (Task 2).
- Produces: `type BuildCompleteFunc func(workspaceID string, origin Origin, response string, isDone bool, agentID string, err error)` — one extra parameter in position 2.

- [ ] **Step 1: Write the failing test**

Append to `internal/agentdesigner/ownership_test.go`:

```go
// The bug this whole change exists for: a build started in the web must not be
// announced in Telegram.
func TestBuildCompleteCarriesTheSessionOrigin(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker",
		State:     StateDesigning,
		Origin:    OriginWeb,
	}
	flow.mu.Unlock()

	got := make(chan Origin, 1)
	flow.OnBuildComplete(func(_ string, origin Origin, _ string, _ bool, _ string, _ error) {
		got <- origin
	})

	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}

	select {
	case origin := <-got:
		if origin != OriginWeb {
			t.Errorf("origin = %q, want %q", origin, OriginWeb)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("build never completed")
	}
}
```

Add `"time"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run TestBuildCompleteCarries -v`
Expected: FAIL — `too many arguments` / signature mismatch.

- [ ] **Step 3: Change the hook signature and capture the origin**

In `flow.go`, replace the `BuildCompleteFunc` declaration:

```go
// BuildCompleteFunc is called when a DETACHED build finishes, with whatever
// runGeneration produced and the origin of the session that requested it.
//
// origin is what makes delivery correct rather than merely reliable: the hook is
// registered once at wiring time and cannot see which surface the user is on, so
// without it every finished build was announced in chat — including builds the
// user started, and was watching, in the browser.
type BuildCompleteFunc func(workspaceID string, origin Origin, response string, isDone bool, agentID string, err error)
```

In `startGeneration`, snapshot the origin under the same lock that snapshots the
hook, and pass it through:

```go
	sess.progressCh = make(chan string, 8)
	done := f.onBuildComplete
	origin := sess.Origin
	f.mu.Unlock()

	go func() {
		resp, isDone, agentID, err := f.runGeneration(context.Background(), workspaceID)
		if done != nil {
			done(workspaceID, origin, resp, isDone, agentID, err)
		}
	}()
```

- [ ] **Step 4: Make the delivery origin-aware in `main.go`**

Replace the `designFlow.OnBuildComplete(...)` block at `cmd/rookery/main.go:588`:

```go
			// Deliver a DETACHED build's result to the surface that OWNS the
			// session — and to no other.
			//
			// This hook is registered once at wiring time, so it long outlives
			// the turn that started the build. That is what makes it chat's
			// recovery channel, and it is also how it used to misdeliver: with no
			// origin it announced every finished build in chat, including builds
			// the user started and was watching in the browser. A web-owned build
			// needs nothing pushed here — the SPA reads the outcome out of the
			// session's History.
			designFlow.OnBuildComplete(func(workspaceID string, origin agentdesigner.Origin, response string, _ bool, _ string, err error) {
				text := response
				if err != nil {
					text = gateway.FriendlyDesignError("agent", err)
				}
				if strings.TrimSpace(text) == "" {
					return
				}
				if origin != agentdesigner.OriginChat {
					slog.Info("agentdesigner: build result withheld from chat",
						"workspace_id", workspaceID, "origin", origin.String(), "chat_suppressed", true)
					return
				}
				if sendErr := gwManager.SendToUser(workspaceID, text); sendErr != nil {
					// Warn, not Debug. A chat-owned build whose result cannot be
					// delivered is the user's ONLY copy going missing — the exact
					// silent failure this change exists to end.
					slog.Warn("agentdesigner: chat delivery of build result failed",
						"workspace_id", workspaceID, "err", sendErr)
					return
				}
				slog.Info("agentdesigner: build result delivered",
					"workspace_id", workspaceID, "target", "chat")
			})
```

Confirm `agentdesigner` is imported in `main.go`; add it if not.

- [ ] **Step 5: Fix the four test call sites in `detached_build_test.go`**

Lines 46, 84, 132, 162 register `OnBuildComplete` with the old arity. Add the
origin parameter:

```go
	flow.OnBuildComplete(func(string, Origin, string, bool, string, error) { close(done) })
	// and, at line 84:
	flow.OnBuildComplete(func(ws string, _ Origin, response string, _ bool, _ string, _ error) {
```

- [ ] **Step 6: Run the tests**

Run: `go build ./... && go test ./internal/agentdesigner/ ./web/ -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/agentdesigner/ cmd/rookery/main.go
git commit -m "fix(agentdesigner): deliver build results only to the owning surface"
```

---

### Task 5: One durable user-facing record — the `note` role

**Files:**
- Modify: `internal/agentdesigner/flow.go` (`recordGenerationFailure`, `runGeneration`, `dbMessagesToPrompt`)
- Modify: `web/handlers_agents.go` (`designHistoryDTO`)
- Test: `internal/agentdesigner/history_roles_test.go` (create)

**Interfaces:**
- Consumes: `reconciledOutcome.message` / `.recordFailNote` (existing).
- Produces: `const roleNote = "note"`; `func (f *Flow) recordGenerationFailure(workspaceID, userMessage, detail string, forceTier1 bool)` — one extra parameter in position 2.

- [ ] **Step 1: Write the failing test**

Create `internal/agentdesigner/history_roles_test.go`:

```go
package agentdesigner

import (
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
)

// The failure path used to write a DIFFERENT message to History than the one it
// returned: chat got the real explanation, the web got a generic "it did not
// succeed". Both must now see the real one.
func TestRecordGenerationFailureStoresTheUserFacingMessage(t *testing.T) {
	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	startedSession(t, flow, workspaceID)

	flow.recordGenerationFailure(workspaceID,
		"I couldn't finish building this: the weather API rejected the key.",
		"the API key was rejected; ask the user to re-check it",
		false)

	hist := flow.GetSession(workspaceID).History
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2 (user-facing + steering note)", len(hist))
	}
	if hist[0].Role != "assistant" || !strings.Contains(hist[0].Content, "weather API rejected") {
		t.Errorf("turn 0 = %+v, want the user-facing message as assistant", hist[0])
	}
	if hist[1].Role != roleNote || !strings.Contains(hist[1].Content, "re-check it") {
		t.Errorf("turn 1 = %+v, want the steering note under the note role", hist[1])
	}
}

// The coder must still receive the steering note — that is what stops the retry
// being context-blind. Mapping note->assistant and coalescing adjacent turns
// keeps the prompt shape identical to before this change.
func TestNoteRoleReachesTheCoderAsAssistant(t *testing.T) {
	msgs := []db.ChatMessage{
		{Role: "user", Content: "approve"},
		{Role: "assistant", Content: "I couldn't finish this."},
		{Role: roleNote, Content: "drop the script and reason directly"},
	}
	got := dbMessagesToPrompt(msgs)

	if len(got) != 2 {
		t.Fatalf("prompt turns = %d, want 2 (the note coalesced into the assistant turn)", len(got))
	}
	if got[1].Role != "assistant" {
		t.Errorf("role = %q, want assistant", got[1].Role)
	}
	if !strings.Contains(got[1].Content, "couldn't finish") ||
		!strings.Contains(got[1].Content, "drop the script") {
		t.Errorf("content = %q, want BOTH the message and the steering note", got[1].Content)
	}
}

// A lone note with no preceding assistant turn still reaches the coder.
func TestLoneNoteBecomesAnAssistantTurn(t *testing.T) {
	got := dbMessagesToPrompt([]db.ChatMessage{
		{Role: "user", Content: "approve"},
		{Role: roleNote, Content: "steering"},
	})
	if len(got) != 2 || got[1].Role != "assistant" || got[1].Content != "steering" {
		t.Errorf("got %+v, want the note as its own assistant turn", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run 'RecordGeneration|NoteRole|LoneNote' -v`
Expected: FAIL — `undefined: roleNote`, and `recordGenerationFailure` takes 3 args.

- [ ] **Step 3: Write the implementation**

In `flow.go`, add the constant near the top of the generation section:

```go
// roleNote marks a History turn that exists for the CODER, not the user: the
// technical steering note recorded after a failed build so the retry is not
// context-blind.
//
// History does double duty — it is both the user's transcript and the coder's
// conversation — and on the failure path those two purposes conflicted. The
// generic steering note was what the web rendered while the real explanation
// went only to chat. This role keeps both without a second store:
// dbMessagesToPrompt folds it into the coder's view, designHistoryDTO drops it
// from the user's.
const roleNote = "note"
```

Rewrite `recordGenerationFailure`:

```go
// recordGenerationFailure records a failed build in two parts.
//
// userMessage is what the USER should read, and is stored as an ordinary
// assistant turn so every surface renders it. detail is the CODER's steering
// note — technical, sometimes blunt — and is stored under roleNote so it reaches
// the next generation prompt without ever being shown.
func (f *Flow) recordGenerationFailure(workspaceID, userMessage, detail string, forceTier1 bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess := f.sessions[workspaceID]
	if sess == nil {
		return
	}
	sess.GenerationFailed = true
	if forceTier1 {
		sess.ForceTier1 = true
	}
	now := time.Now().UTC()
	if msg := strings.TrimSpace(userMessage); msg != "" {
		sess.History = append(sess.History, db.ChatMessage{Role: "assistant", Content: msg, CreatedAt: now})
	}
	note := "I attempted to build the agent but it did not succeed."
	if strings.TrimSpace(detail) != "" {
		note += " Reason: " + strings.TrimSpace(detail) + "."
	}
	note += " On the next attempt I will address this and finish building the agent."
	sess.History = append(sess.History, db.ChatMessage{Role: roleNote, Content: note, CreatedAt: now})
	f.saveDraft(sess)
}
```

Update the single caller in `runGeneration` (line ~1682):

```go
		f.recordGenerationFailure(workspaceID, outcome.message, outcome.recordFailNote, outcome.forceTier1)
```

Rewrite `dbMessagesToPrompt` to map and coalesce:

```go
// dbMessagesToPrompt converts stored history into coder-facing turns.
//
// roleNote turns become assistant turns and are COALESCED into an immediately
// preceding assistant turn. Coalescing is not cosmetic: without it a failed
// build emits two consecutive assistant messages, and several providers reject
// or silently merge same-role runs. Folding them here keeps the prompt shape
// identical to what the coder saw before the note role existed.
func dbMessagesToPrompt(msgs []db.ChatMessage) []prompts.ChatMessage {
	out := make([]prompts.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		role := m.Role
		if role == roleNote {
			role = "assistant"
		}
		if n := len(out); n > 0 && out[n-1].Role == role && role == "assistant" {
			out[n-1].Content += "\n\n" + m.Content
			continue
		}
		out = append(out, prompts.ChatMessage{Role: role, Content: m.Content})
	}
	return out
}
```

In `web/handlers_agents.go`, filter the note out of the DTO:

```go
// designHistoryDTO maps session history to the wire shape. Shared by the agent
// resume/state handlers and the skill resume handler so the three cannot drift.
//
// roleNote turns are dropped: they are the coder's steering context, not the
// user's transcript. Rendering them is what showed a generic "it did not
// succeed" in the browser while the real explanation went to chat.
func designHistoryDTO(hist []db.ChatMessage) []designHistEntry {
	out := make([]designHistEntry, 0, len(hist))
	for _, m := range hist {
		if m.Role == agentdesigner.RoleNote {
			continue
		}
		e := designHistEntry{Role: m.Role, Content: m.Content}
		if !m.CreatedAt.IsZero() {
			e.CreatedAt = m.CreatedAt.Format(time.RFC3339Nano)
		}
		out = append(out, e)
	}
	return out
}
```

`web` is a different package, so export the constant alongside the private one in
`flow.go`:

```go
// RoleNote is roleNote, exported for the web layer's history DTO filter.
const RoleNote = roleNote
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agentdesigner/ ./web/ -count=1`
Expected: PASS

- [ ] **Step 5: Add the DTO filter test**

Append to `web/api_agents_ownership_test.go` (create the file with
`package web` and the needed imports):

```go
// A note turn is the coder's steering context and must never reach the browser.
func TestDesignHistoryDTODropsNoteTurns(t *testing.T) {
	got := designHistoryDTO([]db.ChatMessage{
		{Role: "user", Content: "approve"},
		{Role: "assistant", Content: "here is the real reason"},
		{Role: agentdesigner.RoleNote, Content: "internal steering"},
	})
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	for _, e := range got {
		if strings.Contains(e.Content, "internal steering") {
			t.Errorf("note turn leaked to the browser: %+v", e)
		}
	}
}
```

Run: `go test ./web/ -run TestDesignHistoryDTO -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agentdesigner/ web/handlers_agents.go web/api_agents_ownership_test.go
git commit -m "fix(agentdesigner): record the real build failure message for every surface"
```

---

### Task 6: Web server-side reliability and ownership

**Files:**
- Modify: `web/handlers_agents.go` (`handleDesignProgress`, `handleDesignChat`, `handleDesignState`, `handleCancelDesign`)
- Test: `web/api_agents_ownership_test.go`

**Interfaces:**
- Consumes: `DesignSnapshot.Origin` (Task 2), `agentdesigner.RoleNote` (Task 5).
- Produces: `/design/state` gains `"origin": "web"|"chat"`. The SSE stream emits `event: done\ndata: 1\n\n` before closing.

- [ ] **Step 1: Write the failing test**

Append to `web/api_agents_ownership_test.go`:

```go
// The design stream had no done event, so the browser could only infer
// completion from a 404 on the NEXT attach — after handleDesignProgress's 30s
// poll. run_tracker.go has emitted one all along; this closes the asymmetry.
func TestDesignProgressEmitsDoneBeforeClosing(t *testing.T) {
	// Build a server whose designFlow has a session with a live progress channel,
	// push one milestone, close it, and assert the response body.
	// (Use the existing web test harness: newTestServer / authed request helpers.)
	body := streamDesignProgress(t) // helper defined below

	if !strings.Contains(body, "data: milestone") {
		t.Errorf("body = %q, want the milestone line", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("body = %q, want a terminating done event", body)
	}
}

// A mid-build turn must not clear the failure banner client-side. The
// IsGenerating branch returned no state/generation_failed/can_keep_as_is, so the
// SPA coerced all three to false.
func TestStillBuildingResponseCarriesFullState(t *testing.T) {
	var got map[string]interface{}
	got = postDesignWhileGenerating(t) // helper defined below

	for _, k := range []string{"state", "generation_failed", "can_keep_as_is", "building"} {
		if _, ok := got[k]; !ok {
			t.Errorf("still-building response is missing %q: %v", k, got)
		}
	}
}
```

Write the two helpers concretely against whatever harness the existing
`web/api_agents_test.go` uses (read it first — reuse its server constructor and
auth cookie helper rather than inventing a second one).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run 'DesignProgressEmitsDone|StillBuildingResponse' -v`
Expected: FAIL — no `event: done`; missing keys.

- [ ] **Step 3: Emit the done event**

In `handleDesignProgress`, replace the stream loop's close branch:

```go
	for {
		select {
		case <-reqCtx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				// Named terminal event, matching run_tracker.go. Without it the
				// browser can only infer completion from EventSource's transparent
				// reconnect hitting a 404 — which costs the 30s poll above and, on
				// a clean close, leaves readyState CONNECTING so onDone never fires
				// at all. openSSE already listens for this event unconditionally.
				fmt.Fprint(w, "event: done\ndata: 1\n\n")
				w.Flush()
				return nil
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			w.Flush()
		}
	}
```

- [ ] **Step 4: Return the full state on the still-building branch**

In `handleDesignChat`, replace the `IsGenerating` early return:

```go
	if s.designFlow.IsGenerating(u.ID) {
		// designTurnResponse, not a bare literal: the hand-rolled body omitted
		// state/generation_failed/can_keep_as_is, and the SPA coerces a missing
		// field to false — so a message sent mid-build silently cleared the
		// failure banner and reset the stepper.
		return c.JSON(http.StatusOK, designTurnResponse(
			"⏳ Still building your agent — I'll show the result here as soon as it's done.",
			s.designFlow.Snapshot(u.ID),
		))
	}
```

- [ ] **Step 5: Send the origin, gate cancel, and make the ended-session error honest**

`handleDesignState` — add `origin` to the payload:

```go
		"is_edit":           snap.IsEdit,
		"origin":            snap.Origin.String(),
```

`handleCancelDesign` — only the owner may cancel from the web:

```go
// handleCancelDesign cancels the active design session, killing any in-flight
// coder subprocess and closing the SSE progress channel.
//
// Ownership-gated: the design session is a per-workspace singleton, and the SPA
// adopts whatever session exists on mount. Without this check, opening the agent
// page while a Telegram build ran and clicking Cancel killed that build. Chat's
// /agent cancel is deliberately NOT gated — it is the escape hatch for a
// web-owned session whose browser is gone.
// POST /dashboard/agents/design/cancel
func (s *Server) handleCancelDesign(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.designFlow == nil {
		return c.JSON(http.StatusOK, map[string]string{"status": "cancelled"})
	}
	snap := s.designFlow.Snapshot(u.ID)
	if snap.Active && !snap.Origin.Owns(agentdesigner.OriginWeb) {
		return c.JSON(http.StatusOK, map[string]string{
			"status": "not_owner",
			"error":  "this session is running in " + snap.Origin.Label(),
		})
	}
	s.designFlow.Cancel(u.ID)
	return c.JSON(http.StatusOK, map[string]string{"status": "cancelled"})
}
```

`handleDesignChat` — replace the bare name-required 400:

```go
	if s.designFlow.GetSession(u.ID) == nil {
		if req.Name == "" {
			// This is what a user hits after their session was completed or
			// cancelled from another surface. "name is required to start a new
			// session" described an internal precondition and told them nothing.
			return c.JSON(http.StatusConflict, map[string]string{
				"error":   "This design session has ended — it may have been completed or cancelled from another surface. Start a new one to continue.",
				"code":    "session_ended",
			})
		}
```

Note the status change from 400 to 409; update `web/api_parity_test.go` only if
it asserts on the status (it asserts on route registration, so most likely not).

- [ ] **Step 6: Run the tests**

Run: `go test ./web/ -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add web/
git commit -m "fix(web): deterministic design SSE completion and ownership-gated cancel"
```

---

### Task 7: build_id lifecycle logging

**Files:**
- Modify: `internal/agentdesigner/flow.go` (`DesignSession`, `startGeneration`, `runGeneration`)
- Test: `internal/agentdesigner/ownership_test.go`

**Interfaces:**
- Consumes: `DesignSession.Origin`.
- Produces: `DesignSession.buildID string` (unexported); five `slog.Info` lines keyed `build_id`.

- [ ] **Step 1: Write the failing test**

Append to `internal/agentdesigner/ownership_test.go`:

```go
// A build must be traceable end to end from the logs alone — the incident that
// motivated this change produced zero designer log lines.
func TestBuildEmitsCorrelatedLifecycleLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	flow, workspaceID, _ := newGenFlow(t, newFakeCoder(t, slowCoderScript))
	flow.mu.Lock()
	flow.sessions[workspaceID] = &DesignSession{
		AgentName: "price-tracker", State: StateDesigning, Origin: OriginWeb,
	}
	flow.mu.Unlock()

	done := make(chan struct{})
	flow.OnBuildComplete(func(string, Origin, string, bool, string, error) { close(done) })
	if _, _, _, err := flow.startGeneration(workspaceID); err != nil {
		t.Fatalf("startGeneration: %v", err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("build never completed")
	}

	out := buf.String()
	for _, want := range []string{"build start", "build outcome", "build_id=", "origin=web"} {
		if !strings.Contains(out, want) {
			t.Errorf("logs missing %q:\n%s", want, out)
		}
	}
}
```

Add `"bytes"` and `"log/slog"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/agentdesigner/ -run TestBuildEmitsCorrelated -v`
Expected: FAIL — no such log lines.

- [ ] **Step 3: Write the implementation**

Add to `DesignSession`, beside `progressCh`:

```go
	// buildID correlates every log line of one build. Minted in startGeneration
	// so a single grep reconstructs the lifecycle — the incident that motivated
	// session ownership produced no designer log lines at all.
	buildID string
```

In `startGeneration`, mint it and log the start:

```go
	sess.progressCh = make(chan string, 8)
	sess.buildID = uuid.New().String()[:8]
	buildID := sess.buildID
	done := f.onBuildComplete
	origin := sess.Origin
	agentName := sess.AgentName
	isEdit := sess.IsEdit
	f.mu.Unlock()

	slog.Info("agentdesigner: build start",
		"build_id", buildID, "workspace_id", workspaceID,
		"origin", origin.String(), "agent", agentName, "edit", isEdit)

	go func() {
		started := time.Now()
		resp, isDone, agentID, err := f.runGeneration(context.Background(), workspaceID)
		slog.Info("agentdesigner: build finished",
			"build_id", buildID, "workspace_id", workspaceID,
			"origin", origin.String(), "dur_s", int(time.Since(started).Seconds()),
			"done", isDone, "err", err)
		if done != nil {
			done(workspaceID, origin, resp, isDone, agentID, err)
		}
	}()
```

In `runGeneration`, read `sess.buildID` into a local alongside the other snapshots
(`buildID := sess.buildID`) and add three lines:

After the coder call returns (just before `notify("🔍 Validating agent safety checks…")`):

```go
	slog.Info("agentdesigner: build coder returned",
		"build_id", buildID, "workspace_id", workspaceID,
		"backend", backendType, "result_bytes", len(resultText))
```

After `outcome := reconcileBlockedOutcome(decision, blocked, backendType)`:

```go
	slog.Info("agentdesigner: build decision",
		"build_id", buildID, "workspace_id", workspaceID,
		"advance", outcome.advance, "saveable", decision.saveable,
		"script_verified", decision.scriptVerified, "blocked", blocked != "")
```

Immediately before each of the two `closeProgress()` calls that precede a return
(the `!outcome.advance` branch and the success branch):

```go
	slog.Info("agentdesigner: build outcome",
		"build_id", buildID, "workspace_id", workspaceID,
		"state", "designing", "msg_bytes", len(outcome.message)) // "verifying" on the success branch
```

Confirm `uuid` and `log/slog` are already imported in `flow.go` (both are).

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agentdesigner/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agentdesigner/
git commit -m "feat(agentdesigner): correlate build lifecycle logs with a build_id"
```

---

### Task 8: SPA — read-only mirror, error refetch, and the poll

**Files:**
- Modify: `web/ui/src/components/designer/DesignerSurface.tsx`
- Test: `web/ui/src/components/designer/ownership.test.tsx` (create)

**Interfaces:**
- Consumes: `origin` on the `/design/state` response (Task 6).
- Produces: no exported API change; `StateSnapshot` gains `origin?: string`.

- [ ] **Step 1: Write the failing test**

Read `web/ui/src/components/designer/designer.test.tsx` first and reuse its
render harness, api mock and `openSSE` mock. Then create
`ownership.test.tsx`:

```tsx
import { describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";

// Mirroring a chat-owned session must be strictly read-only: the design session
// is a per-workspace singleton, and a Cancel POST from a mirroring tab killed
// the live Telegram build.
describe("chat-owned session", () => {
  it("renders read-only and never POSTs cancel", async () => {
    const post = vi.fn();
    renderDesigner({
      state: { active: true, state: "designing", origin: "chat", history: [] },
      post,
    });

    await screen.findByText(/running in your chat app/i);
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();

    (await screen.findByRole("button", { name: /cancel/i })).click();
    await waitFor(() => expect(post).not.toHaveBeenCalled());
  });
});

// A dropped stream used to stop the spinner and give up, leaving the finished
// build's result unread.
describe("build completion resilience", () => {
  it("refetches state when the progress stream errors", async () => {
    const get = vi.fn().mockResolvedValue({ active: true, state: "designing", origin: "web", history: [] });
    const { fireSSEError } = renderDesignerBuilding({ get });

    get.mockClear();
    fireSSEError();
    await waitFor(() => expect(get).toHaveBeenCalled());
  });

  it("polls state while generating and stops when it ends", async () => {
    vi.useFakeTimers();
    const get = vi.fn().mockResolvedValue({ active: true, generating: true, origin: "web", history: [] });
    renderDesignerBuilding({ get });

    get.mockClear();
    await vi.advanceTimersByTimeAsync(5000);
    expect(get).toHaveBeenCalledTimes(1);

    get.mockResolvedValue({ active: true, generating: false, origin: "web", history: [] });
    await vi.advanceTimersByTimeAsync(5000);
    get.mockClear();
    await vi.advanceTimersByTimeAsync(15000);
    expect(get).not.toHaveBeenCalled();
    vi.useRealTimers();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/components/designer/ownership.test.tsx`
Expected: FAIL — no read-only banner, no refetch, no poll.

- [ ] **Step 3: Add origin to the snapshot type and track ownership**

```tsx
type StateSnapshot = {
  active: boolean;
  generating?: boolean;
  state?: string;
  history?: HistEntry[];
  name?: string;
  agent_id?: string;
  is_edit?: boolean;
  // The surface that OWNS this session. Absent on an inactive-session response
  // and on a server predating ownership; both read as "we own it", which keeps
  // the surface usable rather than locking it read-only on a stale build.
  origin?: string;
  last_progress?: string;
  generation_failed?: boolean;
  can_keep_as_is?: boolean;
  pending_agent_md?: string;
  pending_tools?: Record<string, string>;
};
```

Add state beside the other `useState` calls:

```tsx
  // "" means unowned/ours. Anything else means another surface is driving and
  // this one is a read-only mirror.
  const [ownerSurface, setOwnerSurface] = useState("");
  const readOnly = ownerSurface !== "" && ownerSurface !== "web";
```

Set it in `refetchState`'s accepted branch, and clear it on the inactive branch:

```tsx
      if (accepted) {
        sessionTouchedRef.current = true;
        sessionOpenedRef.current = true;
        setOwnerSurface(snap.origin ?? "");
        // ... rest unchanged
      } else {
        setOwnerSurface("");
        setGenerating(false);
```

- [ ] **Step 4: Make cancel and the actions ownership-aware**

```tsx
  async function handleCancel() {
    // A read-only mirror has nothing of its own to cancel, and the session is a
    // per-workspace singleton — POSTing here would kill the OTHER surface's live
    // build. (The server refuses it too; this keeps the UI honest.)
    if (sessionTouchedRef.current && !readOnly) {
      try {
        await api.post(endpoints.cancel);
      } catch {
        // Ignore — we're navigating away regardless.
      }
    }
    navigate(cancelTo);
  }
```

Gate the action rows and the composer:

```tsx
  const showDesigningActions =
    fsmState === "designing" && !generating && !busy && lastIsAssistant && !readOnly;
  const showVerifyingActions =
    fsmState === "verifying" && !generating && !busy && lastIsAssistant && !readOnly;
```

Guard `handleSend` at the top:

```tsx
  async function handleSend(text: string) {
    if (readOnly) return;
    setError(null);
```

Render the banner above the transcript (immediately after the header `</div>`,
before the `view === "spec"` ternary):

```tsx
      {readOnly && (
        <div className="border-b border-border bg-chrome px-4 py-2 text-xs text-muted-2">
          This design session is running in your chat app — follow along here,
          and continue there to make changes.
        </div>
      )}
```

Replace the `Composer` with the read-only notice when mirroring:

```tsx
      {readOnly ? (
        <div className="border-t border-border px-4 py-3 text-center text-xs text-muted-2">
          Read-only — this session is being driven from your chat app.
        </div>
      ) : (
        <Composer
          onSend={(v) => void handleSend(v)}
          busy={composerBusy}
          focusSignal={focusSignal}
          initialText={autoSendInitial ? undefined : initialText}
          gutter
        />
      )}
```

Also hide the "Keep it as-is" button when `readOnly` by changing its condition to
`{canKeepAsIs && !readOnly && (`.

- [ ] **Step 5: Refetch on stream error, and poll while generating**

In `ensureSSE`, make `onError` recover instead of giving up:

```tsx
      onError: () => {
        setSse((s) => (s ? { ...s, status: "error" } : s));
        sseHandleRef.current = null;
        setGenerating(false);
        // A dropped or never-opened stream used to end here, stranding a build
        // whose result was already committed to History. The refetch is the
        // second of three independent completion signals (the others being the
        // server's `done` event and the poll below).
        if (!doneRef.current && endpoints.state) {
          awaitingBuildResultRef.current = false;
          void refetchState();
        }
      },
```

Add the poll as a new effect near the mount-recovery effect:

```tsx
  // Third completion signal: a slow poll for as long as a build is running.
  // The `done` event and the error refetch both depend on the SSE stream
  // existing at all; a proxy that swallows it entirely leaves neither. Five
  // seconds is slow enough to be invisible on a multi-minute build and fast
  // enough that the result never feels stuck.
  useEffect(() => {
    if (!generating || !endpoints.state) return;
    const id = setInterval(() => {
      if (doneRef.current || unmountedRef.current) return;
      void refetchState();
    }, 5000);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [generating, endpoints.state]);
```

- [ ] **Step 6: Run the tests**

Run: `cd web/ui && npx vitest run src/components/designer/`
Expected: PASS, including the pre-existing `designer.test.tsx`.

- [ ] **Step 7: Typecheck and lint**

Run: `cd web/ui && npx tsc -b && npx oxlint`
Expected: clean

- [ ] **Step 8: Commit**

```bash
git add web/ui/src/components/designer/
git commit -m "feat(web/designer): read-only mirror for chat-owned sessions and resilient completion"
```

---

### Task 9: Full gate, documentation, and PR

**Files:**
- Modify: `CLAUDE.md` (the agent-designer architecture section)
- Verify: everything

- [ ] **Step 1: Run the full local gate**

Run: `make ci`
Expected: PASS — `ci-fmt`, `ci-vet`, `ci-test` (`-race`), `ci-cross`, `ci-ui`, `ci-docs`.

Fix anything that fails before continuing. `ci-test` under `-race` allows 900s.

- [ ] **Step 2: Manually verify the two reported scenarios**

```bash
make deploy
```

Scenario A — web-owned build on a workspace WITH Telegram linked:
1. Create an agent from the web UI through to Build.
2. Assert the dry-run result appears **in the browser**.
3. Assert **nothing** arrives in Telegram.
4. `grep "build_id" logs/server.log` shows start → coder returned → decision → outcome → `chat_suppressed=true`.

Scenario B — chat-owned build, web mirroring:
1. `/agent create test-mirror` in Telegram; drive it to Build there.
2. Open the web agent page mid-build. Assert the read-only banner, no composer,
   and that Cancel navigates away without killing the build.
3. Assert the dry-run result appears in Telegram **and** in the web mirror.

Scenario C — workspace with NO chat platform:
1. Build an agent from the web. Assert the result (success or failure) renders in
   the browser, with the real reason on a failure rather than the generic note.

- [ ] **Step 3: Run the docs-sync skill**

Use the `docs-sync` skill. This change touches no `ROOKERY_*` variable, no
connector provider, no CLI subcommand and no packaging target, but it DOES change
`/api/v1` behaviour (`/design/state` gains `origin`; the ended-session response
becomes 409 `session_ended`) and the agent-designer architecture, so `CLAUDE.md`'s
"Unified conversational agent creation" section needs a paragraph on exclusive
session ownership, the `note` role, and the three completion signals.

- [ ] **Step 4: Commit the docs**

```bash
git add CLAUDE.md
git commit -m "docs: record agent designer session ownership"
```

- [ ] **Step 5: Push and open the PR**

```bash
git push -u origin worktree-agent-designer-session-ownership
gh pr create --title "fix(agentdesigner): route build results to the owning surface" --body "..."
```

The PR title must be a valid Conventional Commit — it becomes the squashed commit
on `main` and is what release-please reads.

---

## Self-Review

**Spec coverage**

| Spec requirement | Task |
|---|---|
| `Origin` type, fixed at creation | 1, 2 |
| Threaded on all six creation entry points | 2 |
| Non-owner `Step` refused | 3 |
| `Start`/`StartDesign` refusals name the owner | 3 |
| `/agent cancel` unconditional from chat | 3 (step 5) |
| `BuildCompleteFunc` gains origin; chat suppressed for web builds | 4 |
| `Warn` on chat send failure | 4 |
| User-facing message to `History` on both branches | 5 |
| `note` role: coder sees it, DTO drops it | 5 |
| `event: done` | 6 |
| `onError` refetch | 8 |
| 5s poll while generating | 8 |
| `designTurnResponse` on the `IsGenerating` branch | 6 |
| Honest ended-session error | 6 |
| `origin` on `/design/state` | 6 |
| Ownership-gated web cancel | 6 |
| Read-only mirror | 8 |
| `build_id` lifecycle logging | 7 |
| No schema change | all — verified in Task 9 |

No gaps.

**Deviation from the spec, recorded:** the spec's file table listed
`web/ui/src/lib/sse.ts` as needing a change. It does not — `openSSE` registers its
`done` listener unconditionally in `connect()`, so emitting the event server-side
is sufficient. The spec has been corrected.

**Type consistency:** `Origin`/`OriginWeb`/`OriginChat`/`Owns`/`Label`/`String`
are used identically in Tasks 2–8. `roleNote` (private) and `RoleNote` (exported
for `web`) are the same constant. `recordGenerationFailure`'s new parameter is
position 2 in both its definition (Task 5 step 3) and its single caller.
`BuildCompleteFunc`'s origin is position 2 in the type, in `startGeneration`, in
`main.go` and in all four `detached_build_test.go` call sites.
