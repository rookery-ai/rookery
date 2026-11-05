# Agent Run Stop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner stop a running agent from the details page, list page and chat apps, leaving its work as it is.

**Architecture:** A cancel registry in `agentrunner.Runner`, cancelling with the cause `ErrRunStopped`; a `stopped` column on `agent_runs`; a generic prompt line for the next run; a web route, a chat subcommand and UI buttons on top.

**Tech Stack:** Go, SQLite migrations, Echo, React + TanStack Query, vitest.

**Spec:** `docs/superpowers/specs/2026-11-05-agent-run-stop-design.md`

## Global Constraints

- Go commands use `GOTOOLCHAIN=auto`. Vitest runs from `web/ui`.
- Stopped = cause `ErrRunStopped` set AND `runErr != nil`.
- Fixtures use mocked data only; no real server, agent or service names.
- No prompt text outside `internal/prompts`.

## Review Focus

- Parent stopped while a child runs: parent is recorded stopped, not OK (Task 2).
- Stop pressed as a run finishes: normal result, nothing misreported (Task 2).
- Server shutdown during a run: stays interrupted and retried, not stopped (Task 2).
- Another workspace's agent: 404 on web, "not running" in chat (Tasks 2, 3).
- `/agent stop` during an active design session still reaches the command (Task 4).

---

### Task 1: Record a stopped run

**Files:** `migrations/023_agent_runs_stopped.{up,down}.sql`, `internal/db/models.go`, `internal/db/repositories.go`, `internal/db/runs_test.go`, `internal/vault/reflect.go`, `internal/vault/reflect_test.go`, `internal/prompts/prompts.go`, a new prompts test.

**Produces:** `AgentRun.Stopped`, `RunOutcome.Stopped`, `DB.LatestRunStopped(agentID string) (bool, error)`, `vault.RunNote.Stopped`, `CoderPromptParams.PreviousRunStopped`.

- [ ] Tests: `TestFinishAgentRunRecordsStopped` (visible through `GetAgentRun`, `ListAgentRuns`, `RecentAgentRunsWithNames`); `TestLatestRunStopped` (no runs false; stopped true; a newer normal run false; same-second tie resolved by rowid); `TestStoppedRunNote` (status `stopped`, "## Stopped" is the last section, chat lines not under "Output sent to user"); `TestPreviousRunStoppedBlock` (present only when set, no time or step detail).
- [ ] Run them; expect compile failure.
- [ ] Implement all of the above.
- [ ] Run `go test ./internal/db/ ./internal/vault/ ./internal/prompts/`; expect PASS.
- [ ] Commit `feat(agentrunner): record stopped runs`.

### Task 2: Stop in the runner

**Files:** create `internal/agentrunner/stop.go`, `internal/agentrunner/stop_test.go`; modify `internal/agentrunner/runner.go`, `internal/coder/api_engine.go`, `internal/scheduler/scheduler.go`, `web/run_tracker.go`.

**Produces:** `agentrunner.ErrRunStopped`; `(*Runner).StopAgent(workspaceID, agentID string) bool`; `(*Runner).StopAgentByName(workspaceID, name string) bool`; `(*Runner).RunningAgentNames(workspaceID string) []string` (top-level runs only, sorted, unique).

- [ ] Tests, driving a fake coder as existing runner tests do: stop mid-run records `stopped`, returns `ErrRunStopped`, sends no `SendOutput`, no inbox row; notice is the transcript's last event; parent stopped during child records both stopped; late stop keeps normal result; parent ctx cancelled (shutdown) is not stopped; other workspace's stop returns false; next run's prompt carries the block, the one after does not; usage from completed API turns survives.
- [ ] Run; expect failure.
- [ ] Implement. On API-engine cancel, return a partial `Result` carrying `Usage` and `ToolTrace` with the error, and have `runCoderTurns` accumulate them before returning. After a child returns, return `ctx.Err()` if the run's context is done.
- [ ] Web tracker: no `⚠️` line for `ErrRunStopped`. Scheduler: info log.
- [ ] Run `go test ./internal/agentrunner/ ./internal/coder/ ./internal/scheduler/`; expect PASS.
- [ ] Commit `feat(agentrunner): stop a running agent`.

### Task 3: Web route and status

**Files:** `web/api_agents.go`, `web/api_dashboard.go`, `web/api_parity_test.go`, a new web test.

- [ ] Tests: `POST /agents/:id/run/stop` 204 while running, 404 otherwise and for another workspace's agent; `toAPIRun` and `toAPIDashboardRun` report `stopped` over `failed`; parity table includes the route.
- [ ] Implement; `runner == nil` returns 404 as nothing can be running.
- [ ] Run `go test ./web/ -run 'Stop|Parity|Dashboard'`; expect PASS.
- [ ] Commit `feat(web): stop endpoint and stopped run status`.

### Task 4: Chat command

**Files:** `internal/gateway/router.go`, `internal/gateway/commands.go`, `internal/gateway/slack.go`, `cmd/rookery/main.go`, gateway tests.

**Consumes:** runner methods via a narrow `RunStopper` interface set with `(*Router).WithRunStopper`.

- [ ] Tests: all `/agent stop` branches (named running, named idle, none, one, several); works with an active design session; `/run` that returns `ErrRunStopped` reports stopped and never "finished"; Slack event loop keeps reading while a handler blocks.
- [ ] Implement; check where Slack acks before moving `handleEvent` to a goroutine.
- [ ] Run `go test ./internal/gateway/`; expect PASS.
- [ ] Commit `feat(gateway): add /agent stop`.

### Task 5: UI

**Files:** `web/ui/src/lib/agents.ts`, `lib/home.ts`, `pages/agents/RunPanel.tsx`, `AgentsPage.tsx`, `AgentDetailPage.tsx`, `pages/home/cards.tsx`, tests beside them.

- [ ] Tests: RunPanel shows Stop for a run with no stream and posts to the route; a 404 is not an error; list card shows Stop only when running, clicking it does not navigate, and it is not a descendant of the link; run row shows Stopped; `statusDot` and Needs attention ignore stopped.
- [ ] Implement `stopRun` in `useAgentActions` (404 resolves false, always invalidates) and `refetchInterval` of 5000 ms while running on `useAgents` and `useAgent`.
- [ ] Run `make ci-ui`; expect PASS.
- [ ] Commit `feat(web/ui): stop button for running agents`.

### Task 6: Documentation and PR

- [ ] Use the `docs-sync` skill; update `CLAUDE.md`, `README.md` if affected, and the website repository.
- [ ] Run `make ci-fmt ci-vet ci-docs`, `go test ./internal/agentrunner/ ./internal/gateway/ ./internal/db/ ./web/`.
- [ ] Exercise on a real server from this branch on a spare port: start a looping manual run, press Stop, confirm the log, next-run prompt and UI.
- [ ] Grep the diff for local setup details, push, open a PR titled `feat(agents): stop a running agent`.
