# Durable Chat Turns, Agent Residue Cleanup, and KB Retrieval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a chat turn survive leaving the page (with live, masked, collapsible progress on return), sweep the orphaned rows left by the pre-#214 foreign-key bug, send the KB "Chat about this" citation as a real message, and make KB retrieval legible for markdown tables.

**Architecture:** The chat turn adopts `web/run_tracker.go`'s existing shape — persist first, run on a detached `context.Background()`, register an in-memory state keyed by chat id, stream milestones over SSE. The progress UI reuses `components/chat/ActivityCard`, which already implements collapse-to-last-line. Retrieval fixes live in `internal/vault` beside `trimSnippet` and `ChunkMarkdown` so every door (host tool, CLI bridge, designers) benefits from one implementation.

**Tech Stack:** Go 1.26 (`GOTOOLCHAIN=auto` — host Go is older than `go.mod` requires), SQLite via `modernc.org/sqlite`, Echo v4, React 19 + TanStack Query, Vitest, TipTap.

**Spec:** `docs/superpowers/specs/2026-08-20-durable-chat-turns-and-kb-retrieval-design.md`

## Global Constraints

- **Branch, never commit to `main`.** Work happens on a feature branch; `main` advances only through merged PRs.
- **Conventional Commits** — `type(scope): summary`. Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- **`GOTOOLCHAIN=auto` is required** for every Go command. Bare `go test` fails outright on this host.
- **No prompt text outside `internal/prompts`.** UI copy attributed to the user (e.g. `introPrompt.ts`) is not a prompt and stays in the SPA.
- **A slice field on a DTO must never marshal to `null`.** Initialise with `[]T{}` server-side; normalise with `?? []` at the consumer.
- **SSE `event: done` must carry non-empty data** (`data: 1`) or the browser will not dispatch the event.
- **Landlock/sandbox posture is unchanged.** No task enables `run_script` or `bash` in chat.
- **Migration down-files that cannot restore data must say so** in a comment rather than silently no-op.
- Full gate before PR: `GOTOOLCHAIN=auto make ci` plus `make ci-ui`.

---

### Task 1: Sweep orphaned agent rows

**Files:**
- Create: `migrations/015_orphaned_agent_rows.up.sql`
- Create: `migrations/015_orphaned_agent_rows.down.sql`
- Test: `internal/db/orphans_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing importable. Later tasks are unaffected.

Background: `agent_runs.agent_id` is `ON DELETE CASCADE`, but FK enforcement was per-connection until `10926d1` (#214, 2026-08-17). Every orphan in the operator's DB predates that fix, so the delete path is correct and only the residue needs removing.

Five tables are `ON DELETE CASCADE` and get their orphans **deleted**: `agent_runs`, `agent_skills`, `agent_connections`, `agent_schedules`, `agent_mcp_servers`.

Three are `ON DELETE SET NULL` and get their dangling `agent_id` **nulled, with the row preserved**: `inbox_messages`, `pending_actions`, `chats`. `inbox_messages` carries a denormalized `agent_name` (schema comment: "survives agent delete") that renders correctly; the row is real notification history. Nulling the id is exactly what the FK would have done and removes Home's dead deep-link to a deleted agent.

- [ ] **Step 1: Write the failing test**

Create `internal/db/orphans_test.go`:

```go
package db

import "testing"

// Seeds one live agent and one deleted-agent id across every table that
// references agents, then asserts migration 015's two policies: CASCADE
// tables lose the orphan, SET NULL tables keep the row with agent_id nulled.
func TestMigration015SweepsOrphanedAgentRows(t *testing.T) {
	d := newTestDB(t)

	// A surviving agent, so the sweep must be selective rather than a truncate.
	mustExec(t, d, `INSERT INTO workspaces (id,name,password_hash,secrets_salt)
		VALUES ('w1','ws','h','s')`)
	mustExec(t, d, `INSERT INTO agents (id,workspace_id,name,description)
		VALUES ('live','w1','live agent','d')`)

	// 'ghost' is never inserted into agents — these rows are the orphans.
	mustExec(t, d, `INSERT INTO agent_runs (id,agent_id,workspace_id,trigger)
		VALUES ('r-live','live','w1','cron'), ('r-ghost','ghost','w1','cron')`)
	mustExec(t, d, `INSERT INTO agent_skills (agent_id,skill_name)
		VALUES ('live','csv'), ('ghost','pdf')`)
	mustExec(t, d, `INSERT INTO inbox_messages (id,workspace_id,source,agent_id,agent_name,body)
		VALUES ('i-ghost','w1','agent_run','ghost','weather','25C, clear')`)

	if err := sweepOrphanedAgentRows(d); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if n := count(t, d, `SELECT COUNT(*) FROM agent_runs WHERE agent_id='ghost'`); n != 0 {
		t.Errorf("orphaned run survived: %d", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM agent_runs WHERE agent_id='live'`); n != 1 {
		t.Errorf("live agent's run was swept: %d", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM agent_skills WHERE agent_id='ghost'`); n != 0 {
		t.Errorf("orphaned skill survived: %d", n)
	}

	// The inbox row is history and must survive with its name — only the
	// dangling id goes, which is what kills Home's dead deep-link.
	var agentID *string
	var name string
	err := d.QueryRow(`SELECT agent_id, agent_name FROM inbox_messages WHERE id='i-ghost'`).
		Scan(&agentID, &name)
	if err != nil {
		t.Fatalf("inbox row was deleted, expected preserved: %v", err)
	}
	if agentID != nil {
		t.Errorf("dangling agent_id not nulled: %v", *agentID)
	}
	if name != "weather" {
		t.Errorf("denormalized name lost: %q", name)
	}
}

// Re-running must change nothing — migrations run on every boot.
func TestMigration015IsIdempotent(t *testing.T) {
	d := newTestDB(t)
	mustExec(t, d, `INSERT INTO workspaces (id,name,password_hash,secrets_salt)
		VALUES ('w1','ws','h','s')`)
	mustExec(t, d, `INSERT INTO agent_runs (id,agent_id,workspace_id,trigger)
		VALUES ('r-ghost','ghost','w1','cron')`)

	if err := sweepOrphanedAgentRows(d); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if err := sweepOrphanedAgentRows(d); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM agent_runs`); n != 0 {
		t.Errorf("expected 0 runs, got %d", n)
	}
}
```

Add the two helpers at the bottom of the same file if the package lacks them (check first — `newTestDB` already exists in this package):

```go
func mustExec(t *testing.T, d *DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func count(t *testing.T, d *DB, q string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}
```

`sweepOrphanedAgentRows` is a small Go helper that runs the same statements as the migration, so the test exercises the SQL without re-running the migration engine. Add it to `internal/db/orphans.go`:

```go
package db

// orphanSweepStatements is the single source of the migration's SQL, so the
// test and migration 015 can never drift apart.
var orphanSweepStatements = []string{
	`DELETE FROM agent_runs WHERE agent_id NOT IN (SELECT id FROM agents)`,
	`DELETE FROM agent_skills WHERE agent_id NOT IN (SELECT id FROM agents)`,
	`DELETE FROM agent_connections WHERE agent_id NOT IN (SELECT id FROM agents)`,
	`DELETE FROM agent_schedules WHERE agent_id NOT IN (SELECT id FROM agents)`,
	`DELETE FROM agent_mcp_servers WHERE agent_id NOT IN (SELECT id FROM agents)`,
	`UPDATE inbox_messages SET agent_id=NULL
		WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents)`,
	`UPDATE pending_actions SET agent_id=NULL
		WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents)`,
	`UPDATE chats SET agent_id=NULL
		WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents)`,
}

func sweepOrphanedAgentRows(d *DB) error {
	for _, q := range orphanSweepStatements {
		if _, err := d.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/db/ -run TestMigration015 -v`
Expected: FAIL — `undefined: sweepOrphanedAgentRows` before `orphans.go` exists, or a compile error on the helpers.

- [ ] **Step 3: Write the migration**

`migrations/015_orphaned_agent_rows.up.sql` — the same eight statements, with the reasoning at the top:

```sql
-- Sweep rows orphaned by the per-connection foreign-key bug fixed in #214
-- (10926d1, 2026-08-17). busy_timeout/foreign_keys are per-CONNECTION settings
-- and database/sql is a pool, so before that fix `DELETE FROM agents` cascaded
-- only when it happened to run on a connection with foreign_keys on. Every
-- orphan predates the fix; the cascade is correct now, so this is a one-time
-- sweep and not a change to the delete path.

-- CASCADE tables: the schema already says these rows die with their agent.
DELETE FROM agent_runs        WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_skills      WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_connections WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_schedules   WHERE agent_id NOT IN (SELECT id FROM agents);
DELETE FROM agent_mcp_servers WHERE agent_id NOT IN (SELECT id FROM agents);

-- SET NULL tables: the ROW is preserved and only the dangling id goes, which
-- is exactly what the foreign key would have done. inbox_messages carries a
-- denormalized agent_name ("survives agent delete") and renders correctly, so
-- it is real notification history; deleting it would destroy working history.
-- Nulling the id also removes Home's deep-link to a deleted agent page.
UPDATE inbox_messages  SET agent_id=NULL WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents);
UPDATE pending_actions SET agent_id=NULL WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents);
UPDATE chats           SET agent_id=NULL WHERE agent_id IS NOT NULL AND agent_id NOT IN (SELECT id FROM agents);
```

`migrations/015_orphaned_agent_rows.down.sql`:

```sql
-- Deliberately empty. This migration deletes rows whose parent agent no longer
-- exists; nothing on disk can reconstruct them, so a down migration that
-- appeared to reverse it would be lying. Stated rather than left blank so the
-- next reader knows the emptiness is a decision.
SELECT 1;
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/db/ -run TestMigration015 -v`
Expected: PASS (both tests)

Then confirm the migration itself applies cleanly:
Run: `GOTOOLCHAIN=auto go test ./migrations/... ./internal/db/... -count=1`
Expected: PASS

- [ ] **Step 5: Guard the rendering so a nameless run is never blank**

In `web/api_dashboard.go`, where `recentRuns` is mapped into `runsOut` (around line 105), give the DTO an explicit fallback rather than emitting the empty string. Keep the `LEFT JOIN` in `RecentAgentRunsWithNames` — an inner join would silently hide runs, trading a visible bug for an invisible one.

```go
name := r.AgentName
if strings.TrimSpace(name) == "" {
	// A run whose agent no longer exists. Migration 015 sweeps the ones the
	// pre-#214 FK bug left behind, but a row can still reach here mid-delete,
	// and a blank line reads as a broken UI rather than as missing data.
	name = "(deleted agent)"
}
```

- [ ] **Step 6: Commit**

```bash
git add migrations/015_orphaned_agent_rows.up.sql \
        migrations/015_orphaned_agent_rows.down.sql \
        internal/db/orphans.go internal/db/orphans_test.go \
        web/api_dashboard.go
git commit -m "fix(db): sweep agent rows orphaned by the pre-#214 foreign-key bug"
```

---

### Task 2: Send the KB "Chat about this" citation as a real message

**Files:**
- Modify: `web/ui/src/pages/kb/ChatAboutFileButton.tsx`
- Test: `web/ui/src/pages/kb/chataboutfile.test.tsx`

**Interfaces:**
- Consumes: `GlobalChatPanel`'s existing `autoSend?: boolean` prop, already forwarded to `ChatWindow`.
- Produces: `chatPrompt(path: string): string` — unchanged signature, changed wording.

Background: the button passes `forceNew initialText={chatPrompt(path)}`, which *parks* the citation in the composer of a chat holding zero messages. "Open full page" navigates to that same chat id correctly; it looks like a new chat because it genuinely is empty. `autoSend` already exists and is already plumbed through.

`chatPrompt` currently ends in `— `. CLAUDE.md records this exact failure for `selectionChatPrompt`: *"a citation waiting for an instruction — sent alone it asks the model nothing."* Auto-sending the current wording reproduces that bug and spends a coder call for nothing, so the wording must change in the same commit.

- [ ] **Step 1: Write the failing test**

Append to `web/ui/src/pages/kb/chataboutfile.test.tsx`:

```tsx
import { chatPrompt } from "./ChatAboutFileButton";

test("chatPrompt asks a question rather than trailing off", () => {
  const p = chatPrompt("notes/trip.md");
  expect(p).toContain("notes/trip.md");
  // The property that makes it safe to auto-send: it must not end in a
  // dangling separator waiting for the user to finish the sentence.
  expect(p.trimEnd()).not.toMatch(/[—:-]$/);
  expect(p.trimEnd().length).toBeGreaterThan(p.indexOf("notes/trip.md"));
});

test("the button auto-sends so the citation becomes a real message", () => {
  const open = vi.fn();
  vi.mocked(useSlideOver).mockReturnValue({ open, close: vi.fn() });

  render(<ChatAboutFileButton path="notes/trip.md" />);
  fireEvent.click(screen.getByRole("button", { name: /chat about this/i }));

  const panel = open.mock.calls[0]![0];
  expect(panel.props.autoSend).toBe(true);
  expect(panel.props.forceNew).toBe(true);
  expect(panel.props.initialText).toContain("notes/trip.md");
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/kb/chataboutfile.test.tsx`
Expected: FAIL — `autoSend` is `undefined`, and `chatPrompt` ends in `— `.

- [ ] **Step 3: Write the implementation**

In `ChatAboutFileButton.tsx`, change `chatPrompt` and pass `autoSend`:

```tsx
// chatPrompt NAMES the file rather than inlining its content: the chat coder
// already runs rooted at the vault with file tools and its system prompt names
// the vault root, so a path is all it needs. Inlining would blow the context on
// a large note and hand the model a snapshot that goes stale the moment either
// of them edits it.
//
// It ends in a QUESTION, not a dangling "— ". This prompt is auto-sent, and a
// citation with no instruction asks the model nothing — the same failure
// selectionChatPrompt records and selectionEditPrompt exists to avoid.
export function chatPrompt(path: string): string {
  return `Give me a short summary of my knowledge base file \`${path}\`, then ask me what I'd like to know about it.`;
}
```

and in the component:

```tsx
onClick={() =>
  open(
    <GlobalChatPanel forceNew autoSend initialText={chatPrompt(path)} />,
    { title: "Chat" },
  )
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/kb/chataboutfile.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/ui/src/pages/kb/ChatAboutFileButton.tsx \
        web/ui/src/pages/kb/chataboutfile.test.tsx
git commit -m "fix(kb): send the Chat-about-this citation as a message, not a composer prefill"
```

---

### Task 3: Guard the empty assistant reply

**Files:**
- Modify: `internal/chat/clean.go` (the file declaring `CleanReply` — confirm with `grep -rn "func CleanReply" internal/chat/`)
- Test: `internal/chat/clean_test.go`

**Interfaces:**
- Consumes: existing `markerOnlyPlaceholder` constant in the same package.
- Produces: `CleanReply(raw string) string` — unchanged signature; now never returns `""` for a successful call.

Background: `CleanReply` returns `""` when the model produced no text, and `handleChatMessage` persists that unguarded via `AddChatMessage`, so a blank bubble is stored. Four such rows exist in the operator's DB, including the one from the table question that prompted this work. `#242` covered only the *marker-only* case.

This must land before Task 5, or a durable turn will faithfully persist a blank reply and the progress work will look like it caused it.

- [ ] **Step 1: Write the failing test**

Append to `internal/chat/clean_test.go`:

```go
// A successful coder call that returned no text is a real outcome and must be
// legible. Persisting "" produces a blank bubble, which reads as being ignored
// — the lesson UserFacingDesignText already records for the designer.
func TestCleanReplyGivesEmptyOutputAPlaceholder(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\n\t\n"} {
		if got := CleanReply(raw); strings.TrimSpace(got) == "" {
			t.Errorf("CleanReply(%q) = %q, want a non-empty placeholder", raw, got)
		}
	}
}

// The marker-only case keeps its own placeholder — the two causes stay
// distinguishable, which is the distinction the existing comment draws.
func TestCleanReplyStillPlaceholdersMarkerOnlyReplies(t *testing.T) {
	if got := CleanReply("[SILENT]"); strings.TrimSpace(got) == "" {
		t.Errorf("marker-only reply lost its placeholder: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/chat/ -run TestCleanReply -v`
Expected: FAIL — `CleanReply("") = "", want a non-empty placeholder`.

- [ ] **Step 3: Write the implementation**

Replace the early return in `CleanReply`, adding a distinct constant beside `markerOnlyPlaceholder`:

```go
// emptyReplyPlaceholder stands in for a successful coder call that returned no
// text at all. Distinct from markerOnlyPlaceholder because the causes differ:
// there the model spoke only in protocol markers, here it said nothing. Both
// must be legible — an empty bubble reads as being ignored, and it is also
// few-shot evidence to the next turn that a blank answer is acceptable here.
const emptyReplyPlaceholder = "_(no reply — the model returned an empty response. Try asking again.)_"

func CleanReply(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return emptyReplyPlaceholder
	}
	cleaned := cleanMarkers(raw)
	if cleaned != "" {
		return cleaned
	}
	// Nothing survived, so the reply WAS the markers — distinct from the empty
	// input handled above, which now has a placeholder of its own.
	return markerOnlyPlaceholder
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/chat/ -count=1 -v`
Expected: PASS. If an existing test asserted `CleanReply("") == ""`, update it — the empty return was the bug, and the test recorded it.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/
git commit -m "fix(chat): give a genuinely-empty model reply a placeholder instead of a blank bubble"
```

---

### Task 4: Mask identifiers in tool milestones

**Files:**
- Modify: `internal/coder/api_engine.go` (`toolMilestone`, around line 186's call site)
- Test: `internal/coder/api_engine_test.go`

**Interfaces:**
- Consumes: existing `shortenHostPaths(s, vaultRoot, homeDir string) string`, `truncateRunes(s string, n int) string`.
- Produces: `maskIDs(s string) string` — replaces bare UUIDs with `<id>`.

Background: `toolMilestone` already calls `shortenHostPaths(detail, vaultRoot, homeDir)`, so vault paths render as `notes/foo.md` and `$HOME` as `~/…`. Identifiers are the gap. Fixing it here fixes agent builds, agent runs, and (after Task 7) chat, because all three share this function.

Ordering matters and mirrors the existing truncation comment: mask **before** `truncateRunes(detail, 60)`, so a 36-character UUID cannot consume the whole display budget and truncate away the part that says what the tool did.

- [ ] **Step 1: Write the failing test**

Append to `internal/coder/api_engine_test.go`:

```go
func TestToolMilestoneMasksIdentifiers(t *testing.T) {
	tc := llm.ToolCall{
		Name: "read_file",
		Args: []byte(`{"path":"agents/4892b6a2-aad8-4826-a6a4-66fcbcf19875/state.md"}`),
	}
	got := toolMilestone(tc, "", "")
	if strings.Contains(got, "4892b6a2") {
		t.Errorf("raw identifier leaked into a user-visible milestone: %q", got)
	}
	if !strings.Contains(got, "state.md") {
		t.Errorf("masking ate the meaningful part: %q", got)
	}
}

// Masking must run BEFORE truncation, or a 36-char id spends the whole 60-char
// budget and the part naming what the tool actually did is cut away.
func TestToolMilestoneMasksBeforeTruncating(t *testing.T) {
	tc := llm.ToolCall{
		Name: "read_file",
		Args: []byte(`{"path":"agents/4892b6a2-aad8-4826-a6a4-66fcbcf19875/notes/summary.md"}`),
	}
	got := toolMilestone(tc, "", "")
	if !strings.Contains(got, "summary.md") {
		t.Errorf("tail lost to truncation, so masking ran too late: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run TestToolMilestoneMasks -v`
Expected: FAIL — `undefined: maskIDs`, then a leaked identifier once it compiles.

- [ ] **Step 3: Write the implementation**

Add to `internal/coder/api_engine.go`:

```go
// uuidPattern matches a canonical 8-4-4-4-12 hexadecimal identifier. Milestones
// are shown to a human watching their agent or chat work, and a workspace or
// agent UUID tells them nothing while crowding out the part of the line that
// says what the tool did.
var uuidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

func maskIDs(s string) string { return uuidPattern.ReplaceAllString(s, "<id>") }
```

and in `toolMilestone`, insert the call between shortening and truncating:

```go
detail = shortenHostPaths(detail, vaultRoot, homeDir)
// Before truncation, for the same reason shortening is: a 36-character id
// would otherwise spend the whole 60-char budget and truncate away the part
// that says what the command actually does.
detail = maskIDs(detail)
detail = truncateRunes(detail, 60)
```

Add `"regexp"` to the file's imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/coder/api_engine.go internal/coder/api_engine_test.go
git commit -m "fix(coder): mask identifiers in tool milestones before truncating"
```

---

### Task 5: Durable chat turns (backend)

**Files:**
- Create: `web/chat_turn_tracker.go`
- Modify: `web/handlers_misc.go` (`handleChatMessage`, lines 34–220)
- Modify: `web/server.go` (add the registry fields beside `runs`/`runsMu`)
- Test: `web/chat_turn_test.go`

**Interfaces:**
- Consumes: `db.AddChatMessage(chatID, role, content string) error`, `db.ListChatMessages(chatID string) ([]ChatMessage, error)`, `chat.CleanReply` (Task 3), `coder.WithProgress(func(string))`.
- Produces:
  - `type chatTurnState struct { progressCh chan string; mu sync.Mutex; done bool; err error; lines []string }`
  - `(*Server).startChatTurn(workspaceID, chatID, text string) (turnID string, ok bool)`
  - `(*Server).isChatTurnLive(chatID string) bool`

Background: `handleChatMessage` persists **both** messages only after the coder returns, and runs the coder on `c.Request().Context()`. Leaving the page destroys the only copy of the user's message; closing the tab kills the turn. `web/run_tracker.go` already solves exactly this for manual agent runs — this mirrors it rather than inventing a second pattern.

**The read-then-write ordering is load-bearing.** `history` comes from `ListChatMessages`, so persisting the user message before reading history would feed it twice — once as history, once as `text`.

- [ ] **Step 1: Write the failing test**

Create `web/chat_turn_test.go`:

```go
package web

import (
	"strings"
	"testing"
	"time"
)

// The whole point of the change: the message must be durable BEFORE the coder
// runs, so leaving the page cannot destroy it. Asserting the ordering rather
// than the end state is deliberate — the end state is identical either way.
func TestChatTurnPersistsUserMessageBeforeCallingCoder(t *testing.T) {
	s, ws, chatID := newChatTestServer(t)

	seen := make(chan int, 1)
	s.testCoderHook = func() {
		msgs, _ := s.db.ListChatMessages(chatID)
		seen <- len(msgs)
	}

	if _, ok := s.startChatTurn(ws.ID, chatID, "hello"); !ok {
		t.Fatal("startChatTurn refused a first turn")
	}

	select {
	case n := <-seen:
		if n != 1 {
			t.Errorf("coder saw %d persisted messages, want 1 (the user's)", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("coder never ran")
	}
}

// history is read via ListChatMessages, so writing before reading would feed
// the same message twice — once as history, once as the turn's text.
func TestChatTurnDoesNotDoubleFeedTheNewMessage(t *testing.T) {
	s, ws, chatID := newChatTestServer(t)

	got := make(chan int, 1)
	s.testHistoryHook = func(n int) { got <- n }

	s.startChatTurn(ws.ID, chatID, "hello")

	select {
	case n := <-got:
		if n != 0 {
			t.Errorf("history carried %d messages on a fresh chat, want 0", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("history hook never fired")
	}
}

// One turn per chat, for the same reason startManualRun refuses a double run.
func TestChatTurnRefusesASecondConcurrentTurn(t *testing.T) {
	s, ws, chatID := newChatTestServer(t)
	s.testCoderBlock = make(chan struct{})
	defer close(s.testCoderBlock)

	if _, ok := s.startChatTurn(ws.ID, chatID, "first"); !ok {
		t.Fatal("first turn refused")
	}
	if _, ok := s.startChatTurn(ws.ID, chatID, "second"); ok {
		t.Error("second concurrent turn on the same chat was accepted")
	}
}

// A failed turn keeps the user's message. Today nothing persists on failure,
// which is defensible while the client holds the bubble in memory; once the
// message is durable, deleting it would be worse than leaving it — the user
// typed it, and it is the context for the retry.
func TestFailedChatTurnKeepsTheUserMessage(t *testing.T) {
	s, ws, chatID := newChatTestServer(t)
	s.testCoderErr = "provider exploded"

	turnID, ok := s.startChatTurn(ws.ID, chatID, "hello")
	if !ok {
		t.Fatal("turn refused")
	}
	waitForTurn(t, s, chatID)

	msgs, _ := s.db.ListChatMessages(chatID)
	if len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Fatalf("user message not preserved through failure: %+v", msgs)
	}
	st := s.chatTurn(chatID)
	if st == nil || st.err == nil || !strings.Contains(st.err.Error(), "provider exploded") {
		t.Errorf("failure not recorded on the turn: %+v", st)
	}
	_ = turnID
}
```

`newChatTestServer`, `waitForTurn`, and the three `test*` hook fields are test scaffolding. Add the hooks to `Server` guarded by a comment saying they are test-only, mirroring the existing `MarkGeneratingForTest` precedent in `agentdesigner`. `newChatTestServer` follows whatever the package's existing server-construction helper does (check `web/api_dashboard_test.go` for the established shape and reuse it).

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./web/ -run TestChatTurn -v`
Expected: FAIL — `undefined: startChatTurn`.

- [ ] **Step 3: Write the tracker**

Create `web/chat_turn_tracker.go`:

```go
package web

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/chat"
)

// chatTurnState tracks one in-flight chat turn. The turn executes in a
// goroutine on a detached context so navigating away never kills it; this
// state lets the SSE endpoint stream its progress live and lets a client
// returning mid-turn re-attach.
//
// Deliberately in-memory, mirroring agentRunState: a turn is minutes long, not
// days, and a DB-backed row was scoped out. A turn killed by a server restart
// leaves a persisted user message with no reply — visible and self-explanatory,
// rather than a spinner that never resolves.
type chatTurnState struct {
	id         string
	progressCh chan string
	mu         sync.Mutex
	done       bool
	err        error
	// lines accumulates every milestone so a client attaching MID-turn gets the
	// history it missed, not just whatever arrives after it connects. The live
	// channel alone would show such a client an empty card on a busy turn.
	lines []string
}

// startChatTurn persists the user's message, registers the turn, and runs the
// coder on a detached context. Returns false if a turn is already in flight for
// this chat, so a double-send cannot fire two coders at one conversation.
func (s *Server) startChatTurn(workspaceID, chatID, text string) (string, bool) {
	s.chatTurnsMu.Lock()
	if existing, ok := s.chatTurns[chatID]; ok {
		existing.mu.Lock()
		running := !existing.done
		existing.mu.Unlock()
		if running {
			s.chatTurnsMu.Unlock()
			return "", false
		}
	}
	st := &chatTurnState{id: uuid.NewString(), progressCh: make(chan string, 64)}
	s.chatTurns[chatID] = st
	s.chatTurnsMu.Unlock()

	// Read history BEFORE persisting: it comes from ListChatMessages, so writing
	// first would feed this message twice — once as history, once as text.
	rawHistory, _ := s.db.ListChatMessages(chatID)
	history := chat.CleanHistory(rawHistory)
	if s.testHistoryHook != nil {
		s.testHistoryHook(len(history))
	}

	// Durable before the coder runs. This is the fix: leaving the page can no
	// longer destroy the only copy of what the user typed.
	_ = s.db.AddChatMessage(chatID, "user", text)

	go func() {
		// Detached: the turn must outlive the HTTP request that started it. The
		// coder profile's own timeout still bounds it.
		ctx := context.Background()

		onProgress := func(msg string) {
			st.mu.Lock()
			st.lines = append(st.lines, msg)
			st.mu.Unlock()
			select {
			case st.progressCh <- msg:
			default: // buffer full — drop for the live view; lines keeps the record
			}
		}

		reply, err := s.runChatCoder(ctx, workspaceID, chatID, history, text, onProgress)

		st.mu.Lock()
		st.done = true
		st.err = err
		st.mu.Unlock()

		if err == nil {
			// CleanReply never returns "" now (Task 3), so a blank bubble cannot
			// be persisted here.
			_ = s.db.AddChatMessage(chatID, "assistant", chat.CleanReply(reply))
			_ = s.db.TouchChat(chatID)
			if ch, gerr := s.db.GetChat(chatID); gerr == nil {
				chat.MaybeAutoTitle(s.db, s.titleGen, ch, text, chat.CleanReply(reply))
			}
		} else {
			// The user's message stays. Surface the failure on the stream so a
			// watching client sees it rather than a card that just stops.
			onProgress("⚠️ " + err.Error())
		}
		close(st.progressCh)

		// Evict after a grace period so a late or reconnecting viewer can still
		// observe the terminal state; the durable record is the chat history.
		time.AfterFunc(90*time.Second, func() {
			s.chatTurnsMu.Lock()
			if cur, ok := s.chatTurns[chatID]; ok && cur == st {
				delete(s.chatTurns, chatID)
			}
			s.chatTurnsMu.Unlock()
		})
	}()
	return st.id, true
}

// chatTurn returns the tracked turn for a chat, or nil.
func (s *Server) chatTurn(chatID string) *chatTurnState {
	s.chatTurnsMu.Lock()
	defer s.chatTurnsMu.Unlock()
	return s.chatTurns[chatID]
}

// isChatTurnLive reports whether THIS server has a turn in flight for the chat
// — i.e. whether an SSE stream would actually have a producer.
func (s *Server) isChatTurnLive(chatID string) bool {
	st := s.chatTurn(chatID)
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return !st.done
}
```

Add to `Server` in `web/server.go`, beside the existing `runs`/`runsMu`:

```go
chatTurns   map[string]*chatTurnState
chatTurnsMu sync.Mutex

// Test-only hooks, in the spirit of agentdesigner's *ForTest helpers: the
// turn runs in a goroutine, so a test cannot otherwise observe its ordering.
testCoderHook   func()
testHistoryHook func(int)
testCoderBlock  chan struct{}
testCoderErr    string
```

Initialise `chatTurns: map[string]*chatTurnState{}` wherever `runs` is initialised.

- [ ] **Step 4: Extract the coder setup into `runChatCoder`**

`handleChatMessage`'s body from line ~88 (`root := s.vault.Root(u.ID)`) through the `coder.Chat` call is coder *construction* — connector/MCP/KB bridge wiring, allowed tools, system prompt. Move it verbatim into a new method on `Server` in `web/chat_turn_tracker.go`, changing only the context source and adding the progress sink:

```go
// runChatCoder builds the chat coder exactly as the request handler used to and
// runs one turn. Extracted so the turn can execute on a detached context; the
// wiring inside is unchanged and must stay in step with the Telegram/Discord/
// Slack chat path in cmd/rookery, which divergence would give one surface a
// capability the other lacks.
func (s *Server) runChatCoder(
	ctx context.Context,
	workspaceID, chatID string,
	history []db.ChatMessage,
	text string,
	onProgress func(string),
) (string, error) {
	// ... the moved body, with every c.Request().Context() replaced by ctx ...
	// and the coder gaining: coder = coder.WithProgress(onProgress)
	if s.testCoderHook != nil {
		s.testCoderHook()
	}
	if s.testCoderBlock != nil {
		<-s.testCoderBlock
	}
	if s.testCoderErr != "" {
		return "", errors.New(s.testCoderErr)
	}
	result, err := coder.Chat(ctx, workspaceID, history, sysCtx, text)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}
```

Two things must move with it and not be dropped: the `defer s.<bridge>.Unregister(tok)` calls (they now defer to the end of the turn, which is correct — the bridges must outlive the request), and the `if !ch.Active { s.db.ResumeChat(id) }` re-activation.

`handleChatMessage` shrinks to: resolve chat, parse the message, reject empty, call `startChatTurn`, and return.

```go
turnID, ok := s.startChatTurn(u.ID, id, text)
if !ok {
	return c.JSON(http.StatusConflict, map[string]string{
		"error": "turn_in_flight",
		"message": "This chat is already working on a turn.",
	})
}
return c.JSON(http.StatusAccepted, map[string]string{"turn_id": turnID})
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./web/ -run TestChatTurn -v`
Expected: PASS (all four)

Run: `GOTOOLCHAIN=auto go test ./web/ -count=1`
Expected: PASS. Existing tests asserting the `{"response": …}` body will fail — update them to the 202 shape; that contract change is the point of this task.

- [ ] **Step 6: Commit**

```bash
git add web/chat_turn_tracker.go web/handlers_misc.go web/server.go web/chat_turn_test.go
git commit -m "feat(chat): run a chat turn on a detached context and persist the message first"
```

---

### Task 6: Stream chat turn progress over SSE

**Files:**
- Modify: `web/api_chats.go` (route registration ~line 27; `apiGetChat` ~line 150)
- Modify: `web/chat_turn_tracker.go` (add the handler)
- Modify: `web/api_parity_test.go` (add the route to the `want` inventory)
- Test: `web/chat_turn_sse_test.go`

**Interfaces:**
- Consumes: `(*Server).chatTurn`, `(*Server).isChatTurnLive` (Task 5).
- Produces: `GET /api/v1/chats/:id/turn/progress` (SSE); `apiGetChat`'s body gains `in_flight bool` and `turn_lines []string`.

`api_parity_test.go`'s `want` table is a merge gate — a new route that is not listed fails the build.

- [ ] **Step 1: Write the failing test**

Create `web/chat_turn_sse_test.go`:

```go
// A client attaching MID-turn must receive the milestones it missed, or a busy
// turn shows an empty card and reads as though nothing is happening.
func TestChatTurnProgressReplaysMissedLines(t *testing.T) {
	s, ws, chatID := newChatTestServer(t)
	st := &chatTurnState{id: "t1", progressCh: make(chan string, 8),
		lines: []string{"🔧 read_file(notes.md)", "🔧 search_files(dentist)"}}
	s.chatTurns[chatID] = st

	rec := httptest.NewRecorder()
	go func() { time.Sleep(50 * time.Millisecond); close(st.progressCh) }()
	serveChatProgress(t, s, ws, chatID, rec)

	body := rec.Body.String()
	if !strings.Contains(body, "read_file(notes.md)") ||
		!strings.Contains(body, "search_files(dentist)") {
		t.Errorf("missed milestones were not replayed: %q", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("stream did not close with event: done: %q", body)
	}
}

// in_flight is what lets a returning client re-attach deterministically
// instead of guessing from a transparent reconnect hitting a 404.
func TestGetChatReportsInFlight(t *testing.T) {
	s, ws, chatID := newChatTestServer(t)
	s.chatTurns[chatID] = &chatTurnState{id: "t1", progressCh: make(chan string)}

	body := getChatBody(t, s, ws, chatID)
	if !strings.Contains(body, `"in_flight":true`) {
		t.Errorf("in_flight missing or false on a live turn: %s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./web/ -run "TestChatTurnProgress|TestGetChatReportsInFlight" -v`
Expected: FAIL — no such route; `in_flight` absent.

- [ ] **Step 3: Write the handler**

Append to `web/chat_turn_tracker.go`, following `handleRunProgress`'s established shape:

```go
// handleChatTurnProgress streams an in-flight chat turn's milestones via SSE.
// Closing this stream (navigating away) does NOT cancel the turn — the turn
// runs on a detached context and this handler only observes it.
// GET /api/v1/chats/:id/turn/progress
func (s *Server) handleChatTurnProgress(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.WorkspaceID != u.ID {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "chat not found"})
	}
	st := s.chatTurn(id)
	if st == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no active turn"})
	}

	reqCtx := c.Request().Context()
	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx/caddy buffering
	w.WriteHeader(http.StatusOK)

	// Replay what this client missed before following the live channel. A
	// client attaching to a turn already in progress would otherwise watch an
	// empty card until the next tool call, which on a slow turn reads as a hang.
	st.mu.Lock()
	backlog := append([]string(nil), st.lines...)
	st.mu.Unlock()
	for _, line := range backlog {
		writeSSELines(w, line)
	}
	w.Flush()

	for {
		select {
		case <-reqCtx.Done():
			return nil
		case msg, ok := <-st.progressCh:
			if !ok {
				// data must be non-empty or the browser won't dispatch the event.
				fmt.Fprint(w, "event: done\ndata: 1\n\n")
				w.Flush()
				return nil
			}
			writeSSELines(w, msg)
			w.Flush()
		}
	}
}

// writeSSELines emits one data field per line: SSE data fields cannot contain
// raw newlines.
func writeSSELines(w io.Writer, msg string) {
	for _, line := range strings.Split(msg, "\n") {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
}
```

Register in `web/api_chats.go`:

```go
g.GET("/chats/:id/turn/progress", s.handleChatTurnProgress)
```

Extend `apiGetChat`'s body (line ~162). Note the slice must never marshal to `null`:

```go
st := s.chatTurn(id)
lines := []string{}
if st != nil {
	st.mu.Lock()
	lines = append(lines, st.lines...)
	st.mu.Unlock()
}
return c.JSON(http.StatusOK, map[string]any{
	"chat":       toAPIChat(ch),
	"messages":   out,
	"in_flight":  s.isChatTurnLive(id),
	"turn_lines": lines,
})
```

Add the route to `api_parity_test.go`'s `want` table.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./web/ -count=1`
Expected: PASS, including `TestAPIParityInventory`.

- [ ] **Step 5: Commit**

```bash
git add web/api_chats.go web/chat_turn_tracker.go web/api_parity_test.go web/chat_turn_sse_test.go
git commit -m "feat(chat): stream turn progress over SSE and report in_flight"
```

---

### Task 7: Attach the browser to the durable turn

**Files:**
- Modify: `web/ui/src/lib/chats.ts` (`sendChatMessage`, line 54)
- Modify: `web/ui/src/pages/chats/ChatWindow.tsx` (`sendTurn`, lines 176–245)
- Test: `web/ui/src/pages/chats/durableturn.test.tsx`

**Interfaces:**
- Consumes: `GET /api/v1/chats/:id/turn/progress`, `in_flight`, `turn_lines` (Task 6); `ActivityCard` from `@/components/chat/ActivityCard`.
- Produces: `startChatTurn(id, message): Promise<{ turn_id: string }>` replacing `sendChatMessage`.

`ActivityCard` already implements everything the progress display needs — it takes `lines: string[]`, a `status` of `"live" | "done" | "error"`, `startedAt`, and `collapsible`, rendering only the last line when collapsed and the full history when expanded. **Do not build a new component.**

- [ ] **Step 1: Write the failing test**

Create `web/ui/src/pages/chats/durableturn.test.tsx`:

```tsx
// The reported bug: leave mid-turn, come back, find an empty chat.
test("a chat mounted mid-turn shows the user's message and a live card", async () => {
  server.use(
    http.get("/api/v1/chats/c1", () =>
      HttpResponse.json({
        chat: { id: "c1", name: "x", active: true },
        messages: [{ role: "user", content: "how much did I spend?", created_at: NOW }],
        in_flight: true,
        turn_lines: ["🔧 search_files(transactions)"],
      }),
    ),
  );

  render(<ChatWindow chatId="c1" />);

  expect(await screen.findByText("how much did I spend?")).toBeInTheDocument();
  const card = await screen.findByTestId("activity-card");
  expect(card).toHaveTextContent("search_files(transactions)");
  expect(screen.getByTestId("activity-status-dot")).toHaveClass("animate-pulse");
});

// Collapsed shows the current action; expanded shows the whole history.
test("the activity card collapses to the current action", async () => {
  server.use(
    http.get("/api/v1/chats/c1", () =>
      HttpResponse.json({
        chat: { id: "c1", name: "x", active: true },
        messages: [],
        in_flight: true,
        turn_lines: ["🔧 read_file(a.md)", "🔧 read_file(b.md)"],
      }),
    ),
  );

  render(<ChatWindow chatId="c1" />);
  const card = await screen.findByTestId("activity-card");
  expect(card).toHaveTextContent("read_file(b.md)");

  fireEvent.click(within(card).getByRole("button"));
  expect(card).toHaveTextContent("read_file(a.md)");
  expect(card).toHaveTextContent("read_file(b.md)");
});

// reconcilePending keys on role::content, and the message is now persisted
// server-side too — the optimistic bubble must not double.
test("the optimistic bubble does not duplicate the persisted message", async () => {
  render(<ChatWindow chatId="c1" />);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "hello" } });
  fireEvent.click(screen.getByRole("button", { name: /send/i }));

  await waitFor(() => {
    expect(screen.getAllByText("hello")).toHaveLength(1);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/chats/durableturn.test.tsx`
Expected: FAIL — no activity card renders; `sendChatMessage` still awaits a `{response}` body.

- [ ] **Step 3: Replace the API client call**

In `web/ui/src/lib/chats.ts`, replace `sendChatMessage` with:

```ts
// The turn no longer completes inside this request. POST starts it and returns
// 202 with a turn id; the reply arrives by refetching the chat once the SSE
// stream reports done. This is what makes leaving the page survivable.
export async function startChatTurn(
  id: string,
  message: string,
): Promise<{ turn_id: string }> {
  return apiPost(`/api/v1/chats/${id}/messages`, { message });
}
```

Delete the legacy-shape parsing and update `chats.test.ts` — the legacy `{response}` and `{error}` 200-shapes are gone.

- [ ] **Step 4: Rewrite `sendTurn` and mount the card**

In `ChatWindow.tsx`, `sendTurn` becomes: push the optimistic user bubble, POST to start, then attach SSE. Keep `reconcilePending` — it keys on `role::content`, which is what makes the optimistic bubble collapse into the persisted row rather than duplicating.

```tsx
const [turnLines, setTurnLines] = useState<string[]>([]);
const [turnStartedAt, setTurnStartedAt] = useState<number | null>(null);

// Re-attach to a turn already running when this component mounts. This is the
// reported bug's fix on the client side: the turn is durable server-side, so a
// returning tab must pick it up rather than render an empty conversation.
useEffect(() => {
  if (!data?.in_flight) return;
  setTurnLines(data.turn_lines ?? []);
  setTurnStartedAt((prev) => prev ?? Date.now());
  return attachTurnStream();
}, [data?.in_flight]);

function attachTurnStream(): () => void {
  const es = new EventSource(`/api/v1/chats/${chatId}/turn/progress`);
  es.onmessage = (e) => setTurnLines((l) => [...l, e.data]);
  es.addEventListener("done", () => {
    es.close();
    setBusy(false);
    setTurnStartedAt(null);
    void qc.invalidateQueries({ queryKey: ["chat", chatId] });
    qc.invalidateQueries({ queryKey: ["chats"] });
    // A chat turn is the one thing in this browser that can WRITE to the vault.
    qc.invalidateQueries({ queryKey: ["kb-note"] });
    qc.invalidateQueries({ queryKey: ["kb-tree"] });
  });
  es.onerror = () => {
    // Don't give up: refetch to learn the real outcome, the way the designer's
    // SSE does. A proxy can drop a stream without the turn having failed.
    es.close();
    void qc.invalidateQueries({ queryKey: ["chat", chatId] });
    setBusy(false);
  };
  return () => es.close();
}

async function sendTurn(text: string): Promise<{ ok: true } | { ok: false; message: string }> {
  setPending((p) => [...p, { role: "user", content: text, created_at: new Date().toISOString() }]);
  setBusy(true);
  setTurnLines([]);
  setTurnStartedAt(Date.now());
  try {
    await startChatTurn(chatId, text);
    attachTurnStream();
    return { ok: true };
  } catch (err) {
    setBusy(false);
    setTurnStartedAt(null);
    return { ok: false, message: err instanceof ApiError ? err.message : "Something went wrong" };
  }
}
```

Render the card in the message stream where `TypingIndicator` is (line ~482). It replaces the indicator when there is progress to show, and the indicator still covers the gap before the first milestone:

```tsx
{busy && !attaching && (
  turnLines.length > 0 && turnStartedAt !== null ? (
    <ActivityCard
      title="Working"
      lines={turnLines}
      status="live"
      startedAt={turnStartedAt}
      collapsible
    />
  ) : (
    <TypingIndicator />
  )
)}
```

Import `ActivityCard` and `startChatTurn`; drop the `sendChatMessage` import.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd web/ui && npx vitest run src/pages/chats/`
Expected: PASS

Run: `cd web/ui && npx tsc -b && npx oxlint`
Expected: clean

- [ ] **Step 6: Commit**

```bash
git add web/ui/src/lib/chats.ts web/ui/src/lib/chats.test.ts \
        web/ui/src/pages/chats/ChatWindow.tsx \
        web/ui/src/pages/chats/durableturn.test.tsx
git commit -m "feat(web/chat): attach the browser to durable turns with collapsible progress"
```

---

### Task 8: Repeat table headers when chunking

**Files:**
- Modify: `internal/vault/chunk.go` (`splitOversized`, line 249)
- Test: `internal/vault/chunk_test.go`

**Interfaces:**
- Consumes: existing `targetChunkChars` (1500), `hardSplitWindow`.
- Produces: `tableHeader(text string) (header string, ok bool)` — returns a markdown table's header and delimiter rows.

Background: `ChunkMarkdown` splits at heading boundaries and hands an oversized section to `splitOversized`. The operator's `card-transactions.md` is one `# card transactions` heading followed by 155 KB of table, so it becomes ~100 chunks of 1500 characters, **none of which carry the column headers**. A retrieved chunk of bare table rows is uninterpretable — the ranked-pass half of the reported bug.

Repeating the header on each fragment is what a table-aware splitter should do, and it costs a bounded ~2 lines per chunk.

- [ ] **Step 1: Write the failing test**

Append to `internal/vault/chunk_test.go`:

```go
// A chunk of bare table rows is uninterpretable — the model cannot tell an
// amount from a date. Every fragment of a split table must carry its header.
func TestSplitOversizedRepeatsTableHeaders(t *testing.T) {
	var b strings.Builder
	b.WriteString("| Date | Merchant | Amount |\n|---|---|---|\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "| 2026-08-%02d | Merchant %d | %d.00 |\n", i%28+1, i, i)
	}

	chunks := ChunkMarkdown("notes/tx.md", "# Transactions\n\n"+b.String())
	if len(chunks) < 2 {
		t.Fatalf("expected the table to split, got %d chunk(s)", len(chunks))
	}
	for i, c := range chunks {
		if !strings.Contains(c.Text, "| Date | Merchant | Amount |") {
			t.Errorf("chunk %d lost the table header:\n%s", i, c.Text)
		}
		if len(c.Text) > targetChunkChars {
			t.Errorf("chunk %d exceeds the hard bound: %d > %d", i, len(c.Text), targetChunkChars)
		}
	}
}

// Prose must be untouched — the header logic may only fire on a real table.
func TestSplitOversizedLeavesProseAlone(t *testing.T) {
	prose := strings.Repeat("This is an ordinary paragraph of prose. ", 200)
	chunks := ChunkMarkdown("notes/p.md", "# Notes\n\n"+prose)
	for i, c := range chunks {
		if strings.Contains(c.Text, "|---") {
			t.Errorf("chunk %d gained a spurious table header:\n%s", i, c.Text)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -run TestSplitOversized -v`
Expected: FAIL — chunks after the first lack the header.

- [ ] **Step 3: Write the implementation**

Add to `internal/vault/chunk.go`:

```go
// tableHeader returns a markdown table's header and delimiter rows if text
// begins with one. A chunk of bare table rows is uninterpretable — the model
// cannot tell an amount column from a date column — so every fragment of a
// split table repeats them.
//
// The delimiter row is what identifies a table, not the pipes: a line of prose
// containing a pipe is common, while `|---|---|` is not.
func tableHeader(text string) (string, bool) {
	lines := strings.SplitN(strings.TrimLeft(text, "\n"), "\n", 3)
	if len(lines) < 2 {
		return "", false
	}
	head, delim := lines[0], strings.TrimSpace(lines[1])
	if !strings.Contains(head, "|") || !strings.Contains(delim, "|") {
		return "", false
	}
	// Every cell of the delimiter row is dashes, optionally colon-aligned.
	for _, cell := range strings.Split(strings.Trim(delim, "|"), "|") {
		c := strings.TrimSpace(cell)
		if c == "" || strings.Trim(c, ":-") != "" {
			return "", false
		}
	}
	return head + "\n" + lines[1], true
}
```

In `splitOversized`, detect the header once and prepend it to every fragment after the first. The header's own length must come out of the per-fragment budget so `targetChunkChars` still holds — that bound is the contract `SearchKB`'s byte cap depends on:

```go
func splitOversized(text string) []string {
	header, isTable := tableHeader(text)
	budget := targetChunkChars
	if isTable {
		// Reserve the header's cost so a fragment plus its repeated header
		// still respects the hard bound.
		budget -= len(header) + 1
		if budget < targetChunkChars/4 {
			// A pathologically wide header would starve the budget; better to
			// ship unlabelled rows than one row per chunk.
			isTable = false
			budget = targetChunkChars
		}
	}

	// ... existing splitting logic, using `budget` in place of targetChunkChars ...

	if isTable {
		for i := 1; i < len(out); i++ {
			out[i] = header + "\n" + out[i]
		}
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -count=1`
Expected: PASS, including the existing chunk-bound tests, which pin that no output exceeds `targetChunkChars`.

- [ ] **Step 5: Commit**

```bash
git add internal/vault/chunk.go internal/vault/chunk_test.go
git commit -m "fix(vault): repeat markdown table headers across split chunks"
```

---

### Task 9: Structure-aware search snippets

**Files:**
- Modify: `internal/vault/search.go` (`trimSnippet`, line 154)
- Test: `internal/vault/search_test.go`

**Interfaces:**
- Consumes: `tableHeader` from Task 8.
- Produces: `trimSnippet(s string) string` — unchanged signature; `snippetFor(line, fileContent string) string` for the table-aware path.

Background: `trimSnippet` cuts at a flat **200 bytes**. The operator's table rows run to 1774 characters, so a snippet shows about 11% of one row, landing mid-cell with no column names — the exact-pass half of the bug. This is also where slash-menu constructs get handled: a hit inside a callout, toggle, or column wrapper should return readable text, not a raw HTML wrapper. Images stay excluded, per the request.

- [ ] **Step 1: Write the failing test**

Append to `internal/vault/search_test.go`:

```go
func TestSnippetCarriesTableHeaders(t *testing.T) {
	content := "| Date | Merchant | Amount |\n|---|---|---|\n" +
		"| 2026-08-01 | " + strings.Repeat("Very Long Merchant Name ", 40) + " | 42.00 |\n"
	row := strings.Split(content, "\n")[2]

	got := snippetFor(row, content)
	if !strings.Contains(got, "Merchant") || !strings.Contains(got, "Amount") {
		t.Errorf("snippet lost the column names:\n%s", got)
	}
	if !strings.Contains(got, "42.00") {
		t.Errorf("snippet lost the row's tail, where the value lives:\n%s", got)
	}
}

func TestSnippetUnwrapsSlashMenuConstructs(t *testing.T) {
	cases := []struct{ name, line, want string }{
		{"callout", "> [!note] Remember the dentist", "Remember the dentist"},
		{"toggle summary", "<summary>Trip checklist</summary>", "Trip checklist"},
		{"column wrapper", `<div data-cols="2">`, ""},
		{"alignment wrapper", `<div align="center">`, ""},
	}
	for _, tc := range cases {
		got := snippetFor(tc.line, tc.line)
		if tc.want == "" {
			if strings.Contains(got, "<div") {
				t.Errorf("%s: raw wrapper leaked into the snippet: %q", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: want %q in %q", tc.name, tc.want, got)
		}
	}
}

// Images are excluded per the request; the alt text is not content the user
// asked to retrieve.
func TestSnippetExcludesImages(t *testing.T) {
	if got := snippetFor("![a diagram|420](img/x.png)", ""); strings.Contains(got, "img/x.png") {
		t.Errorf("image path leaked into a snippet: %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -run TestSnippet -v`
Expected: FAIL — `undefined: snippetFor`.

- [ ] **Step 3: Write the implementation**

Add to `internal/vault/search.go`:

```go
// snippetMax is the per-hit budget. Larger than the old flat 200 because that
// figure was set for prose lines and is about 11% of one row of a real
// converted-CSV table, landing mid-cell with no column names. SearchKB still
// enforces the overall byte cap at its own boundary, so this changes only how
// the budget is SPENT within it, never the contract callers depend on.
const snippetMax = 600

// snippetFor renders one search hit. A hit on a table row carries the table's
// column headers, without which the row is uninterpretable — the model cannot
// tell an amount from a date. A hit inside a slash-menu block construct is
// unwrapped to its readable text rather than shown as raw HTML.
func snippetFor(line, fileContent string) string {
	line = unwrapConstructs(line)
	if line == "" {
		return ""
	}
	if header, ok := tableHeader(fileContent); ok && strings.Contains(line, "|") {
		return trimTo(header+"\n"+strings.TrimSpace(line), snippetMax)
	}
	return trimTo(line, snippetMax)
}

// unwrapConstructs turns the block constructs the KB editor produces into the
// text a reader would see. Images are deliberately dropped: the request was for
// markdown data and slash-menu snippets EXCEPT images.
func unwrapConstructs(s string) string {
	s = strings.TrimSpace(s)
	// A bare structural wrapper carries no text of its own.
	if divWrapperPattern.MatchString(s) || s == "</div>" ||
		s == "<details>" || s == "</details>" {
		return ""
	}
	s = imagePattern.ReplaceAllString(s, "")
	s = summaryPattern.ReplaceAllString(s, "$1")
	s = calloutPattern.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var (
	// <div align="center"> and <div data-cols="2"> — the alignment and columns
	// nodes. Structure, not content.
	divWrapperPattern = regexp.MustCompile(`^<div\s+[^>]*>$`)
	summaryPattern    = regexp.MustCompile(`</?summary>|<summary>(.*?)</summary>`)
	calloutPattern    = regexp.MustCompile(`^>\s*\[![a-zA-Z]+\]\s*`)
	imagePattern      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
)

// trimTo cuts on a rune boundary — this operator's notes are routinely
// Cyrillic, and a raw byte cut corrupts the last character rather than merely
// shortening the text.
func trimTo(s string, max int) string {
	s = strings.TrimRight(s, "\r\n")
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:runeFloorCutVault(s, max)] + "…"
}

func runeFloorCutVault(s string, max int) int {
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return max
}
```

Keep `trimSnippet` as a thin delegate so existing callers are unchanged:

```go
func trimSnippet(s string) string { return trimTo(s, snippetMax) }
```

Then thread the file's content into the snippet call. In `searchRipgrep`, ripgrep's `--json` output already carries the path; read the file once per path (cache in a `map[string]string` for the call) and pass it to `snippetFor`. In `searchGo`, the content is already in hand.

Add `"regexp"` and `"unicode/utf8"` to the imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/vault/ -count=1 -v`
Expected: PASS. Existing snippet tests asserting the 200-byte cut will need their expectations updated — that cut was the bug.

Verify the budget contract still holds end to end:
Run: `GOTOOLCHAIN=auto go test ./internal/vault/ ./internal/coder/ -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/vault/search.go internal/vault/search_test.go
git commit -m "fix(vault): carry table headers and unwrap block constructs in search snippets"
```

---

### Task 10: Documentation sync and full gate

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md` (only if a route or variable table needs it)
- Test: `GOTOOLCHAIN=auto make ci`, `make ci-ui`

Per CLAUDE.md, a change touching an `/api/v1` route carries a documentation obligation. This change adds one route and alters one route's contract.

- [ ] **Step 1: Run the docs-sync skill**

Use the `docs-sync` skill. It holds the change-to-page trigger map and the cross-repository procedure.

- [ ] **Step 2: Update CLAUDE.md**

At minimum, record in the chat section:
- The chat turn is durable: persisted before the coder runs, executed on a detached context, tracked in memory by `web/chat_turn_tracker.go` mirroring `run_tracker.go`.
- Why history is read **before** the write (double-feed).
- `POST /chats/:id/messages` now returns **202 + turn_id**, not `{response}`.
- `GET /chats/:id/turn/progress` and the `in_flight` / `turn_lines` fields, and why the backlog is replayed on attach.
- Empty replies get a placeholder — the case #242 did not cover.
- Table headers repeat across chunks and snippets, **and the stated limitation**: this improves table lookup and does not enable aggregation, because chat has no compute tool (`includeExecTools` is false when `workDir == vaultRoot`).

- [ ] **Step 3: Run the full gate**

```bash
GOTOOLCHAIN=auto make ci
cd web/ui && npm run build
```
Expected: PASS. `make ci` covers gofmt, vet, `-race`, cross-compile, UI typecheck/lint/vitest, and docs-sync.

- [ ] **Step 4: Verify against the live install**

```bash
GOTOOLCHAIN=auto make deploy
curl -sS http://127.0.0.1:8080/healthz
```
Then confirm by hand: Home shows no blank activity rows; a chat turn survives navigating away and back with the card visible; "Chat about this" produces a real message that appears in the full-page chat.

- [ ] **Step 5: Commit and open the PR**

```bash
git add -A
git commit -m "docs: record durable chat turns, orphan sweep, and KB retrieval limits"
git push -u origin HEAD
gh pr create --draft --title "fix(chat): durable turns, agent residue sweep, and table-aware KB retrieval"
```

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
|---|---|
| §1 Orphaned agent residue | 1 |
| §2 Durable chat turns | 5, 6, 7 |
| §2 Progress display + masking | 4, 7 |
| §3 KB "Chat about this" | 2 |
| §4 Table-aware retrieval | 8, 9 |
| §5 Empty-reply guard | 3 |
| Testing | folded into each task |
| Sequencing | task order (1, 2, 3 → 4 → 5, 6, 7 → 8, 9 → 10) |

No spec requirement is unimplemented.

**Placeholder scan:** One deliberate ellipsis remains, in Task 5 Step 4 and Task 8 Step 3, marking code to be **moved verbatim** rather than written — the surrounding text names the file, the line range, and the exact substitutions. Everything else carries real code.

**Type consistency:** `chatTurnState` fields (`id`, `progressCh`, `mu`, `done`, `err`, `lines`) are used identically in Tasks 5, 6 and 7. `tableHeader(string) (string, bool)` is defined in Task 8 and consumed in Task 9 with matching arity. `startChatTurn` returns `(string, bool)` in Go (Task 5) and the TypeScript `startChatTurn` returns `{turn_id}` (Task 7) — same name, different layers, matching the 202 body defined in Task 5 Step 4.
