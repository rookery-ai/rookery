# Multi-coder CLI backends Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make several popular coder CLIs (Claude, OpenCode, Codex, Gemini, Cursor) work as autodetected, per-workspace-isolated local coders, fixing OpenCode and generalizing the Claude-hardcoded config isolation.

**Architecture:** Extend the existing `internal/coder/CoderBackend` interface with a declarative per-coder isolation contract (`configEnv` + `seedFiles`), add one struct per coder for invocation + output parsing, derive the backend type from the selected binary, and gate every coder behind a fail-loud `Smoke` end-to-end check.

**Tech Stack:** Go, `internal/coder` package, Echo web handlers, `go test`.

## Global Constraints

- Backends MUST be stateless (no mutable fields) — `selectBackend()` constructs them per `Generate` call.
- The API coder path (`coder_kind == "api"`) MUST NOT change — this is only the `local` CLI path.
- No DB migration — `workspaces.coder_backend_type` already exists and carries new values.
- Seed operator **auth/credentials only** — never seed session DBs, history, or logs (each workspace starts fresh).
- Backend type values (local): `claude`, `opencode`, `codex`, `gemini`, `cursor`, `generic` (fallback).
- OpenCode is the only non-Claude coder installed on this host (`/home/rookie/.opencode/bin/opencode`); Codex/Gemini/Cursor adapters are authored-unverified and MUST be labeled as such in code comments.
- Prefer explicit config-dir env vars over POSIX `HOME` for cross-platform (Windows) isolation.
- Existing tests MUST stay green: `go test ./internal/coder/... ./internal/prompts/... ./web/... -count=1`.
- Build check: `go build -o bin/simple-agents ./cmd/simple-agents`.

## File Structure

- `internal/coder/backend.go` — MODIFY: extend `CoderBackend` interface (`configEnv`, `seedFiles`), add `seedSpec` type + shared helpers (`forwardEnv`, NDJSON/single-object parsers), refactor `claudeBackend`, add `opencodeBackend`/`codexBackend`/`geminiBackend`/`cursorBackend`, keep `genericCLIBackend` as fallback.
- `internal/coder/coder.go` — MODIFY: rewire `ensureUserHome`/`buildEnv` to the new contract; extend `selectBackend()`; add `Smoke`.
- `internal/coder/detect.go` — MODIFY: `knownCoders` carries real per-coder backend types; add `BackendForBin`.
- `internal/coder/backend_test.go` — CREATE: arg-building, parser, configEnv/seedFiles, limit-detection unit tests.
- `internal/coder/smoke_test.go` — CREATE: OpenCode end-to-end smoke test (host-gated).
- `internal/coder/detect_test.go` — CREATE: `BackendForBin` mapping tests.
- `internal/db/models.go` — MODIFY: update the `CoderBackendType` comment.
- `web/handlers_misc.go`, `web/handlers_setup.go` — MODIFY: derive backend type from chosen bin; add Smoke handler.
- `web/server.go` — MODIFY: register the Smoke route + a `Smoke`-capable factory if needed.
- `web/templates/dashboard/settings.html` — MODIFY: add a "Test coder" button.
- `internal/prompts/prompts_test.go` — MODIFY: lock the new backend types → `BackendFullCoder`.
- `CLAUDE.md` — MODIFY: document the multi-coder backends.

---

### Task 1: Generalize the isolation contract (interface + Claude refactor)

Replace the Claude-specific `setupHome`/`extraEnvForUser` hooks with a declarative `configEnv` +
`seedFiles` contract. Claude behavior must stay byte-identical (regression-guarded).

**Files:**
- Modify: `internal/coder/backend.go` (interface at lines 17-36; `claudeBackend` 43-97; `genericCLIBackend` 110-151)
- Modify: `internal/coder/coder.go` (`ensureUserHome` 489-504; `buildEnv` 508-526)
- Test: `internal/coder/backend_test.go` (create)

**Interfaces:**
- Produces: `type seedSpec struct { From, To string; Mode os.FileMode }`
- Produces: `CoderBackend.configEnv(workspaceHome string) map[string]string`
- Produces: `CoderBackend.seedFiles(workspaceHome string) []seedSpec`
- Produces: `func forwardEnv(keys ...string) map[string]string` (returns only env vars currently set)
- Consumes (from existing): `claudeBackend{ sysClaudeDir string }`

- [ ] **Step 1: Write the failing test**

Add to `internal/coder/backend_test.go`:

```go
package coder

import (
	"path/filepath"
	"testing"
)

func TestClaudeConfigEnvAndSeed(t *testing.T) {
	b := &claudeBackend{sysClaudeDir: "/op/.claude"}
	home := "/homes/ws1"

	env := b.configEnv(home)
	if got := env["CLAUDE_CONFIG_DIR"]; got != filepath.Join(home, ".claude") {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", got, filepath.Join(home, ".claude"))
	}

	seeds := b.seedFiles(home)
	if len(seeds) != 1 {
		t.Fatalf("seedFiles len = %d, want 1", len(seeds))
	}
	if seeds[0].From != "/op/.claude/.credentials.json" {
		t.Fatalf("seed From = %q", seeds[0].From)
	}
	if seeds[0].To != filepath.Join(home, ".claude", ".credentials.json") {
		t.Fatalf("seed To = %q", seeds[0].To)
	}
	if seeds[0].Mode != 0o600 {
		t.Fatalf("seed Mode = %o, want 600", seeds[0].Mode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/coder/ -run TestClaudeConfigEnvAndSeed -count=1`
Expected: FAIL — `b.configEnv undefined` / `b.seedFiles undefined`.

- [ ] **Step 3: Implement the contract in backend.go**

In `internal/coder/backend.go`, replace the interface methods `setupHome` and `extraEnvForUser`
(lines 25-31) with:

```go
	// configEnv returns env vars that redirect this coder's config/state dir into
	// the per-workspace home (cross-platform: prefers explicit dir env vars over HOME).
	configEnv(workspaceHome string) map[string]string

	// seedFiles returns operator credential/config file(s) to copy into the isolated
	// dir on each invocation. Auth only — never session DBs, history, or logs.
	seedFiles(workspaceHome string) []seedSpec
```

Add near the top of the file (after imports):

```go
// seedSpec describes one operator credential/config file to copy from the host
// into a workspace's isolated coder home before each invocation.
type seedSpec struct {
	From string      // absolute path in the operator's real config
	To   string      // absolute path inside the per-workspace isolated home
	Mode os.FileMode // permissions for the copied file
}

// forwardEnv returns the subset of the given env var names that are currently
// set in the host environment (used to pass operator-provided API keys through).
func forwardEnv(keys ...string) map[string]string {
	out := make(map[string]string)
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			out[k] = v
		}
	}
	return out
}
```

Replace `claudeBackend`'s `setupHome` (62-74) and `extraEnvForUser` (76-80) with:

```go
func (b *claudeBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN")
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(workspaceHome, ".claude")
	return env
}

func (b *claudeBackend) seedFiles(workspaceHome string) []seedSpec {
	if b.sysClaudeDir == "" {
		return nil
	}
	return []seedSpec{{
		From: filepath.Join(b.sysClaudeDir, ".credentials.json"),
		To:   filepath.Join(workspaceHome, ".claude", ".credentials.json"),
		Mode: 0o600,
	}}
}
```

Replace `genericCLIBackend`'s `setupHome` (126-128) and `extraEnvForUser` (130-139) with:

```go
func (b *genericCLIBackend) configEnv(_ string) map[string]string {
	return forwardEnv(knownAuthEnvVars...)
}

func (b *genericCLIBackend) seedFiles(_ string) []seedSpec { return nil }
```

- [ ] **Step 4: Rewire coder.go to the new contract**

In `internal/coder/coder.go`, replace `ensureUserHome` (489-504) with:

```go
func (c *Coder) ensureUserHome(workspaceID string, backend CoderBackend) (string, error) {
	dir := c.UserHomeDir(workspaceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return "", err
	}
	for _, s := range backend.seedFiles(dir) {
		if err := os.MkdirAll(filepath.Dir(s.To), 0o700); err != nil {
			return "", err
		}
		if data, err := os.ReadFile(s.From); err == nil {
			_ = os.WriteFile(s.To, data, s.Mode)
		}
	}
	return dir, nil
}
```

In `buildEnv` (508-526), replace the loop `for k, v := range backend.extraEnvForUser(homeDir)` with:

```go
	for k, v := range backend.configEnv(homeDir) {
		overrides[k] = v
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/coder/ -run TestClaude -count=1 && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: PASS and a clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/coder/backend.go internal/coder/coder.go internal/coder/backend_test.go
git commit -m "refactor(coder): declarative per-coder config isolation contract"
```

---

### Task 2: Shared output parsers (single-object + NDJSON event stream)

Two reusable parsers: one extracts a named field from a single JSON object (Gemini `.response`,
Cursor final result); one scans newline-delimited JSON events keeping the last assistant text and
surfacing `type:"error"` events (OpenCode, Codex).

**Files:**
- Modify: `internal/coder/backend.go`
- Test: `internal/coder/backend_test.go`

**Interfaces:**
- Produces: `func parseSingleJSONField(stdout []byte, fields ...string) (text string, isError bool, err error)`
- Produces: `func parseNDJSONEvents(stdout []byte) (text string, isError bool, err error)`

- [ ] **Step 1: Write the failing tests**

Add to `internal/coder/backend_test.go`:

```go
func TestParseSingleJSONField(t *testing.T) {
	out := []byte(`{"response":"PONG","stats":{}}`)
	text, isErr, err := parseSingleJSONField(out, "response", "result")
	if err != nil || isErr || text != "PONG" {
		t.Fatalf("got (%q,%v,%v)", text, isErr, err)
	}
}

func TestParseNDJSONEventsText(t *testing.T) {
	out := []byte(
		`{"type":"step-start"}` + "\n" +
			`{"type":"text","text":"PONG"}` + "\n" +
			`{"type":"step-finish"}` + "\n")
	text, isErr, err := parseNDJSONEvents(out)
	if err != nil || isErr || text != "PONG" {
		t.Fatalf("got (%q,%v,%v)", text, isErr, err)
	}
}

func TestParseNDJSONEventsError(t *testing.T) {
	out := []byte(`{"type":"error","error":{"data":{"message":"User not found.","statusCode":401}}}`)
	text, isErr, err := parseNDJSONEvents(out)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !isErr {
		t.Fatalf("expected isError=true")
	}
	if text == "" {
		t.Fatalf("expected error message text")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/coder/ -run TestParse -count=1`
Expected: FAIL — undefined `parseSingleJSONField` / `parseNDJSONEvents`.

- [ ] **Step 3: Implement the parsers**

Add to `internal/coder/backend.go`:

```go
// parseSingleJSONField extracts the first present string field from a single JSON
// object emitted by coders that print one final object (Gemini: "response",
// Cursor: "result"). If the object is not valid JSON, the raw trimmed text is
// returned (best-effort for plain-text stragglers).
func parseSingleJSONField(stdout []byte, fields ...string) (string, bool, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return "", false, fmt.Errorf("coder produced no output")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(trimmed, &obj) != nil {
		return string(trimmed), false, nil
	}
	for _, f := range fields {
		raw, ok := obj[f]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), false, nil
		}
	}
	return "", false, fmt.Errorf("no text field %v in response", fields)
}

// ndjsonEvent is the minimal shape shared by OpenCode/Codex event streams.
type ndjsonEvent struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Delta string `json:"delta"`
	Error struct {
		Message string `json:"message"`
		Data    struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
		} `json:"data"`
	} `json:"error"`
}

// parseNDJSONEvents scans newline-delimited JSON events, accumulating assistant
// text and reporting a terminal error event. Returns isError=true (not a Go
// error) for a coder-reported error so looksLikeLimit can classify it.
func parseNDJSONEvents(stdout []byte) (string, bool, error) {
	lines := bytes.Split(bytes.TrimSpace(stdout), []byte("\n"))
	var text strings.Builder
	var errMsg string
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev ndjsonEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "error":
			m := ev.Error.Data.Message
			if m == "" {
				m = ev.Error.Message
			}
			if ev.Error.Data.StatusCode != 0 {
				m = fmt.Sprintf("%s (status %d)", m, ev.Error.Data.StatusCode)
			}
			errMsg = m
		case ev.Text != "":
			text.WriteString(ev.Text)
		case ev.Delta != "":
			text.WriteString(ev.Delta)
		}
	}
	if errMsg != "" {
		return errMsg, true, nil
	}
	if text.Len() == 0 {
		return "", false, fmt.Errorf("no assistant text in event stream")
	}
	return strings.TrimSpace(text.String()), false, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/coder/ -run TestParse -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/backend.go internal/coder/backend_test.go
git commit -m "feat(coder): shared single-object + NDJSON-event output parsers"
```

---

### Task 3: OpenCode backend (fix + verify end-to-end)

The core fix: `opencode run <prompt> --format json`, not `-p <prompt>`. Isolate via XDG dirs,
seed `opencode/auth.json`, parse the NDJSON event stream. Wire dispatch + detection so it is
runnable and testable on this host.

**Files:**
- Modify: `internal/coder/backend.go` (add `opencodeBackend`)
- Modify: `internal/coder/coder.go` (`selectBackend` 475-487)
- Modify: `internal/coder/detect.go` (`knownCoders` 59-68)
- Test: `internal/coder/backend_test.go`

**Interfaces:**
- Consumes: `parseNDJSONEvents`, `forwardEnv`, `seedSpec`
- Produces: `type opencodeBackend struct { model string }` implementing `CoderBackend`
- Produces: `selectBackend()` returns `&opencodeBackend{model: c.apiModelForCLI()}` for `backendType == "opencode"`
- Produces: `Coder.apiModelForCLI() string` — returns the workspace-configured CLI model (empty if none)

Note on model: local CLI coders currently have no model field wired. Add a `cliModel` field to
`Coder` set by `ForWorkspace` from `w.CoderModel`, exposed via `apiModelForCLI()`. OpenCode/Cursor
pass it as `-m`/`--model` when non-empty.

- [ ] **Step 1: Write the failing tests**

Add to `internal/coder/backend_test.go`:

```go
import "reflect"

func TestOpencodeArgs(t *testing.T) {
	b := &opencodeBackend{}
	args := b.buildArgs("hello world", false, "")
	want := []string{"run", "hello world", "--format", "json"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	// The prompt must NEVER be passed via -p (opencode's -p is basic-auth password).
	for i, a := range args {
		if a == "-p" {
			t.Fatalf("opencode args must not contain -p (found at %d): %v", i, args)
		}
	}
}

func TestOpencodeArgsWithModel(t *testing.T) {
	b := &opencodeBackend{model: "anthropic/claude-sonnet-5"}
	args := b.buildArgs("hi", false, "")
	want := []string{"run", "hi", "--format", "json", "-m", "anthropic/claude-sonnet-5"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestOpencodeConfigEnvAndSeed(t *testing.T) {
	b := &opencodeBackend{}
	home := "/homes/ws1"
	env := b.configEnv(home)
	if env["XDG_DATA_HOME"] != filepath.Join(home, ".local", "share") {
		t.Fatalf("XDG_DATA_HOME = %q", env["XDG_DATA_HOME"])
	}
	if env["XDG_CONFIG_HOME"] != filepath.Join(home, ".config") {
		t.Fatalf("XDG_CONFIG_HOME = %q", env["XDG_CONFIG_HOME"])
	}
	seeds := b.seedFiles(home)
	if len(seeds) != 1 || filepath.Base(seeds[0].To) != "auth.json" {
		t.Fatalf("seeds = %+v", seeds)
	}
	if filepath.Base(filepath.Dir(seeds[0].To)) != "opencode" {
		t.Fatalf("seed dest dir = %q, want .../opencode/auth.json", seeds[0].To)
	}
}

func TestOpencodeLooksLikeLimit(t *testing.T) {
	b := &opencodeBackend{}
	if !b.looksLikeLimit("rate limit exceeded", "") {
		t.Fatalf("expected limit detection on rate-limit text")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/coder/ -run TestOpencode -count=1`
Expected: FAIL — undefined `opencodeBackend`.

- [ ] **Step 3: Implement `opencodeBackend`**

Add to `internal/coder/backend.go`:

```go
// ─── OpenCode backend ──────────────────────────────────────────────────────────
// Verified end-to-end on this host. Invocation: `opencode run <prompt> --format json`.
// NOTE: opencode's -p flag is basic-auth PASSWORD, not prompt — the prompt is a
// positional arg after the `run` subcommand.
type opencodeBackend struct {
	model string // provider/model, from workspace CoderModel; passed as -m when set
}

func (b *opencodeBackend) buildArgs(prompt string, _ bool, _ string) []string {
	args := []string{"run", prompt, "--format", "json"}
	if b.model != "" {
		args = append(args, "-m", b.model)
	}
	return args
}

func (b *opencodeBackend) parseOutput(stdout []byte) (string, bool, error) {
	return parseNDJSONEvents(stdout)
}

func (b *opencodeBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv(knownAuthEnvVars...)
	// opencode resolves auth/state under XDG_DATA_HOME and config under XDG_CONFIG_HOME.
	env["XDG_DATA_HOME"] = filepath.Join(workspaceHome, ".local", "share")
	env["XDG_CONFIG_HOME"] = filepath.Join(workspaceHome, ".config")
	return env
}

func (b *opencodeBackend) seedFiles(workspaceHome string) []seedSpec {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	// Operator auth lives at ~/.local/share/opencode/auth.json (XDG_DATA_HOME default).
	src := opencodeAuthPath(home)
	return []seedSpec{{
		From: src,
		To:   filepath.Join(workspaceHome, ".local", "share", "opencode", "auth.json"),
		Mode: 0o600,
	}}
}

func (b *opencodeBackend) looksLikeLimit(stdout, stderr string) bool {
	return containsLimitKeyword(stdout + " " + stderr)
}

// opencodeAuthPath returns the operator's opencode auth file, honoring an explicit
// XDG_DATA_HOME override, else ~/.local/share.
func opencodeAuthPath(home string) string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "opencode", "auth.json")
}
```

Add a shared limit-keyword helper (replaces the duplicated loops in claude/generic
`looksLikeLimit`; keep claude's empty-stdout heuristic in `claudeBackend`):

```go
func containsLimitKeyword(s string) bool {
	combined := strings.ToLower(s)
	for _, kw := range []string{"usage limit", "rate limit", "rate_limit", "quota exceeded", "limit reached", "429"} {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Wire dispatch, model field, and detection**

In `internal/coder/coder.go`, add a field to the `Coder` struct (near `backendType`, line 90):

```go
	cliModel     string            // provider/model for CLI coders that accept -m/--model (opencode, cursor)
```

Add the accessor (near `BackendType()`, after line 166):

```go
// apiModelForCLI returns the workspace-configured model for CLI coders that
// accept one (opencode -m, cursor --model). Empty means "use the coder's default".
func (c *Coder) apiModelForCLI() string { return c.cliModel }
```

In `selectBackend()` (475-487), add cases before the auto-detect fallback:

```go
	case "opencode":
		return &opencodeBackend{model: c.cliModel}
```

In `internal/coder/detect.go`, change the OpenCode `knownCoders` entry (line 65) backend from
`"generic"` to `"opencode"`:

```go
	{"OpenCode", []string{"opencode"}, "opencode"},
```

In `internal/coder/forworkspace.go`, set `cliModel` on the local path. After line 51-54 change to:

```go
	cd := New(bin, timeout, homesDir, dataDir).
		WithBackendType(backendType).
		WithSandbox(enableSandbox).
		WithVault(vlt)
	if w != nil {
		cd.cliModel = w.CoderModel
	}
	return cd
```

- [ ] **Step 5: Run unit tests + build**

Run: `go test ./internal/coder/ -run TestOpencode -count=1 && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: PASS + clean build.

- [ ] **Step 6: Manual end-to-end verification on this host**

Run:

```bash
cat > /tmp/oc_smoke.go <<'EOF'
package main
import ("context";"fmt";"time";"github.com/ilijad1/simple-agents/internal/coder")
func main(){
 c:=coder.New("/home/rookie/.opencode/bin/opencode",60*time.Second,"/tmp/oc-homes","/tmp/oc-data").WithBackendType("opencode")
 ctx,cancel:=context.WithTimeout(context.Background(),60*time.Second);defer cancel()
 r,err:=c.Generate(ctx,"wsSmoke","Reply with exactly the word PONG and nothing else")
 fmt.Printf("err=%v\ntext=%q\n",err,func()string{if r!=nil{return r.Text};return ""}())
}
EOF
go run /tmp/oc_smoke.go
```

Expected: either `text="PONG"` (if operator OpenCode auth is valid) OR a clear auth error
surfaced from the NDJSON error event (e.g. `coder error: User not found. (status 401)`).
The pass criterion for THIS task is that the prompt reaches OpenCode and a structured reply/error
comes back — NOT a silent empty run. Record which occurred. (Operator auth is currently 401; a
clean auth-error path proves the pipeline. Re-authing opencode is out of scope.)

- [ ] **Step 7: Commit**

```bash
rm -f /tmp/oc_smoke.go
git add internal/coder/backend.go internal/coder/coder.go internal/coder/detect.go internal/coder/forworkspace.go internal/coder/backend_test.go
git commit -m "fix(coder): working OpenCode backend (run subcommand + NDJSON parse + XDG isolation)"
```

---

### Task 4: Codex backend (authored, unverified)

`codex exec <prompt> --json`; approval auto-downgrades to `never` in exec (no TTY hang). Isolate
via `CODEX_HOME`, seed `auth.json`, parse NDJSON events.

**Files:**
- Modify: `internal/coder/backend.go`
- Modify: `internal/coder/coder.go` (`selectBackend`)
- Modify: `internal/coder/detect.go`
- Test: `internal/coder/backend_test.go`

**Interfaces:**
- Consumes: `parseNDJSONEvents`, `forwardEnv`, `containsLimitKeyword`, `seedSpec`
- Produces: `type codexBackend struct{}` implementing `CoderBackend`
- Produces: `selectBackend()` case `"codex"` → `&codexBackend{}`

- [ ] **Step 1: Write the failing tests**

```go
func TestCodexArgs(t *testing.T) {
	b := &codexBackend{}
	args := b.buildArgs("do it", false, "")
	want := []string{"exec", "do it", "--json"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestCodexConfigEnv(t *testing.T) {
	b := &codexBackend{}
	home := "/homes/ws1"
	env := b.configEnv(home)
	if env["CODEX_HOME"] != filepath.Join(home, ".codex") {
		t.Fatalf("CODEX_HOME = %q", env["CODEX_HOME"])
	}
}

func TestCodexSeed(t *testing.T) {
	b := &codexBackend{}
	seeds := b.seedFiles("/homes/ws1")
	if len(seeds) != 1 || filepath.Base(seeds[0].To) != "auth.json" {
		t.Fatalf("seeds = %+v", seeds)
	}
	if filepath.Base(filepath.Dir(seeds[0].To)) != ".codex" {
		t.Fatalf("seed dest = %q, want .../.codex/auth.json", seeds[0].To)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/coder/ -run TestCodex -count=1`
Expected: FAIL — undefined `codexBackend`.

- [ ] **Step 3: Implement `codexBackend`**

```go
// ─── Codex backend ─────────────────────────────────────────────────────────────
// AUTHORED, UNVERIFIED — not installed on the build host. Invocation from current
// docs: `codex exec <prompt> --json`; exec mode auto-downgrades approval to
// `never` (no TTY), so it will not hang. Isolation via CODEX_HOME. Must pass
// Coder.Smoke on a host with `codex` before being relied upon.
type codexBackend struct{}

func (b *codexBackend) buildArgs(prompt string, _ bool, _ string) []string {
	return []string{"exec", prompt, "--json"}
}

func (b *codexBackend) parseOutput(stdout []byte) (string, bool, error) {
	return parseNDJSONEvents(stdout)
}

func (b *codexBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv("OPENAI_API_KEY", "CODEX_API_KEY")
	env["CODEX_HOME"] = filepath.Join(workspaceHome, ".codex")
	return env
}

func (b *codexBackend) seedFiles(workspaceHome string) []seedSpec {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	src := os.Getenv("CODEX_HOME")
	if src == "" {
		src = filepath.Join(home, ".codex")
	}
	return []seedSpec{{
		From: filepath.Join(src, "auth.json"),
		To:   filepath.Join(workspaceHome, ".codex", "auth.json"),
		Mode: 0o600,
	}}
}

func (b *codexBackend) looksLikeLimit(stdout, stderr string) bool {
	return containsLimitKeyword(stdout + " " + stderr)
}
```

Add to `selectBackend()`:

```go
	case "codex":
		return &codexBackend{}
```

In `detect.go`, change the Codex entry (line 66):

```go
	{"Codex", []string{"codex"}, "codex"},
```

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/coder/ -run TestCodex -count=1 && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/backend.go internal/coder/coder.go internal/coder/detect.go internal/coder/backend_test.go
git commit -m "feat(coder): Codex backend (exec --json, CODEX_HOME) — authored, unverified"
```

---

### Task 5: Gemini backend (authored, unverified)

`gemini -p <prompt> --output-format json --yolo`; single-object output `.response`. Gemini keys off
the home dir (`~/.gemini`), so isolate via `HOME`+`USERPROFILE` and seed the `.gemini` settings.

**Files:**
- Modify: `internal/coder/backend.go`
- Modify: `internal/coder/coder.go` (`selectBackend`)
- Modify: `internal/coder/detect.go`
- Test: `internal/coder/backend_test.go`

**Interfaces:**
- Consumes: `parseSingleJSONField`, `forwardEnv`, `containsLimitKeyword`
- Produces: `type geminiBackend struct{}` implementing `CoderBackend`
- Produces: `selectBackend()` case `"gemini"` → `&geminiBackend{}`

- [ ] **Step 1: Write the failing tests**

```go
func TestGeminiArgs(t *testing.T) {
	b := &geminiBackend{}
	args := b.buildArgs("summarize", false, "")
	want := []string{"-p", "summarize", "--output-format", "json", "--yolo"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestGeminiParseResponse(t *testing.T) {
	b := &geminiBackend{}
	text, isErr, err := b.parseOutput([]byte(`{"response":"PONG"}`))
	if err != nil || isErr || text != "PONG" {
		t.Fatalf("got (%q,%v,%v)", text, isErr, err)
	}
}

func TestGeminiConfigEnvWindows(t *testing.T) {
	b := &geminiBackend{}
	home := "/homes/ws1"
	env := b.configEnv(home)
	if env["HOME"] != home {
		t.Fatalf("HOME = %q", env["HOME"])
	}
	if env["USERPROFILE"] != home {
		t.Fatalf("USERPROFILE = %q (needed for Windows isolation)", env["USERPROFILE"])
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/coder/ -run TestGemini -count=1`
Expected: FAIL — undefined `geminiBackend`.

- [ ] **Step 3: Implement `geminiBackend`**

```go
// ─── Gemini CLI backend ────────────────────────────────────────────────────────
// AUTHORED, UNVERIFIED — not installed on the build host. Invocation from current
// docs: `gemini -p <prompt> --output-format json --yolo` (--yolo auto-approves
// tool calls so it does not block). Gemini keys off the home dir (~/.gemini) with
// no dedicated config-dir env var, so isolation overrides HOME + USERPROFILE.
// Must pass Coder.Smoke on a host with `gemini` before being relied upon.
type geminiBackend struct{}

func (b *geminiBackend) buildArgs(prompt string, _ bool, _ string) []string {
	return []string{"-p", prompt, "--output-format", "json", "--yolo"}
}

func (b *geminiBackend) parseOutput(stdout []byte) (string, bool, error) {
	return parseSingleJSONField(stdout, "response")
}

func (b *geminiBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv("GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS")
	env["HOME"] = workspaceHome
	env["USERPROFILE"] = workspaceHome // Windows home
	return env
}

func (b *geminiBackend) seedFiles(workspaceHome string) []seedSpec {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	// Seed the operator's ~/.gemini/.env (holds GEMINI_API_KEY) so the workspace
	// inherits the operator's login.
	return []seedSpec{{
		From: filepath.Join(home, ".gemini", ".env"),
		To:   filepath.Join(workspaceHome, ".gemini", ".env"),
		Mode: 0o600,
	}}
}

func (b *geminiBackend) looksLikeLimit(stdout, stderr string) bool {
	return containsLimitKeyword(stdout + " " + stderr)
}
```

Add to `selectBackend()`:

```go
	case "gemini":
		return &geminiBackend{}
```

In `detect.go`, add a Gemini entry to `knownCoders` (after the Codex line):

```go
	{"Gemini CLI", []string{"gemini"}, "gemini"},
```

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/coder/ -run TestGemini -count=1 && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/backend.go internal/coder/coder.go internal/coder/detect.go internal/coder/backend_test.go
git commit -m "feat(coder): Gemini CLI backend (-p --output-format json --yolo) — authored, unverified"
```

---

### Task 6: Cursor backend (authored, unverified)

`cursor-agent -p <prompt> --output-format json --trust [--model <m>]`; single-object aggregated
result. Auth via `CURSOR_API_KEY` passthrough (on-disk login store location unconfirmed — no seed
until verified on a real install).

**Files:**
- Modify: `internal/coder/backend.go`
- Modify: `internal/coder/coder.go` (`selectBackend`)
- Modify: `internal/coder/detect.go`
- Test: `internal/coder/backend_test.go`

**Interfaces:**
- Consumes: `parseSingleJSONField`, `forwardEnv`, `containsLimitKeyword`
- Produces: `type cursorBackend struct { model string }` implementing `CoderBackend`
- Produces: `selectBackend()` case `"cursor"` → `&cursorBackend{model: c.cliModel}`

- [ ] **Step 1: Write the failing tests**

```go
func TestCursorArgs(t *testing.T) {
	b := &cursorBackend{}
	args := b.buildArgs("fix tests", false, "")
	want := []string{"-p", "fix tests", "--output-format", "json", "--trust"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestCursorArgsWithModel(t *testing.T) {
	b := &cursorBackend{model: "gpt-5"}
	args := b.buildArgs("x", false, "")
	want := []string{"-p", "x", "--output-format", "json", "--trust", "--model", "gpt-5"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestCursorParseResult(t *testing.T) {
	b := &cursorBackend{}
	text, isErr, err := b.parseOutput([]byte(`{"result":"PONG","type":"result"}`))
	if err != nil || isErr || text != "PONG" {
		t.Fatalf("got (%q,%v,%v)", text, isErr, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/coder/ -run TestCursor -count=1`
Expected: FAIL — undefined `cursorBackend`.

- [ ] **Step 3: Implement `cursorBackend`**

```go
// ─── Cursor CLI backend ────────────────────────────────────────────────────────
// AUTHORED, UNVERIFIED — not installed on the build host. Invocation from current
// docs: `cursor-agent -p <prompt> --output-format json --trust [--model <m>]`
// (-p is PRINT mode here, not prompt; the prompt is positional). Output is a
// single aggregated JSON object with "result". Auth via CURSOR_API_KEY env
// passthrough; the on-disk login store location is unconfirmed, so no seedFiles
// until verified on a real install. NOTE: cursor-agent has no official native
// Windows build (community patch only). Must pass Coder.Smoke on a host with
// `cursor-agent` before being relied upon.
type cursorBackend struct {
	model string
}

func (b *cursorBackend) buildArgs(prompt string, _ bool, _ string) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--trust"}
	if b.model != "" {
		args = append(args, "--model", b.model)
	}
	return args
}

func (b *cursorBackend) parseOutput(stdout []byte) (string, bool, error) {
	return parseSingleJSONField(stdout, "result", "response")
}

func (b *cursorBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv("CURSOR_API_KEY")
	env["HOME"] = workspaceHome
	env["USERPROFILE"] = workspaceHome
	return env
}

func (b *cursorBackend) seedFiles(_ string) []seedSpec {
	// TBD: cursor-agent login-store path unconfirmed. Until verified, rely on
	// CURSOR_API_KEY passthrough (configEnv). Add a seedSpec once confirmed.
	return nil
}

func (b *cursorBackend) looksLikeLimit(stdout, stderr string) bool {
	return containsLimitKeyword(stdout + " " + stderr)
}
```

Add to `selectBackend()`:

```go
	case "cursor":
		return &cursorBackend{model: c.cliModel}
```

In `detect.go`, change the Cursor entry (line 67):

```go
	{"Cursor", []string{"cursor-agent", "cursor"}, "cursor"},
```

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/coder/ -run TestCursor -count=1 && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/backend.go internal/coder/coder.go internal/coder/detect.go internal/coder/backend_test.go
git commit -m "feat(coder): Cursor backend (print mode --output-format json --trust) — authored, unverified"
```

---

### Task 7: `BackendForBin` + detection backend-type derivation

Add a helper mapping a binary name/path to its backend type, so the web layer never has to trust a
missing form field. Fix the latent bug where local coders saved an empty backend type.

**Files:**
- Modify: `internal/coder/detect.go`
- Test: `internal/coder/detect_test.go` (create)

**Interfaces:**
- Produces: `func BackendForBin(bin string) string` — returns the backend type for a bin (e.g.
  `/home/rookie/.opencode/bin/opencode` → `"opencode"`; unknown → `""`).

- [ ] **Step 1: Write the failing test**

Create `internal/coder/detect_test.go`:

```go
package coder

import "testing"

func TestBackendForBin(t *testing.T) {
	cases := map[string]string{
		"claude":                              "claude",
		"/usr/bin/claude-code":                "claude",
		"/home/rookie/.opencode/bin/opencode": "opencode",
		"codex":                               "codex",
		"gemini":                              "gemini",
		"cursor-agent":                        "cursor",
		"cursor":                              "cursor",
		"something-unknown":                   "",
	}
	for bin, want := range cases {
		if got := BackendForBin(bin); got != want {
			t.Errorf("BackendForBin(%q) = %q, want %q", bin, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/coder/ -run TestBackendForBin -count=1`
Expected: FAIL — undefined `BackendForBin`.

- [ ] **Step 3: Implement `BackendForBin`**

Add to `internal/coder/detect.go`:

```go
// BackendForBin returns the coder backend type for a binary name or path by
// matching its base name against the known-coders catalog. Returns "" if the
// binary is not a recognized coder (caller falls back to name auto-detection).
func BackendForBin(bin string) string {
	if bin == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(bin))
	for _, kc := range knownCoders {
		for _, cand := range kc.Bins {
			if base == cand || base == cand+".exe" {
				return kc.Backend
			}
		}
	}
	return ""
}
```

Add `"strings"` to the imports in `detect.go` (it currently imports only `os`, `os/exec`,
`path/filepath`).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/coder/ -run TestBackendForBin -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/coder/detect.go internal/coder/detect_test.go
git commit -m "feat(coder): BackendForBin binary->backend-type resolver"
```

---

### Task 8: `Coder.Smoke` fail-loud end-to-end check

A trivial-prompt round-trip through the full isolated pipeline, validating a sane structured reply.
Distinguishes reachable+working from wrong-convention/bad-auth failures.

**Files:**
- Modify: `internal/coder/coder.go`
- Test: `internal/coder/smoke_test.go` (create)

**Interfaces:**
- Produces: `func (c *Coder) Smoke(ctx context.Context, workspaceID string) (string, error)` — runs
  `"Reply with exactly the word PONG..."`; returns the reply on success, a descriptive error
  otherwise. For API coders, delegates to the existing `Ping`.

- [ ] **Step 1: Write the failing test**

Create `internal/coder/smoke_test.go`:

```go
package coder

import (
	"context"
	"os"
	"testing"
	"time"
)

// Host-gated: only runs when opencode is installed. Verifies the Smoke pipeline
// reaches the coder and returns a reply OR a descriptive error (never a silent
// empty success).
func TestSmokeOpencodeHostGated(t *testing.T) {
	bin := "/home/rookie/.opencode/bin/opencode"
	if _, err := os.Stat(bin); err != nil {
		t.Skip("opencode not installed; skipping host-gated smoke")
	}
	c := New(bin, 60*time.Second, t.TempDir(), t.TempDir()).WithBackendType("opencode")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	reply, err := c.Smoke(ctx, "wsSmoke")
	if err == nil && reply == "" {
		t.Fatal("Smoke returned empty reply with no error (silent failure)")
	}
	t.Logf("Smoke reply=%q err=%v", reply, err)
}

func TestSmokeMethodExists(t *testing.T) {
	c := New("claude", time.Minute, t.TempDir(), t.TempDir())
	_ = c.Smoke // compile-time check the method exists
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/coder/ -run TestSmoke -count=1`
Expected: FAIL — `c.Smoke undefined`.

- [ ] **Step 3: Implement `Smoke`**

Add to `internal/coder/coder.go`:

```go
// Smoke runs a trivial prompt through the full isolated pipeline (seed → env →
// invoke → parse) and validates a sane structured reply. It is the fail-loud
// gate for the coder-settings UI: a wrong CLI convention or bad/expired operator
// auth returns a descriptive error instead of silently feeding garbage into a
// run. For API coders it delegates to Ping (which resolves the provider key).
func (c *Coder) Smoke(ctx context.Context, workspaceID string) (string, error) {
	if c.api != nil {
		return c.Ping(ctx, workspaceID)
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := c.WithNoTools().Generate(ctx, workspaceID, "Reply with exactly the word PONG and nothing else.")
	if err != nil {
		return "", err
	}
	reply := strings.TrimSpace(res.Text)
	if reply == "" {
		return "", fmt.Errorf("coder %q returned an empty reply", filepath.Base(c.bin))
	}
	return reply, nil
}
```

(Use `WithNoTools()` — for the Claude backend it emits `--allowedTools ""`, avoiding the
documented `--setting-sources ""` + no-`--allowedTools` hang; other backends ignore `noTools` in
`buildArgs`.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/coder/ -run TestSmoke -count=1`
Expected: PASS (`TestSmokeMethodExists` passes; the host-gated test either passes or logs the
auth error and passes, since a non-empty error is acceptable).

- [ ] **Step 5: Commit**

```bash
git add internal/coder/coder.go internal/coder/smoke_test.go
git commit -m "feat(coder): Smoke fail-loud end-to-end coder check"
```

---

### Task 9: Web wiring — derive backend from bin + Test-coder button

Fix both save handlers to set the backend type from the chosen binary (not the absent
`coder_backend_type` form field), and add a "Test coder" action that runs `Smoke`.

**Files:**
- Modify: `web/handlers_misc.go` (`handleSaveWorkspaceCoder` ~547-552; add `handleSmokeCoder`)
- Modify: `web/handlers_setup.go` (`handleSetupCoder` ~211-215)
- Modify: `web/server.go` (register route ~340)
- Modify: `web/templates/dashboard/settings.html` (add button ~57)

**Interfaces:**
- Consumes: `coder.BackendForBin(bin)`, `(*coder.Coder).Smoke`, `s.coderForWorkspace(id)`
- Produces: route `POST /dashboard/settings/coder/test` → `handleSmokeCoder` returning JSON
  `{ok: bool, reply?: string, error?: string}`

- [ ] **Step 1: Fix backend derivation in the settings save handler**

In `web/handlers_misc.go`, replace the local-coder branch (lines ~549-551):

```go
	} else {
		kind = "local"
		bin = c.FormValue("coder_bin")
		backendType = c.FormValue("coder_backend_type")
	}
```

with:

```go
	} else {
		kind = "local"
		bin = c.FormValue("coder_bin")
		backendType = coder.BackendForBin(bin) // derive from the chosen binary; empty bin => "" (auto-detect)
	}
```

- [ ] **Step 2: Fix backend derivation in the setup wizard handler**

In `web/handlers_setup.go`, replace (lines ~211-214):

```go
	} else {
		bin := c.FormValue("coder_bin")
		backend := c.FormValue("coder_backend_type")
		if err := s.db.UpdateWorkspaceCoder(w.ID, "local", bin, timeoutS, backend, "", "", "", ""); err != nil {
```

with:

```go
	} else {
		bin := c.FormValue("coder_bin")
		backend := coder.BackendForBin(bin)
		if err := s.db.UpdateWorkspaceCoder(w.ID, "local", bin, timeoutS, backend, "", "", "", ""); err != nil {
```

Confirm `github.com/ilijad1/simple-agents/internal/coder` is imported in `handlers_setup.go`
(it is — `coder.DetectInstalled()` is already called at line 41). In `handlers_misc.go` confirm the
`coder` package is imported (it is — `coder.DetectInstalled()` at line 394).

- [ ] **Step 3: Add the Smoke handler**

Add to `web/handlers_misc.go`:

```go
// handleSmokeCoder runs a fail-loud end-to-end check of the workspace's currently
// saved coder and returns the result as JSON for the settings page.
func (s *Server) handleSmokeCoder(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	cd := s.coderForWorkspace(w.ID)
	ctx, cancel := context.WithTimeout(c.Request().Context(), 100*time.Second)
	defer cancel()
	reply, err := cd.Smoke(ctx, w.ID)
	if err != nil {
		return c.JSON(http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "reply": reply})
}
```

- [ ] **Step 4: Register the route**

In `web/server.go`, after line 340 (`dash.POST("/settings/coder", s.handleSaveWorkspaceCoder)`):

```go
	dash.POST("/settings/coder/test", s.handleSmokeCoder)
```

- [ ] **Step 5: Add the Test-coder button**

In `web/templates/dashboard/settings.html`, inside the `#coder_local` div, after the
binary-select `form-control` (after line 59, the closing `</div>` of the form-control):

```html
          <button type="button" class="btn btn-sm btn-outline" onclick="testCoder()">Test coder</button>
          <span id="coder_test_result" class="text-sm ml-2"></span>
          <script>
          async function testCoder() {
            const el = document.getElementById('coder_test_result');
            el.textContent = 'Testing…';
            try {
              const r = await fetch('/dashboard/settings/coder/test', {method: 'POST'});
              const j = await r.json();
              el.textContent = j.ok ? ('✅ ' + j.reply) : ('❌ ' + j.error);
            } catch (e) { el.textContent = '❌ ' + e; }
          }
          </script>
```

- [ ] **Step 6: Build + run the web tests**

Run: `go build -o bin/simple-agents ./cmd/simple-agents && go test ./web/... -count=1`
Expected: clean build + PASS.

- [ ] **Step 7: Manual UI smoke**

Run: `make deploy` then `curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/login`
Expected: `200`. (Full UI click-through is manual; the route + handler compile and register.)

- [ ] **Step 8: Commit**

```bash
git add web/handlers_misc.go web/handlers_setup.go web/server.go web/templates/dashboard/settings.html
git commit -m "feat(web): derive coder backend from bin + Test-coder Smoke button"
```

---

### Task 10: Lock backend-type mapping, update model comment + docs

Ensure the new backend types map to `BackendFullCoder`, correct the DB model comment, and document
the multi-coder support.

**Files:**
- Modify: `internal/prompts/prompts_test.go`
- Modify: `internal/db/models.go` (line 30)
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: `prompts.MapCoderBackend`, `prompts.BackendFullCoder`

- [ ] **Step 1: Write the failing test**

Add to `internal/prompts/prompts_test.go`:

```go
func TestMapCoderBackendCLICoders(t *testing.T) {
	for _, bt := range []string{"claude", "opencode", "codex", "gemini", "cursor"} {
		if got := MapCoderBackend(bt); got != BackendFullCoder {
			t.Errorf("MapCoderBackend(%q) = %q, want BackendFullCoder", bt, got)
		}
	}
	if MapCoderBackend("api") != BackendToolCalling {
		t.Errorf("api should map to BackendToolCalling")
	}
}
```

- [ ] **Step 2: Run to verify it passes (default case already covers CLI coders)**

Run: `go test ./internal/prompts/ -run TestMapCoderBackendCLICoders -count=1`
Expected: PASS — the `default` branch of `MapCoderBackend` already returns `BackendFullCoder` for
`opencode/codex/gemini/cursor`, and `claude`/`api` are handled explicitly. This test locks that
behavior against regressions. (If it fails, the mapping regressed — fix `MapCoderBackend`.)

- [ ] **Step 3: Update the DB model comment**

In `internal/db/models.go` line 30, change:

```go
	CoderBackendType  string // '' = auto-detect, 'claude', or 'generic' (local); 'api' (api kind)
```

to:

```go
	CoderBackendType  string // local: '' auto-detect | 'claude' | 'opencode' | 'codex' | 'gemini' | 'cursor' | 'generic'; api kind: 'api'
```

- [ ] **Step 4: Document in CLAUDE.md**

In `CLAUDE.md`, in the `internal/coder` table row (the "CLI engine" description) and the
"`CoderBackend`" paragraph under "Coder tool isolation", replace the two-backend description with a
note that multiple per-coder backends exist. Find the sentence:

> **`CoderBackend`** (`internal/coder/backend.go`): `claudeBackend` (Claude CLI: `--output-format json`, `--setting-sources ""`) and `genericCLIBackend` (any other CLI, plain-text stdout).

and replace with:

> **`CoderBackend`** (`internal/coder/backend.go`): one struct per coder — `claudeBackend` (JSON, `--setting-sources ""`), `opencodeBackend` (`run <prompt> --format json`, NDJSON events, XDG isolation — VERIFIED), and authored-unverified `codexBackend` (`exec --json`, `CODEX_HOME`), `geminiBackend` (`-p --output-format json --yolo`, `~/.gemini`), `cursorBackend` (`-p --output-format json --trust`); `genericCLIBackend` is the last-resort fallback. Each backend declares its own `configEnv` (per-workspace config-dir env vars) + `seedFiles` (operator auth seeded in; sessions/history are not). `coder.BackendForBin` maps a chosen binary to its backend type; `Coder.Smoke` is the fail-loud end-to-end check surfaced in coder settings.

- [ ] **Step 5: Run full test suite + build**

Run: `go test ./... -count=1 -timeout 120s && go build -o bin/simple-agents ./cmd/simple-agents`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/prompts_test.go internal/db/models.go CLAUDE.md
git commit -m "docs+test(coder): lock CLI backend mapping; document multi-coder backends"
```

---

## Self-Review

**Spec coverage:**
- Generalized isolation contract (`configEnv`/`seedFiles`) → Task 1. ✓
- Per-coder adapter table (Claude/OpenCode/Codex/Gemini/Cursor) → Tasks 1,3,4,5,6. ✓
- NDJSON vs single-object parsers → Task 2. ✓
- Fail-loud `Smoke` + detection → Tasks 7,8. ✓
- backendType plumbing (new values, derive-from-bin, MapCoderBackend) → Tasks 3-7,9,10. ✓
- Cross-platform (Windows env vars, `USERPROFILE`, `.exe` in `BackendForBin`) → Tasks 5,6,7. ✓
- Testing (unit per coder + host-gated OpenCode E2E + unverified labeling) → all tasks. ✓
- Honesty/unverified labeling → code comments in Tasks 4,5,6; docs in Task 10. ✓

**Placeholder scan:** The only "TBD" is Cursor's `seedFiles` (Task 6), which is an explicitly
documented open item from the spec with a working fallback (`CURSOR_API_KEY` passthrough), not an
unfilled step. No other placeholders.

**Type consistency:** `seedSpec{From,To,Mode}`, `configEnv(workspaceHome)`, `seedFiles(workspaceHome)`,
`parseNDJSONEvents`, `parseSingleJSONField`, `forwardEnv`, `containsLimitKeyword`, `BackendForBin`,
`Coder.Smoke`, `Coder.cliModel`/`apiModelForCLI` are used with identical signatures across tasks.
`opencodeBackend`/`cursorBackend` carry a `model` field; `codexBackend`/`geminiBackend` are field-less.
