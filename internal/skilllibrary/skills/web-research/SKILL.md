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
- A page that comes back nearly empty is usually JavaScript-rendered. Switch to the
  playwright-browser skill rather than retrying the fetch.
- Extract what was asked for and stop. Dumping the whole page into your reasoning wastes
  the run and truncates the real answer.

## Reporting

Give the answer first, then the sources as links. State what you could not confirm.
