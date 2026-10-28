# Skill Env Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Skill scripts learn the knowledge base root from `ROOKERY_VAULT_ROOT`, and the API engine's `bash` refuses to execute a skill environment.

**Architecture:** `skilldeps.Runner.Run` adds the variable to the env it already builds; `hostToolSet.runBash` checks the command with a pure `invokesSkillEnv(cmd) bool` before running; prompt text in `agentRuntimeBlock`, the API capability text and `skill-creator`.

**Tech Stack:** Go.

**Spec:** `docs/superpowers/specs/2026-10-28-skill-env-boundary-design.md`

## Global Constraints

- `GOTOOLCHAIN=auto` for every go command; Conventional Commits; branch `feat/skill-dependencies` (PR #393).
- Env root pattern: `.local/share/rookery/envs/<name>/` (`skilldeps.EnvsRel`).

## Review Focus

1. `ls ~/.local/share/rookery/envs/x/.tradingagents/logs/reports` must stay allowed (reading). → Task 2 test.
2. `rookery deps run --env x -- s.py` from bash must stay allowed (the contained door). → Task 2 test.
3. An empty VaultRoot must not set an empty `ROOKERY_VAULT_ROOT=`. → Task 1 test.

---

### Task 1: ROOKERY_VAULT_ROOT reaches skill scripts

**Files:** Modify `internal/skilldeps/runner.go`; Test `internal/skilldeps/runner_test.go`; Modify `internal/prompts/prompts.go` (`agentRuntimeBlock`), `internal/skilllibrary/skills/skill-creator/SKILL.md`; Test `internal/prompts/skillbuild_noexec_test.go`.

- [ ] Test `TestRunTellsTheScriptWhereTheKnowledgeBaseIs`: Run with `VaultRoot: "/kb"` → script sees `ROOKERY_VAULT_ROOT=/kb`; with `VaultRoot: ""` → variable absent.
- [ ] Run → FAIL. Implement: append `"ROOKERY_VAULT_ROOT="+req.VaultRoot` when non-empty. Run → PASS.
- [ ] Prompt test: `agentRuntimeBlock` output contains `$ROOKERY_VAULT_ROOT` and "never search for a notes/ folder". FAIL → add the line → PASS. Same sentence added to skill-creator's "Python dependencies" section.
- [ ] Commit `feat(skilldeps): tell skill scripts where the knowledge base is`.

### Task 2: bash refuses to execute a skill environment

**Files:** Create `internal/coder/skillenvguard.go`; Test `internal/coder/skillenvguard_test.go`; Modify `internal/coder/hosttools.go` (`runBash`, bash tool description); `internal/prompts/prompts.go` (`installAtRuntimeLine`).

- [ ] Test `TestInvokesSkillEnv` table: blocked — `~/.local/share/rookery/envs/x/bin/python s.py`, `/h/.local/share/rookery/envs/x/bin/python3 -m cli.main`, `$HOME/.local/share/rookery/envs/x/bin/pip install y`, `source ~/.local/share/rookery/envs/x/bin/activate`, `PYTHONPATH=~/.local/share/rookery/envs/x/lib/python3.14/site-packages python3 s.py`; allowed — `ls ~/.local/share/rookery/envs/x/.tradingagents/logs/reports`, `cat ~/.local/share/rookery/envs/x/rookery-env.json`, `rookery deps run --env x -- s.py`, `python3 tools/fetch.py`.
- [ ] Test `TestBashRefusesToRunASkillEnvironment`: execute bash with a blocked command → result starts `error:` and names `run_in_env`; the command was not run.
- [ ] FAIL → implement `invokesSkillEnv` (regex over `\.local/share/rookery/envs/[^/\s"']+/(bin/(python[0-9.]*|pip[0-9.]*|activate)\b|lib(64)?/python[0-9.]+/)`) and the check at the top of `runBash` → PASS.
- [ ] Update bash description and `installAtRuntimeLine` to say skill scripts run only through run_in_env.
- [ ] Commit `feat(coder): bash refuses to execute a skill environment`.

### Task 3: Docs, push, merge, deploy

- [ ] CLAUDE.md "Skill dependencies": both fixes + residual; website `reference/cli.md` deps run line mentions `ROOKERY_VAULT_ROOT`.
- [ ] Targeted checks: `make ci-fmt ci-vet ci-docs`, touched package tests, GOOS=windows build.
- [ ] Push; wait for PR #393 checks; squash-merge (and rookery-web#90); verify merged on origin/main.
- [ ] Deploy origin/main locally in ~/rookery-main; assert /healthz commit. Do not merge the release PR.
