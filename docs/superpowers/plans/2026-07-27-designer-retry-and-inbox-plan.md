# Implementation plan — designer build-retry reliability + inbox delivery

Specs: `2026-07-27-designer-build-retry-reliability-design.md`,
`2026-07-27-inbox-delivery-channel-design.md`

Ordered so each step compiles and tests green before the next.

## 1. Prompt: the force-TIER-1 block

- `internal/prompts/prompts.go`: add `ForceTier1 bool` to `ImplementationParams`; render
  `forceTier1Block()` from `capabilitySpec()` when set. Block must forbid creating code files
  and name the direct tools to use instead.
- Test: `BuildImplementationPrompt` contains the clause when set, omits it when not.

## 2. Flow: carry the force-TIER-1 signal

- `internal/agentdesigner/flow.go`:
  - `reconciledOutcome` gains `forceTier1 bool`; set in both weak-backend branches of
    `reconcileBlockedOutcome`.
  - Reword the weak-backend `recordFailNote` to drop the "run the script" option.
  - `DesignSession` gains `ForceTier1 bool`; set where `recordGenerationFailure` is called with
    the reconciled outcome.
  - `runGeneration` reads it into `implParams.ForceTier1`, then clears it (consume-once).
- Test: extend `reconcile_blocked_test.go` for `forceTier1` on both branches and off elsewhere.

## 3. Flow: change request after failure rebuilds

- `internal/agentdesigner/flow.go`: add `isDesignQuestion`; in `stepDesigning`, when
  `genFailed` and nothing else matched, `appendUserHistory` + `runGeneration` unless
  `isDesignQuestion`.
- Test: table test over the transcript's real messages + question forms.

## 4. Flow: clear the sticky failed flag

- `internal/agentdesigner/flow.go`: clear `GenerationFailed` on the chat path in
  `stepDesigning`, and on finalize.
- `internal/skilldesigner/flow.go`: same clearing on its chat path.
- Test: flag clears after a chat turn.

## 5. UI: banner copy

- `web/ui/src/components/designer/DesignerSurface.tsx`: replace the banner text.

## 6. Reminder + scheduler inbox delivery

- `internal/reminder/reminder.go`: reorder `tick()` — chat send best-effort and identity-gated,
  `recordInbox` + `MarkReminderSent` unconditional.
- `internal/scheduler/scheduler.go`: drop the `HasPlatformIdentity` skip.
- Test: reminder with no identity / failing sender still records inbox and marks sent.

## 7. Verify

- `go test ./... -count=1`
- `make ui` (banner change compiles)
- `go build ./...`
- Update `CLAUDE.md` where the changed behavior is documented.
