# Inbox as an unconditional delivery channel

**Date:** 2026-07-27
**Status:** approved

## Problem

With no chat platform connected, reminders never appear in the web inbox. Investigating the
report turned up three instances of one root cause, the third larger than the reported symptom:

| Site | Effect when no chat app is connected |
|---|---|
| `internal/reminder/reminder.go` — `HasPlatformIdentity` skip in `tick()` | Reminder never fires, never reaches the inbox, stays pending forever |
| `internal/reminder/reminder.go` — `continue` on `SendToUser` error | Same, on any transient send failure, even with a chat app connected |
| `internal/scheduler/scheduler.go` — `HasPlatformIdentity` skip in the fire path | **Scheduled agents never run at all** |

## Root cause

All three predate the inbox. They encode an assumption that was true when written — a chat
platform is the only way output can reach the user — and was never revisited when the inbox
became a real delivery channel with its own UI, badge, and vault reflection.

The reminder path compounds it: both early exits bypass `recordInbox` *and*
`MarkReminderSent`, so a reminder that cannot be chat-delivered is retried on every tick
forever.

## Design

One principle: **deliver to the inbox unconditionally; the chat send is best-effort on top.**

### B1 — `reminder.tick()`

Reorder so the durable work is unconditional:

1. Build the message.
2. Attempt the chat send only when a platform identity exists. A send failure is logged at
   warn level and does not abort the iteration.
3. `recordInbox` and `MarkReminderSent` run regardless of send outcome.

This fixes the infinite re-fire as a direct consequence: the reminder is marked sent once its
durable delivery succeeds, independent of the chat channel.

### B2 — `scheduler.go`

Drop the `HasPlatformIdentity` skip so scheduled agents run and their output reaches the
inbox via the runner's existing `recordInbox` call. The comment's stated rationale — "the
agent cannot deliver output and running it wastes API quota" — no longer holds: the agent
*can* deliver output, to the inbox.

`UpdateScheduleRunTimes` already runs before this point, so removing the skip does not affect
double-fire protection.

The runner's existing behavior is untouched: `[SILENT]` runs still post nothing, and a run
producing nothing deliverable still yields its visible warning.

## Testing

- `reminder.tick()` — with no platform identity: inbox message created, reminder marked sent.
  With a failing sender: same, and not retried on the next tick.
- Scheduler — agent fires with no platform identity connected.

## Out of scope

Agent designer build-retry reliability — see
`2026-07-27-designer-build-retry-reliability-design.md`.
