---
name: calendar-scheduling
description: Use this skill for calendar work with the connected Google Calendar or Outlook account — reading the day or week ahead, finding a free slot, creating or moving an event, and spotting conflicts. Triggers include "what's on my calendar", "am I free", "schedule a meeting", "book time for", "when's my next", "any conflicts".
version: 1.0.0
license: MIT-0
category: Productivity
---

# Calendar Scheduling

Work with the calendar account the user has already connected (Google
Calendar or Outlook). The connection provides ready-to-use, already
-authenticated tools — there is nothing to configure.

## The connected account is the only way in

Never look for an API key, install a calendar SDK, or guess at event data.
If the tools for a connected Google Calendar/Outlook account are not
available to you, no account is connected — say so plainly and point the
user at the connections page.

## Always work in the workspace timezone

Every time you read or write — "what's on my calendar today", "book this for
3pm", "am I free tomorrow morning" — resolve it in the workspace's configured
timezone, never UTC and never the server's local time. Convert calendar API
timestamps into that timezone before showing them to the user, and convert
the user's stated time into it before sending anything to the calendar. See
`time-and-timezones` for the conversion mechanics.

## Read before you write

Always check the calendar for the affected window before creating or moving
an event — this is how a conflict gets caught before it happens rather than
after. "Schedule a call with Dana Thursday at 2pm" means: read Thursday
first, confirm nothing is already there (or flag what is), then create.
Never create blind.

## All-day events and timed events compare differently

An all-day event spans a whole calendar day with no clock time attached —
comparing it to a timed event ("does my 2pm meeting conflict with the
all-day 'Conference' block?") means comparing by date, not by timestamp
overlap. Two timed events conflict only if their time ranges actually
overlap; a timed event never "conflicts" with an all-day event in the same
way — call out the all-day event as context ("you have 'Conference' blocked
that day") rather than reporting a false overlap.

## Confirm before touching a shared calendar

Creating a new event is usually safe once the user has stated a clear time
and purpose. Before **moving, deleting, or rescheduling** an existing event
that other people may see or be invited to, ask for a explicit confirmation
first — describe what would change ("move Thursday's stand-up from 9am to
10am") and wait for a yes. Don't reschedule speculatively just because the
user asked "is there a better time" — that's a question, not an instruction.

## Never send real invites while being built

During agent generation/testing (build time), do not create or modify real
calendar events on a live calendar under any circumstances. Describe what the
action would do; the first real write happens only on an actual run the user
triggered.

## Report a day as a short list, not raw event JSON

Never paste raw API output at the user. Turn a day or week into an ordered,
skimmable list in local time:

```
Thursday, 12 Nov (Europe/Skopje)
- 09:00–09:30  Stand-up
- 11:00–12:00  Budget review (with Maria) — conflicts with the 11:30 dentist
  appointment below
- 11:30–12:15  Dentist
- All day       Conference (blocked)
```

Lead with anything that needs a decision (conflicts, back-to-back meetings
with no travel time, an event with no attendees confirmed) before the plain
listing.

## Notes

- "Free" means no overlapping timed event and no relevant all-day block in
  the requested window — check both.
- If a search/list call returns nothing for a window, say the calendar is
  clear rather than staying silent.
- When creating an event, confirm the timezone-resolved start/end back to the
  user in the same message as the confirmation ask, so there's no ambiguity
  about what's about to be booked.
