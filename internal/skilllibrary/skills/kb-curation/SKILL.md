---
name: kb-curation
description: Use this skill whenever writing, updating or organising files in the user's knowledge base — deciding where a note belongs, structuring it in clean markdown, linking it to related notes, and keeping memory files short. Triggers include "save this to my notes", "write this down", "update my notes on", "remember this", "organise my knowledge base", "format this file".
version: 1.0.0
license: MIT-0
category: Agent Behaviour
---

# Knowledge-Base Curation

The user's knowledge base (the "vault") is an Obsidian-style folder of interlinked
markdown notes. It is the durable record — write to it, don't just say things and
forget them. But it is also the user's own space: keep it tidy, or it stops being
useful.

## Where things live

- `notes/` — user-authored notes, journals, plans, todos. This is where MOST new
  content goes: research summaries, saved articles, project notes, anything the
  user asked you to "save" or "write down".
- `memory/USER.md` — the user's profile (name, location, role, background). Only
  durable facts about the user themselves.
- `memory/SOUL.md` — communication style and preferences ("keep replies short",
  "prefers metric units"). Only stable preferences, not one-off requests.
- `memory/GENERAL.md` — short quick-notes, the catch-all for small durable facts
  that don't fit USER.md or SOUL.md.
- `agents/<id>/` — an agent's OWN working area (its `AGENT.md`, `state.md`,
  `tools/`, its own `notes/` and `logs/`). Write here for the agent's private
  bookkeeping, not for content the user wants to find later.

**Off limits by convention:** `.kb/` (internal indexing data), `chats/` (reflected
transcripts, system-written), and other agents' `agents/<other-id>/` directories.
Don't write there and don't rely on their contents being stable.

## Choosing notes/ vs memory/

Ask: is this a durable fact ABOUT the user (memory/) or a piece of CONTENT the
user wants to keep (notes/)? "The user's dog is named Buddy" → memory/GENERAL.md.
"Here's a summary of that article about Buddy's breed" → notes/.

**Every file under `memory/` is injected into every single LLM context this
workspace runs — every chat, every design session, every scheduled agent.** That
budget is shared and finite. Keep memory files:

- Short — a handful of bullet points, not paragraphs.
- Factual — no narrative, no "on July 3rd the user mentioned...".
- Current — replace outdated facts, don't accumulate a changelog of them.

If you're tempted to write more than a few lines to a memory file, it belongs in
`notes/` instead, optionally linked FROM the memory file.

## Structuring a note

Use clean, minimal markdown:

```markdown
# Clear, Specific Title

One-line summary of what this note is, right under the title.

## Section

- Bullet points for lists
- Keep paragraphs short

## Related

- [[other-note-name]]
```

- One `#` heading per file, matching the title. `##` for sections below that.
- Prefer bullets and short paragraphs over dense prose — these notes get
  re-read by both the user and future agent runs, and both skim.
- Don't bury the point. Lead with the summary, put supporting detail after.

## Naming a note

**Name it for what it's ABOUT, not when it was written.** The date lives in the
file's metadata already; a filename like `2026-07-21.md` tells nobody anything a
month later. Prefer:

- `notes/vendor-contract-terms.md`, not `notes/2026-07-21.md`
- `notes/kitchen-remodel-budget.md`, not `notes/meeting-notes.md`

Exception: genuine journal/diary entries where the date IS the content's
identity — those may use a date, but even then prefer `notes/journal/2026-07-21.md`
(dated file inside a named folder) over a bare date at the top level.

## Linking notes together

Use `[[wikilink]]` syntax to reference another note by its name (no `.md`, no
path — just the note's base filename):

```markdown
See [[kitchen-remodel-budget]] for the cost breakdown.
```

Link liberally when two notes are genuinely related — that's what turns a folder
of files into a searchable, navigable knowledge base. Don't link to something
that doesn't exist yet; create the target note first (or leave a plain-text
mention until it does).

## Appending versus rewriting

- **Appending fits:** a running log, a list that grows over time, a journal, a
  running list of decisions. Add to the end (or under a dated subsection),
  leave the rest of the file untouched.
- **Rewriting fits:** a note that describes current state (a budget, a status,
  a plan) where old content is now WRONG, not just incomplete. Replace it —
  don't leave stale numbers sitting above the correction.

When unsure, check the note's existing shape: if it already reads like a log,
append; if it reads like a snapshot, rewrite the stale part.

## What NOT to do

- Don't create a new note for something that belongs in an existing one — check
  whether a relevant note already exists (list `notes/`, or search) before
  writing a duplicate.
- Don't write walls of unstructured text — even a quick save deserves a title
  and, if it's more than a couple of sentences, headings.
- Don't stuff long-form content into `memory/` because it felt important — a
  memory file that grows without bound degrades every future prompt.
- Don't hand-edit `state.md`'s ```` ```json ```` fence directly when writing
  notes about an agent's own run — that's the `[STATE]` protocol's job (see the
  `change-detection` and `resilient-runs` skills); `state.md`'s `## Notes`
  section is fine for human-facing prose.
