// Starting points for agent creation (spec §5, "Agent templates").
//
// These seed the CONVERSATION, not the code — the designer still generates
// everything (AGENT.md, tools, schedule). But a template only EARNS its place
// if it saves the user real work, and that is decided by how the design
// conversation is bounded (internal/prompts' <conversation_discipline>): the
// designer asks AT MOST THREE questions, and considers itself ready to build
// once it knows (a) what the agent does, (b) when it runs, (c) whether it
// notifies, and (d) which outside accounts/services it needs.
//
// So every `description` below is a COMPLETE BRIEF that pre-answers (a)-(d) in
// the user's own voice. A one-line gesture ("gather a few numbers I care
// about") forces the user to spend all three questions on basics, which is
// exactly why the earlier version of this file was decorative rather than
// useful.
//
// THE PLATFORM HAS NO EVENT TRIGGERS. Agents are run by a cron scheduler
// (internal/scheduler polls agent_schedules and fires the runner); there is no
// webhook, push, or "when X happens" hook, and the chat adapters are
// deliberately outbound-only with zero inbound port. So a brief must NEVER be
// phrased as event-driven — "as soon as an email arrives", "the moment it goes
// down", "30 minutes before each meeting" all describe a trigger that cannot
// exist. Express the same intent as POLLING plus REMEMBERED STATE:
//
//   bad:  "30 minutes before each meeting, message me a briefing"
//   good: "every 10 minutes, look for meetings starting in the next 30 minutes
//          that you haven't briefed me on yet, and message me about those"
//
// The "haven't ... yet" half matters as much as the cadence: a polling agent
// re-sees the same item on every run, so without remembered state it would
// notify repeatedly. templates.test.tsx enforces both halves.
//
// Conventions, all load-bearing:
//   - [Square brackets] mark ONLY values that cannot have a sensible default —
//     an address, a URL, an account, a threshold. Everything else ships as a
//     working default, so an unedited template is still a valid brief and
//     nothing breaks if the user changes nothing.
//   - Notification wording is PLATFORM-NEUTRAL ("message me", never "message me
//     on Telegram"): the install may be on Telegram, Discord or Slack and the
//     runtime routes to whatever chat app is connected.
//   - Every brief states the quiet case explicitly, so a "nothing happened" run
//     doesn't notify. Without this agents default to chatty.
//   - The non-technical register matters as much as in the designer's own
//     prompts (internal/prompts' jargon blocklist) — templates.test.tsx
//     enforces this with a banned-word check. Note "file" is banned: say
//     "note" or "document".
export type TemplateCategory =
  | "Email & comms"
  | "Monitoring"
  | "Reports & tracking"
  | "Reminders"
  | "Research"
  | "Personal & ops";

export type AgentTemplate = {
  id: string;
  label: string;
  blurb: string;
  // Groups the gallery and participates in search, so a user can find a
  // template by the KIND of job it does, not just its wording.
  category: TemplateCategory;
  // Extra search terms ("context") that don't appear in the prose — the words
  // someone would actually type looking for this ("gmail", "downtime", "kpi").
  keywords: string[];
  // The six shown on the start screen; the rest live behind "View all
  // templates". Kept as a flag rather than a slice of the array so reordering
  // the library can't silently change which ones are promoted.
  featured: boolean;
  description: string;
};

export const AGENT_TEMPLATES: AgentTemplate[] = [
  // ── Email & comms ────────────────────────────────────────────────────────
  {
    id: "daily-digest",
    label: "Morning email digest",
    blurb: "A weekday summary of the mail that actually needs you",
    category: "Email & comms",
    keywords: ["email", "inbox", "gmail", "summary", "morning", "digest"],
    featured: true,
    description:
      "Every weekday at 8:00am, look through the email that arrived in my [work inbox] since yesterday " +
      "and pick out anything that genuinely needs my attention — things waiting on a reply or a decision " +
      "from me. Ignore newsletters, receipts and automated notifications. Message me one short summary, " +
      "grouped by how urgent it is, saying who each one is from and what they want. If nothing needs my " +
      "attention, stay quiet instead of sending me an empty summary.",
  },
  {
    id: "inbox-triage",
    label: "Reply-needed triage",
    blurb: "Flags through the day which messages need an answer",
    category: "Email & comms",
    keywords: ["email", "inbox", "triage", "reply", "priority", "sort"],
    featured: true,
    description:
      "Three times a day — 9:00am, 1:00pm and 5:00pm on weekdays — check my [work inbox] for mail that " +
      "arrived since the last check, and work out which messages actually need a reply or a decision from " +
      "me. Leave out anything automated, promotional, or purely informational. Message me a short list of " +
      "just the ones that need me, each with the sender, what they're asking, and how urgent it looks. " +
      "Don't repeat a message you've already flagged. If there's nothing that needs me, stay quiet.",
  },
  {
    id: "follow-up-chaser",
    label: "Follow-up chaser",
    blurb: "Reminds you about mail you sent that nobody answered",
    category: "Email & comms",
    keywords: ["email", "follow up", "chase", "waiting", "unanswered", "nudge"],
    featured: false,
    description:
      "Every weekday at 5:00pm, look through the email I've SENT from my [work inbox] and find the ones " +
      "that got no reply after [3] days and look like they were expecting one — a question I asked, " +
      "something I needed signed off, a request I made. Skip anything that clearly didn't need an answer. " +
      "Message me the list with who I wrote to, when, and a one-line reminder of what I was waiting on, so " +
      "I can decide whether to chase. Remember which ones you've already told me about and don't repeat " +
      "them. If nothing is outstanding, stay quiet.",
  },

  // ── Monitoring ───────────────────────────────────────────────────────────
  {
    id: "watch-for-changes",
    label: "Page-change watch",
    blurb: "Tells you when a page you care about actually changes",
    category: "Monitoring",
    keywords: ["website", "page", "watch", "change", "diff", "monitor", "url"],
    featured: true,
    description:
      "Check [https://example.com/the-page-I-care-about] once an hour and tell me when something " +
      "MEANINGFUL changes on it — new or edited wording, a new item in a list, a changed date or price. " +
      "Ignore noise like rotating adverts, view counters, timestamps and cookie banners. When something " +
      "real changes, message me what changed, quoting the before and after so I can see it at a glance. " +
      "Remember what the page looked like last time so you only report genuine differences, and stay quiet " +
      "on any check that finds nothing new.",
  },
  {
    id: "uptime-check",
    label: "Uptime check",
    blurb: "Checks a site every few minutes and flags an outage",
    category: "Monitoring",
    keywords: ["uptime", "downtime", "health", "monitor", "outage", "status", "url"],
    featured: false,
    description:
      "Check that [https://example.com] is responding properly every 10 minutes. On the first check that " +
      "finds it down or returning an error, message me with what you saw and the time. Don't keep " +
      "messaging me about the same outage — remember that you've already reported it, tell me once when it " +
      "starts, then once more on the check where it comes back up, including how long it was down for. " +
      "Treat a single failed check as possibly a blip: confirm it's really down on the following check " +
      "before messaging me. Stay quiet the whole time it's healthy.",
  },
  {
    id: "price-watch",
    label: "Price & stock watch",
    blurb: "Watches an item and pings you on a drop or restock",
    category: "Monitoring",
    keywords: ["price", "stock", "deal", "shopping", "restock", "alert", "product"],
    featured: false,
    description:
      "Check [the product page URL] twice a day, at 9:00am and 6:00pm, and keep track of its price and " +
      "whether it's in stock. Message me when the price drops below [my target amount], when it's cheaper " +
      "than the last time you checked, or when it comes back into stock after being unavailable. Include " +
      "the current price, what it was before, and the link. Keep a running note of the prices you've seen " +
      "so I can tell whether a 'sale' is genuine. Stay quiet when nothing has changed.",
  },

  // ── Reports & tracking ───────────────────────────────────────────────────
  {
    id: "scheduled-report",
    label: "Weekly metrics report",
    blurb: "Collects your numbers weekly and tracks the trend",
    category: "Reports & tracking",
    keywords: ["report", "metrics", "kpi", "numbers", "weekly", "dashboard", "trend"],
    featured: true,
    description:
      "Every Monday at 9:00am, collect [the numbers I want to track — for example signups, revenue, open " +
      "support tickets] and record them in a running note in my knowledge base, one row per week so the " +
      "history builds up over time. Then message me a short summary: each number, how it moved versus last " +
      "week as a value and a percentage, and a one-line note on anything that moved unusually sharply. " +
      "Always send this one even if the numbers are flat — it's a scheduled report, not an alert.",
  },
  {
    id: "note-rollup",
    label: "Daily note roll-up",
    blurb: "Summarises each day's notes into a dated log",
    category: "Reports & tracking",
    keywords: ["notes", "journal", "summary", "daily", "log", "knowledge base", "review"],
    featured: false,
    description:
      "Every evening at 7:00pm, look at the notes in my knowledge base that I created or changed today, " +
      "and write a short dated entry in a daily log note summarising what I worked on, any decisions I " +
      "recorded, and anything that reads like an open question or a next step. Link the entry back to the " +
      "notes it came from so I can jump to them. This is for my own record — don't message me about it, " +
      "just keep the log up to date. If I didn't touch any notes today, skip the day entirely.",
  },
  {
    id: "subscription-tracker",
    label: "Subscription & spend tracker",
    blurb: "Monthly look at recurring charges and what changed",
    category: "Reports & tracking",
    keywords: ["subscriptions", "spend", "billing", "recurring", "money", "budget", "charges"],
    featured: false,
    description:
      "On the 1st of each month at 10:00am, go through [where my receipts and billing emails arrive] and " +
      "pull out the recurring charges from the last month — the subscriptions and repeat payments. Keep a " +
      "running note listing each one with its amount and when it was charged, so I can see the history " +
      "month to month. Message me the monthly total plus anything notable: a new subscription that wasn't " +
      "there before, one whose price went up, or one I was charged for twice. Always send it, even in a " +
      "quiet month, and say plainly if nothing changed.",
  },

  // ── Reminders ────────────────────────────────────────────────────────────
  {
    id: "meeting-prep",
    label: "Meeting-prep briefing",
    blurb: "Checks your calendar and briefs you ahead of meetings",
    category: "Reminders",
    keywords: ["calendar", "meeting", "prep", "briefing", "agenda", "before", "context"],
    featured: true,
    description:
      "Every 10 minutes between 8:00am and 6:00pm on weekdays, check my calendar for meetings that start " +
      "within the next 30 minutes, have other people in them, and that you haven't already briefed me on. " +
      "For each of those, message me a short briefing: who's attending, the title and any agenda or " +
      "description on the invite, plus anything I already have on those people or that topic in my notes " +
      "or recent email — what we last discussed and anything I said I'd follow up on. Keep it to a few " +
      "lines I can read on the way in. Remember which meetings you've already briefed me on so I get " +
      "exactly one briefing per meeting, never a repeat on the next check. Skip events I'm the only " +
      "attendee of, all-day entries, and anything marked as blocked-out or personal time. Stay quiet when " +
      "nothing is starting soon.",
  },
  {
    id: "reminder-with-context",
    label: "Deadline nudge with context",
    blurb: "Warns you before things are due, with their latest state",
    category: "Reminders",
    keywords: ["deadline", "due", "reminder", "tasks", "nudge", "overdue", "tracker"],
    featured: false,
    description:
      "Every weekday at 8:30am, check [where I track my tasks and deadlines] for anything due in the next " +
      "[3] days or already overdue. Message me the list, soonest first, and for each one include its " +
      "current status and anything that's changed since I set it — so it's a useful nudge rather than a " +
      "bare reminder. Call out anything overdue clearly at the top. Don't nag me about the same item more " +
      "than once a day. If nothing is due or overdue, stay quiet.",
  },

  // ── Research ─────────────────────────────────────────────────────────────
  {
    id: "topic-news",
    label: "Topic news brief",
    blurb: "A short morning briefing on a subject you follow",
    category: "Research",
    keywords: ["news", "research", "topic", "briefing", "web", "search", "headlines"],
    featured: true,
    description:
      "Every morning at 7:30am, search the web for what's new on [the topic I want to follow] in the last " +
      "24 hours, and message me the 3 to 5 most genuinely interesting items. For each, give me a one-line " +
      "summary in plain language, why it matters, and the link. Prefer primary sources and skip anything " +
      "that's just a rewrite of a story you already sent me. Remember what you've sent before so the " +
      "briefing doesn't repeat itself. If there's genuinely nothing worthwhile, say so in one line rather " +
      "than padding the list.",
  },
  {
    id: "competitor-watch",
    label: "Competitor watch",
    blurb: "Weekly check on what another company announced",
    category: "Research",
    keywords: ["competitor", "company", "product", "launch", "announcement", "market", "weekly"],
    featured: false,
    description:
      "Every Monday at 10:00am, check [the company's website and blog] for anything they've announced or " +
      "published since last week — new products, pricing changes, major posts. Message me a short summary " +
      "of each, with what it is, why it might matter to us, and the link. Keep a running note of their " +
      "announcements over time so I can look back at how they've moved. Ignore minor wording tweaks and " +
      "routine reposts, and stay quiet in a week where they announced nothing.",
  },

  // ── Personal & ops ───────────────────────────────────────────────────────
  {
    id: "weekly-review",
    label: "Weekly review draft",
    blurb: "Drafts your Friday review from the week's activity",
    category: "Personal & ops",
    keywords: ["review", "weekly", "retrospective", "planning", "friday", "summary", "reflection"],
    featured: false,
    description:
      "Every Friday at 4:00pm, pull together what happened this week from my notes and my calendar — what " +
      "I finished, what I decided, the meetings I had, and anything I noted as still open — and write it " +
      "into a dated weekly review note in my knowledge base. Structure it as: what got done, what's still " +
      "open, and a few suggested focuses for next week based on what's outstanding. Then message me a " +
      "three-line version so I can read it without opening the note. Always run it, even in a quiet week.",
  },

  // ── Escape hatch (kept last; not a real template) ─────────────────────────
  {
    id: "start-from-scratch",
    label: "Start from scratch",
    blurb: "Skip the templates and describe it yourself",
    category: "Personal & ops",
    keywords: ["blank", "custom", "own", "scratch"],
    featured: true,
    // Deliberately empty — a blank field is a first-class choice, not a
    // fallback. The gallery and the content assertions both special-case this.
    description: "",
  },
];

// The id of the escape-hatch entry, referenced by the page (which keeps it out
// of the gallery's category listing) and by the tests (which exempt it from the
// "must be a full brief" assertions). Named rather than repeated as a literal
// so the special-casing is greppable from one place.
export const SCRATCH_TEMPLATE_ID = "start-from-scratch";

/** The templates promoted onto the start screen. */
export function featuredTemplates(): AgentTemplate[] {
  return AGENT_TEMPLATES.filter((t) => t.featured);
}

// Matches a template against a free-text query, searching label, blurb,
// category, keywords AND the full description — so a user can find a template
// by the kind of job it does ("downtime"), by a word only its brief contains
// ("newsletters"), or by its category, not just by its title.
export function templateMatches(t: AgentTemplate, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  const haystack = [t.label, t.blurb, t.category, t.keywords.join(" "), t.description]
    .join(" ")
    .toLowerCase();
  // Every whitespace-separated term must appear, so a two-word query narrows
  // rather than widens (a plain substring test on the whole query would miss
  // "email weekly", where the words are far apart in the brief).
  return q.split(/\s+/).every((term) => haystack.includes(term));
}
