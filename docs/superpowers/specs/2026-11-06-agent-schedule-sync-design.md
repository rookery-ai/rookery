# Keep every statement of an agent's schedule in step with the schedule

## Problem

The Schedule card on the agent page writes `agent_schedules` and nothing else. The agent runs at the
new time, but the agent's `AGENT.md` keeps announcing the old one, and so do the other places derived
from it. Remove has the same defect, so a file can name a cron for an agent that has no schedule at all.

An owner who changes a schedule expects the page to agree with itself afterwards.

## Where a schedule is stated

| Surface | Treatment | Why |
|---|---|---|
| `agent_schedules` row | written first | it decides what actually runs |
| `# Suggested schedule:` header | rewritten in code | fixed format, no judgement needed |
| The old cron quoted in the body, e.g. `` `0 7 * * 2` `` | rewritten in code, whole token only | exact text; `10 9 * * *` must survive replacing `0 9 * * *` |
| Sentences: "Every Tuesday at 7am", "on the 3rd of the month" | a model proposes exact edits | free-form and in any language |
| `agents.description` | re-derived when it is still derived from the file | it is the file's first paragraph, so it often states timing |
| `agent_drafts.pending_agent_md` of this agent's edit draft | header and quoted cron rewritten | approving the edit would otherwise restore the old schedule through `reconcileScheduleOnSave` |

Left alone: `state.md` (the agent's own memory), `tools/`, run notes and audit logs (records of what
happened), and the conversation history of a draft.

## Behaviour

**Saving and removing.** The row commits first. The file edits follow, and a failure there is reported
rather than failing the save, because the schedule has already changed. The model step runs in the
background after the response, since a CLI coder can take minutes and a coder that is down must not
stop an owner changing a schedule.

**The model proposes, the server decides.** A whole-file rewrite could silently drop a step from an
agent's instructions, so the model never returns a file. It returns exact `{find, replace}` pairs, and
an edit is applied only when:

- `find` is one line of text and occurs exactly once;
- it does not touch a header line (`# Suggested schedule:`, `# Skills:`, `# Connections:`, `# MCP:`,
  `# Irreversible actions:`, `# Required secrets:` and its items) or a `[CALL:` line;
- `replace` is one line, stays proportionate in length, and adds no protocol marker;
- the batch is at most ten edits, and the resulting file passes `CheckEthics`.

The prompt limits edits to statements of when the agent runs. Instructions whose logic depends on the
old timing ("compare with yesterday's run") are not edited; the model names them in `review` and the
card shows them for the owner to judge. Unparseable output changes nothing and is reported.

**The pass is idempotent.** It is told the schedule the file should state, not the difference from an
old one. A later save cancels an earlier pass, so saving A, B, then C still converges on C. Pressing
Save with an unchanged value re-runs it, which is also how a file that drifted before this change
gets repaired.

**Reporting.** `GET /agents/:id` gains `schedule_sync`: `null`, or a state (`running`, `done`,
`failed`), the edits applied, lines to review, the number of edits refused, and an error. It is held
in memory until the next save or a restart, so an owner who left the page can still read it. The page
polls while it is `running`. The audit log records the count.

**Open edit session.** A live session editing this agent holds its own `AGENT.md` and would save the
old schedule back, so a schedule change returns 409 `design_in_progress`. This is scoped to that agent:
another agent's design session cannot overwrite this schedule, so it refuses nothing.

## The other doors into `AGENT.md`

**The AGENT.md card** is an explicit save by the owner. If its header differs from the header on disk,
that is a schedule change: a valid cron or `none` is applied exactly as the Schedule card would apply
it, and an invalid one is a 400. If it matches the disk, or is absent, the file is written with the
header the row dictates, and the row does not move. Comparing with the disk rather than with the row
is what stops saving an unrelated edit in a drifted file from re-timing the agent. A card already
being edited adopts a new header line when the schedule changes underneath it.

**The KB editor** autosaves, so its header is never taken as a schedule change: a stale tab must not
re-time an agent. A save to `agents/<id>/AGENT.md` of an agent in this workspace has its header set
from the row.

## Non-goals

- A startup backfill of existing files. It would edit files nobody asked to change. A file repairs
  itself on the next schedule save or edit.
- Surviving a restart mid-pass. A pass lost that way leaves the deterministic edits in place and is
  repaired by saving again.
- Writes by an agent run or chat to its own `AGENT.md`. Those are not schedule changes.

## Residual risk, stated

A model can reword a sentence that was about something else ("hourly figures" read as timing). The
guards bound the damage to single lines and every applied edit is shown to the owner with its original
text. A draft whose history names the old schedule may lead a later rebuild to write it back; the
review step shows the header before it is saved. A KB tab left open and dirty on `AGENT.md` can
autosave old sentences over a pass's edits; its header survives because the server rewrites it, and
the sentences do not.
