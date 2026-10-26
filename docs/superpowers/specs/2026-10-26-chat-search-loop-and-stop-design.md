# Chat search loop and the Stop button

## Why

On 2026-10-26 a chat asked *"What is the payment code for accountant services
B2B?"*. The model found the old code (245) in the owner's
`Payment_Codes.md`, then went looking on the web for the new ISO 20022 code,
which that note says is not filled in yet. It issued web searches for more than
ten minutes. At least 20 of them came back with nothing (every provider was
blocked or empty), and the turn showed no sign of ending before the 30-minute
coder timeout. The owner had no way to stop it.

Two independent defects, shipped as two pull requests.

## Defect 1: an empty search counts as progress

`internal/coder/turnbudget.go` spends the turn budget only on UNPRODUCTIVE
turns, and stops after `maxUnproductiveStreak` (6) of them in a row. A turn is
productive when a tool call succeeded, and "succeeded" means "did not return the
`error:` prefix". `web_search` deliberately returns a non-error
`(no search results — tried …)` so the oscillation guard does not count it as
a failing call. As a result, every empty search counted as productive. The
streak never grew, the base budget was never spent, and only the 150-turn hard
ceiling, the context window or the coder timeout could end the turn.

The repeat guard (`matchesRecentFailure`) does not help either. It catches only
repeated *failures* with *identical* arguments, and a model rephrasing a
fruitless query produces neither.

### Fix

1. **A third outcome for a tool call: fruitless.** Neither failed (no `error:`,
   so the oscillation guard stays out of it) nor productive (so it does not
   reset the unproductive streak). `hostToolSet.noteFruitless` records it.
   A web search with no results is fruitless. A turn whose only calls were
   fruitless therefore advances the streak, and six such turns end the loop
   through the existing grace-turn path.
2. **A per-call web-search allowance.** After `maxEmptySearchStreak` (3)
   consecutive empty searches, or `maxEmptySearches` (6) empty searches in the
   whole coder call, `web_search` stops searching. It returns, without touching
   the network, a notice telling the model the search allowance is spent, not
   to search again, and to answer with what it has: what it found, and what it
   could not find. The notice is not an `error:` (it is a settled outcome, not a
   failure to retry) and is itself fruitless. A search that returns results
   resets the streak but not the total.
3. **Search that is down says so.** `websearch.Outcome` gains `Failed`, the
   providers that errored as opposed to returning nothing. When every provider
   tried failed outright, the result says web search is unavailable on this
   server and names the engines. Two such outcomes in one call disable
   `web_search` for the rest of the call with the same notice. Rewording a query
   cannot fix a blocked network, so continuing is pure waste.
4. **Chat is told when to stop looking.** `BuildChatSystemPrompt` gains a short
   section: two or three searches that find nothing mean the answer is not
   there. Stop, say what you found and what you could not find, and suggest
   where the owner could look. Check the knowledge base before the web for
   anything about the owner's own data.

The allowance lives in the engine, not the chat prompt, so agent runs and
builds get it too. A scheduled agent spinning on empty searches is the same
waste, and nobody is watching it.

### Not changed

- Each search costs 15–20 s because the cascade retries every provider. Fixing
  the providers themselves (Mojeek 403, the browser proxy's
  `ERR_TUNNEL_CONNECTION_FAILED`) is a separate bug.
- `search_files` and `glob` empties stay productive. They are cheap, local and
  often a correct answer ("no such note").
- No change to `maxUnproductiveStreak` or the base budgets.

## Defect 2: a chat turn cannot be stopped

Chat turns run on `context.Background()` (`web/chat_turn_tracker.go`) so that
leaving the page does not kill them. Nothing else can cancel one either.

### Fix

- **Server.** `chatTurnState` holds a `cancel` function. The turn runs on
  `context.WithCancel(context.Background())`, so leaving the page still does not
  cancel it. `POST /api/v1/chats/:id/turn/cancel` (workspace-scoped and
  ownership-checked like the progress endpoint) sets `stopped` and calls
  `cancel`. It returns 204, or 404 when no turn is in flight. Idempotent.
- **Outcome of a stopped turn.** The tracker decides from its own `stopped`
  flag, not from the error's type. The engine reports a cancel as `ErrTimeout`
  today, and a CLI coder surfaces a killed process differently again. A stopped
  turn:
  - persists an assistant message, `⏹ Stopped before an answer.`, so the
    transcript keeps alternating and the next turn's history does not hold two
    user messages in a row;
  - emits a `⏹ Stopped` milestone and ends the stream with `done`, not `error`.
    It is not a failure and must not raise the red banner;
  - logs `chat: turn stopped`.
  The user's message stays, as it does on failure.
- **Browser.** `Composer` gains an optional `onStop`. While `busy` and `onStop`
  is set, the Send button becomes a **Stop** button (square icon, accessible
  name "Stop", always enabled), and the textarea stays disabled and dimmed as it
  is today. `ChatWindow` passes `onStop` only while a turn is running (not while
  a file is being attached). It calls the cancel endpoint and keeps following
  the stream, which delivers the terminal `done` and the refetch exactly as a
  normal finish does. If the cancel request fails, it shows the error in the
  banner. Surfaces that do not pass `onStop` (the designers) are unchanged.

### Not changed

- Chat-platform turns (Telegram, Discord, Slack) run inline and get no stop
  command in this change.
- Stopping does not undo knowledge-base edits the turn already made.

## Builder's calls

- Limits of 3 consecutive and 6 total empty searches, and 2 all-failed outcomes
  to declare search unavailable.
- The stopped-turn placeholder text, and persisting it as an assistant message.
- One spec for both PRs. Each PR carries its own plan.
