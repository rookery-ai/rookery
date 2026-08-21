# Durable chat turns, agent residue cleanup, and structure-aware KB retrieval

Date: 2026-08-20

Four defects reported together. They are independent in cause and are
sequenced so the two cheap fixes do not wait on the two expensive ones.
Every root cause below was confirmed against source and against the
operator's live database rather than inferred from the symptom.

---

## 1. Orphaned agent residue

### Root cause

`agent_runs.agent_id` is declared `ON DELETE CASCADE`, but foreign-key
enforcement was **per-connection** until the DSN-pragma fix (`10926d1`,
#214, landed 2026-08-17). `database/sql` is a pool, so `DELETE FROM
agents` ran on whichever connection it was handed and the cascade fired
only when that connection happened to have `foreign_keys` on. Every
delete performed before that date left its dependent rows behind.

The newest orphan in the operator's database is dated 2026-08-14 —
three days *before* the fix. `apiDeleteAgent` calls `db.DeleteAgent`
correctly and the cascade fires today. **This is residue, not an active
leak.** The fix is a one-time sweep plus a guard, not a change to the
delete path.

Measured on the live install (3 surviving agents):

| Table | FK policy | Orphans | Total |
|---|---|---|---|
| `agent_runs` | CASCADE | 61 | 92 |
| `agent_skills` | CASCADE | 13 | 19 |
| `agent_connections` | CASCADE | 7 | 7 |
| `agent_schedules` | CASCADE | 3 | 5 |
| `agent_mcp_servers` | CASCADE | 0 | 0 |
| `inbox_messages` | SET NULL | 41 | 85 |
| `chats`, `pending_actions` | SET NULL | 0 | — |

`agent_connections` is the one with a security dimension: seven
bindings granting live credentials to agents that no longer exist.

### Decision

Sweep the **CASCADE** tables — delete the rows, which is what the schema
already says should have happened.

Do **not** delete the `SET NULL` rows. `inbox_messages` carries a
denormalized `agent_name` whose schema comment reads *"survives agent
delete"*, and all 41 orphans have that name populated, so they render
correctly today. An inbox message is a delivery record — the user's own
notification history — and deleting it because the sender no longer
exists would destroy working history to fix a problem it does not have.

They are not left untouched, though: their `agent_id` still points at a
deleted agent, and `HomePage` deep-links each inbox card to its source
agent, so today those cards navigate to a dead agent page. The migration
**nulls the dangling `agent_id`** — precisely what `ON DELETE SET NULL`
would have done — preserving the row and its name while removing the
dead link.

### Change

`migrations/015_orphaned_agent_rows.{up,down}.sql`:

- `DELETE` from `agent_runs`, `agent_skills`, `agent_connections`,
  `agent_schedules`, `agent_mcp_servers` where `agent_id` has no
  `agents` row.
- `UPDATE inbox_messages SET agent_id = NULL` (and `pending_actions`,
  `chats`) where `agent_id` is non-null and has no `agents` row.

The down migration is **empty**, with a comment saying so. Deleted rows
cannot be reconstructed, and a down migration that silently restores
nothing is honest only if it says why.

Defence in depth, so a nameless run can never render as a blank line
again regardless of how one arises: `RecentAgentRunsWithNames` keeps its
`LEFT JOIN` (an inner join would silently hide runs, trading a visible
bug for an invisible one) and the DTO gains an explicit fallback name so
the row is always legible.

---

## 2. Durable chat turns with live progress

### Root cause

`web/handlers_misc.go:handleChatMessage` persists **both** messages only
after the coder returns:

```go
result, err := coder.Chat(c.Request().Context(), …)   // minutes
…
_ = s.db.AddChatMessage(id, "user", text)             // only now
_ = s.db.AddChatMessage(id, "assistant", reply)
```

Two consequences:

1. For the whole turn the user's message exists **only** in React
   `pending` state. Unmounting the component destroys it, so returning
   to the chat shows an empty conversation.
2. The coder runs on `c.Request().Context()`. Closing the tab cancels
   the request context and kills the turn outright.

Navigating within the SPA keeps the `fetch` alive, which is why a turn
that finishes while the user is away *does* land both messages — the
inconsistency that made this read as flakiness.

### Design

`web/run_tracker.go` already solves this exact problem for manual agent
runs: a detached `context.Background()`, an in-flight registry keyed by
id, a buffered `progressCh`, and an SSE endpoint. This mirrors that
pattern rather than inventing a second one.

**Turn lifecycle** (`web/chat_turn_tracker.go`, new):

1. Read `history` **first**. The user message must be persisted before
   the coder call, and `history` is read via `ListChatMessages`, so
   reading after the write would feed the same message twice — once as
   history, once as `text`.
2. Persist the user message.
3. Register a `chatTurnState{progressCh, done, err}` keyed by chat id.
   Refuse a second concurrent turn on the same chat, exactly as
   `startManualRun` refuses a double run.
4. Launch the coder on a **detached context** with `WithProgress`
   feeding `progressCh`.
5. Return `202 {turn_id}` immediately.

On completion the assistant message is persisted, `TouchChat` and
`MaybeAutoTitle` run as they do today, and the turn is marked done.

On failure the user message **stays**. Today nothing is persisted on
failure, which is defensible when the client holds the bubble in memory;
once the message is durable, deleting it would be actively worse than
leaving it — the user typed it, and it is the context for the retry.
The error is recorded on the turn and surfaced over SSE.

**Endpoints:**

- `GET /api/v1/chats/:id/turn/progress` — SSE, streaming milestone
  lines then `event: done` or `event: error`. Follows the existing
  `handleDesignProgress` / `handleRunProgress` conventions, including
  emitting `event: done` before close (a convention CLAUDE.md records
  as load-bearing: without it the browser can only infer completion
  from a transparent reconnect hitting a 404).
- `GET /api/v1/chats/:id` gains **`in_flight`** so a client mounting
  mid-turn re-attaches deterministically instead of guessing.

**Trade-off.** This replaces the blocking POST, so `ChatWindow.sendTurn`
is rewritten around attach-and-stream. Layering an optional stream over
the blocking call was rejected: two paths would mean "return to the page
mid-turn" behaves differently depending on how long you were gone, which
is the class of inconsistency that produced this report.

**Not chosen:** a DB-backed turn row. It would survive a server restart,
but that was explicitly scoped out; the in-memory registry matches the
`run_tracker` precedent. A turn killed by a restart leaves a persisted
user message with no reply — visible and self-explanatory, rather than a
spinner that never resolves.

### Progress display

`components/chat/ActivityCard` already implements the requested
behaviour: it takes SSE milestone lines, accepts a `collapsible` prop,
renders only the last line when collapsed and the full action history
when expanded, and carries a status dot and elapsed timer. It is already
used for agent builds and runs. **No new component** — it is mounted in
`ChatWindow` and fed by the new stream.

**Masking.** `toolMilestone` already calls `shortenHostPaths(detail,
vaultRoot, homeDir)`, so vault paths already render as `notes/foo.md`
and `$HOME` as `~/…`. What is missing is identifiers: a bare UUID is
passed through verbatim. A `maskIDs` pass is added inside
`toolMilestone`, which fixes agent builds and agent runs at the same
time because all three surfaces share that one function.

Ordering matters and mirrors the existing comment about truncation:
mask **before** `truncateRunes(detail, 60)`, so a 36-character UUID
cannot consume the whole display budget and truncate away the part of
the line that says what the tool actually did.

---

## 3. KB "Chat about this"

### Root cause

`ChatAboutFileButton` opens the panel with `forceNew
initialText={chatPrompt(path)}`. The citation is *parked in the
composer* of a brand-new chat holding zero messages. "Open full page"
navigates to that same chat id correctly — it only looks like a new chat
because it genuinely is empty, and the composer prefill is lost when
`ChatWindow` remounts at the full-page route without `initialText`.

### Change

`autoSend` already exists and is already plumbed through
`GlobalChatPanel` → `ChatWindow`. Turning it on makes the citation a
real, persisted turn, which fixes both halves of the report: the message
is visible as a message, and the full-page chat has it.

`chatPrompt` must change with it. It currently ends in `— `, and
CLAUDE.md records this exact failure for `selectionChatPrompt`: *"a
citation waiting for an instruction — sent alone it asks the model
nothing."* Auto-sending the current wording would reproduce that bug and
spend a coder call for nothing. It gains an instruction, in the shape
`selectionEditPrompt` already uses.

Independent of §2 and shippable on its own.

---

## 4. Structure-aware KB retrieval

### Root cause

The operator's `notes/card-transactions.md` is **155 KB across 114
lines** — roughly 1774 characters per line — converted from a 163 KB
CSV. Three constraints compound:

- `vault.trimSnippet` cuts a snippet at a flat **200 bytes**, about 11%
  of one row of that table, landing mid-row with no column headers.
- `read_file` caps at **8 KiB**; paging exists, but that is ~20 round
  trips for this one file.
- `includeExecTools = filepath.Clean(workDir) != filepath.Clean(vaultRoot)`,
  and chat sets `WithDir(root)` — so **chat has no compute tool at all**.

### Scope

Retrieval only. A host-side table-aggregate tool was considered and
explicitly declined, as was enabling `run_script` in chat (a
security-posture change; CLAUDE.md describes the file-only chat toolset
as deliberate CLI parity).

**Stated limitation, recorded rather than designed around:** this
improves table *lookup* and will not answer *"how much have I spent
total"*. That is aggregation over ~1000 rows, and nothing in chat can
compute. The report must say so plainly rather than implying the
original question now works.

### Change

**Table-aware snippets.** When a hit lands on a markdown table row,
the snippet carries the table's header and delimiter rows along with the
matched row, so the model sees column names instead of an unlabelled
slice of one cell. The per-hit byte budget rises from the flat 200 to
accommodate a header plus a row, bounded so `SearchKB`'s existing
`maxBytes` contract still holds — `SearchKB` is already handed
`maxToolResult` and its result passes through `truncate()`, so the cap
is enforced at the boundary and this changes only how the budget is
spent within it.

**Slash-menu construct retrieval.** Extend retrieval across the block
constructs the editor can produce — callouts, toggles (`<details>`),
column wrappers, alignment wrappers, code blocks — so a hit inside one
returns the construct's readable text rather than its raw HTML wrapper.
Images are excluded, per the request.

Both changes live in `internal/vault` beside `trimSnippet` and
`SearchKB`, so the KB bridge, the API engine's `search_files`, and the
designers' `BuildKBContext` all benefit from one implementation.

---

## 5. Empty-reply guard

Found while tracing §4. `chat.CleanReply` returns `""` for genuinely
empty model output:

```go
if strings.TrimSpace(raw) == "" {
    return ""
}
```

and `handleChatMessage` persists it unguarded via `AddChatMessage`. The
result is a blank assistant bubble — four such rows exist in the live
database, including the one produced by the table question that prompted
this report.

`df3c1fd` (#242) covered only the *marker-only* case, which returns a
placeholder. The genuinely-empty case is still open.

A successful coder call that returns no text is a real outcome and must
be legible: it gets a placeholder, the same way a marker-only reply
does, rather than being persisted as an empty string. The two cases stay
distinguishable — `CleanReply` already comments on exactly this
distinction, and that comment is updated rather than contradicted.

---

## Testing

- **Migration** — a database seeded with orphans across all five CASCADE
  tables plus `inbox_messages`: assert the CASCADE rows are gone, the
  inbox row survives **with its name intact** and its `agent_id` nulled,
  and that re-running is a no-op.
- **Turn tracker** — the user message is persisted before the coder is
  called (the test asserts ordering, not just the end state, since the
  end state is identical either way); a concurrent second turn on one
  chat is refused; a failed turn keeps the user message; `history` is
  read before the write, so the message is not double-fed.
- **Client reconciliation** — `reconcilePending` keys on
  `role::content`, so the optimistic bubble now races a persisted row.
  Test that a returning client re-attaching mid-turn shows exactly one
  copy of the message.
- **Masking** — a milestone carrying a UUID renders it masked, and a
  path plus UUID still shows the meaningful tail after truncation.
- **`chatPrompt`** — a test asserts it does not end in a dangling
  separator, the property that makes it safe to auto-send.
- **Retrieval** — a fixture table whose rows exceed the old 200-byte cap
  returns column headers with the matched row; a fixture per slash-menu
  construct returns readable text; images stay excluded; the result
  still respects `maxBytes`.
- **Empty reply** — a coder returning `""` produces a placeholder, never
  an empty persisted row; a marker-only reply keeps its existing
  placeholder behaviour.

Full gate: `make ci` (note `GOTOOLCHAIN=auto` — the host Go is older
than `go.mod` requires), plus `make ci-ui`.

## Sequencing

1. §1 migration and §3 `autoSend` — small, independent, no shared surface.
2. §5 empty-reply guard — one function, and it is a precondition for
   judging §2's output honestly.
3. §2 durable turns and progress — the substantial piece.
4. §4 retrieval — separable, last.

## Out of scope

- Chat turns surviving a **server restart** (explicitly declined).
- A host-side table-aggregate tool, and `run_script` in chat
  (explicitly declined).
- Deleting `inbox_messages` for deleted agents — they render correctly
  and are preserved by schema design.
- Changing the agent delete path. The cascade works; only the residue
  is broken.
