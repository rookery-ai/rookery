# Live tool trace in chat apps

## Why

On Telegram, Discord and Slack a long chat turn or `/run` shows `⏳ Thinking...` until the
answer lands, so the owner cannot tell a busy model from a stuck one. The web UI shows
every step. The API engine already emits the milestones that view uses, but the chat-app paths
never pass them along.

## What it does

The placeholder message is edited into a running list while the work happens:

```
✓ 📚 Searched knowledge base: `quarterly report`
✓ 📄 Read `notes/report.md`
⏳ 🌐 Searching the web: `example topic`
step 3 · 0:42
```

- Applies to plain chat messages and `/run <agent>` on every adapter with `TypingGateway`
  (Telegram, Discord, Slack). `/run` keeps its `Running agent **x**...` line as a header.
- At most the last 5 steps show, under a `+N earlier` line.
- Edits go out at most once every 3 seconds, the rate Telegram tolerates in a group
  (20 per minute). Steps arriving faster are coalesced. The newest state always gets flushed.
- The elapsed time moves only when a step arrives. There is no ticking edit.
- When the turn finishes, the answer replaces the list as it does today, with `✓ 6 steps · 1:12`
  above it. A turn with no tool calls is unchanged.

## Privacy

Chat-app messages pass through a third party and a Telegram group shows them to every
member, so the labels show less than the web trace does. Unknown tools show their name only.

- `bash`, `run_script`, `run_in_env`: no command text.
- `web_fetch`, `browser_read`: host only, never path or query.
- `browser_click`, `browser_fill`, `browser_press`: no element or value.
- `save_to_kb`: no source.
- Connector and MCP tools: name only, never arguments.
- Search queries and vault-relative paths are shown.

## Requirements the code must keep

- **The summary line is never stored.** It is added outside `wrappedSend`, so it stays out of
  chat history, the vault reflection and the auto-title.
- **A flush can never land after the answer.** `send()` clears the placeholder id, so a late
  edit would post the stale list as a new message below the reply.
- **Recording a step never blocks the tool loop.** Progress runs inline in the engine.
- **`RunInput.OnProgress` is not the hook.** It also carries `[CHAT]` lines and the final
  output. Tool steps get their own `OnStep` sink.
- **Scheduled runs supply no `OnStep`,** so they post nothing new.
- **`sendProgress` is unchanged,** so agent and skill build progress stays as it is.

## Out of scope

- Scheduled agent runs.
- CLI coders, which do not emit milestones on any surface.
- Agent and skill builds started from chat.

## Accepted costs

- A message sent mid-run (`browser_wait`'s notify) consumes the placeholder and carries the
  summary so far. Later steps start a new list, which the final answer then replaces.
- `✓` means the run moved past a step. A step whose tool returned an error also gets one.
