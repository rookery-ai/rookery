---
name: notification-writing
description: Use this skill when composing the message an agent sends to the user — deciding what belongs in a notification, how short it should be, when to stay silent, and how to report a failure in plain language. Triggers include "notify me", "send me a summary", "message me when", "what should the alert say", "keep it brief".
version: 1.0.0
license: MIT-0
category: Agent Behaviour
---

# Notification Writing

The message an agent sends is the ONLY part of a run the user actually sees.
Everything else — the reasoning, the tool calls, the intermediate files — is
invisible to them. Write the message accordingly: it has to stand alone.

## `[CHAT]` carries the whole message

`[CHAT]` runs until the next protocol marker (`[STATE]`, `[CALL]`, `[SILENT]`,
another `[CHAT]`) or the end of output. **Blank lines inside it are part of the
message, not a terminator** — don't worry about breaking a `[CHAT]` block by
leaving a blank line between paragraphs; it stays one message.

```
[CHAT]
3 new PRs are ready for review:

- #142 fix(auth): rotate refresh tokens
- #144 feat(export): add CSV export
- #145 chore: bump deps

[/CHAT]
```

An empty or whitespace-only `[CHAT]` block is dropped and delivers nothing —
if you have nothing to say, use `[SILENT]` instead, don't emit an empty block.

## Lead with the answer

The user reads this on a phone, glances at it, and decides whether to open the
app. Put the actual answer in the first line. Don't narrate your process first.

Bad:
```
[CHAT]
I checked the calendar and compared it against your usual schedule, then
looked at the weather forecast for tomorrow, and after considering both I
think you should know that your 9am meeting has been moved to 2pm.
[/CHAT]
```

Good:
```
[CHAT]
Your 9am meeting moved to 2pm today.
[/CHAT]
```

If detail is genuinely useful, put it AFTER the headline, not before it.

## Length budget

Write for a phone notification, not a report. A good target is a few lines —
one headline plus, if needed, a short bulleted list of specifics. If the
content is naturally long (a full digest, a multi-item summary), that's fine,
but still lead with a one-line summary before the detail, and cut anything
that doesn't change what the user should do next.

Ask: if the user reads only the first sentence, do they have what they need?

## Keep internals out of the message

Never put these in a user-facing `[CHAT]`:

- File paths (`agents/abc123/tools/fetch.py`)
- Script or tool names (`ran fetch_data.py`, `called run_script`)
- Internal state keys (`seen_ids`, `last_checked`)
- Stack traces or raw error output

The user doesn't run this system — they use it. "I checked the API and it
returned a 500" is fine; "Traceback (most recent call last): File
\"tools/x.py\", line 42..." is not. Translate internals into plain language.

## When to say nothing

Emit `[SILENT]` alone as the last line when there's genuinely nothing to tell
the user — a scheduled check that found no changes, a state-only update. This
is the default for "watch and report only what's new" agents most of the time
(see the `change-detection` skill). Don't manufacture a message just because
the run executed; a silent success is not a failure to report on.

## Reporting a failure

A failed run should still tell the user what happened — in plain language, not
a dump of the error:

```
[CHAT]
Couldn't check your GitHub notifications — the connection timed out. I'll try
again at the next scheduled run.
[/CHAT]
```

Not:
```
[CHAT]
Error: requests.exceptions.ConnectionError: HTTPSConnectionPool(host='api.github.com',
port=443): Max retries exceeded...
[/CHAT]
```

State what failed, in terms the user recognizes (the service, the action —
not the library or exception class), and what happens next (retried
automatically, needs the user to do something, or nothing further will
happen). See the `resilient-runs` skill for the retry/give-up decision itself
— this skill only covers how to WORD the result once that decision is made.

Never claim success that didn't happen. If part of a job worked and part
didn't, say which part — "sent the summary but couldn't update the sheet" is
honest; a message that only reports the good part is not.
