# Agent State Consistency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make an agent's two ways of recording memory — the `[STATE]` marker and
editing `state.md` directly — agree, so a well-behaved agent cannot malform its
state and a malformed file repairs itself.

**Architecture:** A new `internal/agentstate` package owns the file format and is
the only code that touches `state.md`. Three surfaces converge on it: the
`[STATE]` marker (runner), `get_state`/`set_state` host tools (API engine), and
`rookery state get|set` over a loopback bridge (CLI coders). `agentdesigner`'s
existing exported functions become thin delegates so no call site changes.

**Tech Stack:** Go 1.26.6 (`GOTOOLCHAIN=auto` required — host Go is 1.26.5),
stdlib only. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-08-19-agent-state-consistency-design.md`

## Global Constraints

- **Nothing existing may break.** A canonical `state.md` must be byte-identical
  after a no-op `Apply`. This is pinned by a test and is the single most
  important assertion in the plan.
- `agentdesigner.ReadState`, `WriteState`, `StateFilePath`, `RenderStateTemplate`
  keep their **exact current signatures**.
- The `[STATE]` output protocol is unchanged. Existing agents keep working with
  no rebuild.
- The dry run still discards its state — `restoreDryRunState` is not touched.
- `MigrateAgentFilesToMarkdown` is not touched.
- The KB 409 guard on a running agent's `state.md` is not touched.
- **Chat gets no state tools.** Agent runs only.
- No schema change, no migration, no new dependency.
- All JSON decoding uses `json.Number` (`dec.UseNumber()`) — plain `Unmarshal`
  rounds integers above 2^53, silently corrupting a 64-bit Discord snowflake.
- `maxStateSize` (currently in `agentrunner`) applies to every write path.
- Go commands need `GOTOOLCHAIN=auto`.

---

## File Structure

| file | responsibility |
|---|---|
| `internal/agentstate/state.go` (new) | format: fence location, render, tolerant `Get`, merging `Apply` |
| `internal/agentstate/state_test.go` (new) | golden round-trips for every observed file shape |
| `internal/agentstate/bridge.go` (new) | loopback HTTP bridge for CLI coders |
| `internal/agentstate/bridge_test.go` (new) | bridge auth + round-trip |
| `internal/agentdesigner/statefile.go` (modify) | becomes thin delegates |
| `internal/agentrunner/runner.go` (modify) | `saveState`/`applyAndSaveState` route through `agentstate` |
| `internal/coder/statetools.go` (new) | `get_state`/`set_state` host tools |
| `cmd/rookery/main.go` (modify) | `rookery state` subcommand + bridge start + env injection |
| `internal/prompts/prompts.go` (modify) | document the tool; keep `[STATE]` |

---

### Task 1: `internal/agentstate` — the choke point

**Files:**
- Create: `internal/agentstate/state.go`
- Test: `internal/agentstate/state_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func StateFilePath(agentDir string) string`
  - `func RenderTemplate(agentName, jsonBody string) string`
  - `func Get(path string) (map[string]any, bool, error)` — the bool is
    `understood`, distinct from "state is empty"
  - `func Apply(path, agentName string, patch map[string]any) (map[string]any, error)`
  - `func Merge(existing, patch map[string]any)`
  - `const MaxStateSize = 64 * 1024`

- [ ] **Step 1: Write the failing tests**

Create `internal/agentstate/state_test.go`. These are the shapes observed in
production — each is a real file that existed on the deployed server.

```go
package agentstate

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "state.md")
	if err := os.WriteFile(p, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

const canonical = "# State — a\n\n*Managed by Rookery. The block below is this agent's memory between runs — edit it if you need to fix something by hand.*\n\n" +
	"```json\n{\n  \"seen\": 1\n}\n```\n"

// A canonical file must survive a no-op Apply byte-for-byte. This is the
// regression that would mean working agents had been broken.
func TestCanonicalFileIsByteIdenticalAfterNoOpApply(t *testing.T) {
	p := write(t, canonical)
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != canonical {
		t.Fatalf("file changed:\n--- want ---\n%s\n--- got ---\n%s", canonical, got)
	}
}

// The hn-watch shape: a VALID but EMPTY fence with the agent's real memory
// stranded one line below it. ReadState returned {} here, which is why the
// agent re-baselined and went silent on every run forever.
func TestRecoversStateStrandedBelowAnEmptyFence(t *testing.T) {
	p := write(t, "```json\n{}\n```\n{\"reported_ids\": [49355606, 49358259]}\n\n## Notes\n\nFirst run.\n")

	st, understood, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if !understood {
		t.Fatal("a recoverable file must report understood=true")
	}
	ids, ok := st["reported_ids"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("stranded state not recovered: %#v", st)
	}
}

// Recovery is not enough on its own — the file must end up canonical so the
// next run reads it the normal way.
func TestRecoveredFileIsNormalisedOnApply(t *testing.T) {
	p := write(t, "```json\n{}\n```\n{\"seen\": 7}\n")
	if _, err := Apply(p, "a", nil); err != nil {
		t.Fatal(err)
	}
	st, _, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if st["seen"] == nil {
		t.Fatalf("state lost by normalisation: %#v", st)
	}
	raw, _ := os.ReadFile(p)
	if got := string(raw); !contains(got, "# State — a") {
		t.Fatalf("file not normalised:\n%s", got)
	}
}

// A legitimate fence inside ## Notes must never be mistaken for state when the
// state fence itself is populated.
func TestPopulatedFenceWinsOverALaterFence(t *testing.T) {
	p := write(t, "```json\n{\"real\": 1}\n```\n\n## Notes\n\n```json\n{\"example\": 2}\n```\n")
	st, _, err := Get(p)
	if err != nil {
		t.Fatal(err)
	}
	if st["real"] == nil || st["example"] != nil {
		t.Fatalf("wrong fence used: %#v", st)
	}
}

// nil deletes; a patch merges rather than replaces.
func TestPatchMergesAndNilDeletes(t *testing.T) {
	p := write(t, canonical)
	if _, err := Apply(p, "a", map[string]any{"other": 2}); err != nil {
		t.Fatal(err)
	}
	st, _, _ := Get(p)
	if st["seen"] == nil || st["other"] == nil {
		t.Fatalf("patch replaced instead of merging: %#v", st)
	}
	if _, err := Apply(p, "a", map[string]any{"seen": nil}); err != nil {
		t.Fatal(err)
	}
	st, _, _ = Get(p)
	if _, still := st["seen"]; still {
		t.Fatalf("nil did not delete: %#v", st)
	}
}

// A file we genuinely cannot understand must report understood=false so the
// caller's no-update turn stays a no-op rather than overwriting it with {}.
func TestUnparseableFenceReportsNotUnderstood(t *testing.T) {
	p := write(t, "```json\n{not json\n```\n")
	_, understood, _ := Get(p)
	if understood {
		t.Fatal("a broken fence must report understood=false")
	}
}

// A missing file is empty memory, not an error.
func TestMissingFileIsEmptyAndUnderstood(t *testing.T) {
	st, understood, err := Get(filepath.Join(t.TempDir(), "none.md"))
	if err != nil || !understood || len(st) != 0 {
		t.Fatalf("got %#v understood=%v err=%v", st, understood, err)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=auto go test ./internal/agentstate/ -count=1`
Expected: FAIL — the package does not exist yet.

- [ ] **Step 3: Implement the package**

Create `internal/agentstate/state.go`. **Move** — do not re-invent —
`findStateFence`, the fence-splice logic and `RenderStateTemplate` from
`internal/agentdesigner/statefile.go`; that code is carefully reasoned and its
comments must travel with it. Rename `RenderStateTemplate` → `RenderTemplate`
in the new package.

Then add on top of it:

```go
// Get reads an agent's state, recovering where the file is malformed.
//
// The second return is `understood` — whether we made sense of the file. It is
// deliberately distinct from "the state is empty", which is a legitimate
// outcome for a fresh agent. Callers use it to decide whether a no-update turn
// may safely write back; overwriting a file we could not parse would destroy
// hand-recoverable state.
func Get(path string) (map[string]any, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	lines := strings.Split(string(raw), "\n")
	loc := findStateFence(lines)

	if loc.OK {
		body := strings.TrimSpace(strings.Join(lines[loc.Open+1:loc.Close], "\n"))
		if body != "" {
			st, err := decode(body)
			if err != nil {
				// A well-formed fence whose body is not JSON: we do NOT guess
				// past it. The fence is the declared location; a broken one is
				// a human's problem, not a cue to go hunting.
				return map[string]any{}, false, nil
			}
			if len(st) > 0 {
				return st, true, nil
			}
		}
	}

	// Fence empty or absent. THIS is the hn-watch case: an agent wrote the file
	// itself and put its memory somewhere the reader never looked. Scan for the
	// first parseable JSON object and adopt it.
	//
	// Narrow on purpose. Only reached when the fence has nothing to offer, and
	// only the FIRST object is taken. The residual risk is an agent with truly
	// empty state and a JSON example in its prose, which would adopt the
	// example — unlikely, self-correcting on the next patch, and far better
	// than a correct agent going silent forever.
	if st, ok := scanFirstJSONObject(raw, loc); ok {
		return st, true, nil
	}
	return map[string]any{}, true, nil
}

// Apply merges patch into the file's current state and writes it back. It is
// the single writer: the [STATE] marker, the API engine's set_state and the CLI
// bridge all land here, so the three doors cannot drift apart.
//
// A nil or empty patch still writes, which is what normalises a recovered file.
// The exception is a file we could not understand — see Get.
func Apply(path, agentName string, patch map[string]any) (map[string]any, error) {
	st, understood, err := Get(path)
	if err != nil {
		return nil, err
	}
	if !understood && len(patch) == 0 {
		return st, nil
	}
	Merge(st, patch)

	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(body) > MaxStateSize {
		return nil, fmt.Errorf("state too large (%d bytes > %d limit)", len(body), MaxStateSize)
	}
	return st, writeFence(path, agentName, string(body))
}

// Merge applies a patch in place. A nil value deletes the key — the semantic
// [STATE] has always had, now the rule for every door.
func Merge(existing, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(existing, k)
		} else {
			existing[k] = v
		}
	}
}
```

`decode` wraps `json.NewDecoder` + `dec.UseNumber()` (integer fidelity above
2^53 — see the Global Constraints).

`scanFirstJSONObject` walks the raw bytes for a `{`, attempts a `json.Decoder`
decode from that offset, and returns the first object that parses into a
non-empty `map[string]any`. It must **skip the region inside the state fence**
(`loc`) so an empty fence's own `{}` is not "recovered" as the answer.

`writeFence` is the existing splice logic, plus: when `Get` had to recover, the
file is re-rendered from `RenderTemplate` with the recovered JSON in the fence
and any prose that was not the recovered object preserved after it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=auto go test ./internal/agentstate/ -count=1`
Expected: PASS (all 7).

- [ ] **Step 5: Mutation-check the two load-bearing tests**

Break the recovery scan (return `false` unconditionally) → confirm
`TestRecoversStateStrandedBelowAnEmptyFence` fails. Break the splice so it
re-renders unconditionally → confirm
`TestCanonicalFileIsByteIdenticalAfterNoOpApply` fails. Restore both. Report
the observed failure text for each.

- [ ] **Step 6: Commit**

```bash
git add internal/agentstate/
git commit -m "feat(agentstate): own the state file format in one package"
```

---

### Task 2: `agentdesigner` delegates

**Files:**
- Modify: `internal/agentdesigner/statefile.go`
- Test: `internal/agentdesigner/statefile_test.go` (existing — must still pass unchanged)

**Interfaces:**
- Consumes: Task 1's `agentstate.Get`, `Apply`, `RenderTemplate`.
- Produces: unchanged public signatures —
  `ReadState(path) (map[string]any, error)`,
  `WriteState(path, agentName string, state map[string]any) error`,
  `StateFilePath(vaultsBase, workspaceID, agentID string) string`,
  `RenderStateTemplate(agentName, jsonBody string) string`.

- [ ] **Step 1: Run the existing tests first and record the baseline**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -run State -count=1 -v`
Record which tests exist and that they pass. They are the contract; **not one
of them may be edited in this task.** If a test needs changing, stop and report
— that means behaviour changed and the delegation is wrong.

- [ ] **Step 2: Replace the bodies with delegates**

```go
func ReadState(path string) (map[string]any, error) {
	st, _, err := agentstate.Get(path)
	return st, err
}

func WriteState(path, agentName string, state map[string]any) error {
	// Whole-state write, not a patch: callers of this function pass the full
	// map they intend the file to hold. Apply merges, so clear first.
	_, err := agentstate.Replace(path, agentName, state)
	return err
}

func RenderStateTemplate(agentName, jsonBody string) string {
	return agentstate.RenderTemplate(agentName, jsonBody)
}
```

**`agentstate.Replace` is required by this task** — add it in Task 1's package
if not already present:

```go
// Replace sets the file's state to exactly `state`, discarding what was there.
// WriteState's long-standing contract: its caller has already done any merging
// and passes the full intended contents.
func Replace(path, agentName string, state map[string]any) (map[string]any, error)
```

- [ ] **Step 3: Run the existing tests unchanged**

Run: `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ -count=1`
Expected: PASS, with no test file edited.

- [ ] **Step 4: Commit**

```bash
git add internal/agentdesigner/statefile.go internal/agentstate/
git commit -m "refactor(agentdesigner): delegate state file handling to agentstate"
```

---

### Task 3: The runner routes through `agentstate`

**Files:**
- Modify: `internal/agentrunner/runner.go` (`saveState` ~1163, `applyAndSaveState` ~1200, `mergeState` ~1151, the read at ~309)
- Test: `internal/agentrunner/runner_test.go`

**Interfaces:**
- Consumes: `agentstate.Get`, `agentstate.Apply`, `agentstate.Merge`.
- Produces: no signature changes outside the package.

- [ ] **Step 1: Write the failing test**

```go
// The hn-watch failure, end to end: an agent whose state.md has an empty fence
// with real memory below it must get that memory back on the next run, and the
// file must be canonical afterwards.
func TestRunRecoversStrandedStateAndNormalisesTheFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.md")
	os.WriteFile(p, []byte("```json\n{}\n```\n{\"seen\":[1,2,3]}\n"), 0o640)

	st, understood, err := agentstate.Get(p)
	if err != nil || !understood {
		t.Fatalf("understood=%v err=%v", understood, err)
	}
	if st["seen"] == nil {
		t.Fatalf("stranded state not recovered: %#v", st)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=auto go test ./internal/agentrunner/ -run Stranded -count=1`
Expected: FAIL until Task 1 is wired in.

- [ ] **Step 3: Replace the internals**

- The initial read at `runner.go:309` becomes
  `stateMap, stateReadOK, err := agentstate.Get(...)`. **`stateReadOK` keeps its
  exact meaning** — the guard in `applyAndSaveState` is unchanged; a recovered
  file now reports `true`, which is the point.
- `saveState` delegates to `agentstate.Replace` (keeping the `maxStateSize`
  check, or deferring to `agentstate.MaxStateSize` — pick one and delete the
  other so there is a single limit).
- `mergeState` delegates to `agentstate.Merge`.
- Leave `applyAndSaveState`'s control flow and its long comment intact.

- [ ] **Step 4: Run the package tests**

Run: `GOTOOLCHAIN=auto go test ./internal/agentrunner/ -count=1`
Expected: PASS, including the pre-existing state tests.

- [ ] **Step 5: Commit**

```bash
git add internal/agentrunner/
git commit -m "fix(agentrunner): recover stranded agent state instead of reading it as empty"
```

---

### Task 4: API engine host tools

**Files:**
- Create: `internal/coder/statetools.go`
- Modify: `internal/coder/hosttools.go` (register in `hostToolSet`)
- Test: `internal/coder/statetools_test.go`

**Interfaces:**
- Consumes: `agentstate.Get`, `agentstate.Apply`.
- Produces: two tools, `get_state` (no args) and `set_state` (`{"patch": {...}}`).

Mirror `internal/coder/connectortools.go` for shape: tool definition, argument
validation, an 8 KiB result cap consistent with `maxToolResult`.

**Gating:** these tools are available on the same terms as the exec tools —
agent builds and runs only, never chat. Follow `includeExecTools`.

- [ ] **Step 1: Write the failing test**

```go
func TestSetStateMergesAndGetStateReturnsIt(t *testing.T) {
	dir := t.TempDir()
	ts := newStateTools(filepath.Join(dir, "state.md"), "a")

	if _, err := ts.call("set_state", map[string]any{"patch": map[string]any{"seen": 1}}); err != nil {
		t.Fatal(err)
	}
	out, err := ts.call("get_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\"seen\"") {
		t.Fatalf("state not returned: %s", out)
	}
}

// Chat must not be able to reach an agent's memory.
func TestStateToolsAreAbsentWithoutExecTools(t *testing.T) {
	set := hostToolSet(hostToolConfig{includeExecTools: false})
	for _, d := range set.defs() {
		if d.Name == "get_state" || d.Name == "set_state" {
			t.Fatalf("%s offered to a non-agent surface", d.Name)
		}
	}
}
```

Adjust constructor/helper names to the file's real conventions once read.

- [ ] **Step 2: Run to verify failure, 3: implement, 4: run to verify pass**

Run: `GOTOOLCHAIN=auto go test ./internal/coder/ -run State -count=1`

- [ ] **Step 5: Commit**

```bash
git add internal/coder/
git commit -m "feat(coder): offer get_state and set_state to the API engine"
```

---

### Task 5: CLI bridge and `rookery state` subcommand

**Files:**
- Create: `internal/agentstate/bridge.go`, `internal/agentstate/bridge_test.go`
- Modify: `cmd/rookery/main.go` (subcommand + bridge start + env injection)

**Interfaces:**
- Consumes: `agentstate.Get`, `agentstate.Apply`.
- Produces: `NewBridge()`, `(*Bridge).Start(ctx) (string, error)`,
  `(*Bridge).Register(agentDir, agentName string) string`,
  `(*Bridge).Unregister(token string)`, `(*Bridge).Addr() string`.

**Mirror `internal/mcp/bridge.go` exactly** — same listener setup, same
`Register`/`Unregister`/`session` shape, same bearer-token check, same 8 KiB
result cap. Routes: `POST /state/get`, `POST /state/set`.

Env: `ROOKERY_STATE_URL`, `ROOKERY_STATE_TOKEN`, injected into the agent run's
`extraEnv` beside the connector and MCP pairs.

**No new tool grant is needed** — agent runs already allow `Bash`. Do not touch
`coder.ChatAllowedTools`.

- [ ] **Step 1: Write the failing test** (mirror `internal/mcp/bridge_test.go`)

```go
func TestBridgeRejectsAnUnknownToken(t *testing.T) {
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	req, _ := http.NewRequest("POST", addr+"/state/get", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer nope")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestBridgeRoundTripsState(t *testing.T) {
	dir := t.TempDir()
	b := NewBridge()
	addr, _ := b.Start(t.Context())
	tok := b.Register(dir, "a")
	defer b.Unregister(tok)

	post(t, addr+"/state/set", tok, `{"patch":{"seen":1}}`)
	if got := post(t, addr+"/state/get", tok, `{}`); !strings.Contains(got, "seen") {
		t.Fatalf("round trip lost state: %s", got)
	}
}
```

- [ ] **Step 2: Run to verify failure, 3: implement bridge, 4: verify pass**

- [ ] **Step 5: Add the subcommand**

`rookery state get` and `rookery state set --patch '<json>'`, modelled on the
`connector exec` subcommand at `cmd/rookery/main.go:743`. Reads
`ROOKERY_STATE_URL`/`ROOKERY_STATE_TOKEN`; prints the JSON result to stdout;
non-zero exit on failure.

- [ ] **Step 6: Wire the bridge into `serve`** beside the connector/MCP bridges,
      register per run, unregister when the run ends.

- [ ] **Step 7: Run the package tests and commit**

```bash
git add internal/agentstate/ cmd/rookery/
git commit -m "feat(agentstate): reach agent state from CLI coders over a loopback bridge"
```

---

### Task 6: Prompts

**Files:**
- Modify: `internal/prompts/prompts.go`
- Test: `internal/prompts/prompts_test.go`

Describe state as: injected into the prompt for reading, `get_state`/`set_state`
(or `rookery state`, per backend) for updating, and `[STATE]` still available as
the one-shot at end of run. Keep it backend-aware the way
`coderCapabilitiesBlock` already is.

**Do not** tell the agent to hand-edit the fence. Do **not** forbid it from
touching the file — it may keep prose notes there, and the owner edits it too.

- [ ] **Step 1: Write the failing test**

```go
func TestRuntimePromptOffersTheStateToolsAndKeepsTheMarker(t *testing.T) {
	p := BuildCoderPrompt(CoderParams{AgentMD: "x", BackendType: BackendToolCalling})
	for _, want := range []string{"set_state", "[STATE]"} {
		if !strings.Contains(p, want) {
			t.Errorf("runtime prompt missing %q", want)
		}
	}
}
```

- [ ] **Steps 2-4: fail, implement, pass. Step 5: Commit**

```bash
git commit -am "feat(prompts): tell agents how to update state without editing the file"
```

---

### Task 7: The four ride-along cleanups

**Files:**
- Modify: `internal/agentdesigner/dryrun.go`, `internal/prompts/` (new const home),
  `internal/agentdesigner/flow.go:920`, `internal/agentrunner/runner.go:232`

Each is independent; commit separately so any one can be reverted alone.

- [ ] **Step 1: Move `dryRunSendProhibition` to `internal/prompts`**

House convention: no prompt text outside that package (`kbassist.go` is the
precedent). Export it, update the one call site in `dryRunPrompt`.
Run `GOTOOLCHAIN=auto go test ./internal/agentdesigner/ ./internal/prompts/ -count=1`.

- [ ] **Step 2: Fix `isDryRunSilent`**

It scans every line, so `[CHAT] something` followed by `[SILENT]` renders
"nothing to report" — diverging from `parseCoderOutput`, where chat wins.

Failing test first:

```go
func TestDryRunPrefersChatOverALaterSilentMarker(t *testing.T) {
	out, ok := dryRunOutput("[CHAT]\nSkopje is 24C.\n[SILENT]")
	if !ok || !strings.Contains(out, "24C") {
		t.Fatalf("chat content lost to a later [SILENT]: ok=%v out=%q", ok, out)
	}
}
```

- [ ] **Step 3: Delete `agentrunner.TestRunFromContent`**

Exported, zero callers, zero tests, now a near-duplicate of `dryRun`. Confirm
with `grep -rn "TestRunFromContent" --include=*.go .` before deleting.

- [ ] **Step 4: Stop `saveDraft` swallowing its error** (`flow.go:920`)

`_ = f.db.UpsertAgentDraft(...)` becomes a checked call that logs on failure —
`slog.Warn` with `workspace_id`, never the error text of a provider call.

- [ ] **Step 5: Commit each separately**

---

### Task 8: Documentation

**Files:**
- Modify: `CLAUDE.md` (the "Agent state" section)

- [ ] **Step 1: Record the design**

Add to the `state.md` paragraph: the three doors and the one choke point; that
`Merge` semantics (`null` deletes) are identical across all three; that `Get`
recovers JSON stranded outside an empty fence and `Apply` normalises the file;
that `understood` is deliberately distinct from "empty" and why
(`applyAndSaveState`'s no-update guard depends on it); and that recovery cannot
help an agent whose baseline was prose, which is what the tools are for.

Match the file's house style: dense prose explaining WHY, naming the failure
that produced the rule. Reference the real incident — two of four agents,
permanently silent, ~930k tokens an hour.

- [ ] **Step 2: Run the docs check**

Run: `GOTOOLCHAIN=auto make docs-sync-check`
Expected: pass.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: record the agent-state choke point and recovery"
```

---

## Self-Review

**Spec coverage.** Choke point → Task 1. Delegation/no-break → Task 2. Runner →
Task 3. API tools → Task 4. CLI bridge + subcommand → Task 5. Prompts → Task 6.
Ride-alongs → Task 7. Docs → Task 8. Dry-run discard, KB guard, migration and
schema are all explicitly *untouched* and appear in the Global Constraints.

**Placeholders.** None: every step names a command, a file, or carries code.

**Type consistency.** `Get` returns `(map[string]any, bool, error)` in Tasks 1,
2 and 3. `Apply` and `Replace` both return `(map[string]any, error)`; `Replace`
is introduced in Task 1 because Task 2 needs it — noted in both. `Merge` has
one signature throughout. Bridge methods match `internal/mcp/bridge.go`.

**Ordering.** Task 1 must land first; 2 and 3 depend on it; 4 and 5 depend on 1
only and could run in parallel but share `cmd/rookery/main.go` with nothing
else, so sequential is fine. 7 is independent of everything. 8 is last.

**Known risk.** The recovery scan is a heuristic and the spec says so. It is
bounded to an empty/absent fence, takes only the first object, skips the fence
region itself, and is self-correcting on the next patch.
