# Chat Stop button Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner stop an in-flight web chat turn from the composer.

**Architecture:** The tracker runs each turn on a cancellable context derived from `context.Background()`. A new endpoint flags the turn stopped and cancels it. The tracker records a stopped outcome (a placeholder assistant message and a `done` stream). The Composer's Send button becomes Stop while a turn runs.

**Tech Stack:** Go/Echo, React + TanStack Query, vitest.

**Spec:** `docs/superpowers/specs/2026-10-26-chat-search-loop-and-stop-design.md` (Defect 2)

## Global Constraints

- Route: `POST /api/v1/chats/:id/turn/cancel` → 204, or 404 `{"error":"no active turn"}`. The same ownership check as `/turn/progress`.
- The placeholder is the exact text `⏹ Stopped before an answer.`, and the milestone is `⏹ Stopped`.
- A stopped turn ends with SSE `done`, never `error`.
- The accessible name of the stop button is exactly `Stop`.

## Review Focus

- Stop pressed after the turn already finished → 404, harmless; the UI must not show an error banner for it.
- A double Stop → the second cancel is a no-op (no second placeholder).
- Leaving the page still does not cancel (the stream closing never calls cancel).
- A stopped turn's history alternates user/assistant (the placeholder is persisted).
- Composer surfaces without `onStop` (the designers) still render a disabled Send while busy.

---

### Task 1: server cancel + stopped outcome

**Files:**
- Modify: `web/chat_turn_tracker.go`, `web/api_chats.go` (route), `web/handlers_misc.go` (`testCoderBlock` also returns on `ctx.Done()`), `web/api_parity_test.go` (inventory)
- Test: `web/chat_turn_stop_test.go`

**Interfaces:**
- Produces: `chatTurnState.cancel context.CancelFunc`, `chatTurnState.stopped bool`; `func (s *Server) stopChatTurn(chatID string) bool`; handler `handleChatTurnCancel`.

- [ ] Tests:
  - `TestStopChatTurnCancelsAndRecordsPlaceholder`: block the coder, start a turn, `stopChatTurn` → true. The turn finishes with `err == nil`, the last line is `⏹ Stopped`, and the messages are `[user, assistant "⏹ Stopped before an answer."]`.
  - `TestStopChatTurnWithNoTurnReportsFalse`.
  - `TestStopChatTurnTwiceRecordsOnePlaceholder`.
  - The HTTP test: a 404 for another workspace's chat, and 204 on success.
- [ ] Implement it. On the error branch, `if stopped` → the stopped path (log `chat: turn stopped`, persist the placeholder, `st.err` stays nil).
- [ ] `GOTOOLCHAIN=auto go test ./web/ -run 'ChatTurn|APIParity'` passes. Commit.

### Task 2: Composer Stop + ChatWindow wiring

**Files:**
- Modify: `web/ui/src/components/chat/Composer.tsx` (`onStop?: () => void`), `web/ui/src/pages/chats/ChatWindow.tsx`, `web/ui/src/lib/api` (the chat API module: `cancelChatTurn(chatId)`)
- Test: `web/ui/src/components/chat/composerstop.test.tsx`, `web/ui/src/pages/chats/stopturn.test.tsx`

- [ ] Composer tests: busy + onStop → a button named `Stop`, enabled, the textarea disabled, and no `Send` button. A click calls onStop. busy without onStop → `Send` disabled.
- [ ] ChatWindow test: while a turn is running, clicking Stop POSTs `/api/v1/chats/<id>/turn/cancel`. A 404 shows no banner.
- [ ] Implement it, using lucide `Square` (filled) as the icon.
- [ ] `cd web/ui && npx vitest run src/components/chat src/pages/chats` and `npx tsc -b` pass. Commit.

### Task 3: docs

- [ ] CLAUDE.md: extend the durable-turn section with cancel. docs-sync: a new `/api/v1` route → the rookery-web API page. Run `make ci-docs`.
