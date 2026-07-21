---
name: time-and-timezones
description: Use this skill whenever an agent works with dates, times or schedules — converting to the user's timezone, handling DST, writing a cron expression, computing "yesterday" or "next Tuesday", or catching up after downtime. Triggers include "every morning at", "schedule this for", "what time", "last week", "since yesterday", "in my timezone".
version: 1.0.0
license: MIT-0
category: Agent Behaviour
---

# Time and Timezones

Every workspace has a configured timezone. Getting time handling wrong is one
of the most common ways an agent quietly misbehaves — a "9am" notification
that arrives at 7am, a "yesterday" that's off by a day near midnight, a
digest that reports the same items twice after a DST shift.

## User-facing times are always in the workspace's timezone

Whenever you SHOW a time to the user — in a `[CHAT]` message, in a note —
convert it to the workspace's configured timezone first. Never show a raw UTC
timestamp and never assume the machine's local time is the user's.

```python
from datetime import datetime
from zoneinfo import ZoneInfo

tz = ZoneInfo("Europe/Skopje")  # the workspace's configured timezone
local = utc_dt.astimezone(tz)
print(local.strftime("%Y-%m-%d %H:%M"))
```

## Store in UTC, convert only for display

Anything you persist — in `[STATE]`, in a note, in a cursor for change
detection — should be stored as UTC (or with an explicit offset), and
converted to local time only at the point you display it. This avoids
ambiguity when the state is read back later, possibly across a DST boundary.

```json
{"last_checked": "2026-07-21T07:00:00Z"}
```

Not:
```json
{"last_checked": "2026-07-21 09:00"}
```
(9am in what zone? On which side of a DST change?)

## Cron fields

A schedule is 5 fields: `minute hour day-of-month month day-of-week`.

```
 ┌──────── minute (0-59)
 │ ┌────── hour (0-23)
 │ │ ┌──── day of month (1-31)
 │ │ │ ┌── month (1-12)
 │ │ │ │ ┌ day of week (0-6, 0=Sunday)
 │ │ │ │ │
 * * * * *
```

Examples:

| Schedule | Cron |
|---|---|
| Every day at 9am | `0 9 * * *` |
| Every 10 minutes | `*/10 * * * *` |
| Every weekday at 6pm | `0 18 * * 1-5` |
| Every Monday at 8am | `0 8 * * 1` |
| Twice a day, 9am and 5pm | `0 9,17 * * *` |

## "Every day at 9" means 9 in the user's timezone, across DST

If the scheduler's cron fires in UTC (or a fixed machine timezone) but the
user's timezone observes DST, a bare cron expression like `0 9 * * *` can
drift by an hour twice a year relative to what the user actually meant.
When writing the agent's own logic (not the cron trigger itself, but any code
that decides "is it actually 9am for the user right now"), compute the
comparison in the user's timezone, not in UTC:

```python
now_local = datetime.now(ZoneInfo("Europe/Skopje"))
if now_local.hour == 9:
    ...
```

Don't hardcode a UTC hour offset ("9am Skopje is 7am UTC in summer") — that
breaks the other half of the year. Always convert through the named timezone,
never a fixed numeric offset.

## Computing dates: never add 86400 seconds

A calendar day is not always 86400 seconds — DST transitions make some days
23 or 25 hours long in wall-clock terms. Adding `86400` to a timestamp to get
"tomorrow" can land on the wrong wall-clock hour, or even the wrong day, right
at a DST boundary. Use calendar-aware date arithmetic instead:

```python
from datetime import timedelta

# correct: date arithmetic, then re-localize
tomorrow = (now_local.date() + timedelta(days=1))
next_9am = datetime.combine(tomorrow, time(9, 0), tzinfo=ZoneInfo("Europe/Skopje"))
```

The same applies to "yesterday", "last week", "next Tuesday" — compute in
terms of calendar days/weeks in the user's timezone, not raw seconds.

## Catch-up after downtime: use state, not "since one hour ago"

If the server was down and a scheduled run is late, don't assume "check
everything from the last hour" — that both misses whatever happened during
the actual downtime window and can re-report things from before it. Use the
STORED cursor from the last successful run (see the `change-detection` skill)
as the starting point, however long ago that was:

```python
since = state.get("last_checked")  # not "now minus 1 hour"
items = fetch_since(since)
```

This makes catch-up correct regardless of how long the gap was — five minutes
or five days — because it's anchored to what was actually last processed, not
to an assumed cadence.
