# Single-field, more-visible reminders

**Date:** 2026-07-23
**Status:** Approved (owner waived per-section approval — build autonomously)

## Problem

On the home screen, reminders are (1) buried at the bottom of the context pane
below an ever-growing inbox, and (2) created through an awkward **two-field**
form (a "message" field + a separate "time" field). The user wants to type one
natural sentence — *"Remind me in 10 minutes to call the doctor"*, *"Remind me
next week to send a copy of the contract"* — and have the system figure out both
the time and the message.

The capability already exists on Telegram: `/remind` parses a single string via
a regex fast-path + LLM fallback (`reminder.ParseNaturalTimeFull` +
`BuildReminderParsePrompt`). The web surface never caught up. This is a
platform-parity gap (web and Telegram must offer the same experience) plus a
visibility fix.

## Design

### 1. Shared single-string parser (`internal/reminder`)

Extract the strategy currently inlined in `gateway/router.go:handleRemind` into a
pure, testable function so **both** surfaces resolve reminders identically:

```go
// ParseReminderText extracts (when, message) from one natural-language string
// like "remind me in 10 minutes to call the doctor" or "buy milk tomorrow 9am".
// Pure: no state, no prompting. Zero `when` + non-empty `message` + nil err
// means "understood the message but found no time" — the caller decides whether
// to prompt (Telegram) or 400 (web).
func ParseReminderText(ctx context.Context, text string, now time.Time,
    loc *time.Location, llm TimeParserFunc, workspaceID string) (when time.Time, message string, err error)
```

Strategy (lifted verbatim from the router, plus one new step):
1. **Strip leading filler** (the one genuinely new bit — Telegram's `/remind`
   already ate the verb, the web field receives the whole "Remind me …" phrase).
   Strip a **prefix with a trailing space** only — `"remind me "`, `"reminder to "`,
   `"reminder "`, `"remind "`, `"me "` — never a substring, so "**me**eting" and
   "**remind**er about X" survive.
2. Try `" to "` split → regex (`ParseNaturalTime`) / `parseDuration` on the left,
   message = right. (Fast, no network — covers both of the user's demo phrases.)
3. Else LLM (`TimeParserFunc`) on the whole string → time + cleaned message.
4. Else legacy first-word-duration fallback.

`handleRemind` is refactored to delegate to this, but **keeps owning** its
stateful `pendingReminderMsg` follow-up ("understood the message, now reply with
a time") — the shared function stays pure. The router's list/delete subcommands
and the `pendingReminderMsg` resume path are unchanged.

### 2. Web API accepts one field (`web/api_home.go`)

`POST /api/v1/reminders` gains an optional `text` field:
- **`text` present** → `ParseReminderText`. On success create; zero `when` → 400
  `no_time` ("couldn't find a time — try 'in 10 minutes'"); parse error → 400
  `unparseable_time`. `text` wins when both are sent.
- **`text` absent** → the existing `{message, when}` two-field path, unchanged,
  so current API tests pass as-is (back-compat).

Response is the created reminder (with the resolved `remind_at`) exactly as today.

### 3. Frontend: one field, moved up, shows the parse (`pages/home/HomePage.tsx`)

- **Move `RemindersSection` above `InboxSection`** in the context pane — reminders
  are few and time-sensitive; the inbox is the ever-growing stream. This alone
  fixes "buried at the end."
- **One input** replacing the two: placeholder *"Remind me in 10 minutes to call
  the doctor…"*. Submits `{ text }`.
- **Loading state is required** (not optional): the general case (time at the end,
  no `" to "`) hits the ~4s LLM path, so the input/button must show pending on
  submit or it feels frozen exactly when the "magic" runs.
- **Show what it figured out**: on success the reminder list refreshes (the row
  already renders `remind_at`), and a success toast — *"Reminder set for Tue,
  15:04"* — is the trust surface for an LLM guess. On `no_time`/parse error, show
  the message inline and keep the typed text.

`useCreateReminder` changes its argument from `{ message, when }` to `{ text }`.

## Testing

- **`internal/reminder/parsetext_test.go`** (new): table tests for the
  deterministic paths — filler stripping (incl. the "meeting"/"reminder about"
  false-positive guards), `" to "` split, bare duration, and the zero-time
  "needs a time" outcome. LLM path is not unit-tested (non-hermetic, ~4s).
- **`web/api_home_test.go`**: add a single-field `{text: "take out trash in 10
  minutes"}` create (regex-deterministic); keep the two-field and
  unparseable-time tests.
- **`web/ui/.../home.test.tsx`**: rewrite the two reminder-form tests for the
  single input; assert the loading state and the success toast.

## Non-goals (YAGNI)

- No dedicated reminders page/tab; the context pane stays the home for reminders.
- No merging reminders into the main-area "Next up" timeline.
- No recurring reminders, no edit-in-place, no snooze.
