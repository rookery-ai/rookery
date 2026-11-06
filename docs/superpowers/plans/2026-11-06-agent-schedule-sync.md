# Agent schedule sync: implementation plan

Spec: `docs/superpowers/specs/2026-11-06-agent-schedule-sync-design.md`

1. **Pure helpers** (`internal/agentdesigner/schedulesync.go`, TDD): `ReadScheduleHeader`,
   `SetScheduleHeader` (replacing the inline logic in `loadAgentForEdit`), `ReplaceCronLiteral`,
   `ParseScheduleProse`, `ApplyScheduleEdits`, `RefreshDescription`.
2. **Prompt** (`internal/prompts/schedulesync.go`): `BuildScheduleProsePrompt`. No prompt text outside
   this package.
3. **Database**: `UpdateEditDraftAgentMD`, scoped to one agent's edit draft.
4. **Server** (`web/schedule_sync.go`): `applyScheduleChange` (row, then file, draft, description),
   the background pass, the tracker, and `schedule_sync` on the detail response. The model call is a
   `Server` func field left nil by `NewServer` and set only by `serve`, so no test can reach a real
   coder by construction.
5. **Doors**: `apiSaveSchedule`, `apiDeleteSchedule`, `apiSaveAgentMD`, `apiSaveKBNote`.
6. **SPA**: type and polling in `lib/agents.ts`, status in `ScheduleCard`, header adoption in
   `AgentMDCard`.
7. **Docs**: `CLAUDE.md`, plus `concepts/scheduling.md` in the website repository.
8. **Verify**: targeted Go and vitest suites, `make ci-fmt ci-vet ci-ui ci-docs`, then a local deploy
   and one live schedule change on a harmless agent, restored afterwards.
