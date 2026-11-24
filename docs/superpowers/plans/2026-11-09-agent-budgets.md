# Agent Budgets and Limits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-agent token / dollar / turn limits per run, per day and per month, enforced on API-coder runs only: a per-run limit ends the run and notifies; a period limit pauses the agent until the period resets.

**Architecture:** A pure `internal/budget` package owns the limit model, period arithmetic, the decision whether a run may start, and the spec-line parser. The runner consults it before a run (refuse with a typed error), installs a mid-run usage guard on the API coder that cancels the run with a new cause (`ErrRunLimited`, mirroring #410's `ErrRunStopped`), and re-evaluates after the run to pause. State lives in a new `agent_limits` table plus pause/notice columns on `agents` and a `limited` column on `agent_runs`. Web API, agent page, Home and the designer surface it.

**Tech Stack:** Go 1.26 (`GOTOOLCHAIN=auto`), SQLite (`modernc.org/sqlite`), Echo v4, React + TanStack Query + Vitest (web/ui).

**Spec:** `docs/superpowers/specs/2026-11-09-agent-budgets-design.md`

## Global Constraints

- Enforced **only** when the coder that runs the agent `IsAPI()`. On a CLI coder limits are kept, shown greyed out, never enforced, never deleted.
- Token limits always available; **dollar limits only when the provider reports cost**: `openrouter` and `xai` (add `usage.cost_in_usd_ticks` parsing, 1 USD = 10^10 ticks). No price table, no token→dollar estimate. Unreported cost is never treated as `0`.
- Never pause via `agents.active` or by editing `agent_schedules`.
- Designer conversation, build and create-build dry run are never limited (they do not go through `agentrunner.Runner.Run`; keep it that way).
- A child run (`[CALL]`) counts against its own limits only.
- Per-run limit notice: at most one per agent per local day. Pause notice: once per pause transition.
- Paused agents are refused for every trigger (cron, cron-retry, manual, chat `/run`, `[CALL]`).
- A budgeted agent (any period limit active) never runs twice concurrently: a second start is refused as busy.
- Day/month boundaries in `profile.ScheduleZone(db, workspaceID)`'s location.
- Next migration number is **024**.
- No personal/server details in code, tests, docs or commit text (mocked data only).
- Run Go tests with `GOTOOLCHAIN=auto go test ./internal/<pkg>/ -count=1`; web/ui with `cd web/ui && npx vitest run <file>`.

## Review Focus

1. **A run that hits a limit while its provider reports no cost on a dollar-only agent** — expected: no false trip, no `$0`; run completes, notice says cost was unreported (Task 2 `TestDecideIgnoresDollarLimitsWhenCostUnreported`, Task 4 `TestRunnerDollarOnlyLimitWithUnreportedCost`).
2. **The owner turns an agent off (`active=0`) while it is budget-paused, then the pause expires** — expected: it stays off (Task 4 `TestPauseExpiryDoesNotReactivateInactiveAgent` via `ListDueSchedules`).
3. **Raising the limit on a paused agent** — expected: pause lifts immediately, next run allowed (Task 5 `TestPutLimitsClearsPause`).
4. **Month boundary in a non-UTC zone** (run at 23:30 UTC on the last day, zone UTC+2) — expected: counted in the next month (Task 2 `TestPeriodStartUsesWorkspaceZone`).
5. **Owner Stop on a run that is also over its limit** — expected: recorded as stopped, not limited; no limit notice (Task 4 `TestOwnerStopWinsOverLimit`).

---

### Task 1: Schema and DB layer

**Files:**
- Create: `migrations/024_agent_limits.up.sql`, `migrations/024_agent_limits.down.sql`
- Modify: `internal/db/models.go` (Agent, AgentRun, RunOutcome), `internal/db/repositories.go` (agent selects/scan, run selects/scan, FinishAgentRun, ListDueSchedules)
- Create: `internal/db/agent_limits.go`, Test: `internal/db/agent_limits_test.go`

**Interfaces:**
- Produces:
  - `type AgentLimits struct { AgentID string; RunTokens, RunTurns, DayTokens, MonthTokens int64; RunUSD, DayUSD, MonthUSD float64 }` (0 = unset)
  - `func (d *DB) GetAgentLimits(agentID string) (AgentLimits, error)` — zero value (AgentID set) when no row
  - `func (d *DB) SaveAgentLimits(l AgentLimits) error` — upsert; all-zero deletes the row
  - `func (d *DB) AgentSpendSince(agentID string, since time.Time) (Spend, error)`; `type Spend struct { Tokens int64; USD float64; CostUnreported bool }` — sums `total_tokens`, `cost_usd` over runs with `started_at >= since` (UTC, `2006-01-02 15:04:05`); `CostUnreported` true if any summed run has `cost_reported=0 AND total_tokens>0`
  - `func (d *DB) SetAgentBudgetPause(agentID string, until time.Time, reason string) (changed bool, err error)` — `changed` false when the same `until` was already set
  - `func (d *DB) ClearAgentBudgetPause(agentID string) error`
  - `func (d *DB) MarkLimitNoticeSent(agentID, day string) (first bool, err error)` — conditional update on `limit_notice_day <> ?`
  - `Agent.BudgetPausedUntil *time.Time`, `Agent.BudgetPauseReason string`
  - `AgentRun.Limited string`, `RunOutcome.Limited string` ("" = not limited; otherwise human reason)
- Migration 024: `CREATE TABLE agent_limits(agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE, run_tokens INTEGER NOT NULL DEFAULT 0, run_turns INTEGER NOT NULL DEFAULT 0, run_usd REAL NOT NULL DEFAULT 0, day_tokens …, day_usd …, month_tokens …, month_usd …, updated_at TEXT NOT NULL DEFAULT (datetime('now')))`; `ALTER TABLE agents ADD COLUMN budget_paused_until TEXT NOT NULL DEFAULT ''`, `budget_pause_reason TEXT NOT NULL DEFAULT ''`, `limit_notice_day TEXT NOT NULL DEFAULT ''`; `ALTER TABLE agent_runs ADD COLUMN limited TEXT NOT NULL DEFAULT ''`. Comment the why (pause is not `active`, not the schedule — cite spec). Down: drop table; leave columns (SQLite) with a comment, matching existing down migrations' style.
- `ListDueSchedules` is **not** changed: a paused agent stays in the due list so `fire` advances `next_run_at` (skipped, not queued) and the runner refuses it. Say so in a comment in `scheduler.fire`.

- [ ] **Step 1:** Write `agent_limits_test.go`: `TestSaveAndGetAgentLimits` (round-trip, all-zero deletes), `TestAgentSpendSince` (two runs before/after `since`; one with `cost_reported=0` → `CostUnreported=true`; tokens summed), `TestBudgetPauseChangedOnlyOnce` (second identical `SetAgentBudgetPause` returns `changed=false`), `TestMarkLimitNoticeSentOncePerDay`, `TestFinishAgentRunRecordsLimited` (GetAgentRun returns `Limited`), `TestDeletingAgentCascadesLimits`.
- [ ] **Step 2:** Run `GOTOOLCHAIN=auto go test ./internal/db/ -run 'Limit|Spend|BudgetPause' -count=1` → FAIL (undefined).
- [ ] **Step 3:** Migration, models, repository code; extend every agent SELECT/scan and run SELECT/scan (list + detail) with the new columns.
- [ ] **Step 4:** Run `GOTOOLCHAIN=auto go test ./internal/db/ ./migrations/ -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(db): agent limits, budget pause and limited runs`.

### Task 2: `internal/budget` — model, periods, decisions, spec line

**Files:**
- Create: `internal/budget/budget.go`, `internal/budget/specline.go`, tests `budget_test.go`, `specline_test.go`

**Interfaces:**
- Consumes: `db.AgentLimits`, `db.Spend`
- Produces:
  - `type Period string` consts `PeriodRun="run"`, `PeriodDay="day"`, `PeriodMonth="month"`
  - `func PeriodStart(p Period, now time.Time, loc *time.Location) time.Time` (day: local midnight; month: local 1st 00:00); `func PeriodEnd(p Period, now time.Time, loc *time.Location) time.Time`
  - `type Enforcement struct { Enforced bool; DollarsSupported bool }`
  - `type StartDecision struct { Allowed bool; PauseUntil time.Time; Reason string; Remaining RunCeiling }`
  - `type RunCeiling struct { Tokens int64; USD float64; Turns int }` (0 = no ceiling) — `min(run limit, day remaining, month remaining)` per unit
  - `func DecideStart(l db.AgentLimits, day, month db.Spend, e Enforcement, now time.Time, loc *time.Location) StartDecision` — month checked before day (longer pause wins); dollar limits ignored when `!e.DollarsSupported` or the period's spend has `CostUnreported`; `Allowed=true` with empty ceiling when `!e.Enforced`
  - `func (c RunCeiling) Exceeded(u llm.Usage, turns int, dollars bool) (bool, string)` — reason like `"per-run limit of 50,000 tokens"`; USD checked only when `dollars && u.CostReported`
  - `func HasPeriodLimit(l db.AgentLimits) bool`
  - `func FormatPauseReason(p Period, limit string) string` → `"monthly limit of $30.00 reached"`
  - `func ParseSpecLine(v string) (l db.AgentLimits, set bool, ok bool)` — value of `Limits:`; `"none"` → zero, set=true; `"unchanged"`/empty → set=false; grammar `per run: 50000 tokens, 20 turns, $0.50; per day: 500k tokens, $2; per month: $30` (case-insensitive, `k`/`m` suffixes, thousands separators, `$` amounts); ok=false on any unrecognised item (caller ignores the whole line)
  - `func FormatSpecLine(l db.AgentLimits) string` (inverse; `"none"` when zero)

- [ ] **Step 1:** Tests: `TestPeriodStartUsesWorkspaceZone` (2026-11-30 23:30 UTC, loc UTC+2 → month start 2026-12-01 00:00 +02:00), `TestDecideStartPausesOnMonthBeforeDay`, `TestDecideStartRemainingIsMinimum` (run 50k, day remaining 10k → ceiling 10k), `TestDecideIgnoresDollarLimitsWhenCostUnreported`, `TestDecideNotEnforcedAllowsAll`, `TestCeilingExceededTurns`, `TestParseSpecLineRoundTrip` (table: none, unchanged, full grammar, `500k`, `1,000,000`, garbage → ok=false), `TestFormatPauseReason`.
- [ ] **Step 2:** Run `GOTOOLCHAIN=auto go test ./internal/budget/ -count=1` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Same command → PASS.
- [ ] **Step 5:** Commit `feat(budget): limit model, periods and start decisions`.

### Task 3: Coder — usage guard, cost capability, xAI cost

**Files:**
- Modify: `internal/coder/coder.go` (field + option), `internal/coder/api_engine.go` (call guard after each `addUsage` in `runToolLoop` and `graceTurnOnBudgetExhausted`), `internal/coder/api_config.go` (accessor), `internal/coder/detect.go` (`ReportsCost` field; true for `openrouter`, `xai`), `internal/llm/openai.go` (parse `cost_in_usd_ticks`)
- Test: `internal/coder/usageguard_test.go`, `internal/llm/openai_cost_test.go`

**Interfaces:**
- Produces:
  - `func (c *Coder) WithUsageGuard(f func(delta llm.Usage)) *Coder` — called once per provider completion with that completion's usage; after the call, if `ctx.Err() != nil` the loop returns `cancelledResult(ctx, total, trace)` **before executing the response's tool calls**
  - `func (c *Coder) APIProviderName() string` ("" for CLI)
  - `func ProviderReportsCost(name string) bool`
  - `openAIResponse.Usage.CostInUSDTicks *int64 json:"cost_in_usd_ticks"` → `Cost = ticks/1e10`, `CostReported=true` (only if `cost` absent)

- [ ] **Step 1:** Tests: `TestUsageGuardSeesEveryCompletion` (fake provider, 3 completions with tool calls then final → guard called 3×), `TestUsageGuardCancelStopsBeforeToolExecution` (guard cancels on 1st; assert the tool was not executed and result carries usage), `TestXAICostTicks` (`{"usage":{"total_tokens":10,"cost_in_usd_ticks":25000000}}` → Cost 0.0025, reported), `TestProviderReportsCost` (openrouter, xai true; openai, anthropic false). Use existing fake-provider helpers in `api_engine_test.go`.
- [ ] **Step 2:** Run `GOTOOLCHAIN=auto go test ./internal/coder/ ./internal/llm/ -run 'UsageGuard|CostTicks|ReportsCost' -count=1` → FAIL.
- [ ] **Step 3:** Implement. The cancel itself is owned by the runner (Task 4); the engine only calls the guard and re-checks `ctx`.
- [ ] **Step 4:** Run `GOTOOLCHAIN=auto go test ./internal/coder/ ./internal/llm/ -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(coder): per-completion usage guard and cost capability`.

### Task 4: Runner enforcement

**Files:**
- Create: `internal/agentrunner/limits.go`, Test: `internal/agentrunner/limits_test.go`
- Modify: `internal/agentrunner/stop.go` (trackRun returns the cancel; `runningCount(agentID)`), `internal/agentrunner/runner.go` (`runCoderAgent`, `coderRunContext`, `outcome`, `runCoderTurns` `[CALL]` handling), `internal/scheduler/scheduler.go` (log, no notify), `web/run_tracker.go`, `internal/gateway/router.go`

**Interfaces:**
- Consumes: Task 1 DB API, Task 2 `budget.*`, Task 3 `WithUsageGuard`, `APIProviderName`, `ProviderReportsCost`
- Produces:
  - `var ErrRunLimited = errors.New("agent run limited")`; cause type `type limitCause struct{ reason string }` with `Unwrap() error { return ErrRunLimited }`
  - `type BlockedError struct { Busy bool; Until time.Time; Reason string }` (`Error()` = user-facing sentence) — returned by `Run` **before** a run row is created
  - `func (r *Runner) coderForAgent(workspaceID, agentID string) *coder.Coder` — today returns `coderForWorkspace`; the single seam multi-coder replaces. `runCoderAgent` uses it instead of `coderForWorkspace`.
  - `func (r *Runner) Enforcement(c *coder.Coder) budget.Enforcement`
  - `func (r *Runner) CheckRunnable(agent *db.Agent) error` — exported for web pre-check; returns `*BlockedError` or nil
  - `coderRunContext.limited string`, recorded via `outcome()` → `RunOutcome.Limited`

Behaviour in `runCoderAgent`, in order:
1. After resolving the coder, compute enforcement. If enforced: if `agent.BudgetPausedUntil` is in the future → `*BlockedError`. Load limits + day/month spend; `DecideStart`; if not allowed → `SetAgentBudgetPause`; if `changed`, send pause notice (SendOutput + `recordInbox(..., "error")`); return `*BlockedError`. If `HasPeriodLimit` and `runningCount(agentID) > 0` → `*BlockedError{Busy:true}` (checked before `trackRun` registers this run).
2. If ceiling non-zero, install `WithUsageGuard`: accumulate deltas + count turns; when `ceiling.Exceeded` → `cancel(limitCause{reason})`.
3. After the run: `rctx.stopped` as today; else `rctx.limited = reason` when `errors.As(context.Cause(ctx), &limitCause)`. Stopped wins.
4. Limited path (before the generic `runErr` path): `FinishAgentRun` with `exit -1`, transcript closing line `"⛔ Ended: reached its <reason>."`, `OnProgress` same line; notice `"⛔ <reason> reached — this run was ended. Raise or remove the limit on the agent's page if it needed more."` sent only when `MarkLimitNoticeSent(agentID, localDay)` returns first; return `ErrRunLimited` (wrapped with reason).
5. After any finished enforced run (success, failed or limited): re-run `DecideStart` with fresh spend; if now over a period limit → `SetAgentBudgetPause` + pause notice on change.
- Pause notice text: `"⏸ Paused until <Mon 2 Jan 15:04> — <FormatPauseReason>. Scheduled and manual runs are blocked until then; raise or remove the limit on the agent's page to resume now."`
- `[CALL]` child returning `*BlockedError` → parent warning `"skipping [CALL: x]: <reason>"`.
- Callers: scheduler logs `BlockedError`/`ErrRunLimited` at Info without notifying; `run_tracker` shows the BlockedError sentence on the stream and does not treat `ErrRunLimited` as a failure line (the notice line is already there); router replies with the BlockedError sentence, and for `ErrRunLimited` behaves like a delivered run.

- [ ] **Step 1:** Tests in `limits_test.go` using the existing runner test harness (fake API coder that reports configurable usage per completion): `TestRunnerEndsRunAtPerRunTokenLimit` (run row `Limited` non-empty, exit -1, one inbox row), `TestLimitNoticeOncePerDay` (two limited runs → one inbox notice), `TestRunnerPausesAfterMonthlySpend` (pause set, notice sent once), `TestPausedAgentRefusedWithoutRunRow`, `TestBusyBudgetedAgentRefused`, `TestCLICoderIgnoresLimits`, `TestOwnerStopWinsOverLimit`, `TestRunnerDollarOnlyLimitWithUnreportedCost`, `TestPauseExpiryDoesNotReactivateInactiveAgent` (expired pause + `active=0` → not in `ListDueSchedules`), `TestCallChildPausedIsSkippedWithWarning`.
- [ ] **Step 2:** Run `GOTOOLCHAIN=auto go test ./internal/agentrunner/ -run 'Limit|Pause|Busy|CLICoderIgnores|OwnerStopWins|CallChildPaused' -count=1` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Run `GOTOOLCHAIN=auto go test ./internal/agentrunner/ ./internal/scheduler/ ./internal/gateway/ -count=1` → PASS.
- [ ] **Step 5:** Commit `feat(agentrunner): enforce per-run and period limits`.

### Task 5: Web API

**Files:**
- Create: `web/api_agent_limits.go`, Test: `web/api_agent_limits_test.go`
- Modify: `web/api_agents.go` (routes, `toAPIRun` limited status + field, agent detail `limits` block, `apiRunAgent` pre-check), `web/api_dashboard.go`, `web/api_parity_test.go`

**Interfaces:**
- `GET /api/v1/agents/:id/limits` and `PUT /api/v1/agents/:id/limits` → body/response:
  `{"run":{"tokens":0,"turns":0,"usd":0},"day":{"tokens":0,"usd":0},"month":{"tokens":0,"usd":0},"enforced":true,"dollars_supported":false,"spend":{"day":{"tokens":0,"usd":0,"usd_reported":true},"month":{…}},"paused_until":null,"pause_reason":""}`
  PUT validates non-negative, `turns` ≤ 149; saves; **clears the pause**; returns the GET shape.
- Agent detail response gains the same object under `"limits"` (one shape, one builder func `s.agentLimitsDTO(ctx, agent, workspaceID)`).
- `toAPIRun`: `"limited": r.Limited`; status `"limited"` when `r.Limited != ""` and not stopped.
- `apiRunAgent`: `s.runner.CheckRunnable(agent)` → `409 {"error":"budget_paused"|"busy","message":…}`.
- Dashboard: `"paused_agents":[{"agent_id","agent_name","until","reason"}]` (never null); recent runs status `"limited"`.

- [ ] **Step 1:** Tests: `TestGetLimitsShape` (raw bytes contain `"paused_until":null`, `"spend"`), `TestPutLimitsClearsPause`, `TestPutLimitsRejectsNegativeAndTurnsOver149`, `TestRunAgentReturns409WhenPaused`, `TestRunListShowsLimitedStatus`, `TestDashboardPausedAgentsNeverNull` (raw bytes `"paused_agents":[]`); add the two routes to the parity inventory.
- [ ] **Step 2:** Run `GOTOOLCHAIN=auto go test ./web/ -run 'Limits|409WhenPaused|LimitedStatus|PausedAgents|Parity' -count=1` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** Same → PASS.
- [ ] **Step 5:** Commit `feat(web): agent limits API, limited runs and paused agents`.

### Task 6: Agent page and Home UI

**Files:**
- Create: `web/ui/src/pages/agents/LimitsCard.tsx`, `web/ui/src/pages/agents/limits.test.tsx`
- Modify: `web/ui/src/lib/agents.ts` (types + `useSaveAgentLimits`), `web/ui/src/pages/agents/AgentDetailPage.tsx` (card after ScheduleCard; paused banner under the header; Run button disabled with tooltip when paused), `web/ui/src/pages/agents/RunPanel.tsx`/`StatusChip.tsx` ("Limited" chip, warn colour, tooltip = reason), `web/ui/src/lib/home.ts`, `web/ui/src/pages/home/cards.tsx` (`AgentsAtAGlanceCard` takes `paused`, shows a "Paused" pill), `web/ui/src/pages/home/HomePage.tsx` (`NeedsAttentionCard` lists paused agents and the latest limited run per agent)

**UI rules:** card title "Limits"; three rows Per run / Per day / Per month with Tokens (and Turns on per run) inputs; Dollars input rendered only when `dollars_supported`, else a muted line "Dollar limits need a provider that reports cost (OpenRouter, xAI)."; when `!enforced`, the whole card is disabled with "Limits apply only when this agent runs on an API coder. They are kept and take effect again if it does."; empty input = no limit; spend shown under day/month rows ("12,300 tokens · $0.42 today"; omit $ when not reported). Banner: "Paused — monthly limit of $30.00 reached. Resumes Mon 1 Dec 00:00." with a "Change limits" link scrolling to the card. Follow design-system rules in CLAUDE.md (tokens, icons from `lib/entityIcons.tsx`, buttons with icons, no `text-[px]`).

- [ ] **Step 1:** Tests in `limits.test.tsx`: renders inputs from detail; empty input posts 0; dollars hidden when unsupported; disabled card when not enforced; banner shows when `paused_until` set; Run button disabled when paused; StatusChip renders "Limited". Home: Needs attention lists a paused agent; glance card shows "Paused".
- [ ] **Step 2:** `cd web/ui && npx vitest run src/pages/agents/limits.test.tsx src/pages/home` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `cd web/ui && npx vitest run && npx tsc -b && npx oxlint` → PASS.
- [ ] **Step 5:** Commit `feat(web/ui): limits card, paused banner, limited chip and Home flags`.

### Task 7: Designer — ask, suggest, save

**Files:**
- Modify: `internal/prompts/prompts.go` (`DesignSystemParams.LimitsAvailable bool`, `LimitsDollars bool`; question 6 in the design checklist + `Limits:` spec line, only when `LimitsAvailable`; allow the word "token" in the jargon rules; edit variant: write `Limits: unchanged` unless the owner asked to change them), `internal/agentdesigner/flow.go` (pass params from the workspace coder: `IsAPI()` + `ProviderReportsCost`; on save call `persistLimits`), Create `internal/agentdesigner/limits.go` (`latestSpecInHistory(history []db.ChatMessage) string` scanning backwards over assistant turns; `func (f *Flow) persistLimits(agentID string, history []db.ChatMessage)` using `budget.ParseSpecLine`, applying only when `set && ok`), `web/ui/src/components/designer/SpecPanel.tsx` (add `"Limits"` to `TECHNICAL_SPEC_FIELDS`)
- Test: `internal/prompts/limits_test.go`, `internal/agentdesigner/limits_test.go`, extend SpecPanel test

- [ ] **Step 1:** Tests: `TestDesignPromptAsksLimitsOnlyWhenAvailable`, `TestDesignPromptOffersDollarsOnlyWhenSupported`, `TestEditPromptDefaultsLimitsUnchanged`, `TestPersistLimitsFromLatestSpec` (review turn after spec does not hide it), `TestPersistLimitsIgnoresUnchangedAndGarbage`, `TestPersistLimitsNoneClears`; SpecPanel parses a `Limits:` line.
- [ ] **Step 2:** Run `GOTOOLCHAIN=auto go test ./internal/prompts/ ./internal/agentdesigner/ -run Limits -count=1` → FAIL.
- [ ] **Step 3:** Implement; call `persistLimits` in both `saveAndFinish` and `updateAndFinish` beside `persistIrreversible`.
- [ ] **Step 4:** Run `GOTOOLCHAIN=auto go test ./internal/prompts/ ./internal/agentdesigner/ -count=1` and `cd web/ui && npx vitest run src/components/designer` → PASS.
- [ ] **Step 5:** Commit `feat(agentdesigner): ask for and save agent limits`.

### Task 8: Documentation and checks

**Files:** `CLAUDE.md` (new "Agent budgets and limits" section: the anti-goals, the cause-based limit, API-only, xAI ticks, period zone; migration list entry 024; route groups), `README.md` if it lists features/routes, `~/rookery-web` docs via the `docs-sync` skill.

- [ ] **Step 1:** Use the `docs-sync` skill; update every surface it names.
- [ ] **Step 2:** Run `make ci-fmt ci-vet ci-docs` and `GOTOOLCHAIN=auto go test ./internal/... ./web/ -count=1 -timeout 900s` → PASS.
- [ ] **Step 3:** Commit `docs: agent budgets and limits`.
