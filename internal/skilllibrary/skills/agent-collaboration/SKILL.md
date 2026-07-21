---
name: agent-collaboration
description: Use this skill when one agent needs to invoke another to do part of its job, or when deciding whether a task should be one agent or several. Covers the call protocol, depth limits, and when splitting work across agents helps versus hurts. Triggers include "have it call", "use the other agent", "should this be two agents", "reuse that agent".
version: 1.0.0
license: MIT-0
category: Agent Behaviour
---

# Agent Collaboration

Agents on this platform can invoke each other synchronously. Used well, this
lets a genuinely reusable capability live in one place instead of being
copy-pasted into every agent that needs it. Used badly, it turns one simple
task into a fragile chain of hops for no reason.

## The `[CALL: <agent-name>]` marker

Emit `[CALL: <agent-name>]` to invoke another agent by name. The call is
**synchronous** — your run pauses, the called agent runs to completion, and
its output comes back into YOUR context before you continue:

```
[CALL: expense-categorizer]
```

Whatever the called agent produces becomes available to you to reason over and
incorporate into your own output — you are still the one who decides what, if
anything, to tell the user; the called agent's own notification behavior
(see below) is separate from that.

## Depth limit and cycles

Calls can nest — agent A calls B, which calls C — but the platform enforces a
**maximum depth of 3** and detects cycles (A calling B calling A). Design
around this:

- Don't build a call chain more than a couple of levels deep; if a task needs
  that many hops, it's a sign the work should be restructured, not chained.
- Never have an agent call another agent that might call it back, even
  indirectly — the platform will catch the cycle and abort, but the run has
  already failed by that point. Think through the call graph before shipping,
  don't rely on the guard to catch a design mistake.

## When splitting work across agents is RIGHT

- **A genuinely separate schedule.** One agent checks email hourly; a
  different agent compiles a weekly digest. Different cadences are different
  agents — don't force a weekly job to also handle hourly polling just
  because they're related.
- **A reusable capability.** If the same piece of logic ("categorize this
  expense", "look up this contact's company") is needed by more than one
  agent, build it once as its own agent and have others call into it, rather
  than duplicating the logic in each.

## When splitting is WRONG

- **One task, artificially split.** If a job is "fetch the data, then
  summarize it, then send it" and nothing about those steps runs on a
  different schedule or gets reused elsewhere, that's ONE agent doing three
  steps — not three agents calling each other. A call adds a real hop (state,
  latency, another point of failure) for zero benefit if the steps always run
  together for one purpose.
- **Splitting to work around context size.** If the real problem is that one
  script does too much reasoning in one place, restructure the tiers (see the
  agent-philosophy guidance baked into every design conversation — reasoning
  vs. one script vs. multi-file) before reaching for a second agent.
- **A call chain that exists only to pass data along.** If B does nothing but
  immediately hand off to C, B probably shouldn't exist — collapse it.

Default to ONE agent. Only split when you can name the SEPARATE reason (its
own schedule, or genuine reuse from more than one caller) — "this felt like a
distinct step" is not that reason.

## Notification behaviour is per-agent

A called agent's `[CHAT]`/`[SILENT]` decisions are its OWN — calling another
agent does not automatically suppress or forward its notifications, and the
caller does not automatically inherit them. If agent B sends its own
notification as part of being called, the user may get two messages (B's, and
whatever A decides to send afterward). Design deliberately:

- If B should never notify on its own when called this way, its own
  instructions need to say so (or B needs a way to know it's being invoked as
  a helper vs. running on its own schedule).
- If A wants to relay B's result rather than let B speak for itself, A should
  incorporate B's returned output into A's OWN `[CHAT]`, and B should go
  `[SILENT]` for that path.

Don't assume silence or forwarding happens automatically — write the intended
behaviour into both agents' instructions.
