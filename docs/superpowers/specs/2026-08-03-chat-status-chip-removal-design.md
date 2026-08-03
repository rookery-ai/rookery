# Removing the chat Active/Stopped chips from the web UI

**Date:** 2026-08-03
**Status:** approved, implemented

## Problem

Clicking through chats in the chats list made every one of them flip to
"Active". The chips read as user-facing state, so a list where everything is
active looks broken — you cannot chat with three conversations at once.

## What `chats.active` actually is

It is not a lifecycle. It has two jobs, neither of which the web user performs:

1. **A chat-platform routing pointer.** On Telegram, Discord and Slack there is
   one DM stream, so `GetActiveChatForPlatform(workspace, platform)` answers
   "which chat does this bare message belong to". Its four callers are all in
   `internal/gateway/router.go`; `/chat start` and `/chat resume` stop the
   current chat first to keep it single. Web chats carry `platform: "web"` and
   **no web path ever reads the flag for routing** — selection is explicit via
   `?chat=<id>`. Because the query filters on `platform`, web rows going active
   cannot affect chat-platform routing.

2. **The KB reflection gate.** `ListStaleChats` is
   `WHERE active=1 AND last_seen < ?`, and the idle sweeper in `internal/chat`
   is the only caller of `reflectTranscript`. A chat left stopped is invisible
   to the sweeper, so its transcript never reaches the vault.

The visible flipping came from `ChatWindow`'s mount-time auto-resume. Only its
display was wrong, so only the display is removed. Note that this resume is
*not* what keeps job 2 working: `handleChatMessage`
(`web/handlers_misc.go:157-160`) resumes on send, before the coder call, so any
chat that gains turns is swept and re-reflected regardless. The mount-time
resume only reactivates a chat on *open*, before any turn.

## Change

Presentational only. No behaviour changes, no schema changes, no API changes.

- `ChatsPage.tsx` — drop the per-row chip. A row is now name + `created_at ·
  timeAgo`.
- `ChatWindow.tsx` — delete the local `StatusChip` component (no other caller)
  and its use in the header.
- `ChatWindow.tsx` — delete the Stop/Resume button. Its label flipped on
  `chat.active`, so keeping it would have gone on reporting the flag after the
  chips were gone. `Delete…` in the `⋯` menu is now the header's only action.
- The `Play` icon import becomes unused and is removed. `useChatAction` stays —
  the auto-resume still uses it.

Explicitly unchanged: the mount-time auto-resume, `apiStopChat` /
`apiResumeChat` (still registered, still in `api_parity_test.go`'s inventory,
still used by the auto-resume and the chat platforms), the 30-minute idle
sweeper, `GlobalChatButton`'s active-chat filter, and every `/chat` command.

## Tests

- The list-row test now asserts the row shows name + relative time and does
  **not** contain "Active"/"Stopped". Fixture chats One and Two differ on
  `active`, so a re-introduced chip fails.
- The two tests driving the removed button are replaced by one asserting the
  header offers no Stop or Resume control.
- `stopping a chat after an auto-resume does not re-resume it` used the button
  to stop. It is rewritten as `a chat stopped elsewhere mid-mount is not
  re-resumed`: the fixture is mutated directly (standing in for the idle sweeper
  or `/chat stop`) and a send forces the detail refetch that surfaces it.
  Verified non-vacuous by mutation — removing the `autoResumeDecidedRef` guard
  fails it.

## Known, not fixed

The auto-resume sets `last_seen = now`, so *opening* an old chat restarts the
sweeper's 30-minute clock and defers mirroring its transcript to the KB;
browsing repeatedly can defer it indefinitely. This was visible before as chats
flipping to "Active" and is now invisible.

The fix is one line — delete the auto-resume, since the send path already
resumes — and per the section above nothing depends on it. It changes behaviour,
so it was deliberately left out of a presentational change. If it is ever taken
up, `a chat stopped elsewhere mid-mount is not re-resumed` goes with it: that
test exists only to pin the latch guarding this effect.
