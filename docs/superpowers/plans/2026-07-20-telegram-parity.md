# Telegram Parity Implementation Plan (SP9)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the two recorded platform-parity gaps — no `/skill` command, and `/remind` with only create wired — so Telegram (and Discord, and Slack) reach the same surface as the web UI.

**Architecture:** All work is in `internal/gateway/router.go` plus its wiring in `cmd/simple-agents/main.go`. No new packages, no schema changes, no new endpoints. `internal/skilldesigner.Flow` and the `db.*Reminder` helpers are consumed as-is — SP9 adds no methods to either.

**Tech Stack:** Go. Tests: `go test ./internal/gateway/... -count=1`.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-07-20-telegram-parity-design.md`. It governs; this plan implements it.
- **Baseline: all Go tests green.** `go test ./... -count=1 -timeout 120s`. Do not regress any.
- **Every behaviour must be pinned by a test that fails without its implementation.** Red-verify each one: write it, watch it fail for the *stated* reason, then implement. A test that passes before the implementation is not a test.
- **At most one conversational design session per workspace** (agent OR skill). This is the spec's central decision — do not weaken it to a priority order.
- **No input that creates a reminder today may stop creating one.** Subcommand matching is exact.
- `Router` is platform-neutral. Nothing in this plan may reference Telegram specifically in a way that breaks Discord or Slack.
- Match the file's existing conventions: `switch sub` handlers, `send(...)` for user-facing text, CommonMark (the render subsystem converts per platform — never emit MarkdownV2 escapes by hand).

---

### Task 1: Flow-aware pending-cancel

Do this first. It is a refactor of existing behaviour with no user-visible change, and both later tasks build on it. Landing it separately keeps the risky part isolated and reviewable.

**Files:**
- Modify: `internal/gateway/router.go`
- Test: `internal/gateway/router_test.go` (or the existing test file for this package — check what's there first)

- [ ] **Step 1: Write the failing test**

Assert that a pending cancel choice records which flow raised it, and that resolving it touches only that flow. The sharpest form is the data-loss case from the spec:

```
given an agent draft exists AND a skill cancel is pending
when the user replies "discard"
then the skill draft is dismissed AND the agent draft still exists
```

- [ ] **Step 2: Run to verify failure.** Expected: FAIL — `pendingCancel` is a `map[string]bool` and `resolveCancelChoice` calls `designFlow` unconditionally, so the agent draft is destroyed.

Confirm the failure message names the surviving-agent-draft assertion. If it fails for a compile error instead, that is fine — but read it, don't assume.

- [ ] **Step 3: Make `pendingCancel` carry the flow kind**

Change `map[string]bool` to a map carrying which flow is pending (a small named type or a string kind — match how the file models similar state; `secretChallenge` is the local precedent for a struct-valued pending map). `resolveCancelChoice` dispatches on it.

- [ ] **Step 4: Run all.** `go test ./internal/gateway/... -count=1`, then the full suite.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor(gateway): make pending-cancel choice flow-aware"
```

---

### Task 2: `/remind list` and `/remind delete <n>`

Independent of Tasks 1 and 3. Mirrors `handleMemory`.

**Files:**
- Modify: `internal/gateway/router.go` (`handleRemind`, `helpText`)
- Test: the gateway package's test file

- [ ] **Step 1: Write the failing tests**

Three behaviours, three tests:

1. `/remind list` with two reminders → both appear, numbered, newest ordering matching `db.ListReminders`, times rendered in the workspace timezone.
2. `/remind delete 1` → the first *listed* reminder is gone; the reply names it.
3. **The fall-through guard:** `/remind list the groceries` still *creates* a reminder and does not list. Same for `/remind delete the old note`.

Test 3 is the one that matters most — it is the regression this task could plausibly cause.

- [ ] **Step 2: Run to verify failure.** Expected: 1 and 2 FAIL (no such subcommands — they fall into the time parser); 3 PASSES already. Note that: test 3 is a *characterization* test locking in current behaviour, so it passing now is correct and expected. Do not "fix" it.

- [ ] **Step 3: Implement**

At the top of `handleRemind`, after the empty-arg usage check, match exactly:

- argument is exactly `list` → list branch
- argument is `delete` followed by exactly one integer → delete branch
- otherwise → fall through to the existing logic, untouched

Use `db.ListReminders` / `db.DeleteReminder`. Index into the *same* slice the list rendered so the numbers agree, exactly as `handleMemory` does. Render times with `profile.LoadLocation(r.db, msg.WorkspaceID)` — `handleRemind` already loads it.

Empty list gets a real message, not an empty bullet list. Out-of-range `delete` reports how many exist.

- [ ] **Step 4: Update `helpText()`** with both new forms, matching the surrounding entries' voice.

- [ ] **Step 5: Run all + commit**

```bash
git add -A && git commit -m "feat(gateway): /remind list and /remind delete"
```

---

### Task 3: `/skill` command route

Needs Task 1.

**Files:**
- Modify: `internal/gateway/router.go` (struct field, `NewRouter` or a `With…` setter, `Handle` dispatch, new `handleSkill`, `handleText`, `helpText`)
- Modify: `cmd/simple-agents/main.go` (pass the skill flow into the router)
- Test: the gateway package's test file

- [ ] **Step 1: Decide how the flow is injected — read before writing**

`NewRouter` already takes five positional arguments. Adding a sixth makes an unreadable call site. The file already uses `With…` setters (`WithTimeParserFallback`). **Use a `WithSkillFlow` setter**, and keep `skillFlow == nil` meaning "skill creation unavailable" — exactly how `designFlow == nil` is handled today, so an operator running without it gets a clear message, not a panic.

Note the import direction before you start: `internal/gateway` importing `internal/skilldesigner` must not create a cycle. Verify with `go build ./...` early rather than discovering it at the end.

- [ ] **Step 2: Write the failing tests**

1. `/skill list` → the workspace's skills, plus core skills, in the shape `handleAgent`'s list uses.
2. `/skill create <name>` → starts a session; a following plain-text message routes to `skillFlow.Step`, not to one-off chat.
3. **Mutual exclusion:** with an agent design session live, `/skill create x` is refused, the refusal names the live agent session, and the agent session survives. And the mirror: with a skill session live, `/agent create y` is refused.
4. `StateDescribing` is actually entered by `/skill create <name>` and the next message advances out of it. (Per the spec this state has never executed — test it as new code.)
5. `skillFlow == nil` → a clear "not available" message, no panic.

- [ ] **Step 3: Run to verify failure.** Expected: all FAIL — `/skill` is an unknown command today.

- [ ] **Step 4: Implement `handleSkill`**

Mirror `handleAgent`'s structure closely — same `parts`/`sub`/`rest` split, same `switch sub`, same nil-flow guard, same draft-resume offer via `HasDraft` → `OfferDraftResume`. Subcommands: `list`, `create <name>`, `cancel`, default usage line.

For `create`, put the new session in `StateDescribing` so the user is asked what the skill should do before generation starts — that is what the state exists for. Do not call `StartDesign` directly from the command handler with a canned first message; that skips the description turn and is what the web path does because the web form collects the description up front.

- [ ] **Step 5: Wire dispatch, text routing, and mutual exclusion**

- `Handle`: add `case "skill"`.
- `handleText`: route to `skillFlow.Step` when a skill session is live, alongside the existing agent branch. Register `SetProgressHandler` for the skill build the same way the agent branch does for `StateDesigning`, so the placeholder streams instead of looking frozen.
- Both `create` paths check the *other* flow's `GetSession` first and refuse per the spec's message shape.

- [ ] **Step 6: Wire `cmd/simple-agents/main.go`**

The skill flow is already constructed there for the web layer — pass the same instance to the router via `WithSkillFlow`. One flow instance, not two: two would each hold their own session map and the mutual exclusion would not hold.

- [ ] **Step 7: Update `helpText()`** with the `/skill` forms.

- [ ] **Step 8: Run all + commit**

```bash
go build ./... && go test ./... -count=1 -timeout 120s
git add -A && git commit -m "feat(gateway): /skill command — skill creator on chat platforms"
```

---

### Task 4: Verification sweep and docs

**Files:**
- Modify: `CLAUDE.md` (the Known gaps entries this closes, and the router's command list)
- Test: full suites

- [ ] **Step 1: Full verification**

```bash
go build ./... && go test ./... -count=1 -timeout 120s
```

- [ ] **Step 2: Update `CLAUDE.md`**

Two edits, both required:

- The inbound-message-pipeline block lists the router's commands — add `/skill` and the `/remind` subcommands.
- **Known gaps:** remove the "Skill creator via Telegram" and "`/remind` list/delete via Telegram" entries. They are the whole point of this sub-plan; leaving them would misrepresent the codebase to the next session. If `/skill edit` and skill import remain out of scope, say so in their place rather than deleting the entry outright — the gap narrowed, it did not vanish.

Match the document's existing density and voice. No changelog entries.

- [ ] **Step 3: Manual-check list for the operator**

Write `docs/superpowers/sp9-smoke.md`: link a Telegram account, run `/skill create`, describe a trivial skill, watch progress stream, approve, confirm it appears in the web skills list; `/skill cancel` mid-session with an agent draft present, confirm the agent draft survives; `/remind list` and `/remind delete 1`; `/remind list the groceries` still creates a reminder.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "docs: SP9 telegram-parity close-out"
```

---

## Notes for the executing agent

**Two things verified before this plan was written — do not re-derive them:**

1. **There is no `router_test.go`.** `internal/gateway` has tests for credspec, discord, msgid,
   registry and slack — the 945-line router that dispatches every chat command has none. Every test
   in this plan lands in a new file. That is a bonus, not a blocker: SP9 gives the router its first
   coverage. Use the established `db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")`
   pattern (see `internal/db/*_test.go`) — a real temp DB, not a mock.

2. **Both design flows start without calling a coder.** `agentdesigner.Flow.Start` creates the
   session in `StateDescribing` and returns a canned "describe what this should do" prompt — no
   subprocess, no LLM. So every mutual-exclusion test is cheap and deterministic: start a session,
   assert the other command is refused. No coder stub needed.

   This also *confirms the Task 3 Step 4 decision independently*: the agent flow's chat entry point
   already does exactly what the spec asks the skill flow to do. Parity here is literal, not
   analogous — `handleSkill` should read almost line-for-line like `handleAgent`.

- Task order is 1 → (2 ‖ 3) → 4. Task 2 is fully independent of 1 and 3 and can go in parallel.
- The spec's mutual-exclusion decision is the load-bearing one. If while implementing you find a case it handles badly, say so in your report and stop — do not quietly downgrade it to a priority order.
- `StateDescribing` has never executed. Treat every line it touches as new code: if it does something surprising, the bug is probably there and not in your wiring.
- Where this plan names a helper or type, it is a starting point, not a contract — match the surrounding code's conventions if they differ.
