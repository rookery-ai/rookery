# Agent run retention and per-run scratch — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep only each agent's last 15 runs, give every run a scratch folder deleted when it ends, and tell agents where scratch belongs.

**Architecture:** The runner owns both guarantees because every trigger (cron, manual, chat, `[CALL]`) passes through `runCoderAgent`. The database slims old rows rather than deleting them so spend limits stay exact; the vault deletes old run notes and sidecars. A startup pass applies both to existing installs.

**Tech Stack:** Go, SQLite (`modernc.org/sqlite`), embedded core skills.

**Spec:** `docs/superpowers/specs/2026-11-25-agent-run-retention-design.md`

## Global Constraints

- Cap: `agentrunner.RunHistoryKeep = 15`. No setting anywhere.
- Never delete or blank a row with `finished_at IS NULL`.
- Never change usage/cost columns (`AgentSpendSince` reads them).
- Never delete anything under `agents/<id>/` except `logs/run_*.md`.
- Scratch dir: `<workspace home>/tmp/run-<run id>`, mode 0700.
- Tests and fixtures use invented data only.
- Conventional commits, `fix(...)`.

## Review Focus

1. Two runs started in the same second — ordering must still keep exactly 15 (`rowid` tie-break).
2. A run that is still in progress while an older-than-15 prune happens — must stay intact.
3. An agent with fewer than 15 runs — prune is a no-op, deletes nothing.
4. A sidecar belonging to another agent, or unreadable — never deleted.
5. Coder with no scratch dir set (chat, builds) — `TMPDIR` stays `<home>/tmp`.

---

### Task 1: Database prune

**Files:** Modify `internal/db/repositories.go`; Test `internal/db/run_retention_test.go`

**Interfaces:** Produces `func (d *DB) PruneAgentRuns(agentID string, keep int) (keptIDs []string, err error)` and `func (d *DB) ListAllAgentRefs() ([]AgentRef, error)` with `type AgentRef struct{ ID, WorkspaceID string }`.

- [ ] Test `TestPruneAgentRunsKeepsNewestAndSlimsTheRest`: 20 finished runs with distinct `started_at`; `keep=15` → returns the 15 newest ids; those keep transcript/stdout/stderr; the 5 older have all three empty and unchanged `total_tokens`/`cost_usd`.
- [ ] Test `TestPruneAgentRunsLeavesInProgressAndFewer`: a 16-run agent whose oldest row has `finished_at` NULL keeps it intact; a 3-run agent is untouched.
- [ ] Test `TestPruneAgentRunsSameSecondTieBreak`: 17 runs sharing one `started_at` → exactly 15 kept, the last inserted.
- [ ] Test `TestPruneKeepsSpendExact`: `AgentSpendSince` equal before and after.
- [ ] Implement in one transaction; order `started_at DESC, rowid DESC`. Run `GOTOOLCHAIN=auto go test ./internal/db/ -run Prune`. Commit.

### Task 2: Vault prune

**Files:** Modify `internal/vault/reflect.go`; Test `internal/vault/run_retention_test.go`

**Interfaces:** Consumes kept ids from Task 1. Produces `func (r *Reflector) PruneAgentRuns(workspaceID, agentID string, keep int, keepRunIDs []string) (removed int, err error)`.

- [ ] Test: 18 `run_*.md` notes + matching sidecars, plus a sidecar of another agent and a corrupt one → newest 15 notes stay; sidecars not in `keepRunIDs` of this agent removed; foreign and corrupt sidecars stay; `AGENT.md`, `state.md`, `logs/other.txt` untouched.
- [ ] Implement: list `agents/<id>/logs`, keep the `keep` lexically-largest `run_*.md`; reuse `UnreflectAgentRuns`' read-and-check loop for sidecars (sidecar field `RunID`). Nil reflector → no-op. Run tests. Commit.

### Task 3: Scratch dir in the coder

**Files:** Modify `internal/coder/coder.go`, `internal/coder/hosttools.go`, `internal/coder/api_engine.go`; Test `internal/coder/scratch_test.go`

**Interfaces:** Produces `func (c *Coder) WithScratchDir(dir string) *Coder` and `func SweepRunScratch(homesDir string) (int, error)`.

- [ ] Tests: `buildEnv` with scratch set → `TMPDIR`,`TMP`,`TEMP` = scratch; unset → `<home>/tmp`. `runBash("echo $TMPDIR")` (sandbox off) prints the scratch dir. `SweepRunScratch` removes `*/tmp/run-*` dirs and leaves `*/tmp/keep.txt` and `*/tmp/other/`.
- [ ] Implement: `scratchDir` field copied into `hostToolSet`; a `tmpDir()` helper used by `buildEnv`, `runScript`, `runBash`. Run tests. Commit.

### Task 4: Runner wiring, prompt line, startup pass

**Files:** Modify `internal/agentrunner/runner.go`, `internal/prompts/prompts.go`, `cmd/rookery/main.go`; Create `internal/agentrunner/retention.go`; Test `internal/agentrunner/retention_test.go`, prompts test.

**Interfaces:** Consumes Tasks 1–3. Produces `const RunHistoryKeep = 15`, `func (r *Runner) pruneRunHistory(workspaceID, agentID string)`, `func (r *Runner) PruneAllRunHistory() int`.

- [ ] Test: `pruneRunHistory` on a seeded DB + vault keeps 15 of 18 in both. `PruneAllRunHistory` covers two workspaces.
- [ ] Wire: after `CreateAgentRun`, `MkdirAll` scratch, `coderSvc = coderSvc.WithScratchDir(dir)`, `defer os.RemoveAll(dir)`, `defer r.pruneRunHistory(...)` (registered so it runs after the row is finished and the note reflected).
- [ ] `SkillEnvBlock`: replace the `$TMPDIR (=<home>/tmp)` line with the run-private wording and the knowledge-base/`logs/` rule; test asserts the new sentences and that `/tmp` advice remains.
- [ ] `serve`: call `coder.SweepRunScratch(homesDir)` and `runner.PruneAllRunHistory()` before the scheduler starts; log counts.
- [ ] Run `GOTOOLCHAIN=auto go test ./internal/agentrunner/ ./internal/prompts/ ./cmd/rookery/`. Commit.

### Task 5: Core skill and docs

**Files:** Create `internal/skilllibrary/skills/run-hygiene/SKILL.md`; Modify `CLAUDE.md`, `README.md` if it counts skills, website docs via the docs-sync skill.

- [ ] Write SKILL.md (frontmatter `name: run-hygiene`, a description carrying triggers; body per spec). No `scripts/`.
- [ ] Update skill counts and add a "Run retention" paragraph to CLAUDE.md.
- [ ] Run `GOTOOLCHAIN=auto go test ./internal/skilllibrary/` and `make ci-docs`. Commit.
