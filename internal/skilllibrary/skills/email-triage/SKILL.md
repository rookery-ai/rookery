---
name: email-triage
description: Use this skill for working through a mailbox with the connected Gmail or Outlook account — summarising what arrived, sorting by importance, pulling out action items, finding a specific thread, or drafting a reply. Triggers include "check my email", "what's in my inbox", "anything important", "summarise my mail", "draft a reply to", "find the email about".
version: 1.0.0
license: MIT-0
category: Productivity
---

# Email Triage

Work through a mailbox using the account the user has already connected
(Gmail or Outlook). The connection provides ready-to-use, already-authenticated
tools — there is nothing to configure.

## The connected account is the only way in

Never look for an API key, install an email SDK, or try to scrape a webmail
page. If the tools for a connected Gmail/Outlook account are not available to
you, that means no account is connected — say so plainly and point the user at
the connections page. Do not attempt a workaround.

## Search before you list

A targeted query is cheaper and more useful than paging through the whole
inbox. Reach for search first:

- "anything important" → search for `is:unread` or a recent date window, not
  a full inbox dump.
- "find the email about the invoice" → search `invoice`, not a scan of every
  subject line.
- "what came in today" → search bounded to today's date range in the
  workspace timezone (see `time-and-timezones`).

Only fall back to a plain listing when the user's request has no useful
search terms (e.g. "what's new since yesterday" with nothing to search on).

## Read the thread before replying to it

Never draft a reply from a subject line or a snippet alone. Open and read the
full thread first — the last message may have already answered the question,
or the thread may need context from earlier messages to reply correctly.

## Drafts are drafts — sending needs an explicit ask

Creating a reply always means creating a **draft**, never sending it, unless
the user's own words asked for it to be sent ("send it", "reply and send",
"go ahead and send that"). "Draft a reply to Sarah" means leave it in drafts
for the user to review. When in doubt, draft and say so — do not guess that
silence means "send".

## Never send mail while being built

During agent generation/testing (build time), do not send real outbound
email under any circumstances, even if the task description implies sending
is the end goal. Draft and describe what would happen; the first real send
only happens on an actual run the user triggered.

## Summarise by sender and action needed, not a subject dump

A wall of subject lines is not a summary. Group by sender (or by
thread), and for each say what it's about and what — if anything — needs to
happen:

```
- Maria Chen (2 emails) — approved the Q3 budget, no action needed.
- Billing@vendor — invoice #4471 overdue, needs payment by Friday.
- Dana (thread, 4 messages) — asking for feedback on the draft proposal;
  reply needed.
```

Skip newsletters/notifications from the summary unless the user asked for
everything, and call out anything time-sensitive first.

## Notes

- Respect the user's timezone for any date reasoning ("today", "this week") —
  see `time-and-timezones`.
- If a search returns nothing, say so plainly rather than broadening silently
  and presenting unrelated results as a match.
- Large threads: summarise the last few messages plus anything that changed
  the outcome, not the entire history verbatim.
