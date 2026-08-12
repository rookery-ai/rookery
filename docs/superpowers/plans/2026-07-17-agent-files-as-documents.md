# Agent Files as Documents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make everything an agent writes readable in the knowledge base — `state.json` becomes a markdown document, `agent.json` disappears, and the KB opens code/data files.

**Architecture:** A small `statefile.go` in `internal/agentdesigner` owns the `state.md` read/write contract (first ` ```json ` fence is the state; everything outside it is preserved). A startup migration converts existing agents and absorbs the legacy skills-reconciler before deleting `agent.json`. The `[STATE]` output protocol is unchanged. The KB gains a `kind` discriminator so non-markdown files render read-only. Spec: `docs/superpowers/specs/2026-07-17-agent-files-as-documents-design.md`.

**Tech Stack:** Go (stdlib `regexp`/`encoding/json`), React/TypeScript SPA, existing vault + run-tracker infrastructure.

## Global Constraints

- Branch: create `agent-files-md` off `main`. Never commit to `main` directly.
- Suites green at every commit: `go test ./... -count=1 -timeout 120s`; `cd web/ui && npm test -- --run`; `npm run build`.
- **State loss is the one unacceptable outcome.** Every migration step is verify-then-delete; any failure leaves both files in place and logs at error level.
- The `[STATE]{...}[/STATE]` protocol and `parseCoderOutput` are **not** modified. Agents emit exactly what they emit today.
- `state.md` template text is exact (Task 1) — no HTML comments (they break editor round-trip), italic prose only.
- Fence contract: the state object is the **first** ` ```json ` fence. Replacement is index-spliced, never `regexp.ReplaceAll` (JSON containing `$` would corrupt a template-expanded replacement).
- File permissions stay `0o640`, matching the current `state.json` write. Atomicity is not currently guaranteed for state writes; do not add it here (no regression, no gold-plating).
- Existing `parseRequiredSecrets` (`internal/agentdesigner/flow.go:2112`) is the secrets source of truth — see "Spec corrections" below.

## Spec corrections (found while grounding this plan — the spec is superseded on these two points)

1. **No new `# Secrets:` header is needed.** `parseRequiredSecrets(agentMD)` already extracts secrets from AGENT.md's existing `# Required secrets:` / `# - NAME: description` block, and the designer writes that block today. `agent.json`'s `RequiredSecrets` is merely a *cache* of it. So consumers repoint to the existing parser; **AGENT.md's format does not change and no header migration is required.**
2. **The migration must absorb `ReconcileSkillAttachmentsToDB`** (`internal/agentdesigner/manifest.go:140`). That function reads `manifest.Skills` from `agent.json` to seed the `agent_skills` table when the DB has no rows. Deleting `agent.json` without folding this in would silently break skill recovery for anyone restoring an older vault backup. The migration does the reconcile first, then deletes the file.

---

### Task 1: `state.md` read/write core

**Files:**
- Create: `internal/agentdesigner/statefile.go`
- Test: `internal/agentdesigner/statefile_test.go`

**Interfaces:**
- Produces (every later task consumes these):
  - `StateFilePath(vaultsBase, workspaceID, agentID string) string` → `<agentDir>/state.md`
  - `RenderStateTemplate(agentName, jsonBody string) string`
  - `ReadState(path string) (map[string]any, error)` — missing file or missing fence → `map[string]any{}`, nil error
  - `WriteState(path, agentName string, state map[string]any) error` — preserves everything outside the first fence

- [ ] **Step 1: Write the failing test** — `internal/agentdesigner/statefile_test.go`:

```go
package agentdesigner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if err := WriteState(p, "Gmail Digest", map[string]any{"last_seen_id": "abc", "count": float64(4)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["last_seen_id"] != "abc" || got["count"] != float64(4) {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "# State — Gmail Digest") {
		t.Fatalf("template heading missing:\n%s", raw)
	}
}

func TestWriteStatePreservesProseOutsideFence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n_intro_\n\n```json\n{\"a\":1}\n```\n\n## Notes\n\nAgent wrote this.\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"a": float64(2)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "## Notes") || !strings.Contains(string(raw), "Agent wrote this.") {
		t.Fatalf("prose lost:\n%s", raw)
	}
	if !strings.Contains(string(raw), "_intro_") {
		t.Fatalf("intro lost:\n%s", raw)
	}
	got, _ := ReadState(p)
	if got["a"] != float64(2) {
		t.Fatalf("state not updated: %#v", got)
	}
}

func TestReadStateMissingFileAndMissingFence(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadState(filepath.Join(dir, "nope.md"))
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: %#v %v", got, err)
	}

	p := filepath.Join(dir, "state.md")
	os.WriteFile(p, []byte("# State — X\n\nno fence here\n"), 0o640)
	got, err = ReadState(p)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing fence should self-heal to empty: %#v %v", got, err)
	}
	// Next write appends a fence and keeps the existing prose.
	if err := WriteState(p, "X", map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "no fence here") || !strings.Contains(string(raw), "```json") {
		t.Fatalf("append failed:\n%s", raw)
	}
}

func TestWriteStateDollarSignSafe(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if err := WriteState(p, "X", map[string]any{"cmd": "$1 and $0 and ${x}"}); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadState(p)
	if got["cmd"] != "$1 and $0 and ${x}" {
		t.Fatalf("dollar signs corrupted: %#v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agentdesigner/ -run TestState -count=1`
Expected: FAIL — `undefined: WriteState`.

- [ ] **Step 3: Implement `internal/agentdesigner/statefile.go`**

```go
package agentdesigner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// stateFenceRE matches a fenced json block. Non-greedy so the FIRST block wins:
// an agent's own "## Notes" prose may legitimately contain another fence.
var stateFenceRE = regexp.MustCompile("(?s)```json\\s*\\n(.*?)```")

// StateFilePath returns the path to an agent's state.md — its memory between
// runs, kept as a markdown document so it is readable in the knowledge base.
func StateFilePath(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "state.md")
}

// RenderStateTemplate builds a fresh state.md. The intro is italic prose, never
// an HTML comment: comments do not round-trip through the KB editor and would
// pin the file in raw mode forever.
func RenderStateTemplate(agentName, jsonBody string) string {
	return fmt.Sprintf(`# State — %s

_Managed by Simple Agents. The block below is this agent's memory between runs —
edit it if you need to fix something by hand._

`+"```json\n%s\n```"+`
`, agentName, jsonBody)
}

// ReadState returns the state object held in the first json fence of state.md.
// A missing file, a missing fence, or an empty fence all yield an empty map so a
// damaged file degrades to "no memory" instead of failing the run.
func ReadState(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	m := stateFenceRE.FindSubmatch(raw)
	if m == nil {
		return map[string]any{}, nil
	}
	body := bytes.TrimSpace(m[1])
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("state.md json block: %w", err)
	}
	if st == nil {
		st = map[string]any{}
	}
	return st, nil
}

// WriteState replaces only the first json fence, leaving the heading, intro and
// any agent-written prose untouched. A file with no fence gains one; a missing
// file is created from the template.
func WriteState(path, agentName string, state map[string]any) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	fence := "```json\n" + string(body) + "\n```"

	raw, readErr := os.ReadFile(path)
	if readErr != nil || len(bytes.TrimSpace(raw)) == 0 {
		return os.WriteFile(path, []byte(RenderStateTemplate(agentName, string(body))), 0o640)
	}

	loc := stateFenceRE.FindIndex(raw)
	if loc == nil {
		out := strings.TrimRight(string(raw), "\n") + "\n\n" + fence + "\n"
		return os.WriteFile(path, []byte(out), 0o640)
	}

	// Index splice, never ReplaceAll: JSON containing "$1"/"${x}" would be
	// mangled by regexp template expansion.
	out := make([]byte, 0, len(raw)+len(fence))
	out = append(out, raw[:loc[0]]...)
	out = append(out, []byte(fence)...)
	out = append(out, raw[loc[1]:]...)
	return os.WriteFile(path, out, 0o640)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/agentdesigner/ -run TestState -count=1 -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/agentdesigner/statefile.go internal/agentdesigner/statefile_test.go
git commit -m "feat(agents): state.md read/write core — fenced json, prose preserved"
```

---

### Task 2: Runner and agent-detail read/write `state.md`

**Files:**
- Modify: `internal/agentrunner/runner.go:189` (sandbox seed), `:249` (state read), and the post-run `[STATE]` write site (grep `WriteFile` near the state merge)
- Modify: `web/handlers_agents.go:397` (detail-page state read)
- Modify: `internal/agentdesigner/manifest.go` — `AgentStatePath` now returns `state.md` (or is deleted in favour of `StateFilePath`; pick one and update all callers)
- Test: `internal/agentrunner/runner_test.go` (extend), `web/api_agents_test.go` (extend)

**Interfaces:**
- Consumes: `StateFilePath`, `ReadState`, `WriteState` (Task 1).
- Produces: nothing new — behavioural change only.

- [ ] **Step 1: Write the failing test** — add to `internal/agentrunner/runner_test.go`:

```go
func TestAgentStatePersistsAcrossRunsAsMarkdown(t *testing.T) {
	// Seeds an agent dir, writes state via WriteState, and asserts the runner's
	// state-loading path reads it back. Follow the existing runner_test harness
	// for constructing agentDir/vaultsBase (grep an existing test that builds a
	// temp vault); the assertion is what matters:
	dir := t.TempDir()
	p := filepath.Join(dir, "state.md")
	if err := agentdesigner.WriteState(p, "T", map[string]any{"cursor": "abc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json must not be created any more")
	}
	got, err := agentdesigner.ReadState(p)
	if err != nil || got["cursor"] != "abc" {
		t.Fatalf("state not readable: %#v %v", got, err)
	}
}
```

- [ ] **Step 2: Repoint the runner.** At `runner.go:189` the sandbox temp dir is seeded with `state.json` containing `{}` — replace with `agentdesigner.WriteState(filepath.Join(tmpDir, "state.md"), agent.Name, map[string]any{})`. At `:249` replace the `os.ReadFile(...state.json)` + unmarshal with `agentdesigner.ReadState(agentdesigner.StateFilePath(...))`. At the post-run merge site, replace the marshal+`os.WriteFile` with `agentdesigner.WriteState(path, agent.Name, merged)`. The merge logic itself (null deletes a key) is unchanged.

- [ ] **Step 3: Repoint the detail page.** `web/handlers_agents.go:397` reads `AgentStatePath` into `data.State` (a string shown read-only). Replace with a read of `StateFilePath(...)`'s raw bytes so the page keeps showing the file verbatim (it is a document now — show it as-is, not re-marshalled).

- [ ] **Step 4: Run**

Run: `go test ./internal/agentrunner/... ./internal/agentdesigner/... ./web/... -count=1`
Expected: PASS. Then `grep -rn "state.json" --include=*.go . | grep -v _test` → only the migration (Task 3) may mention it.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(agents): runner and agent page read/write state.md"
```

---

### Task 3: Startup migration (state + manifest + skills reconcile)

**Files:**
- Create: `internal/agentdesigner/migrate_files.go`
- Test: `internal/agentdesigner/migrate_files_test.go`
- Modify: `cmd/simple-agents/main.go` — replace the `ReconcileSkillAttachmentsToDB(...)` call with the new migration (it must run **before** the scheduler goroutine starts)

**Interfaces:**
- Produces: `MigrateAgentFilesToMarkdown(database skillDB, vaultsBase string, coreSkillNames []string) (converted int, err error)` — idempotent; safe to call on every boot.
- Consumes: `skillDB` (already declared in `manifest.go` for the reconciler), `WriteState`/`ReadState` (Task 1).

The migration walks `<vaultsBase>/<workspaceID>/agents/*/` (including `draft_*` dirs) and per agent dir:

1. **State:** if `state.json` exists and `state.md` does not — unmarshal the JSON, `WriteState` it, `ReadState` it back, and only if the re-read object deep-equals the original, delete `state.json`. Any mismatch or error: leave both files, `slog.Error`, continue to the next agent.
2. **Manifest:** if `agent.json` exists — unmarshal locally (a private struct; do **not** depend on `LoadManifest`, which Task 4 deletes), and if `Skills` is non-empty and `database.ListAgentSkillNames(agentID)` returns none, seed them exactly as `ReconcileSkillAttachmentsToDB` does today (including its `manifestIsFallbackBloat` guard). Then delete `agent.json`.
3. Return the count of agents touched; log a single summary line.

- [ ] **Step 1: Write the failing test** — `migrate_files_test.go`:

```go
func TestMigrateConvertsStateAndDeletesManifest(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"cursor":"xyz","n":3}`), 0o640)
	os.WriteFile(filepath.Join(agentDir, "agent.json"), []byte(`{"id":"a1","name":"A","skills":["pdf"]}`), 0o640)

	db := &fakeSkillDB{} // reuse the fake from the existing reconciler test
	n, err := MigrateAgentFilesToMarkdown(db, base, []string{"pdf", "csv"})
	if err != nil || n != 1 {
		t.Fatalf("migrate: n=%d err=%v", n, err)
	}

	if _, err := os.Stat(filepath.Join(agentDir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("state.json should be gone")
	}
	if _, err := os.Stat(filepath.Join(agentDir, "agent.json")); !os.IsNotExist(err) {
		t.Fatal("agent.json should be gone")
	}
	got, err := ReadState(filepath.Join(agentDir, "state.md"))
	if err != nil || got["cursor"] != "xyz" || got["n"] != float64(3) {
		t.Fatalf("state not migrated: %#v %v", got, err)
	}
	if len(db.seeded["a1"]) != 1 || db.seeded["a1"][0] != "pdf" {
		t.Fatalf("skills not reconciled before deletion: %#v", db.seeded)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"a":1}`), 0o640)

	db := &fakeSkillDB{}
	if _, err := MigrateAgentFilesToMarkdown(db, base, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(agentDir, "state.md"))
	if _, err := MigrateAgentFilesToMarkdown(db, base, nil); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(agentDir, "state.md"))
	if string(first) != string(second) {
		t.Fatalf("second run changed the file:\n%s\n---\n%s", first, second)
	}
}

func TestMigrateKeepsBothFilesWhenStateMdAlreadyExists(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "ws1", "agents", "a1")
	os.MkdirAll(agentDir, 0o755)
	os.WriteFile(filepath.Join(agentDir, "state.json"), []byte(`{"old":1}`), 0o640)
	WriteState(filepath.Join(agentDir, "state.md"), "A", map[string]any{"new": float64(2)})

	if _, err := MigrateAgentFilesToMarkdown(&fakeSkillDB{}, base, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadState(filepath.Join(agentDir, "state.md"))
	if got["new"] != float64(2) {
		t.Fatalf("existing state.md was clobbered: %#v", got)
	}
}
```

- [ ] **Step 2:** Verify FAIL (`undefined: MigrateAgentFilesToMarkdown`), then implement per the three numbered rules above. Use `reflect.DeepEqual` for the verify-then-delete check.
- [ ] **Step 3:** Wire into `cmd/simple-agents/main.go` in place of the reconciler call, before the scheduler starts; log `slog.Info("agent files migrated", "agents", n)`.
- [ ] **Step 4:** Run `go test ./internal/agentdesigner/... -count=1` → PASS; `go build ./...` → clean.
- [ ] **Step 5:** Commit `feat(agents): idempotent migration to state.md, absorbing the skills reconciler`.

---

### Task 4: Delete `agent.json` machinery

**Files:**
- Modify: `internal/agentdesigner/manifest.go` (delete `AgentManifest`, `LoadManifest`, `AgentManifestPath`, `ReconcileSkillAttachmentsToDB`, `manifestIsFallbackBloat` if now unused), `internal/agentdesigner/designer.go:126-135` (stop writing the manifest), `internal/agentdesigner/flow.go` (export `parseRequiredSecrets` → `ParseRequiredSecrets`), `internal/agentrunner/runner.go:152-155`, `web/handlers_agents.go:386,419-427`
- Test: extend `web/api_agents_test.go`

**Interfaces:**
- Produces: `ParseRequiredSecrets(agentMD string) []string` (exported rename of the existing private function; behaviour unchanged).
- Consumers read AGENT.md (already loaded on both paths — the detail handler reads it at `handlers_agents.go` for `data.AgentMD`, the runner reads it for the prompt) and call `ParseRequiredSecrets`.

Note for the implementer: `grep -n "manifest\." internal/agentrunner/runner.go` currently returns nothing — the runner loads the manifest and passes it to `runCoderAgent` without dereferencing a field. Check `runCoderAgent`'s signature: if the parameter is genuinely unused, **drop the parameter** rather than passing a synthesised struct.

- [ ] **Step 1: Write the failing test** — assert the API still reports missing secrets after the manifest is gone:

```go
func TestAPIAgentDetailMissingSecretsFromAgentMD(t *testing.T) {
	// Seed an agent whose AGENT.md declares required secrets, with NO agent.json.
	// Expect detail.missing_secrets to contain the undeclared one.
	// (Build on the existing seedAgent helper + the vault temp dir used by
	// TestAPIAgentsListDetailSchedule.)
	md := "# Agent\n\n# Required secrets:\n# - SENDGRID_KEY: for sending\n"
	// …write md to AgentDescPath(...), no agent.json…
	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if !contains(rec.Body.String(), "SENDGRID_KEY") {
		t.Fatalf("missing_secrets should come from AGENT.md: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2:** Verify FAIL, then delete the manifest machinery and repoint both consumers to `ParseRequiredSecrets(agentMD)`.
- [ ] **Step 3:** Prove completeness: `go build ./...` clean **and** `grep -rn "agent.json\|AgentManifest\|LoadManifest" --include=*.go . | grep -v migrate_files` returns zero.
- [ ] **Step 4:** `go test ./... -count=1 -timeout 120s` → PASS.
- [ ] **Step 5:** Commit `refactor(agents)!: delete agent.json — secrets come from AGENT.md`.

---

### Task 5: KB opens code and binary files

**Files:**
- Modify: `web/api_kb.go` (`apiGetKBNote` gains `kind`), `web/api_kb_test.go`
- Modify: `web/ui/src/lib/kb.ts` (type), `web/ui/src/pages/kb/KBPage.tsx` (route by kind), `web/ui/src/pages/kb/NoteEditor.tsx` (unchanged) 
- Create: `web/ui/src/pages/kb/FileViewer.tsx` (+ `fileviewer.test.tsx`)

**Interfaces:**
- Produces: `apiKBNoteResponse.Kind string` — `"markdown" | "code" | "binary"`; frontend `KBNote.kind`.
- Rule (verbatim from spec §7): `kind` is decided by **content sniffing**, not an extension allowlist — `markdown` for `.md`; otherwise `code` when the bytes are valid UTF-8 **and** the file is under **1 MB**; else `binary`. `binary` responses omit `content`.
- `KBPage`'s current gate `path.endsWith(".md")` is replaced by "any file path opens"; the editor renders for `kind==="markdown"`, `FileViewer` for `code`/`binary`.

- [ ] **Step 1: Failing Go test** — `.py` returns `kind:"code"` with content; a >1 MB file returns `kind:"binary"` with empty content; a `.md` file still returns `kind:"markdown"`; invalid UTF-8 returns `binary`.
- [ ] **Step 2:** Implement using `utf8.Valid(raw)` and a `const kbInlineMax = 1 << 20`.
- [ ] **Step 3: Failing frontend test** — `FileViewer` renders `.py` content in a `<pre>` with no save affordance; a `binary` kind shows "Binary file" + a Download link to `rawURL(path)`.
- [ ] **Step 4:** Implement `FileViewer` (monospace `<pre>` with `overflow-auto`, the existing UI-owned header pattern: breadcrumb, Download, Delete — **no save button**), and route from `KBPage` by `kind`.
- [ ] **Step 5:** Suites green; commit `feat(kb): open code and data files read-only (kind discriminator)`.

---

### Task 6: 409 `agent_running` guard on `state.md` saves

**Files:**
- Modify: `web/api_kb.go` (`apiSaveKBNote`), `web/api_kb_test.go`
- Modify: `web/ui/src/pages/kb/NoteEditor.tsx` (surface the envelope message)

**Interfaces:**
- Produces: `PUT /api/v1/kb/note` → `409 {"error":{"code":"agent_running","message":"…"}}` when the path matches `agents/<agentID>/state.md` and that agent has a run in flight.
- Consumes: the existing in-flight check used by the agent detail page (`s.isAgentRunning(agentID)` in `web/run_tracker.go`).

- [ ] **Step 1: Failing Go test:**

```go
func TestAPISaveKBNoteBlockedWhileAgentRunning(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)
	s.runs[a.ID] = &agentRunState{} // in-flight, mirrors TestAPIRunAgentAlreadyRunning

	path := "agents/" + a.ID + "/state.md"
	rec := doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": path, "content": "# State\n"}, cookies)
	if rec.Code != http.StatusConflict || !contains(rec.Body.String(), "agent_running") {
		t.Fatalf("expected 409 agent_running, got %d %s", rec.Code, rec.Body.String())
	}

	// A different note is unaffected.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/free.md", "content": "hi"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("unrelated note should save: %d", rec.Code)
	}
}
```

- [ ] **Step 2:** Verify FAIL, then implement: a small `agentIDFromStatePath(rel string) (string, bool)` helper (matches `agents/<id>/state.md`, `<id>` non-empty, no deeper nesting) checked before the write.
- [ ] **Step 3:** Frontend — the save-error path already renders `ApiError.message`; add a test that a 409 shows the message and leaves the editor dirty (so the user can retry after the run).
- [ ] **Step 4:** Suites green; commit `feat(kb): refuse state.md saves while the agent is running`.

---

### Task 7: Prompts + fidelity corpus

**Files:**
- Modify: `internal/prompts/prompts.go` lines ~122, ~292, ~372, ~409, ~638 (every `state.json` mention)
- Modify: `web/ui/src/pages/kb/corpus.test.ts`
- Test: `internal/prompts/prompts_test.go` (add) 

- [ ] **Step 1:** Add a Go test asserting no built prompt string contains `state.json` and that the runtime prompt mentions `state.md`. Verify FAIL.
- [ ] **Step 2:** Update all five sites: the file is `state.md`; its json fence is the memory; `## Notes` is the agent's own prose space; `[STATE]` output is unchanged (line ~638's "not 'write to state.json'" becomes "not 'write to state.md'" — the point of that sentence is preserved: agents emit the marker, they do not edit the file).
- [ ] **Step 3:** Add a corpus entry pinning `RenderStateTemplate("Gmail Digest", "{\n  \"a\": 1\n}")` output as **clean** (`expectLossy: false`) so an editor upgrade that breaks state.md fails loudly. Import the template string as a literal in the test — do not import Go.
- [ ] **Step 4:** Suites green; commit `feat(prompts): agents' state lives in state.md`.

---

### Task 8: Docs and close-out

**Files:** Modify `/home/user/simple-agents-v2/CLAUDE.md`.

- [ ] **Step 1:** Update the vault-layout diagram (`agents/<agentID>/` block: `AGENT.md  state.md` — drop `agent.json`/`state.json`), the agent-output-protocol section (`[STATE]` merges into `state.md`'s json fence), and the "Skill attachments source of truth" note (the one-time `ReconcileSkillAttachmentsToDB` is now folded into `MigrateAgentFilesToMarkdown`). Add one line to the KB section that non-markdown files open read-only.
- [ ] **Step 2:** Full suites: `go test ./... -count=1 -timeout 120s`; `cd web/ui && npm test -- --run`; `npm run build`.
- [ ] **Step 3:** Smoke on `SA_PORT=8090` (kill only your own PID; production runs on 8080): serve, then `curl` the KB tree for an agent dir and confirm `state.md` is listed; `GET /api/v1/kb/note?path=agents/<id>/state.md` returns `kind:"markdown"`; a `tools/*.py` path returns `kind:"code"`. Record output in the report.
- [ ] **Step 4:** Commit `docs: agent files are documents — state.md, no manifest`.

---

## Self-review notes (already applied)

- **Spec coverage:** §4 state format → Task 1; §4.2 parsing → Task 1; §4.3 editing guard → Task 6; §5 manifest removal → Task 4 (with the corrected secrets story); §6 migration → Task 3 (plus the reconciler absorption the spec missed); §7 KB kinds → Task 5; §8 prompts → Task 7; §9 testing → distributed, with the corpus entry in Task 7 and the E2E state persistence in Task 2; §10 risks → each mitigation lands in its named task.
- **Two spec corrections are stated up front** rather than silently implemented: no new `# Secrets:` header is needed (the parser already exists), and the migration must absorb `ReconcileSkillAttachmentsToDB` before deleting `agent.json`.
- **Type consistency:** `StateFilePath`/`ReadState`/`WriteState`/`RenderStateTemplate` (Task 1) are used by name in Tasks 2, 3, 7; `ParseRequiredSecrets` (Task 4) is the exported rename; `kind` values are the same three strings in Task 5's Go and TS.
- **Ordering is load-bearing:** the migration (Task 3) reads `agent.json` with its own private struct so Task 4's deletion cannot break it.
