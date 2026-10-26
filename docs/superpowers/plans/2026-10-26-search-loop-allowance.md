# Web-search allowance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the API engine's tool loop from treating empty web searches as progress, and cap fruitless searching per coder call.

**Architecture:** `hostToolSet` gains a third call outcome (fruitless), counted neither as a failure nor as productive. `webSearch` tracks empty and all-failed outcomes and, once the allowance is spent, returns a settled notice instead of searching. Chat's system prompt gains a "when to stop looking" section.

**Tech Stack:** Go (stdlib).

**Spec:** `docs/superpowers/specs/2026-10-26-chat-search-loop-and-stop-design.md` (Defect 1)

## Global Constraints

- `maxEmptySearchStreak = 3`, `maxEmptySearches = 6`, `maxUnavailableSearches = 2`.
- The allowance notice and the unavailable notice must NOT start with `error:`.
- `search_files`/`glob` empties stay productive.
- Run Go with `GOTOOLCHAIN=auto`.

## Review Focus

- A search that returns results after two empties resets the streak, so the next two empties do not trip the cap (total still counts).
- After the cap, a different query still does not hit the network (assert the test server's hit count does not change).
- A turn made only of fruitless calls advances the unproductive streak (`callStats().Productive` does not change).
- The allowance is per `hostToolSet`, i.e. per coder call, so a new chat turn starts fresh.
- The prompt section must reach chat only and not change the agent prompts.

---

### Task 1: websearch reports failed providers

**Files:**
- Modify: `internal/websearch/websearch.go` (`Outcome`, `Search`)
- Test: `internal/websearch/websearch_test.go`

**Interfaces:**
- Produces: `Outcome.Failed []string`, the provider names whose request errored (in `Tried` order).

- [ ] Test `TestSearchRecordsFailedProviders`: one provider returning an error and one returning empty. `Tried` has both, `Failed` holds only the erroring one.
- [ ] Append to `out.Failed` in the error branch of `Search`.
- [ ] `GOTOOLCHAIN=auto go test ./internal/websearch/` passes. Commit.

### Task 2: fruitless outcome and search allowance

**Files:**
- Modify: `internal/coder/hosttools.go` (`hostToolSet` fields, `executeOrNudge`, `noteCall` neighbour, `webSearch`)
- Test: `internal/coder/search_allowance_test.go`

**Interfaces:**
- Consumes: `Outcome.Failed`.
- Produces: `func (h *hostToolSet) noteFruitless(name string)`; field `lastFruitless bool`, set by `webSearch` and consumed (and cleared) by `executeOrNudge`, which calls `noteFruitless` instead of `noteCall`.

- [ ] Tests, using an httptest DDG fixture with a hit counter and `h.executeOrNudge`:
  - `TestEmptySearchIsNotProductive`: after one empty search, `callStats().Productive == 0` and `Failed == 0`.
  - `TestSearchAllowanceStopsAfterThreeEmptyInARow`: the 4th call does not hit the server, its result contains `search allowance`, and it does not start with `error:`.
  - `TestSearchResultResetsStreak`: the sequence empty, empty, hit, empty, empty → the 6th call still reaches the server.
  - `TestSearchAllowanceTotalCap`: alternating empty and hit (never 3 empties in a row). After 6 empties, the next call is refused.
  - `TestAllProvidersFailingDisablesSearch`: the fixture returns 403. The result says `unavailable`. The 3rd call does not hit the server.
- [ ] Implement it. The counters are `emptySearchStreak`, `emptySearches` and `unavailableSearches`. The checks run before the network call. Copy:
  - allowance: `(web search allowance spent: N searches this turn found nothing. Do not search again — answer now with what you already have, say plainly what you could not find, and suggest where the user could look.)`
  - unavailable result: `(web search is unavailable on this server right now — every engine failed: <labels>. Retrying will not help; answer from what you have.)`
- [ ] `GOTOOLCHAIN=auto go test ./internal/coder/` passes. Commit.

### Task 3: chat prompt — when to stop looking

**Files:**
- Modify: `internal/prompts/prompts.go` (`BuildChatSystemPrompt`)
- Test: `internal/prompts/chat_stop_looking_test.go`

- [ ] Test `TestChatPromptSaysWhenToStopLooking`: the chat prompt contains `## When to stop looking`. `BuildCoderPrompt` output does not.
- [ ] Add the section. Its points: check the knowledge base first for the owner's own data; two or three searches that find nothing mean stop; answer with what you found plus what you could not find; never keep rephrasing the same search.
- [ ] Prompt tests pass. Commit.

### Task 4: docs

- [ ] Add a short paragraph to CLAUDE.md under the turn-budget bullet ("fruitless" calls and the allowance). Run `make ci-fmt ci-vet ci-docs`.
