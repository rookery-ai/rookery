# Stopping a running agent

## Why

An agent can go into a loop, and today nothing stops it short of restarting the
server. The owner needs to end the current run from wherever they are looking.
The chat composer already has Stop; agent runs do not. The agent and skill
designers are out of scope: their existing Cancel stays as it is.

## Behaviour (owner's decisions)

- Stop ends **the current run and nothing else**. The schedule is untouched, the
  next scheduled or manual run goes ahead normally, and no pause is offered.
- **No rollback.** Whatever the run already did stays: knowledge base edits, state
  already saved, messages sent, connector calls made, actions parked for approval.
- The stopped run's log records its tool calls as usual and ends with a notice
  that it was stopped. That is all it adds.
- The next run receives one generic instruction and **no details** about the
  stopped run: state may be incomplete; fix anything interfering, then carry on
  normally. It must not mention this in its messages.
- Stop appears on the agent details page, on running agents on the list page, and
  as `/agent stop` in chat apps.

## Implementer's calls

- No confirmation dialog: Stop is pressed because something is already wrong.
- `/agent stop <name>` stops that agent. With no name: one running agent is
  stopped, several are listed with a request to choose, none says so. A named agent
  that is not running says so. `/agent cancel` keeps its designer meaning.
- Running children are cancelled with their parent and each is logged as stopped.
  Stopping only a child is possible by name; its parent carries on with a warning.
- If two runs of one agent are in flight (a child call alongside its own schedule),
  Stop ends both.
- No chat or inbox message is sent for a stopped run: only a confirmation where
  Stop was pressed. Any `[CHAT]` not yet delivered is dropped from delivery but kept
  in the log, labelled as not sent.
- A stopped run shows a neutral **Stopped** label, and does not count as a failure
  in Home's "Needs attention".
- The notice to the next run appears only on the run immediately after a stop.
- Usage and cost are kept for what the run used before it stopped, including the
  API-engine turn in progress. A provider call in flight when the stop lands may
  not be counted, since its response never arrives.

## How it works

**Registry in the runner.** All triggers (manual, cron, chat, child) pass through
`runCoderAgent`, so that is where a run registers its cancel function. The web run
tracker cannot own this because a cron run has no tracker entry.

**Stopped is decided from what the owner did, never from the error.** A cancelled
API turn can report `ErrTimeout` and a killed CLI coder reports `signal: killed`, so
the error says nothing reliable. `Stop` cancels with the cause `ErrRunStopped`;
children inherit it through their context. A run counts as stopped only when the
cause is set **and** the coder returned an error: a run that finished in the same
instant keeps its normal result, as chat's Stop does. Unregistering before reading
the cause means a `Stop` that reported success is always visible to the run. A server
shutdown cancels with a different cause and stays an interrupted run, retried on boot
as today.

**Record.** `agent_runs.stopped` (migration 023, not backfilled). `exit_code` stays
-1. Every status mapper checks `stopped` first (`toAPIRun`, `toAPIDashboardRun`).
The transcript's last event and the vault note's last section are the stop notice.

**Next run.** Before creating its own row, the runner asks whether the agent's latest
run was stopped and sets `CoderPromptParams.PreviousRunStopped`. The designer's dry
run never sets it.

**Callers.** `runCoderAgent` returns `ErrRunStopped`. The web tracker must not print
it as a warning, the scheduler logs it at info, and the chat `/run` reply says the
agent was stopped rather than "finished".

**API.** `POST /api/v1/agents/:id/run/stop`: 204 when a run was stopped, 404 when
none was running, which the browser treats as already finished. Scoped through the
agent's workspace.

**Slack.** Its read loop handles events serially, so a `/run` from Slack would block
its own `/agent stop`. Events move to their own goroutine, which is how Telegram and
Discord already behave.

**UI.** Detail and list pages show Stop from `agent.running`, not from the live
stream, because a cron run has no stream. Both poll while an agent is running, so
scheduled runs clear without a reload. The list card is a link, so Stop sits beside
it rather than inside it.

## Non-goals

Stopping designer builds or turns; rollback of a run's work; pausing schedules on
stop; prior-run details in the prompt; queued runs waiting for a scheduler slot,
which have no run yet.

## Limits to state plainly

A CLI coder does not report tool calls, so its stopped log has the same lack of step
detail as any of its runs. `[STATE]` emitted at the end of a reply is lost if the run
is stopped first, which is why the next run is told state may be incomplete.
