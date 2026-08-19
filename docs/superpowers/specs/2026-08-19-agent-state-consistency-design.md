# Agent state: one choke point, three doors

**Status:** design, approved — awaiting implementation
**Scope:** `internal/agentstate` (new), `internal/agentrunner`, `internal/agentdesigner`,
`internal/coder`, `cmd/rookery`, `internal/prompts`

## What went wrong

Two of four memory-keeping agents built on 2026-08-19 were permanently silent and
permanently expensive. `hn-watch` ran twice, reported nothing both times, and its
own `## Notes` said *"First run — baseline established"* on the **second** run.
`time mk` did the same on an hourly schedule at ~930k tokens a run — roughly 22M
tokens a day to accomplish nothing and say nothing.

Their state files looked like this:

```
```json
{}
```
{"reported_ids": [49355606, 49358259, …30 ids…]}
```

The fence — the only thing `ReadState` parses — is empty. The agent's memory is
one line below it.

### Why the existing self-heal did not catch it

`applyAndSaveState` already writes state back on every turn precisely to repair
a file an agent's own tools mangled, and it guards that write with
`stateReadOK`. But `stateReadOK` distinguishes *unparseable* from *empty*, and
an empty fence is valid JSON. So the read succeeded, `currentState` was `{}`,
and every run faithfully wrote `{}` back into the fence while the real data sat
below it, preserved and invisible. The self-heal worked exactly as designed and
could not help.

### The actual defect

There are two doors into an agent's memory and they disagree:

| | write | read |
|---|---|---|
| **door 1** | `[STATE]{…}[/STATE]` in output → runner merges → fence spliced | `StateJSON` injected into the runtime prompt |
| **door 2** | the agent edits `state.md` with its own file tools | the agent reads `state.md` with its own file tools |

Door 2 is legitimate and stays — an agent may keep notes, and the owner may
hand-edit the file. But door 2 has no way to say *"this is my state"* other than
hitting one specific fenced block without disturbing it, and door 1's reader
understands nothing else. Four disagreements follow:

1. **Format tolerance.** Door 2 can produce many shapes; door 1 reads one.
2. **Merge semantics.** `[STATE]` *merges* (`null` deletes a key); a direct file
   write *replaces*. Nothing documents which you get.
3. **Normalisation.** The `[STATE]` path preserves heading, intro and `## Notes`
   byte-for-byte; a direct write can flatten them. Both broken files had lost
   the `# State —` heading.
4. **Composition.** If an agent uses both doors in one run, the order is
   undefined — it writes the file, then the runner splices into what it left.

This is not a model-quality problem. `rookery-watcher` (same engine) and
`time mk2` (built under a CLI coder, later run under Mistral) both produced
perfectly canonical files. It is decided per build, by whether that build's
`AGENT.md` tells the agent to emit `[STATE]` or to manage the file itself.

## The design

**Give the agent a first-class way to write state that cannot be malformed, and
make the file tolerant enough that the old way still works.** Prevention plus
recovery; neither alone is sufficient, because the file stays writable by
design.

### One choke point

A new `internal/agentstate` package owns the format and is the only code that
touches `state.md`:

```go
// Get reads an agent's state, recovering from a malformed file where possible.
// The bool reports whether the file was understood — distinct from "the state
// is empty", which is a legitimate outcome.
func Get(path string) (map[string]any, bool, error)

// Apply merges a patch into the file's current state and writes it back.
// It is the single writer. A nil or empty patch still normalises the file.
func Apply(path, agentName string, patch map[string]any) (map[string]any, error)
```

This mirrors `connectors.Execute` and `mcp.Execute` — one typed function every
surface converges on, so behaviour cannot drift between them.

### Three doors, one implementation

| surface | mechanism |
|---|---|
| `[STATE]` marker | the runner calls `Apply` (agents see no change) |
| API engine | `get_state` / `set_state` in `hostToolSet` |
| CLI coder | `rookery state get\|set` over a loopback bridge |

The CLI path is the **fourth** instance of a pattern this codebase has already
walked three times — `internal/connectors/bridge.go`,
`internal/mcp/bridge.go`, `internal/vault/bridge.go`. Same shape: a `127.0.0.1`
listener, a run-scoped bearer token in `ROOKERY_STATE_URL` / `ROOKERY_STATE_TOKEN`,
and a thin `rookery state` subcommand.

**No new tool grant is required.** An agent run already allows
`"Bash,WebFetch,Read,Write,Edit"`, so a CLI coder can invoke the subcommand
today. The narrowly-scoped `Bash(<bin> …:*)` grants exist only for *chat*,
which is otherwise file-only — and chat gets no state tools, so that path is
untouched.

Tokens never leave the host. Landlock restricts the filesystem, not loopback
TCP, so a sandboxed coder reaches it exactly as it reaches the other three.

### Semantics, stated once

- **A patch merges.** `null` deletes a key. Identical for `[STATE]`,
  `set_state`, and the bridge — today's `[STATE]` behaviour, now the rule
  everywhere.
- **The file is authoritative content.** A patch merges on top of whatever is in
  it, not on top of a cached copy.
- **End of run is one sequence:** read → merge → render → write once. Using both
  doors in a single run stops being a race.

### Recovery

`Get` behaves exactly as today when the fence holds parseable JSON. Only when
the fence is **empty or absent** does it scan the document for the first
parseable JSON object and adopt it. `Apply` then rewrites the file canonically,
moving that data into the fence and keeping the surrounding prose.

One run repairs the file permanently. `hn-watch` would have recovered its 30
ids on its next run.

**The scan is deliberately narrow.** Only on an empty or absent fence, only the
first parseable object. The residual risk is an agent with genuinely empty state
*and* a JSON example in its `## Notes`, which would adopt the example. That is
unlikely, bounded — the next patch overwrites it — and preferable to the current
behaviour, where a correct agent goes silent forever.

**Recovery cannot fix everything, and the spec says so.** `time mk` recorded its
baseline as English prose. There is no JSON to find and no runtime change can
invent one. Recovery addresses the common failure; the tool addresses the cause.

### `stateReadOK` keeps its meaning

The guard stays and its contract is unchanged: a no-update turn does not write
when the file could not be understood. Recovery makes the file *understood*, so
a recovered read reports `true` and the normalising write proceeds. A genuinely
unparseable file still reports `false` and is left alone for a human.

## What does not change

This is the load-bearing half of the design. The platform is in use; nothing
below may alter.

- `agentdesigner.ReadState` / `WriteState` / `StateFilePath` /
  `RenderStateTemplate` keep their exact signatures and become thin delegates.
  Every existing call site — the runner, the migration, the designer, the KB
  guard — is untouched.
- **A canonical file is byte-identical after `Apply`.** Pinned by a test; it is
  the regression that would mean we had broken working agents.
- The dry run still discards its state (`restoreDryRunState` untouched).
  Confirmed as a product decision: a change-detector must start fresh, or its
  first real run is silent — the failure v0.3.8 fixed.
- The KB 409 guard on a running agent's `state.md` is untouched. The owner keeps
  hand-editing the file, and it stays readable in the KB browser.
- `MigrateAgentFilesToMarkdown` is untouched.
- No schema change, no migration, no new dependency.
- **Chat gets no state tools.** Agents only — chat has no agent and no state.
- The `[STATE]` output protocol is unchanged. Existing agents keep working with
  no rebuild.

## Prompt changes

With a real tool available, the runtime prompt stops presenting the file as
*the* state mechanism and presents it as what it is: a projection you can read,
and a tool you can call. The `[STATE]` marker remains documented — it is the
cheapest path for an agent that has one update at the end of a run.

This reduces incidence at the root, but it is a prompt: it steers and does not
guarantee, which is exactly why the recovery half exists.

## Testing

- **Golden round-trips** for every shape observed in the wild: canonical;
  empty fence with JSON below (`hn-watch`); prose-only (`time mk`); no heading;
  orphaned fence; multiple fences with a legitimate one in `## Notes`.
- **Byte-identity**: a canonical file is unchanged by `Apply` with an empty
  patch.
- **Recovery**: an `hn-watch`-shaped file yields the stranded state and is
  canonical afterwards.
- **Merge semantics**: `null` deletes; a patch merges rather than replaces;
  identical results through all three doors.
- **Both bridges** tested as the connector bridge is, including auth rejection.
- **`stateReadOK`**: a genuinely unparseable file still suppresses the
  no-update write.

## Riding along

Four items from the same subsystem, each small and independently revertable:

- `dryRunSendProhibition` moves to `internal/prompts` — house convention says no
  prompt text lives outside that package, and `kbassist.go` is the precedent.
- `isDryRunSilent` scans every line, so `[CHAT]` followed by `[SILENT]` renders
  "nothing to report" — diverging from `parseCoderOutput`, where chat wins.
- `agentrunner.TestRunFromContent` — exported, zero callers, zero tests, now a
  near-duplicate of `dryRun`. Delete.
- `saveDraft` swallows `UpsertAgentDraft`'s error (`flow.go:920`) — the same
  silent-failure shape one layer down.

## Deliberately not in scope

- **The dry run discarding `res.UsedConnectionIDs`** — auto-bind uses the
  build's connections, so a rehearsal that calls a connector the build did not
  produces output the saved agent cannot reproduce. Real, but it changes
  binding behaviour.
- **The dry run's missing `Skills`/`Connections`** — same reason. (`VaultRoot`
  stays omitted deliberately: it is the only thing keeping a rehearsal out of
  the owner's live knowledge base.)
- **Extracting `finalReviewMessage`** so the caveat-ordering constraint can be
  tested — `StopReason` has no seam today.

All three widen the blast radius of a change whose entire purpose is not
breaking anything. They stay on the list.
