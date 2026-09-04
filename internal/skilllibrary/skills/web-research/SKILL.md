---
name: web-research
description: Use this skill when the user wants something researched on the web — finding current facts, comparing sources, checking documentation, or extracting structured data (a table, a price, an article body) out of a page you fetched. Triggers include "research this", "look up", "what's the latest on", "find recent info on", "scrape this page", "extract the table from", "get the article text", "compare these options".
version: 1.0.0
license: MIT-0
category: Web & Research
---

# Web Research

Search, fetch and extract. This skill covers the JUDGEMENT — the fetching itself is
built in.

## What is already built in

You have `web_search` and `web_fetch` as native tools. Do not write a script to make a
plain public request; call the tool. `web_fetch` already reduces HTML to readable text.

Write a script only when the request needs a secret, a session, or authentication.

## Research strategy

1. **Search broadly first.** One query rarely settles a question. Run two or three
   phrasings — a general one and a specific one — before concluding anything.
2. **Prefer primary sources.** Official docs, the project's own repository, the
   organisation's own site. A blog summarising a doc can be stale or wrong.
3. **Check dates.** A confident answer from three years ago is a wrong answer. Prefer
   pages that state when they were written, and say so when you cannot tell.
4. **Corroborate anything surprising.** If one source claims something the others do not,
   treat it as unverified and say so rather than reporting it as fact.
5. **Report uncertainty plainly.** "I found two conflicting figures" is a useful answer.
   A confident wrong number is not.

## Extracting from a fetched page

`web_fetch` gives you readable text, which is usually enough. When you need STRUCTURE —
a table's rows, every link, a repeated card — parse the HTML.

- Locate the data by a stable anchor (a heading, a label, an id), not by position. Page
  layouts change; "the third div" breaks silently.
- A page that comes back nearly empty is usually JavaScript-rendered. Use `browser_read`
  on the same URL rather than retrying the fetch — it opens the page in a real browser
  and returns what actually rendered. `web_fetch` will tell you when this is the case.
- Extract what was asked for and stop. Dumping the whole page into your reasoning wastes
  the run and truncates the real answer.

## Acting on a page, not just reading it

`browser_read` renders a page. When a task needs you to *drive* one — sign in,
fill a form, click through — the acting tools do it: `browser_click`,
`browser_fill`, `browser_press`, `browser_wait`. Address elements by the `ref=`
handles the page snapshot gives you; never compose a CSS selector.

Four things worth knowing before you try:

- **Never type a credential.** Write `${SECRET_NAME}` into `browser_fill` and
  the host substitutes the real value. You never see it, and it is redacted out
  of the page text, the field value, the final URL and any error you get back.
  A secret that does not exist fails the call rather than typing its own name
  into the field.
- **Irreversible actions need the owner's permission** — paying, ordering,
  deleting, publishing. If you are refused one, that is a settled answer, not a
  transient error: report what you were about to do and stop. Do not look for
  another route to the same click.
- **A wall is a finding, not an obstacle.** A captcha or bot check cannot be
  worked around; say so. A login wall is different and worth reporting
  separately — the owner can fix that one by storing credentials.
- **Waiting for a human step** — a bank push, a code — uses `browser_wait` with
  a `notify` message. The message reaches the owner immediately, which a `[CHAT]`
  block would not: that is only delivered when the run ENDS, which is after the
  thing you are waiting for.

Reading a page is cheap; driving one is slow and stateful. Fetch first, render
second, act only when the task genuinely requires it.

## Reporting

Give the answer first, then the sources as links. State what you could not confirm.
