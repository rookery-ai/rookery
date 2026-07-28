# CI/CD and Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make simple-agents installable as a versioned native binary on Linux/macOS/Windows and as a slim container image, with a CI pipeline that gates every PR and automates releases from Conventional Commits.

**Architecture:** Three code changes come first because everything else depends on them — a build-tagged process-group helper that makes `GOOS=windows` compile, an `SA_CODER_MODE` policy flag that removes the CLI coder kind from slim builds at every layer, and a `/healthz` endpoint reporting sandbox and host-tool status. On top of those sit four GitHub Actions workflows (PR checks, security scanning, release-please, release) plus a goreleaser config and a multi-stage Dockerfile.

**Tech Stack:** Go 1.26.4 (CGo-free), React/Vite SPA, GitHub Actions, goreleaser + nfpm, release-please, Docker buildx, cosign, syft, Trivy, govulncheck, gitleaks, CodeQL.

## Global Constraints

- **Go version** comes from `go-version-file: go.mod` (currently 1.26.4). Never hardcode a Go version in a workflow.
- **Node version** comes from a committed `.nvmrc` pinning major `24`. Never hardcode a Node version in a workflow.
- **`CGO_ENABLED=0`** for every build, everywhere. The project is deliberately CGo-free (`modernc.org/sqlite`); this is what makes cross-compilation and small images possible.
- **Conventional Commits** for every commit: `type(scope): summary`. Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- **Never commit directly to `main`.** All work is on a feature branch merged via PR.
- **First release is `v0.1.0`**, pinned via release-please `initial-version`. Not `1.0.0`.
- **Container registry** is `ghcr.io/ilijad1/simple-agents-v2`, private for now.
- **`SA_CODER_MODE`** takes exactly `full` (default) or `slim`. Any other value is an error at config load.
- The **SPA embed tolerates a missing build**: `web/ui/dist/.gitkeep` is committed, so `go build` and `go test` work without Node. Only artifact-producing jobs need Node.
- **Baseline is green**: `go vet` clean, 24 Go packages pass, 732 frontend tests pass. Any red is something you introduced.

### Naming corrections from the spec

The spec was written from CLAUDE.md, which names two handlers that **do not
exist**. Use the real names:

| Spec name | Real name | Location |
|---|---|---|
| `handleSaveWorkspaceCoder` | `apiPutSettingsCoder` | `web/api_settings.go:256` |
| `handleSetupCoder` | `apiSetupCoder` | `web/api_settings.go:529` |

Note also that `apiPutSettingsCoder` delegates to a shared core,
`saveWorkspaceCoderCore(w, coderForm)`. Guards belong in the handler, before
that call.

---

### Task 1: Make `GOOS=windows` compile

`GOOS=windows go build ./cmd/simple-agents` currently fails with four errors.
`internal/coder/coder.go:452` and `internal/coder/hosttools.go:1061` contain an
identical eight-line block using `syscall.SysProcAttr{Setpgid: true}` and
`syscall.Kill(-pid, SIGKILL)`, both Unix-only. Extract it once behind a
build-tagged helper.

**Files:**
- Create: `internal/coder/procgroup_unix.go`
- Create: `internal/coder/procgroup_windows.go`
- Create: `internal/coder/procgroup_test.go`
- Modify: `internal/coder/coder.go:451-458`
- Modify: `internal/coder/hosttools.go:1060-1067`
- Modify: `internal/connectors/openai_test.go` (gofmt only)
- Modify: `internal/vault/links_test.go` (gofmt only)

**Interfaces:**
- Consumes: nothing.
- Produces: `func setProcGroup(cmd *exec.Cmd)` and `func processAlive(pid int) bool` — both package-private in `internal/coder`, both defined once per platform file. `setProcGroup` sets `cmd.SysProcAttr` and `cmd.Cancel` so cancelling the context kills the subprocess *and every process it spawned*. Callers still set `cmd.WaitDelay` themselves.

- [ ] **Step 1: Confirm the build is broken before changing anything**

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/simple-agents
```

Expected: FAIL with exactly four errors —
`unknown field Setpgid in struct literal of type syscall.SysProcAttr` and
`undefined: syscall.Kill`, twice each, in `coder.go` and `hosttools.go`.

If this already passes, stop: someone else fixed it and this task needs rewriting.

- [ ] **Step 2: Write the failing test**

Create `internal/coder/procgroup_test.go`:

```go
package coder

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// setProcGroup must wire both the platform process-group attribute and a
// Cancel hook. Without Cancel, exec.CommandContext signals only the direct
// child and a coder that shelled out to python leaves orphans behind.
func TestSetProcGroupWiresCancel(t *testing.T) {
	cmd := exec.Command("go", "version")
	setProcGroup(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("setProcGroup left SysProcAttr nil")
	}
	if cmd.Cancel == nil {
		t.Fatal("setProcGroup left Cancel nil")
	}
	// Cancel on a process that never started must be a no-op, not a panic:
	// buildCommand wires Cancel before Start, and a failed Start still runs it.
	if err := cmd.Cancel(); err != nil {
		t.Fatalf("Cancel on unstarted process returned %v, want nil", err)
	}
}

// The whole point of the helper is TREE termination. Spawn a shell that spawns
// a long-lived grandchild, cancel, and assert the grandchild dies too — that is
// the behaviour a plain CommandContext does not give you.
func TestSetProcGroupKillsGrandchild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("grandchild assertion uses a POSIX shell")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 60 & echo $!; wait")
	setProcGroup(cmd)
	cmd.WaitDelay = 2 * time.Second

	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var grandchild int
	if _, err := fmt.Fscan(out, &grandchild); err != nil {
		t.Fatalf("read grandchild pid: %v", err)
	}
	if !processAlive(grandchild) {
		t.Fatalf("grandchild %d was not running at start", grandchild)
	}

	cancel()
	_ = cmd.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(grandchild) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived cancellation", grandchild)
}
```

- [ ] **Step 3: Run the test to verify it fails**

```bash
go test ./internal/coder/ -run TestSetProcGroup -v
```

Expected: FAIL to compile with `undefined: setProcGroup`.

- [ ] **Step 4: Write the Unix implementation**

Create `internal/coder/procgroup_unix.go`:

```go
//go:build !windows

package coder

import (
	"os/exec"
	"syscall"
)

// setProcGroup gives cmd its own process group and kills the whole group on
// cancel, so child processes are never orphaned. exec.CommandContext otherwise
// signals only the direct child — and a coder subprocess routinely shells out
// to python or bash, which would survive.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// processAlive reports whether pid is still running. Signal 0 performs error
// checking without delivering a signal.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
```

- [ ] **Step 5: Write the Windows implementation**

Create `internal/coder/procgroup_windows.go`:

```go
//go:build windows

package coder

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// setProcGroup is the Windows counterpart of the Unix process-group kill.
// Windows has no equivalent of kill(-pgid), so cancellation shells out to
// taskkill /T, which terminates the process and its entire descendant tree.
// CREATE_NEW_PROCESS_GROUP stops a console Ctrl event aimed at our own process
// from also reaching the coder subprocess.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return kill.Run()
	}
}

// processAlive reports whether pid is still running.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
```

`golang.org/x/sys` is already a dependency. If `go build` reports it as
indirect-only, run `go mod tidy` and commit the `go.mod`/`go.sum` change with
this task.

- [ ] **Step 6: Replace the first call site**

In `internal/coder/coder.go`, replace lines 451-458 — this block:

```go
	// Own process group + group-wide SIGKILL on cancel so child processes are
	// never orphaned (CommandContext otherwise signals only the direct child).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
```

with:

```go
	// Own process group + tree-wide kill on cancel so child processes are never
	// orphaned (CommandContext otherwise signals only the direct child).
	setProcGroup(cmd)
```

Then remove the now-unused `"syscall"` import from `coder.go` **only if nothing
else in the file uses it** — check with `grep -n 'syscall\.' internal/coder/coder.go`.

- [ ] **Step 7: Replace the second call site**

In `internal/coder/hosttools.go`, replace lines 1060-1067 — the identical block:

```go
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
```

with:

```go
	setProcGroup(cmd)
```

Remove the `"syscall"` import from `hosttools.go` if unused, same check as above.

- [ ] **Step 8: Run the tests to verify they pass**

```bash
go test ./internal/coder/ -run TestSetProcGroup -v
```

Expected: both tests PASS.

- [ ] **Step 9: Verify all six platform targets compile**

```bash
for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  echo -n "$t: "
  CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} go build -o /dev/null ./cmd/simple-agents \
    && echo OK || echo FAIL
done
```

Expected: six `OK` lines. This is the acceptance criterion for the task.

- [ ] **Step 10: Fix the two gofmt offenders**

```bash
gofmt -w internal/connectors/openai_test.go internal/vault/links_test.go
gofmt -l . | grep -v '^\.claude/'
```

Expected: the second command prints nothing.

- [ ] **Step 11: Run the full Go suite**

```bash
go test ./... -count=1 -timeout 300s
```

Expected: no `FAIL` lines. 24 packages `ok`.

- [ ] **Step 12: Commit**

```bash
git add internal/coder/procgroup_unix.go internal/coder/procgroup_windows.go \
        internal/coder/procgroup_test.go internal/coder/coder.go \
        internal/coder/hosttools.go internal/connectors/openai_test.go \
        internal/vault/links_test.go go.mod go.sum
git commit -m "fix(coder): make GOOS=windows compile via build-tagged setProcGroup

Setpgid and syscall.Kill are Unix-only and were duplicated verbatim in
coder.go and hosttools.go, so no Windows binary could ever be produced.
Extract one setProcGroup helper with per-platform files; Windows uses
taskkill /T because it has no kill(-pgid) equivalent.

Also gofmt two test files."
```

---

### Task 2: `simple-agents version` and build-time metadata

An installed binary that cannot report what it is cannot be supported. Add a
`version` subcommand and the linker variables the release build populates.

**Files:**
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/buildinfo/buildinfo_test.go`
- Modify: `cmd/simple-agents/main.go:53-59` (command list)
- Modify: `Makefile`

**Interfaces:**
- Consumes: nothing.
- Produces: `buildinfo.Version`, `buildinfo.Commit`, `buildinfo.Date` (package-level `string` vars, set via `-ldflags -X`); `buildinfo.String() string` returning `"<version> (<commit>, built <date>)"`; `buildinfo.Short() string` returning just the version. Task 4 reads `Version` and `Commit`; Task 9 sets all three via ldflags.

- [ ] **Step 1: Write the failing test**

Create `internal/buildinfo/buildinfo_test.go`:

```go
package buildinfo

import "testing"

// An unstamped binary (go build with no -ldflags, i.e. every developer build)
// must still produce something honest rather than an empty string.
func TestDefaultsAreHonest(t *testing.T) {
	if Version == "" {
		t.Error("Version must never be empty")
	}
	if Short() != Version {
		t.Errorf("Short() = %q, want %q", Short(), Version)
	}
	if got := String(); got == "" {
		t.Error("String() must never be empty")
	}
}

func TestStringIncludesAllFields(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, Date
	defer func() { Version, Commit, Date = oldV, oldC, oldD }()

	Version, Commit, Date = "0.1.0", "abc1234", "2026-07-28T00:00:00Z"
	want := "0.1.0 (abc1234, built 2026-07-28T00:00:00Z)"
	if got := String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/buildinfo/ -v
```

Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/buildinfo/buildinfo.go`:

```go
// Package buildinfo carries the version metadata stamped into the binary at
// link time. It is its own package (rather than variables in main) so that any
// package can report the build without importing main — notably internal/health,
// which serves /healthz.
package buildinfo

import "fmt"

// Set via -ldflags -X at release time. The defaults are what a plain
// `go build` produces, and they are deliberately not "unknown": a developer
// build should say so.
var (
	Version = "0.0.0-dev"
	Commit  = "none"
	Date    = "unknown"
)

// Short returns just the version string.
func Short() string { return Version }

// String returns the full human-readable build identity.
func String() string {
	return fmt.Sprintf("%s (%s, built %s)", Version, Commit, Date)
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/buildinfo/ -v
```

Expected: both tests PASS.

- [ ] **Step 5: Add the `version` subcommand**

In `cmd/simple-agents/main.go`, add `"github.com/ilijad1/simple-agents/internal/buildinfo"`
to the imports, then add `versionCmd(),` to the `Commands` slice at lines 53-59
(after `kbCmd(),`). Add this function at the end of the file:

```go
func versionCmd() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "Print the build version and exit",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			fmt.Println(buildinfo.String())
			return nil
		},
	}
}
```

- [ ] **Step 6: Verify the subcommand runs**

```bash
go run ./cmd/simple-agents version
```

Expected: `0.0.0-dev (none, built unknown)`

- [ ] **Step 7: Verify ldflags stamping actually works**

```bash
go build -ldflags "-X github.com/ilijad1/simple-agents/internal/buildinfo.Version=9.9.9 -X github.com/ilijad1/simple-agents/internal/buildinfo.Commit=deadbee" \
  -o /tmp/sa-version-check ./cmd/simple-agents
/tmp/sa-version-check version
rm /tmp/sa-version-check
```

Expected: `9.9.9 (deadbee, built unknown)`. If the version is still `0.0.0-dev`,
the `-X` import path is wrong — it must be the full module path.

- [ ] **Step 8: Stamp the Makefile build**

In `Makefile`, add these variables after the `SHELL := /bin/bash` line:

```makefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/ilijad1/simple-agents/internal/buildinfo.Version=$(VERSION) \
           -X github.com/ilijad1/simple-agents/internal/buildinfo.Commit=$(COMMIT) \
           -X github.com/ilijad1/simple-agents/internal/buildinfo.Date=$(DATE)
```

and change the `build-go` recipe to:

```makefile
build-go:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)
```

- [ ] **Step 9: Verify the Makefile stamping**

```bash
make build-go && ./bin/simple-agents version
```

Expected: a real commit hash and today's date, not `none`/`unknown`.

- [ ] **Step 10: Commit**

```bash
git add internal/buildinfo/ cmd/simple-agents/main.go Makefile
git commit -m "feat(cli): add version subcommand and build-time metadata

buildinfo is its own package so internal/health can report the build
without importing main. Defaults say 0.0.0-dev rather than unknown so a
developer build identifies itself honestly."
```

---

### Task 3: `SA_CODER_MODE` — config and runtime enforcement

Slim builds do not contain a CLI coder. Hiding it in the UI alone would be a
fake setting; this task enforces it at the config and runtime layers, Task 5
does the API and SPA.

This task comes before `/healthz` because the health report includes the coder
mode, and the config field must exist first.

**Files:**
- Modify: `internal/config/config.go` (`CoderConfig`, `defaults`, `applyEnv`, `Load`)
- Create: `internal/config/config_test.go`
- Modify: `internal/coder/forworkspace.go:24`
- Modify: `internal/coder/coder.go` (struct field + `Generate`/`Chat`/`Ping`/`Smoke` guards)
- Modify: `internal/coder/forworkspace_test.go:18,32,53`
- Modify: `cmd/simple-agents/main.go:139,268`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `config.CoderConfig.Mode string` — `"full"` or `"slim"`; `config.Load` returns an error on any other value.
  - `config.ModeFull = "full"`, `config.ModeSlim = "slim"` constants.
  - `coder.ErrLocalCoderDisabled` — sentinel error.
  - `coder.ForWorkspace(w, homesDir, dataDir, vlt, defaultBin, defaultTimeout, enableSandbox, allowLocal bool) *Coder` — **note the new trailing `allowLocal` parameter**.
  - Task 4 reads `config.CoderConfig.Mode`; Task 5 reads it via `Server.cfg`; Task 10's smoke test asserts on it.

- [ ] **Step 1: Write the failing config test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"testing"
)

func TestCoderModeDefaultsToFull(t *testing.T) {
	os.Unsetenv("SA_CODER_MODE")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Mode != ModeFull {
		t.Errorf("Mode = %q, want %q", cfg.Coder.Mode, ModeFull)
	}
}

func TestCoderModeSlimFromEnv(t *testing.T) {
	t.Setenv("SA_CODER_MODE", "slim")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Coder.Mode != ModeSlim {
		t.Errorf("Mode = %q, want %q", cfg.Coder.Mode, ModeSlim)
	}
}

// A typo must fail at startup, not silently fall back to full. A slim image
// whose env var was misspelled would otherwise advertise CLI coders it does
// not contain.
func TestCoderModeRejectsUnknownValue(t *testing.T) {
	t.Setenv("SA_CODER_MODE", "minimal")
	if _, err := Load(""); err == nil {
		t.Fatal("Load accepted SA_CODER_MODE=minimal, want an error")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/config/ -v
```

Expected: FAIL to compile — `ModeFull` undefined, `Coder.Mode` undefined.

- [ ] **Step 3: Implement the config change**

In `internal/config/config.go`, add the constants above `type Config struct`:

```go
// Coder modes. A slim build ships without any CLI coder binary, so the "local"
// coder kind must not be offered or accepted. This is POLICY ("this build does
// not support it"), deliberately distinct from DETECTION ("no coder binary is on
// PATH right now") — see coder.DetectInstalled. Auto-hiding on detection would
// confuse a user who installs a coder afterwards.
const (
	ModeFull = "full"
	ModeSlim = "slim"
)
```

Add the field to `CoderConfig`:

```go
type CoderConfig struct {
	ClaudeBin string        `yaml:"claude_bin"` // path to claude binary
	Timeout   time.Duration `yaml:"timeout"`
	Mode      string        `yaml:"mode"` // "full" (default) or "slim"
}
```

In `defaults()`, set `Mode: ModeFull` inside the `Coder:` literal.

Change `applyEnv` to return an error, and add the mode handling at its end:

```go
func applyEnv(cfg *Config) error {
	// ... existing assignments unchanged ...
	if v := os.Getenv("SA_CODER_MODE"); v != "" {
		cfg.Coder.Mode = strings.ToLower(strings.TrimSpace(v))
	}
	switch cfg.Coder.Mode {
	case ModeFull, ModeSlim:
	case "":
		cfg.Coder.Mode = ModeFull
	default:
		return fmt.Errorf("invalid coder mode %q: want %q or %q",
			cfg.Coder.Mode, ModeFull, ModeSlim)
	}
	return nil
}
```

And in `Load`, replace the `applyEnv(cfg)` call and `return cfg, nil` with:

```go
	if err := applyEnv(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
```

- [ ] **Step 4: Run the config test**

```bash
go test ./internal/config/ -v
```

Expected: all three PASS.

- [ ] **Step 5: Write the failing coder test**

Append to `internal/coder/forworkspace_test.go`:

```go
func TestForWorkspaceRejectsLocalWhenNotAllowed(t *testing.T) {
	w := &db.Workspace{ID: "w1", CoderKind: "local", CoderBin: "claude"}
	c := ForWorkspace(w, "/homes", "/data", nil, "claude", time.Minute, false, false)

	if _, err := c.Ping(context.Background(), "w1"); !errors.Is(err, ErrLocalCoderDisabled) {
		t.Fatalf("Ping error = %v, want ErrLocalCoderDisabled", err)
	}
	if _, err := c.Generate(context.Background(), "w1", "hi"); !errors.Is(err, ErrLocalCoderDisabled) {
		t.Fatalf("Generate error = %v, want ErrLocalCoderDisabled", err)
	}
}

// An API-kind workspace must keep working in a slim build — that is the whole
// point of slim.
func TestForWorkspaceAllowsAPIWhenLocalDisabled(t *testing.T) {
	w := &db.Workspace{ID: "w1", CoderKind: "api", CoderProvider: "openai", CoderModel: "gpt-4o"}
	c := ForWorkspace(w, "/homes", "/data", nil, "claude", time.Minute, false, false)

	if !c.IsAPI() {
		t.Fatal("api workspace did not produce an API coder")
	}
	if _, err := c.Ping(context.Background(), "w1"); errors.Is(err, ErrLocalCoderDisabled) {
		t.Fatal("API coder was wrongly disabled in slim mode")
	}
}
```

Add `"context"` and `"errors"` to that file's imports. Update the three existing
`ForWorkspace(` calls at lines 18, 32 and 53 to pass a trailing `true`.

- [ ] **Step 6: Run it to verify it fails**

```bash
go test ./internal/coder/ -run TestForWorkspace -v
```

Expected: FAIL to compile — too many arguments, `ErrLocalCoderDisabled` undefined.

- [ ] **Step 7: Implement the coder change**

In `internal/coder/forworkspace.go`, add above `ForWorkspace`:

```go
// ErrLocalCoderDisabled is returned by every entry point of a coder built for a
// "local" workspace in a build that ships no CLI coder (SA_CODER_MODE=slim).
// It is returned at USE time rather than construction time because ForWorkspace
// has no error return and is called from hot paths; failing loudly here beats
// spawning a binary that does not exist and surfacing "executable file not found".
var ErrLocalCoderDisabled = errors.New(
	"this build has no CLI coder (SA_CODER_MODE=slim) — switch this workspace to the API engine in Settings → Coder")
```

Change the signature and add the guard:

```go
func ForWorkspace(w *db.Workspace, homesDir, dataDir string, vlt *vault.Vault, defaultBin string, defaultTimeout time.Duration, enableSandbox, allowLocal bool) *Coder {
	if w != nil && w.CoderKind == "api" {
		// ... unchanged ...
	}

	if !allowLocal {
		return New(defaultBin, defaultTimeout, homesDir, dataDir).withDisabled(ErrLocalCoderDisabled)
	}

	// ... rest unchanged ...
}
```

Import `"errors"`.

In `internal/coder/coder.go`, add a field to the `Coder` struct:

```go
	// disabled, when non-nil, is returned by every entry point instead of
	// running anything. Set when the build cannot honour the workspace's coder
	// kind — see ForWorkspace and ErrLocalCoderDisabled.
	disabled error
```

Add the modifier next to the other `With*` methods (they return shallow copies):

```go
func (c *Coder) withDisabled(err error) *Coder {
	cp := *c
	cp.disabled = err
	return &cp
}
```

Add the guard as the first statement of each of the four entry points —
`Generate` (line 243), `Chat` (317), `Ping` (365), `Smoke` (385):

```go
	if c.disabled != nil {
		return nil, c.disabled
	}
```

for `Generate` and `Chat` (which return `*Result, error`), and:

```go
	if c.disabled != nil {
		return "", c.disabled
	}
```

for `Ping` and `Smoke` (which return `string, error`).

- [ ] **Step 8: Run the coder tests**

```bash
go test ./internal/coder/ -v
```

Expected: all PASS, including the two new ones.

- [ ] **Step 9: Update the two `main.go` call sites**

At `cmd/simple-agents/main.go:139` and `:268`, both currently read:

```go
				return coder.ForWorkspace(w, homesDir, cfg.Data.Dir, vlt,
					cfg.Coder.ClaudeBin, cfg.Coder.Timeout, cfg.Sandbox.Enabled)
```

Add the mode-derived argument:

```go
				return coder.ForWorkspace(w, homesDir, cfg.Data.Dir, vlt,
					cfg.Coder.ClaudeBin, cfg.Coder.Timeout, cfg.Sandbox.Enabled,
					cfg.Coder.Mode == config.ModeFull)
```

- [ ] **Step 10: Verify the whole suite and a bad-mode boot**

```bash
go build ./... && go test ./... -count=1 -timeout 300s 2>&1 | grep FAIL || echo "no failures"
SA_CODER_MODE=bogus go run ./cmd/simple-agents serve 2>&1 | head -2
```

Expected: no failures, then `invalid coder mode "bogus": want "full" or "slim"`.

- [ ] **Step 11: Commit**

```bash
git add internal/config/ internal/coder/forworkspace.go internal/coder/coder.go \
        internal/coder/forworkspace_test.go cmd/simple-agents/main.go
git commit -m "feat(coder): add SA_CODER_MODE policy flag with runtime enforcement

A slim build ships no CLI coder binary. Hiding the option in the UI alone
would be a fake setting, so ForWorkspace now returns a coder whose entry
points fail with ErrLocalCoderDisabled — naming the fix — instead of
spawning a binary that does not exist.

An unknown SA_CODER_MODE value is a startup error: a misspelled env var
in an image would otherwise silently advertise coders it does not have."
```

---

### Task 4: `/healthz` and the startup capability report

Three consumers need one endpoint: the container `HEALTHCHECK`, the CI smoke
test (Task 10), and an operator diagnosing an install. It must not require a
session — the person debugging is the person who cannot log in yet.

The load-bearing part is the **python3 warning**.
`internal/agentdesigner/guardrails.go:74` probes for `python3` and the AST
guardrail self-skips when it is absent. On a shipped install that is a security
control turning itself off silently.

**Files:**
- Create: `internal/health/health.go`
- Create: `internal/health/health_test.go`
- Create: `web/api_health.go`
- Create: `web/api_health_test.go`
- Modify: `internal/sandbox/landlock_linux.go` (add `ABI`)
- Modify: `internal/sandbox/landlock_other.go` (add `ABI`)
- Modify: `web/server.go:159-176` (`setupRoutes`) and the accessor helpers
- Modify: `cmd/simple-agents/main.go` (startup log in `serveCmd`)

**Interfaces:**
- Consumes: `buildinfo.Version`/`Commit` (Task 2); `config.CoderConfig.Mode` (Task 3); `sandbox.Supported() bool`.
- Produces:
  - `sandbox.ABI() int` — Landlock ABI version, `0` when unavailable.
  - `health.Report` struct with the JSON tags below.
  - `health.Detect(sandboxEnabled bool, coderMode string) Report`.
  - `(Report).Warnings() []string` — empty when all is well.
  - `(*Server).coderMode() string` and `(*Server).sandboxEnabled() bool` — nil-safe accessors reading `s.cfg`. Task 5 uses `coderMode()`.

- [ ] **Step 1: Add `sandbox.ABI` (Linux)**

In `internal/sandbox/landlock_linux.go`, add:

```go
// ABI reports the Landlock ABI version the running kernel supports, or 0 when
// Landlock is unavailable. It is informational only — Exec uses BestEffort,
// which negotiates the version itself. Calling landlock_create_ruleset with a
// nil attr and the VERSION flag is the documented way to query it without
// creating a ruleset.
func ABI() int {
	r, _, errno := syscall.Syscall(
		unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(r)
}
```

Both constants exist in the vendored `golang.org/x/sys/unix` — verified:
`SYS_LANDLOCK_CREATE_RULESET` = 444, `LANDLOCK_CREATE_RULESET_VERSION` = 1.
Ensure `"syscall"` and `"golang.org/x/sys/unix"` are imported.

In `internal/sandbox/landlock_other.go`, add:

```go
// ABI returns 0 off Linux: Landlock is a Linux-only LSM.
func ABI() int { return 0 }
```

- [ ] **Step 2: Write the failing test for the health report**

Create `internal/health/health_test.go`:

```go
package health

import (
	"runtime"
	"strings"
	"testing"
)

func TestDetectReportsCoderModeAndStatus(t *testing.T) {
	r := Detect(true, "slim")
	if r.Status != "ok" {
		t.Errorf("Status = %q, want ok", r.Status)
	}
	if r.CoderMode != "slim" {
		t.Errorf("CoderMode = %q, want slim", r.CoderMode)
	}
	if r.Version == "" {
		t.Error("Version must be populated from buildinfo")
	}
}

// Sandbox.Enabled is the operator's setting; Supported is the kernel's answer.
// Reporting only one of them would hide "configured on, silently inactive".
func TestSandboxEnabledIsDistinctFromSupported(t *testing.T) {
	off := Detect(false, "full")
	if off.Sandbox.Enabled {
		t.Error("Sandbox.Enabled must follow the passed-in setting")
	}
	on := Detect(true, "full")
	if !on.Sandbox.Enabled {
		t.Error("Sandbox.Enabled must be true when enabled")
	}
	if runtime.GOOS != "linux" && on.Sandbox.Supported {
		t.Error("Sandbox.Supported must be false off Linux")
	}
}

// The one absence that weakens a security control must be a warning, and the
// warning must name the consequence, not just the missing binary.
func TestMissingPython3WarnsAboutGuardrail(t *testing.T) {
	r := Detect(true, "full")
	r.Tools.Python3 = false

	warns := strings.Join(r.Warnings(), "\n")
	if !strings.Contains(warns, "python3") {
		t.Errorf("warnings must mention python3, got: %q", warns)
	}
	if !strings.Contains(strings.ToLower(warns), "guardrail") {
		t.Errorf("warnings must name the consequence (guardrail), got: %q", warns)
	}
}

func TestUnsupportedSandboxWarns(t *testing.T) {
	r := Detect(true, "full")
	r.Sandbox.Supported = false

	warns := strings.Join(r.Warnings(), "\n")
	if !strings.Contains(strings.ToLower(warns), "sandbox") {
		t.Errorf("warnings must mention the sandbox, got: %q", warns)
	}
}

func TestNoWarningsWhenHealthy(t *testing.T) {
	r := Report{
		Status:  "ok",
		Sandbox: Sandbox{Supported: true, Enabled: true, ABI: 8},
		Tools:   Tools{Python3: true, Ripgrep: true, PDFToText: true, Tesseract: true},
	}
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("healthy report produced warnings: %v", w)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

```bash
go test ./internal/health/ -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 4: Write the health package**

Create `internal/health/health.go`:

```go
// Package health builds the capability report served at /healthz and logged at
// startup. It exists because several runtime dependencies degrade SILENTLY when
// absent — most importantly python3, whose absence makes the agent-tool AST
// guardrail self-skip (internal/agentdesigner/guardrails.go). On a developer
// machine that reads as a skipped test; on a shipped install it is a security
// control switching itself off. This package makes that visible.
package health

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/ilijad1/simple-agents/internal/buildinfo"
	"github.com/ilijad1/simple-agents/internal/sandbox"
)

// Tools reports presence only — never paths, never versions. /healthz is
// unauthenticated, so it must not disclose filesystem layout.
type Tools struct {
	Python3   bool `json:"python3"`
	Ripgrep   bool `json:"rg"`
	PDFToText bool `json:"pdftotext"`
	Tesseract bool `json:"tesseract"`
}

// Sandbox separates the operator's setting (Enabled) from the kernel's answer
// (Supported). Collapsing them would hide the "configured on but inactive" case.
type Sandbox struct {
	Supported bool `json:"supported"`
	Enabled   bool `json:"enabled"`
	ABI       int  `json:"abi"`
}

type Report struct {
	Status    string  `json:"status"`
	Version   string  `json:"version"`
	Commit    string  `json:"commit"`
	Sandbox   Sandbox `json:"sandbox"`
	CoderMode string  `json:"coder_mode"`
	Tools     Tools   `json:"tools"`
}

// Detect probes the host. It is cheap (four PATH lookups plus one syscall) but
// not free, so callers should not put it on a hot path.
func Detect(sandboxEnabled bool, coderMode string) Report {
	return Report{
		Status:  "ok",
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Sandbox: Sandbox{
			Supported: sandbox.Supported(),
			Enabled:   sandboxEnabled,
			ABI:       sandbox.ABI(),
		},
		CoderMode: coderMode,
		Tools: Tools{
			Python3:   have("python3"),
			Ripgrep:   have("rg"),
			PDFToText: have("pdftotext"),
			Tesseract: have("tesseract"),
		},
	}
}

func have(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// Warnings returns the degradations worth telling a human about. Order is
// stable: security-affecting first.
func (r Report) Warnings() []string {
	var w []string
	if !r.Tools.Python3 {
		w = append(w, "python3 not found — the agent-tool AST guardrail is INACTIVE; "+
			"generated tool scripts are not statically checked before they run")
	}
	if !r.Sandbox.Supported {
		w = append(w, fmt.Sprintf("filesystem sandbox unavailable on %s — coder "+
			"subprocesses run unconfined (Landlock is Linux-only)", runtime.GOOS))
	} else if !r.Sandbox.Enabled {
		w = append(w, "filesystem sandbox is supported but DISABLED (SA_SANDBOX) — "+
			"coder subprocesses run unconfined")
	}
	if !r.Tools.Ripgrep {
		w = append(w, "rg not found — knowledge-base search falls back to the slower pure-Go searcher")
	}
	if !r.Tools.PDFToText {
		w = append(w, "pdftotext not found — PDF text extraction uses the weaker pure-Go fallback")
	}
	if !r.Tools.Tesseract {
		w = append(w, "tesseract not found — image OCR is unavailable")
	}
	return w
}
```

- [ ] **Step 5: Run the test to verify it passes**

```bash
go test ./internal/health/ -v
```

Expected: all five tests PASS.

- [ ] **Step 6: Add the nil-safe Server accessors**

`Server` already holds `cfg *config.Config` (field at `web/server.go:38`), so do
**not** add duplicate fields — read through the config. Add to `web/server.go`:

```go
// coderMode returns the build's coder policy, defaulting to full. Nil-safe
// because tests construct a bare &Server{}.
func (s *Server) coderMode() string {
	if s.cfg == nil || s.cfg.Coder.Mode == "" {
		return config.ModeFull
	}
	return s.cfg.Coder.Mode
}

// sandboxEnabled reports whether Landlock confinement is switched on.
func (s *Server) sandboxEnabled() bool {
	return s.cfg != nil && s.cfg.Sandbox.Enabled
}
```

- [ ] **Step 7: Write the failing handler test**

Create `web/api_health_test.go`:

```go
package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/labstack/echo/v4"
)

// /healthz must answer without a session: the operator debugging a broken
// install is exactly the person who cannot authenticate.
func TestHealthzNeedsNoSession(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	s := &Server{cfg: &config.Config{
		Coder:   config.CoderConfig{Mode: config.ModeSlim},
		Sandbox: config.SandboxConfig{Enabled: true},
	}}
	if err := s.apiHealthz(c); err != nil {
		t.Fatalf("apiHealthz returned %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
	if body["coder_mode"] != "slim" {
		t.Errorf("coder_mode = %v, want slim", body["coder_mode"])
	}
	if _, ok := body["sandbox"]; !ok {
		t.Error("response is missing the sandbox block")
	}
	if _, ok := body["tools"]; !ok {
		t.Error("response is missing the tools block")
	}
}

// A bare Server (as tests construct) must not panic on the accessors.
func TestCoderModeDefaultsWithoutConfig(t *testing.T) {
	s := &Server{}
	if got := s.coderMode(); got != config.ModeFull {
		t.Errorf("coderMode() = %q, want %q", got, config.ModeFull)
	}
	if s.sandboxEnabled() {
		t.Error("sandboxEnabled() must be false with no config")
	}
}
```

- [ ] **Step 8: Run it to verify it fails**

```bash
go test ./web/ -run 'TestHealthz|TestCoderModeDefaults' -v
```

Expected: FAIL to compile — `s.apiHealthz` undefined.

- [ ] **Step 9: Write the handler**

Create `web/api_health.go`:

```go
package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/health"
	"github.com/labstack/echo/v4"
)

// apiHealthz serves the unauthenticated capability report. It sits outside
// /api/v1 deliberately: it is infrastructure (container HEALTHCHECK, CI smoke
// test, operator triage), not part of the application API, and it must answer
// before any workspace has been entered.
//
// It discloses version, commit and tool PRESENCE to anyone who can reach the
// port. That is accepted — the app binds a LAN or loopback interface by default
// — but it must never grow to include paths or configuration values.
func (s *Server) apiHealthz(c echo.Context) error {
	return c.JSON(http.StatusOK, health.Detect(s.sandboxEnabled(), s.coderMode()))
}
```

- [ ] **Step 10: Register the route**

In `web/server.go`, inside `setupRoutes()`, add **before** `s.setupAPIRoutes()`:

```go
	// Unauthenticated infrastructure endpoint — see apiHealthz. Registered
	// before the SPA catch-all so /healthz is never swallowed by it.
	s.echo.GET("/healthz", s.apiHealthz)
```

- [ ] **Step 11: Run the handler tests**

```bash
go test ./web/ -run 'TestHealthz|TestCoderModeDefaults' -v
```

Expected: both PASS.

- [ ] **Step 12: Log the report at startup**

In `cmd/simple-agents/main.go`, in `serveCmd`'s `Action`, after the config is
loaded and the data dir created but **before** the server starts, add:

```go
			rep := health.Detect(cfg.Sandbox.Enabled, cfg.Coder.Mode)
			slog.Info("simple-agents starting",
				"version", rep.Version, "commit", rep.Commit,
				"sandbox_supported", rep.Sandbox.Supported,
				"sandbox_enabled", rep.Sandbox.Enabled,
				"sandbox_abi", rep.Sandbox.ABI,
				"coder_mode", rep.CoderMode)
			for _, w := range rep.Warnings() {
				slog.Warn("capability degraded", "detail", w)
			}
```

Import `"log/slog"` and `"github.com/ilijad1/simple-agents/internal/health"`.

- [ ] **Step 13: Verify end to end**

```bash
make build-go && (./bin/simple-agents serve &) && sleep 3 && \
  curl -sS http://127.0.0.1:8080/healthz; echo; \
  pkill -f 'bin/simple-agents serve'
```

Expected: JSON with `"status":"ok"`, a `sandbox` block showing `"abi":8` on this
host, and a `tools` block. The server log shows the startup line, plus a
`capability degraded` warning for any missing tool.

- [ ] **Step 14: Commit**

```bash
git add internal/health/ internal/sandbox/landlock_linux.go \
        internal/sandbox/landlock_other.go web/api_health.go \
        web/api_health_test.go web/server.go cmd/simple-agents/main.go
git commit -m "feat(health): add /healthz and a startup capability report

Several runtime dependencies degrade silently. Most seriously, a missing
python3 makes the agent-tool AST guardrail self-skip, so a security
control switches itself off with no signal. Report sandbox support (with
Landlock ABI), the coder mode, and host-tool presence, and warn loudly
about the degradations that matter.

Unauthenticated by design: the operator debugging an install is the
person who cannot log in yet. Reports presence booleans only, never paths."
```

---

### Task 5: `SA_CODER_MODE` — settings API and SPA

The write path must reject `coder_kind=local` in slim mode, because a stale
client or a plain `curl` bypasses any UI-only change.

**Files:**
- Modify: `web/api_settings.go:115` (`apiGetSettings`), `:256` (`apiPutSettingsCoder`), `:368` (`apiGetSetup`), `:529` (`apiSetupCoder`)
- Create: `web/api_settings_codermode_test.go`
- Modify: `web/ui/src/pages/settings/CoderSection.tsx:62,79,138`
- Modify: `web/ui/src/pages/settings/CoderSection.test.tsx`
- Modify: `web/ui/src/lib/settings.ts` (response type)
- Modify: `web/ui/src/pages/settings/SettingsPage.tsx` (pass the prop)

**Interfaces:**
- Consumes: `(*Server).coderMode()` (Task 4), `config.ModeSlim` (Task 3).
- Produces: `/api/v1/settings` and `/api/v1/setup` gain a top-level `"coder_mode": "full"|"slim"` key; in slim mode their `detected` array is always `[]`. `CoderSection` gains an optional `coderMode?: "full" | "slim"` prop.

- [ ] **Step 1: Write the failing API test**

Create `web/api_settings_codermode_test.go`:

```go
package web

import (
	"testing"

	"github.com/ilijad1/simple-agents/internal/config"
)

func slimServer() *Server {
	return &Server{cfg: &config.Config{Coder: config.CoderConfig{Mode: config.ModeSlim}}}
}

func fullServer() *Server {
	return &Server{cfg: &config.Config{Coder: config.CoderConfig{Mode: config.ModeFull}}}
}

// In slim mode the host probe is pointless work AND misleading output: a coder
// binary that happens to be installed still cannot be used.
func TestSlimModeSkipsCoderDetection(t *testing.T) {
	if got := slimServer().detectedCoders(); len(got) != 0 {
		t.Errorf("detectedCoders() returned %d entries in slim mode, want 0", len(got))
	}
}

// Must return an empty slice, never nil — the JSON field has to marshal as []
// rather than null, which the SPA would have to special-case.
func TestDetectedCodersNeverNil(t *testing.T) {
	if slimServer().detectedCoders() == nil {
		t.Error("detectedCoders() returned nil, want an empty slice")
	}
	if fullServer().detectedCoders() == nil {
		t.Error("detectedCoders() returned nil, want an empty slice")
	}
}

func TestSlimModeRejectsLocalKind(t *testing.T) {
	s := slimServer()
	if err := s.rejectLocalInSlim("local"); err == nil {
		t.Fatal("slim mode accepted coder kind local")
	}
	if err := s.rejectLocalInSlim("api"); err != nil {
		t.Fatalf("slim mode rejected coder kind api: %v", err)
	}
}

func TestFullModeAcceptsLocalKind(t *testing.T) {
	if err := fullServer().rejectLocalInSlim("local"); err != nil {
		t.Fatalf("full mode rejected coder kind local: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./web/ -run 'SlimMode|FullMode|DetectedCoders' -v
```

Expected: FAIL to compile — `detectedCoders` and `rejectLocalInSlim` undefined.

- [ ] **Step 3: Add the two helpers**

In `web/api_settings.go`, add near the top of the file:

```go
// detectedCoders returns the installed CLI coders, or an empty slice in slim
// mode. Short-circuiting matters twice over: the probe hits the host filesystem
// on every settings load, and in slim mode a coder that happens to be installed
// still cannot be used, so listing it would be a lie.
func (s *Server) detectedCoders() []apiDetectedCoderDTO {
	out := []apiDetectedCoderDTO{}
	if s.coderMode() == config.ModeSlim {
		return out
	}
	for _, d := range coder.DetectInstalled() {
		out = append(out, apiDetectedCoderDTO{Name: d.Name, Bin: d.Bin, BackendType: d.BackendType})
	}
	return out
}

// rejectLocalInSlim guards the write path. The SPA hides the local option in
// slim mode, but a stale tab or a plain curl would otherwise still persist
// coder_kind=local and produce a workspace that can never run.
func (s *Server) rejectLocalInSlim(kind string) error {
	if s.coderMode() == config.ModeSlim && kind == "local" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"this build has no CLI coder (SA_CODER_MODE=slim) — choose the API engine")
	}
	return nil
}
```

Import `"github.com/ilijad1/simple-agents/internal/config"` if not already present.

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./web/ -run 'SlimMode|FullMode|DetectedCoders' -v
```

Expected: all five PASS.

- [ ] **Step 5: Use the helpers in the four handlers**

In `apiGetSettings` (line 115), replace the `detected := coder.DetectInstalled()`
block and its loop (lines 127-131) with:

```go
	detOut := s.detectedCoders()
```

Add `"coder_mode": s.coderMode(),` to the returned JSON map.

In `apiGetSetup` (line 368), do the same for its `detected := coder.DetectInstalled()`
block near line 378, and add `"coder_mode": s.coderMode(),` to its response.

In `apiPutSettingsCoder` (line 256), add the guard immediately after
`bindAPI(c, &req)` succeeds and **before** the `coderForm` is built:

```go
	if err := s.rejectLocalInSlim(req.Kind); err != nil {
		return err
	}
```

In `apiSetupCoder` (line 529), add the same guard in the equivalent position —
after the request is bound, before anything is persisted. It receives an
`apiSetupRequest`; use the field that carries the coder kind (confirm with
`grep -n 'Kind' web/api_settings.go`).

- [ ] **Step 6: Verify the Go side**

```bash
go build ./... && go test ./web/ -count=1
```

Expected: build clean, tests pass.

- [ ] **Step 7: Write the failing SPA test**

`CoderSection` takes `{ coder, detectedCoders, catalog, saveOverride?,
hideTest?, showApiKeyInput? }`. The test file already defines `LOCAL_CODER`,
`DETECTED`, `CATALOG` and a `mockFetch` helper — reuse them.

Add to `web/ui/src/pages/settings/CoderSection.test.tsx`:

```tsx
it("hides the local CLI option when the build is slim", () => {
  mockFetch();
  renderSection({ coder: LOCAL_CODER, detectedCoders: [], catalog: CATALOG, coderMode: "slim" });

  // The engine toggle must offer API only — a slim build has no CLI binary.
  expect(screen.queryByText("Local CLI")).not.toBeInTheDocument();
  expect(screen.getByText("API")).toBeInTheDocument();
});

it("shows the local CLI option in a full build", () => {
  mockFetch();
  renderSection({ coder: LOCAL_CODER, detectedCoders: DETECTED, catalog: CATALOG, coderMode: "full" });

  expect(screen.getByText("Local CLI")).toBeInTheDocument();
});
```

Use the file's existing render helper. If it is inlined rather than extracted,
add one next to the other helpers:

```tsx
function renderSection(props: Parameters<typeof CoderSection>[0]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <CoderSection {...props} />
    </QueryClientProvider>,
  );
}
```

- [ ] **Step 8: Run it to verify it fails**

```bash
cd web/ui && npx vitest run CoderSection --reporter=dot
```

Expected: FAIL — the local option renders regardless of `coderMode`.

- [ ] **Step 9: Implement the SPA change**

In `web/ui/src/pages/settings/CoderSection.tsx`:

Add to the destructured props and the props type:

```tsx
  coderMode = "full",
```

```tsx
  coderMode?: "full" | "slim";
```

Change the initial state at line 62:

```tsx
  const [engine, setEngine] = useState<Engine>(coderMode === "slim" ? "api" : "local");
```

Keep the reset in sync at line 79:

```tsx
    setEngine(coder.kind === "api" || coderMode === "slim" ? "api" : "local");
```

Add the derived list just above the return (near line 130):

```tsx
  // A slim build ships no CLI coder binary, so "local" is not an option the
  // user can pick. This mirrors the server-side guard in rejectLocalInSlim —
  // the UI is the convenience, the API is the enforcement.
  const engines: Engine[] = coderMode === "slim" ? ["api"] : ["local", "api"];
```

At line 138, replace `{(["local", "api"] as const).map((eng) => (` with:

```tsx
        {engines.map((eng) => (
```

Line 161's `{engine === "local" ? (` needs no change — with `engine` forced to
`"api"` in slim mode the local branch is unreachable.

- [ ] **Step 10: Pass `coderMode` from the settings page**

In `web/ui/src/lib/settings.ts`, add `coder_mode?: "full" | "slim";` to the
settings response type (the one that already declares `detected`).

In `web/ui/src/pages/settings/SettingsPage.tsx`, find the `<CoderSection` render
and add `coderMode={settings.coder_mode}`.

- [ ] **Step 11: Run the SPA tests and typecheck**

```bash
cd web/ui && npx vitest run CoderSection --reporter=dot && npx tsc -b
```

Expected: tests PASS and `tsc` reports no errors.

- [ ] **Step 12: Verify end to end in slim mode**

```bash
make build && (SA_CODER_MODE=slim ./bin/simple-agents serve &) && sleep 3 && \
  curl -sS http://127.0.0.1:8080/healthz | grep -o '"coder_mode":"[a-z]*"'; \
  pkill -f 'bin/simple-agents serve'
```

Expected: `"coder_mode":"slim"`.

- [ ] **Step 13: Commit**

```bash
git add web/api_settings.go web/api_settings_codermode_test.go \
        web/ui/src/pages/settings/ web/ui/src/lib/settings.ts
git commit -m "feat(settings): enforce SA_CODER_MODE across the API and SPA

Slim builds no longer offer or accept the local coder kind. The SPA hides
the option and the write path rejects it with a 400, because a stale tab
or a plain curl bypasses a UI-only change. Slim also skips the host
filesystem probe entirely — listing an installed coder that cannot be
used would be a lie, and that probe runs on every settings load."
```

---

### Task 6: PR checks workflow

The core gate: build, test, cross-compile, frontend. Security scanning is Task 7
and the container smoke test is Task 10; both extend this file.

**Files:**
- Create: `.nvmrc`
- Create: `.github/workflows/pr.yml`

**Interfaces:**
- Consumes: Task 1's six-platform compilability.
- Produces: a workflow with jobs named `go`, `cross-compile` and `frontend`. Tasks 7 and 10 add jobs to this same file. These job names become the required status checks on the branch protection rule.

- [ ] **Step 1: Pin the Node version**

```bash
echo "24" > .nvmrc
```

- [ ] **Step 2: Create the workflow**

Create `.github/workflows/pr.yml`:

```yaml
name: PR

on:
  pull_request:
    branches: [main]

# A force-push should not leave the previous run burning minutes.
concurrency:
  group: pr-${{ github.event.pull_request.number }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  commit-lint:
    name: Conventional commit title
    runs-on: ubuntu-latest
    permissions:
      pull-requests: read
    steps:
      # The PR TITLE is linted rather than individual commits because merges are
      # squashes: the title becomes the commit that lands on main and the input
      # release-please reads to compute the next version.
      - uses: amannn/action-semantic-pull-request@v6
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        with:
          types: |
            feat
            fix
            refactor
            docs
            test
            chore
            perf
            build
            ci
          requireScope: false

  go:
    name: Go build and test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      # python3 is present on ubuntu-latest, which matters: the AST guardrail
      # tests self-skip without it, and a silently skipped security test is
      # worse than a failing one.
      - name: Verify python3 is available
        run: python3 --version

      - name: gofmt
        run: |
          unformatted="$(gofmt -l . | grep -v '^\.claude/' || true)"
          if [ -n "$unformatted" ]; then
            echo "::error::gofmt found unformatted files:"
            echo "$unformatted"
            exit 1
          fi

      - name: go vet
        run: go vet ./...

      # -race is the point of this job. The scheduler, gateway manager and
      # connector bridge all run concurrent goroutines against shared state.
      - name: go test -race
        run: go test ./... -race -count=1 -timeout 600s

  cross-compile:
    name: Cross-compile ${{ matrix.goos }}/${{ matrix.goarch }}
    runs-on: ubuntu-latest
    strategy:
      fail-fast: false
      matrix:
        include:
          - { goos: linux, goarch: amd64 }
          - { goos: linux, goarch: arm64 }
          - { goos: darwin, goarch: amd64 }
          - { goos: darwin, goarch: arm64 }
          - { goos: windows, goarch: amd64 }
          - { goos: windows, goarch: arm64 }
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      # This matrix is the regression guard for the build-tagged setProcGroup
      # helper. GOOS=windows was broken for the project's entire history because
      # nothing ever compiled it.
      - name: Build
        env:
          CGO_ENABLED: "0"
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: go build -o /dev/null ./cmd/simple-agents

  frontend:
    name: Frontend
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: web/ui
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-node@v6
        with:
          node-version-file: .nvmrc
          cache: npm
          cache-dependency-path: web/ui/package-lock.json

      - run: npm ci
      - name: Typecheck
        run: npx tsc -b
      - name: Lint
        run: npm run lint
      - name: Test
        run: npx vitest run
      - name: Build
        run: npm run build
```

- [ ] **Step 3: Validate the YAML locally**

```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/pr.yml')); print('valid')"
```

Expected: `valid`.

- [ ] **Step 4: Verify each job's commands pass locally**

```bash
gofmt -l . | grep -v '^\.claude/' || echo "gofmt clean"
go vet ./...
go test ./... -race -count=1 -timeout 600s 2>&1 | grep -E '^(FAIL|ok)' | grep FAIL || echo "go tests pass"
(cd web/ui && npx tsc -b && npm run lint && npx vitest run --reporter=dot 2>&1 | tail -3)
```

Expected: every line reports success. `-race` may surface failures the
non-race run did not — fix them here rather than merging a red workflow.

- [ ] **Step 5: Commit**

```bash
git add .nvmrc .github/workflows/pr.yml
git commit -m "ci: add PR checks — build, test, cross-compile, frontend

Lints the PR title rather than individual commits because merges are
squashes, so the title is what lands on main and what release-please
reads.

The cross-compile matrix is the regression guard for GOOS=windows, which
was broken for the repository's entire history precisely because nothing
ever compiled it."
```

---

### Task 7: Security scanning

Four scanners covering different ground: known-vulnerable Go dependencies,
OS/library CVEs, committed secrets, and code-level defects.

**Files:**
- Modify: `.github/workflows/pr.yml` (add a `security` job)
- Create: `.github/workflows/codeql.yml`
- Create: `.github/dependabot.yml`

**Interfaces:**
- Consumes: the `pr.yml` structure from Task 6.
- Produces: a `security` job in `pr.yml`; a separate CodeQL workflow (it needs `security-events: write`, which should not be granted to the whole PR workflow).

- [ ] **Step 1: Add the security job to `pr.yml`**

Append to the `jobs:` map in `.github/workflows/pr.yml`:

```yaml
  security:
    name: Security scan
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      # govulncheck is call-graph aware: it reports only vulnerabilities in code
      # paths the binary actually reaches, so it is far less noisy than a
      # manifest-level scanner and worth failing the build on.
      - name: govulncheck
        run: |
          go install golang.org/x/vuln/cmd/govulncheck@latest
          govulncheck ./...

      # Trivy in filesystem mode covers the npm tree and Go modules. Ignore
      # unfixed findings: failing on a CVE with no available patch blocks work
      # nobody can unblock.
      - name: Trivy filesystem scan
        uses: aquasecurity/trivy-action@0.33.1
        with:
          scan-type: fs
          scan-ref: .
          severity: CRITICAL,HIGH
          ignore-unfixed: true
          exit-code: "1"

      - name: gitleaks
        uses: gitleaks/gitleaks-action@v2
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 2: Create the CodeQL workflow**

Create `.github/workflows/codeql.yml`:

```yaml
name: CodeQL

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]
  schedule:
    # Weekly, so a newly published query still finds old code.
    - cron: "17 3 * * 1"

permissions:
  contents: read

jobs:
  analyze:
    name: Analyze ${{ matrix.language }}
    runs-on: ubuntu-latest
    permissions:
      security-events: write
      actions: read
    strategy:
      fail-fast: false
      matrix:
        language: [go, javascript-typescript]
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6
        if: matrix.language == 'go'
        with:
          go-version-file: go.mod
          cache: true

      - uses: github/codeql-action/init@v4
        with:
          languages: ${{ matrix.language }}
          queries: security-and-quality

      - uses: github/codeql-action/autobuild@v4
      - uses: github/codeql-action/analyze@v4
        with:
          category: /language:${{ matrix.language }}
```

- [ ] **Step 3: Create the Dependabot config**

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
    commit-message:
      prefix: "chore(deps)"

  - package-ecosystem: npm
    directory: /web/ui
    schedule:
      interval: weekly
    commit-message:
      prefix: "chore(deps)"
    # The tiptap editor ships as a dozen coordinated packages; separate PRs for
    # each would conflict with one another on every bump.
    groups:
      tiptap:
        patterns: ["@tiptap/*"]

  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
    commit-message:
      prefix: "ci(deps)"

  - package-ecosystem: docker
    directory: /
    schedule:
      interval: weekly
    commit-message:
      prefix: "build(deps)"
```

The `docker` entry has no Dockerfile to read until Task 10; Dependabot skips
ecosystems with no manifest rather than erroring, so committing it now is safe.

- [ ] **Step 4: Verify govulncheck passes on the current tree**

```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
"$(go env GOPATH)/bin/govulncheck" ./... 2>&1 | tail -20
```

Expected: `No vulnerabilities found.` If vulnerabilities ARE found, fix them in
this task by bumping the affected module — do not merge a workflow that is red
on arrival, and do not weaken the scanner to make it pass.

- [ ] **Step 5: Validate the YAML**

```bash
for f in .github/workflows/pr.yml .github/workflows/codeql.yml .github/dependabot.yml; do
  python3 -c "import yaml,sys; yaml.safe_load(open('$f')); print('$f valid')"
done
```

Expected: three `valid` lines.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/pr.yml .github/workflows/codeql.yml .github/dependabot.yml
git commit -m "ci: add govulncheck, Trivy, gitleaks and CodeQL scanning

CodeQL lives in its own workflow because it needs security-events:write,
which should not be granted to the whole PR workflow.

Trivy ignores unfixed findings deliberately: failing a PR on a CVE with
no available patch blocks work nobody can unblock."
```

---

### Task 8: release-please

Versioning derived from Conventional Commits, released through a merged PR —
the only mechanism consistent with "main only ever advances through merged PRs".

**Files:**
- Create: `.release-please-manifest.json`
- Create: `release-please-config.json`
- Create: `.github/workflows/release-please.yml`

**Interfaces:**
- Consumes: the conventional-commit titles enforced in Task 6.
- Produces: git tags of the form `v0.1.0`, and a `release_created` output that Task 9's release workflow keys off. The tag is what triggers `release.yml`.

- [ ] **Step 1: Create the manifest**

Create `.release-please-manifest.json`:

```json
{
  ".": "0.0.0"
}
```

`0.0.0` is the starting point; the first release computes `0.1.0` from it.

- [ ] **Step 2: Create the config**

Create `release-please-config.json`:

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "packages": {
    ".": {
      "release-type": "go",
      "package-name": "simple-agents",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": true,
      "bump-patch-for-minor-pre-major": false,
      "include-v-in-tag": true,
      "changelog-sections": [
        { "type": "feat", "section": "Features" },
        { "type": "fix", "section": "Bug Fixes" },
        { "type": "perf", "section": "Performance" },
        { "type": "refactor", "section": "Refactoring" },
        { "type": "docs", "section": "Documentation" },
        { "type": "build", "section": "Build" },
        { "type": "ci", "section": "CI", "hidden": true },
        { "type": "test", "section": "Tests", "hidden": true },
        { "type": "chore", "section": "Chores", "hidden": true }
      ]
    }
  }
}
```

`bump-minor-pre-major` is what keeps a breaking change on `0.x` bumping the
minor rather than jumping to `1.0.0`. Reaching `1.0.0` stays a deliberate act at
public release.

- [ ] **Step 3: Create the workflow**

Create `.github/workflows/release-please.yml`:

```yaml
name: release-please

on:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  release-please:
    runs-on: ubuntu-latest
    steps:
      # RELEASE_PLEASE_TOKEN rather than GITHUB_TOKEN: a pull request opened
      # with GITHUB_TOKEN does not trigger other workflows, so merging the
      # release PR would create a tag that release.yml never sees and no
      # artifacts would be produced.
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ secrets.RELEASE_PLEASE_TOKEN }}
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json
```

- [ ] **Step 4: Validate the config files**

```bash
python3 -c "import json; json.load(open('release-please-config.json')); json.load(open('.release-please-manifest.json')); print('json valid')"
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release-please.yml')); print('yaml valid')"
```

Expected: `json valid` then `yaml valid`.

- [ ] **Step 5: Record the required repository setup**

This is the one manual step in the whole plan and it must not be lost. Create
`docs/ci-setup.md`:

```markdown
# CI setup — one-time repository configuration

These steps cannot be automated from within the repository.

## 1. `RELEASE_PLEASE_TOKEN` secret

release-please needs a token that is NOT `GITHUB_TOKEN`. Pull requests opened
with `GITHUB_TOKEN` do not trigger other workflows, so merging the release PR
would tag the repository without ever running `release.yml`.

1. Create a fine-grained PAT scoped to `ilijad1/simple-agents-v2`.
2. Grant it **Contents: read and write** and **Pull requests: read and write**.
3. Add it as the repository secret `RELEASE_PLEASE_TOKEN`.

This is the ONLY secret the pipeline needs. GHCR authenticates with the
built-in `GITHUB_TOKEN`; cosign signs keylessly through GitHub's OIDC provider;
govulncheck, Trivy, gitleaks and CodeQL need no credentials.

## 2. Branch protection on `main`

Require these status checks to pass before merging:

- `Conventional commit title`
- `Go build and test`
- `Cross-compile linux/amd64` … through all six matrix entries
- `Frontend`
- `Security scan`
- `Container smoke test`

Also enable: require a pull request before merging, and **squash merging only**
(the PR title is what the conventional-commit lint validates and what
release-please reads).

## 3. GHCR package visibility

The package `ghcr.io/ilijad1/simple-agents-v2` is private. A host pulling it
needs a one-time login:

```bash
podman login ghcr.io -u <github-username>   # password: a PAT with read:packages
```

Making it public at launch is a visibility toggle in the package settings; no
pipeline change is needed.
```

- [ ] **Step 6: Commit**

```bash
git add .release-please-manifest.json release-please-config.json \
        .github/workflows/release-please.yml docs/ci-setup.md
git commit -m "ci: add release-please for conventional-commit versioning

Chosen over semantic-release because it releases through a merged PR,
which is the only mechanism consistent with the project rule that main
only ever advances through merged PRs.

Pinned to start at v0.1.0 with bump-minor-pre-major, so reaching 1.0.0
stays a deliberate act at public release rather than an accident of the
first breaking change."
```

---

### Task 9: goreleaser — binaries, packages, signatures

**Files:**
- Create: `.goreleaser.yaml`
- Create: `packaging/systemd/simple-agents.service`
- Create: `packaging/README.md`
- Create: `.github/workflows/release.yml`

**Interfaces:**
- Consumes: `buildinfo.Version`/`Commit`/`Date` (Task 2); Task 1's six-platform compilability; the tag produced by Task 8.
- Produces: release assets — six binary archives, `checksums.txt`, `checksums.txt.sig`, `checksums.txt.pem`, `.deb` and `.rpm` for amd64/arm64, and an SBOM per archive.

- [ ] **Step 1: Write the systemd user unit**

Create `packaging/systemd/simple-agents.service`:

```ini
[Unit]
Description=Simple Agents control plane
Documentation=https://github.com/ilijad1/simple-agents-v2
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/bin/simple-agents serve
Restart=on-failure
RestartSec=5s

# The data directory holds the SQLite database, every workspace vault, and the
# claude-homes credential trees. Keep it out of the binary's install prefix so a
# package upgrade cannot touch user data.
Environment=SA_DATA_DIR=%h/.simple-agents-v2

# Hardening. NOT a substitute for the Landlock confinement the app applies to
# coder subprocesses itself — this protects the host from the server process,
# Landlock protects one workspace from another.
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%h/.simple-agents-v2

[Install]
WantedBy=default.target
```

`ProtectHome=read-only` plus an explicit `ReadWritePaths` is deliberate: the
server needs its own data dir and nothing else under `$HOME`.

- [ ] **Step 2: Write the packaging README**

Create `packaging/README.md`:

```markdown
# Installing from a package

## Debian / Ubuntu

```bash
sudo dpkg -i simple-agents_<version>_linux_amd64.deb
```

## Fedora / RHEL

```bash
sudo rpm -i simple-agents-<version>.x86_64.rpm
```

## Running it so it survives a reboot

The packaged unit is a **systemd user unit**, so it needs no root and keeps all
data under your own home directory:

```bash
mkdir -p ~/.config/systemd/user
cp /usr/share/simple-agents/simple-agents.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now simple-agents

# Without lingering, a user unit stops when your last session ends and does not
# start at boot. This is the step people miss.
sudo loginctl enable-linger "$USER"
```

Check it:

```bash
systemctl --user status simple-agents
curl -sS http://127.0.0.1:8080/healthz
```

`/healthz` reports the sandbox status and which optional host tools are present.
Warnings about a missing `python3` are worth acting on: without it the agent-tool
AST guardrail is inactive.

## Bootstrapping

```bash
simple-agents owner bootstrap -u <username> -p <password>
```
```

- [ ] **Step 3: Write the goreleaser config**

Create `.goreleaser.yaml`:

```yaml
version: 2

project_name: simple-agents

before:
  hooks:
    # The SPA must exist before the Go build, because it is embedded. A clean
    # checkout only has dist/.gitkeep, which keeps `go build` working but ships
    # a binary with no UI.
    - npm --prefix web/ui ci
    - npm --prefix web/ui run build

builds:
  - id: simple-agents
    main: ./cmd/simple-agents
    binary: simple-agents
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X github.com/ilijad1/simple-agents/internal/buildinfo.Version={{.Version}}
      - -X github.com/ilijad1/simple-agents/internal/buildinfo.Commit={{.ShortCommit}}
      - -X github.com/ilijad1/simple-agents/internal/buildinfo.Date={{.Date}}

archives:
  - id: default
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files:
      - README.md
      - packaging/README.md
      - packaging/systemd/simple-agents.service

nfpms:
  - id: packages
    package_name: simple-agents
    vendor: Ilija Dimitrovski
    homepage: https://github.com/ilijad1/simple-agents-v2
    maintainer: Ilija Dimitrovski <ilija.dimitrovski@kroute.ai>
    description: |
      Multi-workspace AI agents control plane with a built-in knowledge base,
      connector layer and scheduler.
    license: proprietary
    formats: [deb, rpm]
    bindir: /usr/bin
    contents:
      # Shipped to /usr/share rather than installed into ~/.config: a package
      # must not write into a user's home directory, and the unit is per-user.
      - src: packaging/systemd/simple-agents.service
        dst: /usr/share/simple-agents/simple-agents.service
      - src: packaging/README.md
        dst: /usr/share/doc/simple-agents/README.md
    # These make the difference between a working install and a mystifying one.
    recommends:
      - python3
      - ripgrep
      - poppler-utils
      - tesseract-ocr

checksum:
  name_template: checksums.txt

sboms:
  - artifacts: archive

signs:
  # Keyless: the certificate is issued against the workflow's OIDC identity, so
  # there is no private key to store or rotate.
  - cmd: cosign
    signature: "${artifact}.sig"
    certificate: "${artifact}.pem"
    args:
      - sign-blob
      - "--output-signature=${signature}"
      - "--output-certificate=${certificate}"
      - "${artifact}"
      - "--yes"
    artifacts: checksum
    output: true

changelog:
  # release-please owns the changelog; goreleaser must not write a second one.
  disable: true

release:
  draft: false
  prerelease: auto
  footer: |
    ## Verifying this release

    ```bash
    cosign verify-blob checksums.txt \
      --signature checksums.txt.sig \
      --certificate checksums.txt.pem \
      --certificate-identity-regexp 'https://github\.com/ilijad1/simple-agents-v2/.*' \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com
    ```
```

- [ ] **Step 4: Validate the config**

```bash
go install github.com/goreleaser/goreleaser/v2@latest
"$(go env GOPATH)/bin/goreleaser" check
```

Expected: `1 configuration file(s) validated`.

- [ ] **Step 5: Dry-run a full build**

```bash
"$(go env GOPATH)/bin/goreleaser" release --snapshot --clean --skip=sign,sbom
ls -la dist/*.tar.gz dist/*.zip dist/*.deb dist/*.rpm 2>/dev/null
./dist/simple-agents_linux_amd64_v1/simple-agents version
```

Expected: six archives, two `.deb` and two `.rpm`, and a `version` output
carrying a real snapshot version rather than `0.0.0-dev`. If the build fails on
the npm hook, run `npm --prefix web/ui ci` once by hand and retry.

- [ ] **Step 6: Clean up the dry run**

```bash
rm -rf dist/
```

- [ ] **Step 7: Write the release workflow**

Create `.github/workflows/release.yml`:

```yaml
name: Release

on:
  push:
    tags: ["v*"]

permissions:
  contents: read

jobs:
  goreleaser:
    name: Binaries and packages
    runs-on: ubuntu-latest
    permissions:
      contents: write   # create the GitHub Release
      id-token: write   # cosign keyless signing via OIDC
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0   # goreleaser needs full history for the changelog range

      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true

      - uses: actions/setup-node@v6
        with:
          node-version-file: .nvmrc
          cache: npm
          cache-dependency-path: web/ui/package-lock.json

      - uses: sigstore/cosign-installer@v4
      - uses: anchore/sbom-action/download-syft@v0

      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 8: Validate the workflow YAML**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml')); print('valid')"
```

Expected: `valid`.

- [ ] **Step 9: Commit**

```bash
git add .goreleaser.yaml packaging/ .github/workflows/release.yml
git commit -m "build: add goreleaser for binaries, deb/rpm and signatures

Ships a systemd USER unit rather than a system one, matching how the
target host already runs its services, and documents enable-linger —
the step people miss that makes the difference between a service that
survives a reboot and one that stops at logout.

Signing is cosign keyless via OIDC, so there is no private key to store.
The npm before-hook is mandatory: a clean checkout has only dist/.gitkeep,
which keeps go build working but would ship a binary with no UI."
```

---

### Task 10: Container image, GHCR publish, and the smoke test

The smoke test is the highest-value item in this plan: it is the only thing that
closes an existing documented gap ("No integration or e2e test coverage") rather
than adding new machinery.

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`
- Modify: `.github/workflows/pr.yml` (add a `container` job)
- Modify: `.github/workflows/release.yml` (add an `image` job)

**Interfaces:**
- Consumes: `/healthz` (Task 4), `SA_CODER_MODE` (Tasks 3 and 5).
- Produces: `ghcr.io/ilijad1/simple-agents-v2` tagged `:v<version>`, `:<major>.<minor>`, `:latest`.

- [ ] **Step 1: Write `.dockerignore`**

Create `.dockerignore`:

```
.git
.github
.claude
.superpowers
bin/
dist/
logs/
web/ui/node_modules
web/ui/dist
docs/
plans/
*.md
!README.md
simple-agents
.server.pid
```

`web/ui/dist` is excluded so the image always builds the SPA from source rather
than silently embedding a developer's stale local build.

- [ ] **Step 2: Write the Dockerfile**

Create `Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1

# ── SPA ──────────────────────────────────────────────────────────────────────
FROM node:24-alpine AS ui
WORKDIR /src/web/ui
COPY web/ui/package.json web/ui/package-lock.json ./
RUN npm ci
COPY web/ui/ ./
RUN npm run build

# ── Go ───────────────────────────────────────────────────────────────────────
# Pinned to BUILDPLATFORM and cross-compiled to TARGETARCH. Because the project
# is CGo-free this needs no QEMU: a multi-arch build stays as fast as a native
# one instead of emulating a foreign architecture.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
ARG VERSION=0.0.0-dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
COPY --from=ui /src/web/ui/dist ./web/ui/dist

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/ilijad1/simple-agents/internal/buildinfo.Version=${VERSION} \
        -X github.com/ilijad1/simple-agents/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/ilijad1/simple-agents/internal/buildinfo.Date=${BUILD_DATE}" \
      -o /out/simple-agents ./cmd/simple-agents

# ── Runtime ──────────────────────────────────────────────────────────────────
# Debian rather than Alpine: tesseract's language data packaging is saner here,
# and glibc stays available for whatever a future :full target installs.
FROM debian:trixie-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      python3 \
      ripgrep \
      poppler-utils \
      tesseract-ocr \
      tesseract-ocr-eng \
    && rm -rf /var/lib/apt/lists/*

# A fixed uid/gid keeps volume ownership predictable across rootless Podman and
# Docker, which map users differently.
RUN groupadd --gid 10001 app \
    && useradd --uid 10001 --gid app --create-home --home-dir /home/app app

COPY --from=build /out/simple-agents /usr/bin/simple-agents

# HOME must sit inside the volume: the per-workspace claude-homes trees live
# under the data dir and must be writable and persistent.
ENV SA_DATA_DIR=/data \
    SA_HOST=0.0.0.0 \
    SA_PORT=8080 \
    SA_CODER_MODE=slim \
    HOME=/data

RUN mkdir -p /data && chown -R app:app /data
VOLUME ["/data"]
USER app
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/bin/simple-agents", "healthcheck"]

ENTRYPOINT ["/usr/bin/simple-agents"]
CMD ["serve"]

ARG VERSION=0.0.0-dev
ARG COMMIT=none
LABEL org.opencontainers.image.title="simple-agents" \
      org.opencontainers.image.description="Multi-workspace AI agents control plane" \
      org.opencontainers.image.source="https://github.com/ilijad1/simple-agents-v2" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
```

- [ ] **Step 3: Add the `healthcheck` subcommand**

The `HEALTHCHECK` above uses the binary rather than curl, because the runtime
image has no curl and adding one purely for a health probe is wasteful.

Add to `cmd/simple-agents/main.go`'s `Commands` slice: `healthcheckCmd(),` and
this function:

```go
func healthcheckCmd() *cli.Command {
	return &cli.Command{
		Name:  "healthcheck",
		Usage: "Probe the local server's /healthz and exit non-zero if unhealthy",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.Root().String("config"))
			if err != nil {
				return err
			}
			url := fmt.Sprintf("http://127.0.0.1:%d/healthz", cfg.Server.Port)
			c := &http.Client{Timeout: 4 * time.Second}
			resp, err := c.Get(url)
			if err != nil {
				return fmt.Errorf("healthcheck: %w", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
			}
			return nil
		},
	}
}
```

Ensure `"net/http"` and `"time"` are imported.

- [ ] **Step 4: Build the image locally**

```bash
podman build -t simple-agents:test \
  --build-arg VERSION=0.0.0-test --build-arg COMMIT=local .
```

Expected: a successful build. Note the final image size —
`podman images simple-agents:test`.

- [ ] **Step 5: Run the smoke test by hand**

```bash
podman run -d --name sa-smoke -p 18080:8080 simple-agents:test
for i in $(seq 1 30); do
  curl -fsS http://127.0.0.1:18080/healthz >/dev/null 2>&1 && break
  sleep 1
done
curl -sS http://127.0.0.1:18080/healthz
echo
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/
podman rm -f sa-smoke
```

Expected: JSON reporting `"status":"ok"` and `"coder_mode":"slim"`, then `200`
for the SPA root. The `sandbox` block should report `"supported":true` with a
non-zero ABI — the probe run during design confirmed Landlock survives rootless
Podman.

- [ ] **Step 6: Add the container job to `pr.yml`**

Append to the `jobs:` map in `.github/workflows/pr.yml`:

```yaml
  container:
    name: Container smoke test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: docker/setup-buildx-action@v3

      # Single-arch and loaded locally: this job proves the image RUNS, which a
      # multi-arch build cannot do (you cannot start an arm64 container here
      # without emulation). The multi-arch build happens at release.
      - name: Build image
        uses: docker/build-push-action@v6
        with:
          context: .
          load: true
          tags: simple-agents:pr
          cache-from: type=gha
          cache-to: type=gha,mode=max

      # Image mode complements the filesystem scan in the security job: that one
      # sees source manifests, this one sees the Debian base layer and the
      # apt-installed runtime (python3, ripgrep, poppler, tesseract), which no
      # source scan can reach.
      - name: Trivy image scan
        uses: aquasecurity/trivy-action@0.33.1
        with:
          scan-type: image
          image-ref: simple-agents:pr
          severity: CRITICAL,HIGH
          ignore-unfixed: true
          exit-code: "1"

      # This is the project's only end-to-end test. It asserts a real binary,
      # in a real image, actually serves — and that the image did not forget
      # its SA_CODER_MODE, which would advertise CLI coders it does not contain.
      - name: Smoke test
        run: |
          set -euo pipefail
          docker run -d --name sa-smoke -p 18080:8080 simple-agents:pr

          for i in $(seq 1 45); do
            if curl -fsS http://127.0.0.1:18080/healthz >/dev/null 2>&1; then break; fi
            if [ "$i" = "45" ]; then
              echo "::error::server never became healthy"
              docker logs sa-smoke
              exit 1
            fi
            sleep 1
          done

          body="$(curl -fsS http://127.0.0.1:18080/healthz)"
          echo "healthz: $body"

          echo "$body" | grep -q '"status":"ok"'      || { echo "::error::status not ok"; exit 1; }
          echo "$body" | grep -q '"coder_mode":"slim"' || { echo "::error::image is missing SA_CODER_MODE=slim"; exit 1; }

          code="$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/)"
          [ "$code" = "200" ] || { echo "::error::SPA root returned $code, want 200"; exit 1; }

          settings="$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/api/v1/auth/session)"
          [ "$settings" = "200" ] || { echo "::error::session endpoint returned $settings"; exit 1; }

          docker rm -f sa-smoke
```

- [ ] **Step 7: Add the image job to `release.yml`**

Append to the `jobs:` map in `.github/workflows/release.yml`:

```yaml
  image:
    name: Container image
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write   # push to GHCR
      id-token: write   # cosign keyless
    steps:
      - uses: actions/checkout@v5
      - uses: docker/setup-buildx-action@v3
      - uses: sigstore/cosign-installer@v4

      # GITHUB_TOKEN is sufficient for GHCR — no stored registry credential.
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/${{ github.repository }}
          tags: |
            type=semver,pattern=v{{version}}
            type=semver,pattern={{major}}.{{minor}}
            type=raw,value=latest

      - id: build
        uses: docker/build-push-action@v6
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ github.ref_name }}
            COMMIT=${{ github.sha }}
          sbom: true
          provenance: mode=max
          cache-from: type=gha
          cache-to: type=gha,mode=max

      - name: Sign the image
        env:
          DIGEST: ${{ steps.build.outputs.digest }}
        run: |
          cosign sign --yes "ghcr.io/${{ github.repository }}@${DIGEST}"
```

- [ ] **Step 8: Validate both workflows**

```bash
for f in .github/workflows/pr.yml .github/workflows/release.yml; do
  python3 -c "import yaml; yaml.safe_load(open('$f')); print('$f valid')"
done
```

Expected: two `valid` lines.

- [ ] **Step 9: Re-verify the healthcheck subcommand inside the image**

```bash
podman build -t simple-agents:test . >/dev/null
podman run -d --name sa-hc -p 18081:8080 simple-agents:test
sleep 8
podman exec sa-hc /usr/bin/simple-agents healthcheck && echo "healthcheck OK"
podman rm -f sa-hc
```

Expected: `healthcheck OK`. If it fails, the `SA_PORT` the subcommand reads does
not match what the server bound — check `config.Load` picks up the image's env.

- [ ] **Step 10: Commit**

```bash
git add Dockerfile .dockerignore cmd/simple-agents/main.go \
        .github/workflows/pr.yml .github/workflows/release.yml
git commit -m "build(docker): add slim multi-arch image, GHCR publish and smoke test

Cross-compiles from BUILDPLATFORM to TARGETARCH rather than emulating,
which the CGo-free build makes possible — a multi-arch build costs
roughly what a native one does.

The smoke test is the project's first end-to-end coverage: it starts the
real image and asserts /healthz, the SPA root and the session endpoint
all answer. It also fails the build if the image ever loses its
SA_CODER_MODE=slim, which would advertise CLI coders it does not contain.

HEALTHCHECK shells the binary's own healthcheck subcommand so the runtime
image needs no curl."
```

---

### Task 11: CLAUDE.md, `make ci`, and the container docs

**Files:**
- Modify: `CLAUDE.md` (extend the "Git workflow" section)
- Modify: `Makefile` (add `ci`, `docker-build`, `docker-run`)

**Interfaces:**
- Consumes: every workflow from Tasks 6-10.
- Produces: `make ci` — the local mirror of the PR gate.

- [ ] **Step 1: Add the Makefile targets**

Add to `.PHONY` and append these targets to `Makefile`:

```makefile
## ci: run the same checks pr.yml runs, locally (catch it before you push)
ci: ci-fmt ci-vet ci-test ci-cross ci-ui
	@echo "all PR checks passed"

ci-fmt:
	@unformatted="$$(gofmt -l . | grep -v '^\.claude/' || true)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	@echo "gofmt: clean"

ci-vet:
	go vet ./...

ci-test:
	go test ./... -race -count=1 -timeout 600s

## ci-cross: the GOOS=windows regression guard — the reason this target exists
ci-cross:
	@for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do \
		printf "%-16s" "$$t"; \
		CGO_ENABLED=0 GOOS=$${t%/*} GOARCH=$${t#*/} go build -o /dev/null $(PKG) \
			&& echo OK || { echo FAIL; exit 1; }; \
	done

ci-ui:
	cd web/ui && npm ci && npx tsc -b && npm run lint && npx vitest run

## docker-build: build the slim container image locally (podman or docker)
docker-build:
	$(CONTAINER_ENGINE) build -t simple-agents:local \
		--build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

## docker-run: run the locally built image with a persistent data volume
docker-run:
	$(CONTAINER_ENGINE) run --rm -it -p 8080:8080 \
		-v simple-agents-data:/data simple-agents:local
```

And near the top, after `SHELL := /bin/bash`:

```makefile
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null || echo podman)
```

Add `ci ci-fmt ci-vet ci-test ci-cross ci-ui docker-build docker-run` to the
`.PHONY` line.

- [ ] **Step 2: Verify `make ci` passes**

```bash
make ci
```

Expected: ends with `all PR checks passed`. This takes several minutes — the
frontend suite alone is around 130 seconds.

- [ ] **Step 3: Verify the docker targets**

```bash
make docker-build && podman images simple-agents:local
```

Expected: an image is listed.

- [ ] **Step 4: Extend CLAUDE.md**

In `CLAUDE.md`, replace the `## Git workflow` section's content by **appending**
the following after the existing bullets (keep the existing three bullets intact):

```markdown
## CI/CD and release process

**Every change ships through this path. There are no exceptions and no manual
tags.**

1. **Branch** off `main`. Never commit to `main` directly.
2. **Commit** using Conventional Commits (`type(scope): summary`).
3. **Open a PR.** Its **title** must itself be a valid Conventional Commit —
   merges are squashes, so the title becomes the commit that lands on `main` and
   is what release-please reads to compute the next version.
4. **PR checks must pass** (`.github/workflows/pr.yml`):
   - `Go build and test` — gofmt, `go vet`, `go test -race`
   - `Cross-compile` — all six GOOS/GOARCH pairs. **This is the guard that keeps
     `GOOS=windows` compiling**; it was broken for the repo's entire history
     because nothing ever built it.
   - `Frontend` — `npm ci`, `tsc -b`, `oxlint`, `vitest`, `vite build`
   - `Security scan` — govulncheck, Trivy, gitleaks (CodeQL runs separately)
   - `Container smoke test` — builds the image, runs it, asserts `/healthz`,
     the SPA root and the session endpoint all answer. This is the project's
     only end-to-end coverage.
5. **Run the same checks locally first** with `make ci` — it mirrors the gate
   exactly, including the cross-compile matrix.
6. **Squash-merge.** release-please then maintains a release PR on `main`.
7. **Merging the release PR** tags the repo, which fires
   `.github/workflows/release.yml`: goreleaser publishes binaries, `.deb`/`.rpm`,
   checksums, cosign signatures and SBOMs, and buildx pushes the multi-arch
   image to GHCR.

Versioning starts at **v0.1.0** with `bump-minor-pre-major`, so a breaking
change bumps the minor while the project is pre-1.0. Reaching 1.0.0 is a
deliberate act at public release.

**Secrets:** the pipeline needs exactly one, `RELEASE_PLEASE_TOKEN` — see
`docs/ci-setup.md`. GHCR authenticates with the built-in `GITHUB_TOKEN`, cosign
signs keylessly via OIDC, and the scanners need no credentials. Do not add
secrets that have no consumer.

## Distribution

**Native binaries are the primary artifact**; the container image is secondary.

| Target | Sandbox | Service | Notes |
|---|---|---|---|
| linux amd64/arm64 | Landlock | systemd **user** unit + `enable-linger` | tier 1 |
| container (linux) | Landlock (verified under rootless Podman) | runtime-managed | tier 1 |
| darwin amd64/arm64 | **none** | launchd (not yet shipped) | tier 2 |
| windows amd64/arm64 | **none** | SCM (not yet shipped) | tier 2 |

**Off Linux there is no filesystem sandbox at all** — `sandbox.Supported()`
returns false and callers do not wrap, so coder subprocesses run unconfined.
`/healthz` and the startup log both report this. One-command installers
(`install.sh`/`install.ps1`), a Homebrew tap and Windows service registration
are deferred until the repository is public: release assets on a private repo
require an authenticated request, so `curl | sh` cannot work yet.

### Container

```bash
make docker-build           # honours podman or docker, whichever is installed
make docker-run             # port 8080, data in the simple-agents-data volume

podman run -d --name simple-agents -p 8080:8080 \
  -v simple-agents-data:/data ghcr.io/ilijad1/simple-agents-v2:latest
```

The image is **slim**: it contains no CLI coder binary and sets
`SA_CODER_MODE=slim`, so workspaces must use the `api` coder kind.

### Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `SA_HOST` | `0.0.0.0` | bind address; `127.0.0.1` for loopback-only |
| `SA_PORT` | `8080` | listen port |
| `SA_DATA_DIR` | `~/.simple-agents-v2` | data root; also relocates the DB |
| `SA_SESSION_KEY` | generated | hex 32-byte session key |
| `SA_PUBLIC_URL` | — | externally reachable base URL for OAuth callbacks |
| `SA_SANDBOX` | `1` | `0`/`false`/`off` disables Landlock confinement |
| `SA_CODER_MODE` | `full` | `slim` removes the local CLI coder kind entirely |

### Health

`GET /healthz` is unauthenticated and reports version, commit, sandbox status
(including Landlock ABI), coder mode and host-tool presence. **A `python3`
warning is not cosmetic** — without it the agent-tool AST guardrail self-skips,
so generated tool scripts run unchecked.
```

- [ ] **Step 5: Verify the CLAUDE.md edit renders and the deploy note is consistent**

```bash
grep -n "CI/CD and release process\|## Distribution\|SA_CODER_MODE" CLAUDE.md | head
```

Expected: the new headings are present. Confirm the existing "Deploy workflow"
blockquote near the top still reads correctly alongside the new section — `make
deploy` remains the local/testing path, while releases now go through tags.

- [ ] **Step 6: Final full verification**

```bash
make ci
```

Expected: `all PR checks passed`.

- [ ] **Step 7: Commit**

```bash
git add CLAUDE.md Makefile
git commit -m "docs: document the CI/CD process and add make ci

make ci mirrors the PR gate exactly, including the cross-compile matrix,
so a contributor finds a failure before pushing rather than after.

Records the distribution tiering honestly: off Linux there is no
filesystem sandbox, and one-command installers cannot exist while the
repository is private."
```

---

## Notes for the implementer

**Task order is not arbitrary.** Task 1 unblocks the cross-compile matrix that
Task 6 depends on; Task 2 provides the ldflags targets Task 9 sets; Task 3
provides the `config.CoderConfig.Mode` field that Task 4's health report and
Task 5's handlers both read; Task 4 provides the `/healthz` endpoint that Task
10's smoke test asserts against. Tasks 6-11 can be reordered among themselves,
but 1-5 are a chain — implement them in order and no task ever references
something that does not yet exist.

**Do not add duplicate state to `Server`.** Task 4 adds `coderMode()` and
`sandboxEnabled()` accessors that read the existing `s.cfg` field
(`web/server.go:38`). Resist adding parallel `coderMode`/`sandboxEnabled`
struct fields — the config is already there, and two sources for one value
drift.

**Do not weaken a scanner to make CI green.** If govulncheck or Trivy fails,
bump the dependency. The one sanctioned suppression is Trivy's
`ignore-unfixed: true`, which is already in the config and exists because a CVE
with no available patch blocks work nobody can unblock.

**The manual setup in `docs/ci-setup.md` is real.** The pipeline is not
functional until `RELEASE_PLEASE_TOKEN` exists and branch protection lists the
required checks. Neither can be done from inside the repository.
