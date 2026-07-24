# Agent templates — useful briefs + searchable gallery

**Date:** 2026-07-24
**Status:** approved

## Problem

The agent-creation start screen offers six templates whose descriptions are
one-line gestures — "gather a few numbers I care about", "keep an eye on a page
or feed I care about". They name no schedule, no notification behaviour, and no
service, so they hand the designer almost nothing.

That matters because of how the design conversation is bounded
(`internal/prompts/prompts.go`, `<conversation_discipline>`): the designer asks
**at most three questions total**, and considers itself ready to build once it
knows (a) what the agent does, (b) when it runs, (c) whether it notifies, and
(d) which outside accounts/services it needs. A vague template forces the user
to spend all three questions supplying basics — so picking a template saves
nothing over typing from scratch. The templates read as decorative.

A previous change made templates *functional* (picking one fills the
description; **Continue** now sends it as the opening message) but left the
content untouched. This spec fixes the content, and adds a gallery so a larger
library stays browsable.

## Goals

1. Every template is a **complete brief** that pre-answers (a)–(d), so the
   designer typically asks 0–1 questions instead of three.
2. What the user is expected to change is **obvious**.
3. A library of ~14 templates, discoverable through a searchable modal.

## Design

### Data model

`AgentTemplate` (`web/ui/src/pages/agents/templates.ts`) gains four fields:

```ts
export type TemplateCategory =
  | "Email & comms" | "Monitoring" | "Reports & tracking"
  | "Reminders" | "Research" | "Personal & ops";

export type AgentTemplate = {
  id: string;
  label: string;
  blurb: string;              // one-line card subtitle
  category: TemplateCategory; // groups the gallery; searchable
  keywords: string[];         // extra search terms ("context")
  featured: boolean;          // the 6 shown on the start screen
  description: string;        // the FULL brief, sent as the opening message
};
```

`start-from-scratch` stays as-is (empty `description`) and is handled as a
special case: it is featured, carries no category grouping in the gallery, and
is excluded from the structural content assertions.

### What a "full brief" must contain

Each non-scratch `description` is written in the user's own first-person voice
and must state:

- **what** the agent does, concretely (not "a few numbers I care about");
- **when** it runs — a real default schedule ("every weekday at 8:00am");
- **whether/how it notifies** — a real default, including the quiet case
  ("if nothing needs my attention, stay quiet");
- **which outside service** it touches, named plainly ("my email", "the page").

`[Square brackets]` mark **only** values that cannot have a sensible default —
an address, a URL, an account, a threshold. Everything else ships as a working
default, so an unedited template is still a valid, complete brief and nothing
breaks if the user changes nothing.

**Notification wording is platform-neutral** — "message me", never "message me
on Telegram". The install may be on Telegram, Discord, or Slack; the runtime
routes to whatever chat app is connected. This still answers (c) concretely
without presuming a platform.

Descriptions must keep passing the existing banned-jargon test
(`templates.test.tsx`): no `script`, `python`, `cron`, `file`, `json`,
`webhook`, `endpoint`, `api key`. Note `file` is banned — use "note"/"document".

Example (*Morning email digest*):

> Every weekday at 8:00am, look through the email that arrived in my [work
> inbox] since yesterday and pick out anything that genuinely needs my
> attention — things waiting on a reply or a decision from me. Ignore
> newsletters, receipts and automated notifications. Message me one short
> summary, grouped by how urgent it is, saying who it's from and what they
> want. If nothing needs my attention, stay quiet instead of sending an empty
> summary.

### The library (14 + scratch)

| Category | Templates |
|---|---|
| Email & comms | morning digest\*, reply-needed triage\*, follow-up chaser |
| Monitoring | page-change watch\*, uptime check, price/stock watch |
| Reports & tracking | weekly metrics report\*, daily note roll-up, subscription tracker |
| Reminders | meeting-prep briefing\*, deadline nudge with context |
| Research | topic news brief\*, competitor watch |
| Personal & ops | weekly review draft |

`*` = featured on the start screen (exactly 6), shown alongside
"Start from scratch".

### "View all templates" gallery

New component `web/ui/src/pages/agents/TemplateGallery.tsx`, built on the
existing `Dialog` primitives.

- The start screen renders a **View all templates** button next to the template
  grid heading, only when `AGENT_TEMPLATES.length > featured count` (so the
  button can never appear over an empty gallery).
- The modal has an autofocused search input. A query matches against **label,
  blurb, description, keywords, and category** — the "searchable by context and
  description" requirement. Matching is case-insensitive substring on the
  concatenation of those fields.
- Results are grouped under category headings, preserving library order within
  a group. Each row shows label, blurb, and a truncated description preview.
- An empty result set shows a "No templates match" state.
- Selecting a row calls back into the page's existing `selectTemplate()`, so
  the unsaved-custom-text confirmation guard still applies, then closes the
  modal.

### Interaction with existing behaviour

Unchanged and relied upon: picking a template fills the description field, and
**Continue** sends it as the design conversation's opening message. The richer
briefs are what turn that into a real saving — the designer now starts with
(a)–(d) already answered.

## Testing

`templates.test.tsx` (extended):
- existing banned-jargon check, across all templates;
- ids unique; exactly 6 featured; every non-scratch template has a category and
  at least one keyword;
- every non-scratch description states a schedule cue and a notification
  behaviour (asserted by matching a small vocabulary of time/notify phrases),
  and is substantially longer than the old one-liners;
- the existing auto-send test stays green.

`TemplateGallery.test.tsx` (new): opens from the button; filters by a word that
appears only in a description; filters by category/keyword ("context"); shows
the empty state; selecting a row fills the description and closes the modal.

## Out of scope

Structured template metadata that pre-fills a schedule picker or notification
toggle in the UI. Templates remain a text brief seeding the conversation — the
designer still owns schedule/notification decisions, which keeps one source of
truth for that logic.
