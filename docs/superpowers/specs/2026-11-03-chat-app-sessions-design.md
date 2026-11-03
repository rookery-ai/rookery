# Chat apps open real chats — no more one-off messages

**Date:** 2026-11-03 · **Status:** approved (owner delegated the remaining calls)

## Intent

On Telegram, Discord and Slack a plain message with no active chat was a
**one-off turn**: the coder saw no history, nothing was saved, and the next
message had no idea what the previous one said. The web UI never worked that
way — every message there belongs to a saved, resumable chat. The owner wants
chat apps to behave like the web: start talking and you are in a conversation
that remembers, is saved, auto-titled, listed in `/chat list` and in the web
Chats page, and reflected into the knowledge base.

## Goals

- A plain message with no live chat **creates a new chat** and is answered with
  that chat's (empty) history. There is no one-off path any more.
- A chat ends after **30 minutes of inactivity**; the next message starts a new
  one and the reply **tells the owner the previous chat ended**, naming it and
  how to go back (`/chat resume <id>`).
- `/chat` / `/chat start [name]` keeps meaning "end the current chat and start
  a fresh one" — the explicit way to start over without waiting 30 minutes.
- `/start` (account linking) is untouched.

## Non-goals

- No proactive "your chat ended" push at the 30-minute mark. **My call:** a
  message out of nowhere after silence is noise; the notice rides on the next
  reply, which is the moment it matters.
- No change to the web chat path, the durable turn tracker, or the chat-app
  turn becoming durable (it stays inline — there is no page to leave).
- No change to design sessions, master-password challenges, reminder
  follow-ups or `/agent cancel` choices: they still take priority over chat.

## Anti-goals

- Telling the owner a chat "ended after inactivity" when they stopped it
  themselves with `/chat stop` (they already got a confirmation).
- Repeating the notice on every message, or announcing an empty chat.
- Breaking `/start`: an unlinked sender must still be able to link, and linking
  must not create a chat.

## Design

### Why a column (`chats.ended_reason`)

The notice must fire only for a chat that ended from **inactivity**, not one the
owner stopped. Nothing records why a chat is inactive, and inferring it from
`last_seen` is wrong for "stopped with `/chat stop`, came back two hours later".
Migration `022_chat_ended_reason` adds `ended_reason TEXT NOT NULL DEFAULT ''`:
`''` (active, or legacy), `'idle'`, `'user'`. Not backfilled — a historical
stopped chat reads `''` and is simply never announced.

- `db.StopChat` → `'user'` (web stop, `/chat stop`, `/chat start` replacing one).
- `db.StopIdleChat` → `'idle'` (the sweeper and the message-time check).
- `db.ResumeChat` → clears it (and already touches `last_seen`).
- `db.GetLatestChatForPlatform` — most recently seen chat on that platform,
  active or not.

### Idle is checked at message time too

The sweeper polls once a minute, so a message at 30m30s could still land in the
old chat. The router therefore treats an active chat whose `last_seen` is older
than `chat.IdleTimeout` as idle and ends it itself — via `chat.EndIdleChat`, the
same function the sweeper now calls, so both stop it the same way **and** reflect
its transcript into the vault.

### Router flow (`handleText`, after all the existing priority branches)

1. Under a router mutex: find the active chat for (workspace, platform).
   - Fresh (< 30 min) → use it.
   - Stale → `EndIdleChat`, remember it as `previous`.
   - None → `previous` = latest chat on this platform if its `ended_reason` is
     `'idle'`.
   - Create a new chat named `Chat <YYYY-MM-DD HH:MM>` in the workspace's
     timezone — the default-name shape `MaybeAutoTitle` recognises, so it gets
     a real title after the first reply (the same shape the web uses).
2. `previous` is announced only if it has at least one message. Creating the
   new chat makes it the latest, so the notice fires exactly once.
3. History is read, then the **user message is persisted before the turn**
   (parity with the web: a failed turn keeps the user's message).
4. The reply is persisted and the chat touched. The notice is **prepended to
   the delivered message** but never stored in the transcript.

`/chat start` with no name now uses the same default name, so it also gets
auto-titled (previously it stored `""`, which the auto-titler never matches).
`/chat stop` says the next message starts a new chat.

### `/start` safety

`/start` is dispatched by `Router.Handle` before `handleText` and never touches
chats; an unlinked sender is rejected by the gateway before the router except
for `/start`. A test pins that `/start` creates no chat.

## Plan

1. Migration 022 (+ down), `db.Chat.EndedReason`, scan lists, the three DB
   methods. Tests in `internal/db`.
2. `internal/chat`: export `IdleTimeout`, add `EndIdleChat` + `DefaultName`;
   sweeper uses them.
3. Router: `conversationFor`, rewritten tail of `handleText`, `/chat` wording,
   command help text. Tests in `internal/gateway/chat_session_test.go`:
   auto-create, history continues, idle → new chat + notice once, `/chat stop`
   → no notice, empty idle chat → no notice, `/start` creates no chat.
4. Docs: `CLAUDE.md` pipeline line, the website's `concepts/chat.md`.

## Left to the builder

Exact notice wording; the mutex granularity (one router-wide mutex around the
get-or-create is enough — it never covers the coder call).
