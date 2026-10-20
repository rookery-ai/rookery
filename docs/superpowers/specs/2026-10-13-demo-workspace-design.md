# Demo workspace and README screenshots — design

**Date:** 2026-10-13 · **Status:** approved in conversation, awaiting spec review

## Goal

Replace the current personal deployment with a clean install holding one
**Demo** workspace that shows Rookery at full strength, and capture README
screenshots from it: the two hero shots already specced in `README.md`'s
commented slot, plus a ~6-shot tour gallery, each in light and dark.

Success means: every screenshot shows real product output produced through real
flows (no database inserts, no mocked UI), no personal data of the owner appears
anywhere, and we hold a written record of how both designers behaved on the
weak-model tier.

## Decisions

| Decision | Choice |
|---|---|
| Current deployment | **Destroyed permanently.** One final `rookery backup` is taken first and the `.rkb` moved to `~/rookery-pre-demo.rkb` (outside the data dir) as the only rollback. |
| Coder | **DeepSeek Flash via OpenRouter** (`api` kind) for builds, runs and chat. Deliberately the weak tier: observing the designer there is a goal. |
| Persona | **Alex Morgan**, freelance product engineer in **Lisbon** (`Europe/Lisbon`), small consulting practice. Entirely fictional. |
| Connections | Keyless public APIs, Google (re-consented on a throwaway account), paste-key SaaS on free tiers (GitHub, Todoist), Telegram (new demo bot). |
| MCP | Not shown. |
| Hero shot | **Refuse, then grant** — two runs of the same browser agent. |
| Deliverable | Two hero shots + tour gallery, light and dark via `<picture>`. |
| Designer driving | **Playwright** drives the agent and skill designers through the real SPA; every turn is logged. |

## Constraints

- Agents and skills are created **only** through the designers.
- Everything else is seeded through product flows: setup wizard, KB upload, KB
  editor API, natural-language reminders, chat turns, manual and scheduled runs.
- Throwaway / sandbox accounts only. Test card data only — a screenshot cannot
  be redacted.
- Before `rm -rf ~/.rookery`, stop and get the owner's explicit go-ahead
  (CLAUDE.md rule 2).

## Correction to the README hero-shot comment

The commented slot describes an agent that "filled the card fields from stored
secrets, then stopped before paying and asked for approval". That is not how the
guard works (`internal/browser/policy.go`, `judgeIrreversible`): **without**
`browser_irreversible`, *any* mutating action on a page whose path reads as
checkout/payment is refused — including typing into a card field — and per-click
human approval is explicitly "not built". **With** the grant, the agent fills
and pays. The README comment and alt text are rewritten to describe what the
shots actually show (refusal + permission card, then a granted run).

## Agents

Each card: the opening prompt typed into the designer, follow-up answers to give
if asked, the feature(s) showcased, what to expect, and what failure looks like
on DeepSeek Flash. Follow-ups are answered verbatim by the Playwright driver
when the designer asks the matching question; anything unanticipated is
answered by hand and logged.

### A1 · Toolshop restock (hero)

- **Prompt:** "On the 1st of every month, log into my Toolshop account at
  https://practicesoftwaretesting.com, add 2× Combination Pliers and 1× Claw
  Hammer to the cart, and check out with my saved credit card. Tell me the
  order total and the invoice number."
- **Follow-ups:** login is in secrets `TOOLSHOP_EMAIL`/`TOOLSHOP_PASSWORD`; card
  is `CARD_HOLDER`, `CARD_NUMBER`, `CARD_EXPIRY`, `CVV`; notify me every time;
  yes, it may place the order.
- **Shows:** browser acting, `${SECRET}` placeholders, the irreversible guard,
  **Allow and build**, the agent-page permission card, `browser_wait` notify.
- **Expect:** designer's spec carries `Irreversible actions:`, the build bar
  reads **Allow and build** (→ hero shot 2). Build's dry run is refused at the
  irreversible step (build phase always refuses). After save, withdraw the
  permission on the agent page; **run 1** reaches `/checkout`, is refused at the
  first card-field fill, reports it. Re-grant; **run 2** completes the sandbox
  order (→ hero shot 1, its run timeline).
- **Failure on weak model:** designer refuses ("agents can't click"), omits the
  `Irreversible actions:` line (no Allow-and-build — then the shot comes from a
  re-run of the design turn), or the agent loses its way in the Angular SPA.
- **Preparation:** register a fresh Toolshop account via Playwright; generate a
  well-formed test card. Check Toolshop's checkout path still contains
  `checkout` before the runs.

### A2 · Weekend in Lisbon

- **Prompt:** "Every Friday at 5pm, check the weekend forecast for Lisbon and
  suggest one outdoor plan and one rainy-day plan from my 'Lisbon places' note."
- **Follow-ups:** send it to me on Telegram; keep it short.
- **Shows:** keyless connector (Open-Meteo), KB read, TIER 1, timezone-aware
  schedule (`0 17 * * 5` in `Europe/Lisbon`), Telegram + inbox delivery.
- **Expect:** `# Connections: open_meteo`, no script, dry run shows a real
  forecast.
- **Failure:** writes a script for a TIER 1 job; schedule converted to UTC.

### A3 · Self-hosting digest

- **Prompt:** "Every weekday at 8am, find new Hacker News stories about
  self-hosting or local-first software, read the articles, and save a short
  digest note to my knowledge base. Only tell me if something is really worth
  reading."
- **Follow-ups:** save under `digests/`; at most 5 stories; skip ones you've
  already covered.
- **Shows:** `web_search` (Brave), Hacker News connector, `browser_read`, KB
  writes, `state.md` seen IDs, **silent runs** (Silent chip in run history).
- **Expect:** digest notes accumulate in `digests/`; most runs `[SILENT]`.
- **Failure:** notifies every run; state not persisted (re-reports same
  stories).

### A4 · Morning brief

- **Prompt:** "Weekdays at 8:30, look at my unread Gmail from the last day and
  today's Google Calendar. Tell me which emails need a reply, and draft replies —
  don't send — for anything from a client."
- **Follow-ups:** clients are listed in my `clients/` notes.
- **Shows:** Google OAuth (Gmail + Calendar), multiple bound connections, drafts
  vs `public_write`, KB-informed judgement.
- **Expect:** `# Connections:` binds `google` (Gmail) + `google_calendar`; drafts appear
  in the throwaway Gmail.
- **Preparation:** seed the throwaway Gmail with ~6 realistic emails (two from
  "client" addresses) and its calendar with 3 events.

### A5 · Issue → task

- **Prompt:** "When someone opens a new issue on my GitHub repo
  alexmorgan-dev/tidy-invoice, summarize it and add a Todoist task in my 'Open
  source' project."
- **Follow-ups:** check every hour; don't notify me, the task is enough.
- **Shows:** two paste-key connectors chained, auto-bind, change detection.
- **Expect:** bindings to `github` + `todoist`; one task per new issue.
- **Preparation:** the user creates the repo (actual name per their throwaway
  account) with 2–3 issues, and a Todoist project "Open source".

### A6 · Framework price watch

- **Prompt:** "Check the price of the Framework Laptop 13 DIY Edition on
  frame.work every morning and tell me only if it drops."
- **Shows:** `browser_read` on a JavaScript site, `change-detection` core skill,
  mostly silent runs.
- **Failure:** site blocks headless Chromium → `Classify` reports bot-check.
  Fallback target: another JS-rendered product page decided at build time.

### A7 · Monthly expenses

- **Prompt:** "On the 2nd of each month, read my expenses table, total last month
  by category, compare it with the month before, convert USD invoices to EUR at
  the current rate, and write a summary note."
- **Follow-ups:** the table is `finance/expenses-2026`; use my
  pt-expense-rules skill for categories.
- **Shows:** `kb_table_query` / `kb_file_map`, Frankfurter connector, a **user
  skill** attached (S2), KB write.
- **Expect:** `# Skills: pt-expense-rules`, summary note in `finance/`.

## Skills (skill designer)

- **S1 · meeting-notes** — prompt: "Turn a raw meeting transcript into my notes
  template: attendees, decisions, action items with owners and due dates, open
  questions. Save it to clients/<client>/meetings/." Shows: the skill designer
  FSM, vetting verdict.
- **S2 · pt-expense-rules** — prompt: "Rules for categorising my Portuguese
  business expenses: IVA rates (23/13/6%), which categories are deductible for a
  freelancer, and how to label software, travel, equipment and meals." Shows:
  a knowledge-only skill reused by an agent (A7).

## Seed data

All source files authored in the scratchpad, then uploaded through the UI/API.

- **Setup wizard:** owner, workspace **Demo** with the Rookery-mark icon,
  profile (Alex Morgan, Lisbon, `Europe/Lisbon`, English, concise) → seeds
  `memory/ABOUT.md` + `STYLE.md`.
- **KB uploads:** `finance/expenses-2026.csv` (~120 rows, Jul–Sep, EUR + USD,
  categories matching S2); `clients/Northwind — Statement of Work.pdf`;
  `clients/Brand refresh brief.docx` (with an embedded image).
- **KB notes (editor API):** `travel/Lisbon places`, `travel/Sintra day trip`,
  `clients/Northwind`, `clients/Halcyon Labs`, `notes/Weekly review`,
  `notes/Q4 goals`, `notes/Home lab` (callout, toggle, columns, resized image,
  table) — interlinked with wikilinks.
- **Reminders** (typed in natural language): "tomorrow at 9am send the Q3
  invoice to Northwind", "next Monday at noon renew alexmorgan.dev", "on Friday
  at 6pm book the Sintra train", plus two short-fuse ones that fire on day 1 so
  they reach the inbox.
- **Chats (Playwright):**
  1. "How much did I spend on software subscriptions in September vs August?"
  2. "What's the weather this weekend, and what do my notes say about Sintra?"
  3. "Read https://frame.work/products/laptop13-diy-intel-ultra-1 and tell me
     what the DIY edition starts at."
  4. "Edit with AI" on `notes/Q4 goals` ("tighten this into three goals").
- **Run history:** manual run of every agent on day 1; schedules fire days 2–3.

## Secrets and accounts

Supplied by the user: OpenRouter API key, Brave Search API key, Telegram bot
token (new demo bot), Google OAuth client ID + secret and consent from a
throwaway Google account (from `http://localhost:8899`, directly or via
`ssh -L 8899:localhost:8899`), GitHub PAT + public repo with issues (throwaway),
Todoist API token (throwaway).

Generated here: owner password, workspace master password, Toolshop account and
`TOOLSHOP_*` secrets, test-card `CARD_*`/`CVV` secrets, keyless connections
(Open-Meteo, Wikipedia, Hacker News, Frankfurter, OpenStreetMap).

## Sequence

1. **Teardown.** `rookery backup` → move `.rkb` to `~/rookery-pre-demo.rkb`;
   `make stop`; **confirm with the owner**; `rm -rf ~/.rookery`; `make deploy`
   from `main`; assert `/healthz` reports `95d1a9c` (or newer `main`);
   `rookery owner bootstrap`.
2. **Day 1.** Wizard → secrets → connections (user does OAuth + pasted keys) →
   KB seed → S1, S2 → A1…A7 → manual runs → A1 refuse/grant pair → chats →
   reminders.
3. **Days 2–3.** Scheduled runs accrue; daily health check of runs and the
   inbox (fix nothing in product code as part of this work — defects found are
   recorded and filed separately).
4. **Capture** (day 3). Playwright script in the scratchpad, viewport 1440×900,
   `device_scale_factor=2`, each shot in light and dark, saved to
   `docs/assets/screenshots/<name>-{light,dark}.png`, compressed with
   `pngquant` if available. Shots: `run-timeline` (A1 run 2),
   `build-conversation` (A1 with Allow and build), `home`, `kb-note`, `chat`,
   `connections`, `skills`, `agent`.
5. **README.** Fill both hero slots, rewrite their comment/alt text per the
   correction above, add a `<picture>` tour gallery; run the `docs-sync` skill;
   open a PR from this branch.

## Designer behaviour log

`docs/superpowers/specs/2026-10-13-demo-designer-log.md` (written during the
work): per agent/skill — turns exchanged, questions asked, whether the
`[TECHNICAL SPEC]` / Allow-and-build appeared, build duration, `build_id`,
retries, dry-run outcome, and any defect worth filing.

## Risks

- **Weak-model builds** may need retries or rephrased prompts; a rephrase is
  logged, never hidden.
- **Toolshop** is a shared public sandbox and may change or go down; check on
  the day of the A1 runs.
- **frame.work** may block headless Chromium (A6 fallback above).
- **Screenshots and privacy:** every capture is reviewed for real names, emails,
  tokens and hostnames before commit.

## Out of scope

MCP servers; any product-code change; the documentation-site repository beyond
what `docs-sync` requires.
