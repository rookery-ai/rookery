# Agent run visibility and UI fixes

Six reported defects. Three share one root cause — a run's progress is never
retained — and three are independent interface bugs.

## 1–3. A run's progress is never retained

### What is wrong

**The live view forgets.** `agentRunState.progressCh` (`web/run_tracker.go`) is a
Go channel: consume-once, single-reader, no history. Leaving the agent page
closes the SSE stream, and every line already delivered is gone. `RunPanel`
compounds it by stamping `startedAtRef = Date.now()` when it *attaches*, so the
elapsed timer restarts from zero on every return to the page. Two tabs on the
same run steal each other's lines, because each message goes to whichever reader
happens to receive it.

**The durable record is thinner than the live one.** Tool-call milestones
(`toolMilestone`, `internal/coder/api_engine.go`) are written only to the
progress sink, which feeds the SSE stream and nothing else. The coder's raw
per-turn responses (`rctx.rawChunks`) reach the vault run note but never the
database: `FinishAgentRun` stores `finalOutput` — the `[CHAT]` lines — as
`stdout`. So Run history can only ever replay what the user already saw, which
is precisely the wrong half for debugging an agent you are about to edit.

**A silent run is indistinguishable from a broken one.** `rctx.silentSignaled`
is known at the end of every run and discarded. In the database a `[SILENT]` run
and a "produced no notification" run are both exit 0 with an empty `stdout`, so
the interface can only render the same nothing for both.

One related fact shapes the design: the scheduler wires `SendOutput` but **not**
`OnProgress`, so cron runs emit no progress at all today.

### Design

**Retention lives on `agentRunState`.** The channel is replaced by a retained
`lines []string`, a server-stamped `startedAt`, and a set of subscriber channels.
`startManualRun` appends under the mutex and fans out to every subscriber;
`handleRunProgress` takes the lock once, snapshots the buffer, registers itself,
releases, then emits an `event: meta` carrying `started_at` before replaying the
snapshot and streaming live.

Anchoring the timer server-side is the whole point of the `meta` event: a
client-side stamp is a measure of how long *this tab* has been watching, which is
the bug. Fan-out fixes the two-tab case as a side effect, and the current
`default:` drop on a full buffer disappears — appending to a slice never drops.
The buffer is capped (`maxRetainedLines`) with an explicit truncation marker
rather than growing without bound; retention still dies with the existing 90s
eviction, after which the finished run's transcript is the record.

**Accumulation happens inside the runner, not the web layer.** That is the only
layer both triggers pass through — the scheduler supplies no `OnProgress`, so
anything wired at the web layer would leave cron runs exactly as blind as they
are now. The runner wraps its progress sink, collects events, and persists them.

**Storage is two new columns, not an inference.** `agent_runs.transcript` holds
the JSON event list; `agent_runs.silent` holds the flag. Silent could be
*inferred* as `exit==0 && stdout=="" && stderr==""` for no migration cost, and
that is the wrong trade: the runner already computed the answer and threw it
away, and reconstructing a state the code once held is the exact shape of defect
this repository keeps recording. The run list needs the flag without fetching a
transcript, which is the second reason it is a column rather than a field inside
the JSON.

The transcript is byte-capped with an honest truncation marker, following
`capBridgeData`: a payload cut in place still parses and reads as complete, which
is worse than an explicit note that data is missing.

**Delivery is a lazy endpoint.** `GET /api/v1/agents/:id/runs/:runID` serves one
run's transcript, fetched only when a row expands. Folding it into the
agent-detail DTO would ship every run's full transcript on every page load, for
a panel that is collapsed by default.

The vault run note gains a `## Tool calls` section for the same information,
since that note is the durable copy an agent can read.

### Interface

A silent run renders a `Silent` chip beside its status chip. The expander reads
"Show output" when there is user-facing output and "Show details" when there is
only a transcript, so a silent run stops being a row with nothing behind it.

### Out of scope

Making cron runs **live**-streamable. The scheduler would need a handle on the
web server's tracker, which is a real refactor for a case the transcript already
addresses after the fact.

## 4. Page-title icons are smaller than their titles

`--text-2xl` is remapped to 28px in `index.css`, while `PageTitle` renders its
icon at `size-6` (24px). `size-7` is exactly 28px.

The fix belongs in `PageTitle` rather than at the four reported pages: all four
use the shared component, as do four more. This is the reason the component
exists, and patching call sites would re-create the divergence it was built to
end.

## 5. The connections explainer omits MCP servers

`ConnectionsPage`'s explainer block describes Chat apps, Services and Web search
— three of the four sections its own nav lists. A fourth paragraph is inserted in
nav order, between Services and Web search.

## 6. The knowledge-base folder picker shows agent IDs

`GET /api/v1/kb/folders` returns bare paths, so `FolderSelect` renders
`agents/<uuid>/logs` verbatim in the new-file Location field.

The tree already solved this: `enrichKBDisplayNames` resolves `agents/<id>` to
the agent's name from the database. The endpoint returns a label per folder and
reuses that same derivation, so the picker and the tree cannot disagree about
what a folder is called — which is the failure mode a second, parallel
implementation would introduce.

## Testing

Go tests cover the tracker's replay and fan-out, the transcript round-trip and
its cap, and the silent column. Vitest covers the replay-driven timer, the silent
chip, the icon size, the MCP paragraph and the folder labels.
