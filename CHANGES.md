# Changes — branch `feat/agent-edit-flow`

Date: 2026-07-09

Summary of the uncommitted work on this branch: the conversational **agent edit flow** and its
supporting draft/working-dir model, plus a set of weak-model reliability fixes for the API coder
(most notably surfacing the build engine's script-verification ground truth to the agent designer).

## Agent edit flow & draft working-dir model

- **Readable draft working dir.** Create-mode builds now run in a readable `draft_<slug>`
  directory derived from the agent's NAME instead of the opaque agent UUID, so a
  work-in-progress agent is recognizable in the KB browser.
  (`internal/agentdesigner/manifest.go` `DraftAgentDir`, `slugifyAgentName`).
- **Draft dir kept across builds; promoted on finalize.** The `draft_<name>` dir is kept through
  blocked/designing/verifying builds (not just verifying) and is promoted to the canonical
  `AgentDir(<uuid>)` only when the user finalizes. `finalizeAgent` reconstitutes the real agent
  from the captured pending content; `recoverBuiltAgentFromDisk` + `HasSaveableBuild` support
  resume and "keep it as-is". (`internal/agentdesigner/flow.go`).
- **Draft GC sweep fixed.** Nightly GC now removes the `draft_<name>` dir on draft expiry (any
  state), and never touches an edit draft's LIVE agent dir. (`cmd/simple-agents/main.go`).
- **Live design-session state API.** New `GET /dashboard/agents/design/state` returns the
  in-memory design session (`state`, `is_edit`, …) so the browser can recover an in-progress
  conversation after a reload. (`web/server.go`, `web/handlers_agents.go` `handleDesignState`;
  `Flow.Snapshot`/`IsGenerating` in `flow.go`).
- **Create/edit UI.** Dashboard conversational create/edit page updated for the above (live state
  recovery, keep-going / keep-as-is affordances). (`web/templates/dashboard/agent_new.html`).

## API-coder reliability (weak models)

- **Script-verification bridge (headline fix).** The API build engine already tracked, per
  authored `tools/*.py`, whether it RAN with real output (`producedOutput`), but that signal was
  dropped from `coder.Result`, so the agent designer re-derived "verified?" solely from a
  `[TEST_OUTPUT]` marker the weak model often forgets — falsely reporting *"I couldn't confirm the
  helper it wrote actually runs."* `coder.Result` now carries `ScriptVerified` / `ScriptOutput`
  (secret-redacted) / `ScriptRan`; `decideBuildOutcome` trusts the engine's ground truth: a
  confirmed run advances to review showing the real captured output, and the weak-backend gate
  only fires when the engine did NOT confirm a run. A `script_ran` field on the
  `build not presentable` log discriminates "ran but produced nothing" from "never ran".
  (`internal/coder/{coder,api_engine,hosttools}.go`, `internal/agentdesigner/flow.go`).
- **AGENT.md-first ordering.** The implementation prompt now tells the coder to write AGENT.md
  FIRST (before any helper script) and not to loop trying to make a build-time-blocked helper
  return live data. (`internal/prompts/prompts.go`).
- **Stray `[/CHAT]` handling.** The runner now strips a stray `[/CHAT]` close tag weak models
  sometimes emit, so it never leaks into a delivered message. (`internal/agentrunner/runner.go`).
- **Telegram build progress for every trigger.** The design progress callback is now registered
  for ANY message while Designing (approve, "keep going", "try again", "keep it as-is"), not only
  "approve", so milestones stream on every build trigger. (`internal/gateway/router.go`).
- **Empty-200 provider retry.** OpenAI-compatible providers (seen on OpenRouter) occasionally
  return `200 OK` with an empty body; the LLM transport now treats that as transient and retries
  instead of failing with an opaque JSON-parse error. (`internal/llm/provider.go`).

## Ops

- **`owner reset-password` CLI.** New subcommand to reset the single owner's password without
  login. (`cmd/simple-agents/main.go`).

## Tests & docs

- New / expanded coverage: `internal/agentdesigner/{build_outcome_test,draft_test,reconcile_blocked_test,generation_keepfiles_test}.go`,
  `internal/coder/api_engine_test.go`, `internal/agentrunner/runner_test.go`, `internal/llm/retry_test.go`.
- Design doc: `docs/agent-designer-flow.md` (+ rendered `.html`).
- `bin/simple-agents` rebuilt.
