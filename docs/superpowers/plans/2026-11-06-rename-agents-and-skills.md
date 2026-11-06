# Rename Agents and Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename an agent or user skill from its page, updating every structural reference and leaving prose alone.

**Architecture:** A new `internal/rename` package holds pure rewriters and a two-phase service: prepare (read, plan edits, report who refers to the name), then apply. `internal/db` owns the transactions. Thin `web` handlers run the refusal checks. One shared SPA dialog.

**Tech Stack:** Go, SQLite, Echo, React, TanStack Query.

**Spec:** `docs/superpowers/specs/2026-11-06-rename-agents-and-skills-design.md`

## Global Constraints

- Agent name: trimmed, 1-120 characters, no control characters, none of `[ ] | #`; collisions checked case-insensitively, excluding itself.
- Skill name: `skillstore.SanitizeName` plus the existing `validSkillName` rule.
- Files: atomic per file, mode preserved, only changed files written; skip non-UTF-8, symlinks and files over 5 MiB.
- `still_mentions` capped at 20 and, like `warnings`, always an array.
- Public repository: fixtures use invented names only.
- Run Go with `GOTOOLCHAIN=auto`.

## Review Focus

1. A wikilink whose target resolves to a real note sharing the old name: it must not be rewritten.
2. A one-word skill name such as `notes`: `notes/` paths and the word in sentences must survive.
3. `agent_skills` already holding a dangling row with the new name: rename must not hit the primary key.
4. A case-only agent rename (`price watch` → `Price Watch`): allowed, not a collision.
5. A rename retried after a crash between the folder move and the commit: it must complete.

---

### Task 1: Pure rewriters

**Files:** Create `internal/rename/rewrite.go`, `internal/rename/rewrite_test.go`

**Interfaces:**
- Produces: `RewriteAgentCalls(text, old, new string) string`, `RewriteAgentTitle(text, old, new string) string`, `RewriteStateHeading`, `RewriteRunHeading`, `RewriteSkillRefs(text, old, new string) string`, `RewriteWikilinks(text, old, new string) string`, `RewriteSkillLinks(text, old, new string) string`, `ContainsToken(text, name string) bool`, `ValidateAgentName(raw string) (string, error)`.

- [ ] **Step 1: Write failing tests**, pinning: `[CALL: Price watch]` rewritten but `[CALL: price watch]` not (exact, like the runner); title only on an exact `# old` line; state heading only on line 1; run heading keeps its status and frontmatter; hyphenated `report-helper` replaced in prose but not inside `my-report-helper2`; one-word `notes` rewrites `name: notes`, `skills/notes/x`, `env="notes"`, `--env notes`, `# Skills: a, notes`, a bullet under `# Skills`, and leaves `notes/todo.md` and "my notes" alone; wikilinks keep `|alias` and `#heading`; agent names `a[b`, `a|b`, `a#b`, a newline and 121 characters refused.
- [ ] **Step 2: Run** `GOTOOLCHAIN=auto go test ./internal/rename/`. Expected: FAIL, undefined.
- [ ] **Step 3: Implement.** Build each regexp from `regexp.QuoteMeta`; use `ReplaceAllLiteral`-style replacement so a `$` in a name is inert.
- [ ] **Step 4: Run tests.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(rename): add structural reference rewriters`.

### Task 2: Database transactions

**Files:** Create `internal/db/rename.go`, `internal/db/rename_test.go`; Modify `internal/skillstore/skillstore.go` (export `ValidName(string) bool`)

**Interfaces:**
- Produces: `ErrNameTaken`; `(*DB).RenameAgent(agentID, newName string, draftAgentMD *string) error`; `(*DB).RenameSkill(workspaceID, skillID, oldName, newName, description string, draftAgentMD *string) error`. A non-nil `draftAgentMD` replaces the workspace's `agent_drafts.pending_agent_md`.

- [ ] **Step 1: Write failing tests:** `TestRenameAgentUpdatesDenormalisedLabels` (inbox, pending actions, edit draft; another agent's rows untouched); `TestRenameAgentCollisionIsErrNameTaken`; `TestRenameSkillMovesAttachments`; `TestRenameSkillToleratesADanglingAttachment` (Review Focus 3: row with the new name exists, old row gone afterwards, no error); `TestRenameSkillLeavesOtherWorkspacesAttachments`.
- [ ] **Step 2: Run** `GOTOOLCHAIN=auto go test ./internal/db/ -run Rename`. Expected: FAIL.
- [ ] **Step 3: Implement** one transaction each. `UPDATE OR IGNORE agent_skills`, then `DELETE` what still has the old name, both scoped by `agent_id IN (SELECT id FROM agents WHERE workspace_id=?)`. Zero rows on the main update is `ErrNotFound`.
- [ ] **Step 4: Run tests.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(db): add agent and skill rename transactions`.

### Task 3: Rename service

**Files:** Create `internal/rename/service.go`, `internal/rename/service_test.go`; Modify `internal/vault/reflect.go` (add `RenameAgentInRuns`)

**Interfaces:**
- Consumes: Tasks 1 and 2.
- Produces: `New(d *db.DB, v *vault.Vault, homesDir string) *Service`; `(*Service).PrepareAgent(workspaceID string, a *db.Agent, newName string) (*Plan, error)`; `(*Service).PrepareSkill(workspaceID string, sk *db.Skill, rawName string) (*Plan, error)`; `Plan{NewName string; Unchanged bool; ReferencingAgentIDs []string}`; `(*Plan).Apply() (*Result, error)`; `Result{Name string; StillMentions, Warnings []string; EnvRemoved bool}`; errors `ErrInvalidName`, `db.ErrNameTaken`. `(*vault.Reflector).RenameAgentInRuns(workspaceID, agentID, newName string) error`.

- [ ] **Step 1: Write failing tests** against a temp DB and vault with invented data. `TestRenameAgentEverywhere` asserts each row of the spec's agent table, plus that the sidecar keeps its other fields and a run note's output body stays; `TestRenameAgentKeepsLinkToARealNote` (Review Focus 1); `TestCaseOnlyAgentRename` (Review Focus 4); `TestAgentRenameDoesNotCreateStateFile`; `TestStillMentionsListsProse`; `TestRenameSkillEverywhere` (folder, frontmatter, description, script mode preserved, attached agent's `AGENT.md` and tools, wikilinks, staging folder gone, environment gone and `EnvRemoved` true); `TestOneWordSkillKeepsOrdinaryWords` (Review Focus 2); `TestSkillRenameRefusesCoreAndExistingNames`; `TestSkillRenameRetriesAfterInterruptedMove` (Review Focus 5); `TestSkillRenameRollsTheFolderBackWhenCommitFails`; `TestUnchangedNameIsANoOp`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Prepare reads and computes every edit into memory. Apply order per spec: agent = commit then files; skill = move folder, commit (move back on failure), files, remove staging folder and environment. Failures after commit go to `Warnings` and `slog.Warn`. Then `Indexer().Invalidate`. `ReferencingAgentIDs` is agents whose `AGENT.md` or tools hold a reference, plus `agent_skills` for a skill. Find the environment with `skilldeps.EnvDir(filepath.Join(homesDir, workspaceID), name)`.
- [ ] **Step 4: Run tests.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(rename): add agent and skill rename service`.

### Task 4: API

**Files:** Create `web/api_rename.go`, `web/api_rename_test.go`; Modify `web/api_agents.go`, `web/api_skills.go`, `web/api_parity_test.go`, `web/server.go` if a field is needed

**Interfaces:** Consumes Task 3. Produces `PUT /api/v1/agents/:id/name` and `PUT /api/v1/skills/:id/name`, returning `{id,name,still_mentions,warnings,env_removed}`.

- [ ] **Step 1: Write failing tests:** success for each; raw bytes hold `"still_mentions":[]`, not `null`; another workspace's id is 404; bad name 400 `invalid_name`; taken name 409 `name_taken`; unfinished run 409 `agent_running`, including when a referring agent is the one running; core-skill route absent; `design_in_progress` for an open session; skill draft with the old or new name refused; audit row written.
- [ ] **Step 2: Run** `GOTOOLCHAIN=auto go test ./web/ -run Rename`. Expected: FAIL.
- [ ] **Step 3: Implement** handlers, using `isAgentRunning`, `designFlow.GetSession`, `skillFlow.GetSession` and `skillFlow.HasDraft`, each nil-guarded. Add both routes to the parity `want` table.
- [ ] **Step 4: Run tests.** Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): add rename endpoints for agents and skills`.

### Task 5: Interface

**Files:** Create `web/ui/src/components/RenameDialog.tsx`, `web/ui/src/components/RenameDialog.test.tsx`; Modify `web/ui/src/lib/agents.ts`, `web/ui/src/lib/skills.ts`, `web/ui/src/pages/agents/AgentDetailPage.tsx`, `web/ui/src/pages/skills/SkillDetailPage.tsx`, `web/ui/src/pages/skills/SkillView.tsx`

**Interfaces:** Consumes Task 4. Produces `RenameDialog` props `{open, onOpenChange, label, currentName, hint?, onRename(name: string): Promise<void>}`, plus `rename(id, name)` on `useAgentActions` and `useSkillActions`, and `onRename?: () => void` on `SkillView`.

- [ ] **Step 1: Write failing tests:** dialog prefilled; submit disabled while empty or unchanged; server refusal shown inline and dialog stays open; a successful rename closes it. `SkillView`'s Rename button is disabled while dirty and absent on a core skill.
- [ ] **Step 2: Run** `cd web/ui && npx vitest run src/components/RenameDialog.test.tsx`. Expected: FAIL.
- [ ] **Step 3: Implement.** On success invalidate `agents`, `agent`, `skills`, `skill`, `kb-tree`, `kb-note`, and the home and inbox query keys. Toast still-mentions and a removed environment.
- [ ] **Step 4: Run** `make ci-ui`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(web/ui): add rename dialog for agents and skills`.

### Task 6: Docs, deploy, PR

- [ ] **Step 1:** Add a section to `CLAUDE.md`, and use the `docs-sync` skill for the API reference on the website, in its own PR.
- [ ] **Step 2:** Run `make ci-fmt ci-vet ci-docs` and `GOTOOLCHAIN=auto go test ./internal/rename/ ./internal/db/ ./internal/vault/ ./web/ -count=1`.
- [ ] **Step 3:** Exercise the service on a copy of real-shaped data through a real browser against a scratch server. Never run `serve` on a copy of a live data directory: it would start bots and scheduled runs.
- [ ] **Step 4:** Grep diff, commit messages and PR text for local details. Push, open the PR, deploy from `~/rookery-main` with `GOTOOLCHAIN=auto make deploy`, and assert `/healthz` reports this commit.
