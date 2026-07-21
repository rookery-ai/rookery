// Static starting points for agent creation (spec §5, "Agent templates").
//
// These seed the CONVERSATION, not the code — the designer still generates
// everything (AGENT.md, tools, schedule). A template is just a well-phrased
// opening message that gives the designer a strong starting brief, so every
// `description` below says what the user WANTS, never how to build it. The
// non-technical register matters here as much as it does in the designer's
// own prompts (internal/prompts' jargon blocklist) — templates.test.tsx
// enforces this with a banned-word check.
//
// Chosen to span the three-tier agent taxonomy the prompts already encode
// (reasoning-only / one script / multi-file) rather than to cover every
// possible use case. "Start from scratch" keeps the blank field a first-class
// choice, not a fallback — its description is deliberately empty.
export type AgentTemplate = {
  id: string;
  label: string;
  blurb: string;
  description: string;
};

export const AGENT_TEMPLATES: AgentTemplate[] = [
  {
    id: "daily-digest",
    label: "Daily digest",
    blurb: "A morning summary of something you care about",
    description:
      "Every morning, look through my email from the last day and send me a short summary of anything that needs my attention. Skip newsletters and automated notifications.",
  },
  {
    id: "watch-for-changes",
    label: "Watch for changes",
    blurb: "Tell me when a page or feed changes",
    description:
      "Keep an eye on a page or feed I care about, and let me know only when something actually changes. Don't bother me with a check-in that finds nothing new.",
  },
  {
    id: "inbox-triage",
    label: "Inbox triage",
    blurb: "Sort through new mail and flag what matters",
    description:
      "Look through my new email throughout the day and tell me which messages actually need a reply or a decision from me, so I can ignore the rest.",
  },
  {
    id: "scheduled-report",
    label: "Scheduled report",
    blurb: "Pull numbers on a schedule and write them down",
    description:
      "On a schedule I choose, gather a few numbers I care about — like a metric, a count, or a balance — and keep a running note of them so I can see how they change over time.",
  },
  {
    id: "reminder-with-context",
    label: "Reminder with context",
    blurb: "Nudge me about something, with the latest details attached",
    description:
      "Remind me about something at the time I choose, and include the latest relevant details at that moment — not just a bare nudge, but what's actually going on right now.",
  },
  {
    id: "start-from-scratch",
    label: "Start from scratch",
    blurb: "Skip the templates and describe it yourself",
    description: "",
  },
];
