package coder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ilijad1/simple-agents/internal/composioassets"
	"github.com/ilijad1/simple-agents/internal/llm"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/vault"
)

// maxToolResult is the per-result byte cap injected back into the model context.
const maxToolResult = 8 * 1024

// noStdoutSentinel prefixes a run_script result when the script printed nothing to stdout
// but did write to stderr. It's surfaced to the model as useful diagnostic context, but it
// is NOT "real output" for the build-time verification gate (see trackScriptProgress).
const noStdoutSentinel = "(no stdout; stderr)"

// hostToolSet is the bundle of host-executed tools the API engine offers to the
// model, plus the runtime needed to execute them safely against the user's vault.
// All file tools are vault-root-relative (or absolute within the vault); run_script
// paths are relative to the agent working directory, matching the CLI coder's CWD
// convention so existing AGENT.md instructions work unchanged.
type hostToolSet struct {
	workspaceID      string
	vlt              *vault.Vault
	workDir          string            // agent dir (runs) or vault root (chat); CWD for run_script
	subprocessEnv    map[string]string // env for run_script (user secrets, provider key already stripped)
	sandbox          bool
	selfExe          string
	dataDir          string
	homesDir         string
	includeRunScript bool

	// Build-time script verification. verifyBuild is set ONLY during agent generation
	// (SA_BUILD_PHASE=generation) on the API/tool-calling backend — the weaker-model path
	// that, unlike a full CLI coder, does not reliably run-and-fix its own scripts. The
	// engine uses these to refuse to "finish" a build while the model has authored a
	// helper script it never once got real output from (see verifyFinishNudge +
	// runToolLoop), driving it to actually run, inspect, and fix the script — or, after a
	// bounded number of attempts, report the failure to the user in plain language.
	verifyBuild     bool
	authoredScripts map[string]bool // non-seeded tools/*.py the model WROTE this build (by canonical path)
	producedOutput  map[string]bool // authored scripts that RAN with real (non-empty) output
	verifyNudges    int             // how many finish-verification nudges have fired

	// Loop guard: bounded memory of recent failing calls. A model sometimes re-requests
	// the exact same call that just failed (e.g. a script that exits 1) — but a weaker
	// model can also OSCILLATE between a couple of different failing approaches (A fails,
	// B fails, A fails, B fails, ...), which a single-slot "last failure" memory never
	// catches because B overwrites A's record before A's retry is ever checked. recentFails
	// is a small ring of the last failHistorySize failing (name, args) pairs, checked
	// against on every call, so a repeat of ANY recent failure — not just the immediately
	// preceding one — is short-circuited. consecutiveFails additionally counts failures in
	// a row (regardless of which tool) so a stronger nudge can fire before the whole turn
	// budget is silently burned. Per-run state on the toolset (one toolset per run).
	recentFails      []failedCall
	consecutiveFails int
}

// failedCall identifies one failing tool invocation by name+args for the oscillation guard.
type failedCall struct {
	name string
	args string
}

// failHistorySize bounds how many distinct recent failures executeOrNudge remembers.
const failHistorySize = 4

// consecutiveFailWarnThreshold is how many tool-call failures in a row (regardless of
// which tool) trigger a stronger "stop iterating, consider [BLOCKED]" nudge, so the model
// course-corrects with turn budget still left to explain itself clearly.
const consecutiveFailWarnThreshold = 3

// hostTools returns the llm.Tool definitions this host toolset offers. The model
// sees these as native function-calling tools; the host executes them.
func (h *hostToolSet) tools() []llm.Tool {
	tools := []llm.Tool{
		{Name: "read_file", Description: "Read a file from the user's knowledge base (vault). Path is relative to the vault root, or an absolute path inside the vault.", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string","description":"vault-relative or absolute-within-vault path"}},"required":["path"]}`)},
		{Name: "write_file", Description: "Create or overwrite a file in the vault (creates parent folders). Path is relative to the vault root, or absolute within the vault.", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string","description":"full file contents"}},"required":["path","content"]}`)},
		{Name: "edit_file", Description: "Replace a unique substring in a vault file. old_string must appear exactly once.", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"]}`)},
		{Name: "list_dir", Description: "List entries in a vault directory. Path is relative to the vault root (default \".\" lists the vault root).", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string","description":"vault-relative directory; defaults to vault root"}}}`)},
	}
	if h.includeRunScript {
		tools = append(tools, llm.Tool{
			Name: "run_script",
			Description: "Run a Python helper script under your working directory's tools/ folder (e.g. \"tools/foo.py\") and return its stdout. " +
				"Pass command-line arguments via `args` (e.g. [\"tools/payload.json\"] for a script that reads sys.argv[1]) and/or pipe input via `stdin` — " +
				"this matches how scripts are invoked on a host CLI coder (python3 tools/foo.py tools/payload.json). " +
				"The script runs with your working directory as CWD, sandboxed; secrets are available as env vars.",
			Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string","description":"path to the .py file, relative to your working directory"},"args":{"type":"array","items":{"type":"string"},"description":"command-line arguments to pass to the script (e.g. a payload file path the script reads via sys.argv)"},"stdin":{"type":"string","description":"text to feed to the script's stdin"}},"required":["path"]}`),
		})
	}
	return tools
}

func rawSchema(s string) json.RawMessage { return json.RawMessage(s) }

// executeOrNudge runs one tool call, but short-circuits a repeat of any call that failed
// recently (exact repeat OR oscillating back to an earlier failing approach — see the
// recentFails doc comment). A weak model that gets an error result with no obvious fix
// (e.g. a script exiting 1) will sometimes re-request the same failing call — sometimes
// immediately, sometimes after trying one other thing first — burning the turn budget on
// retries that can never succeed. Re-executing would produce the same failure, so we
// return a nudge telling the model to change approach or report the outcome instead. This
// is the loop-breaker; surfacing stdout on script failure (see runScript) is the
// diagnostic that usually lets the model self-correct before this even trips. Once
// consecutiveFailWarnThreshold failures have happened in a row (regardless of which
// tool), an additional nudge toward [BLOCKED] is appended so the model still has turns
// left to explain itself clearly instead of silently exhausting the budget.
func (h *hostToolSet) executeOrNudge(ctx context.Context, call llm.ToolCall) string {
	argsKey := strings.TrimSpace(string(call.Args))
	if h.matchesRecentFailure(call.Name, argsKey) {
		return "error: you already tried " + call.Name + " with these exact arguments recently and it failed. " +
			"Do NOT retry it — including by switching to something else and coming back to it. Either change " +
			"the arguments, use a fundamentally different tool/approach, or stop and report the outcome to the " +
			"user with [CHAT], or emit [BLOCKED] if you're stuck."
	}
	result := h.execute(ctx, call)
	isErr := strings.HasPrefix(result, "error:")
	h.trackScriptProgress(call, result, isErr)
	if isErr {
		h.recordFailure(call.Name, argsKey)
		h.consecutiveFails++
		if h.consecutiveFails >= consecutiveFailWarnThreshold {
			result += fmt.Sprintf("\n\n(%d tool calls in a row have failed. Stop iterating blindly — try a "+
				"fundamentally different approach, or emit [BLOCKED] now while you still have turns left to "+
				"explain clearly what's blocking you and what alternatives exist.)", h.consecutiveFails)
		}
	} else {
		h.consecutiveFails = 0
		h.recentFails = nil
	}
	// A tool result must never be an empty string. A run_script that produces no stdout,
	// or a read_file of an empty file, otherwise yields "" — and a tool-result message
	// with empty content is dropped/omitted by the OpenAI-compatible serializer, which
	// Mistral rejects with HTTP 422 ("content: Field required"), failing the whole run.
	// Normalizing here (the single funnel for every tool result) fixes it for every
	// provider and also tells the model something happened instead of nothing.
	if strings.TrimSpace(result) == "" {
		result = "(the tool produced no output)"
	}
	return result
}

// matchesRecentFailure reports whether (name, argsKey) matches any of the last
// failHistorySize failing calls.
func (h *hostToolSet) matchesRecentFailure(name, argsKey string) bool {
	for _, f := range h.recentFails {
		if f.name == name && f.args == argsKey {
			return true
		}
	}
	return false
}

// recordFailure appends a failing call to the bounded ring, dropping the oldest entry
// once it exceeds failHistorySize.
func (h *hostToolSet) recordFailure(name, argsKey string) {
	h.recentFails = append(h.recentFails, failedCall{name: name, args: argsKey})
	if len(h.recentFails) > failHistorySize {
		h.recentFails = h.recentFails[len(h.recentFails)-failHistorySize:]
	}
}

// maxVerifyNudges bounds how many times runToolLoop refuses to let a build finish with
// an unverified script before letting it stop (and report to the user). At the last
// nudge the model is told to give up fixing and report the failure in plain language.
const maxVerifyNudges = 5

// trackScriptProgress records, PER SCRIPT, which non-seeded helper scripts the model
// authored and which of them actually ran with real output. Tracking per-script (not a
// single flag) matters because a Composio build runs the SEEDED composio_discover.py
// early — that must NOT count as verifying the model's OWN fetch/send script — and
// because one working script must not mask a second, broken one. Cheap, side-effect free.
func (h *hostToolSet) trackScriptProgress(call llm.ToolCall, result string, isErr bool) {
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(call.Args, &a)
	if !isAgentScriptPath(a.Path) { // ignores non-.py, non-tools, and seeded helpers
		return
	}
	key := canonScriptPath(a.Path)
	switch call.Name {
	case "write_file", "edit_file":
		if h.authoredScripts == nil {
			h.authoredScripts = map[string]bool{}
		}
		h.authoredScripts[key] = true
	case "run_script":
		// Only real stdout counts as "the script produced output". A run that printed
		// nothing to stdout and only logged to stderr returns the "(no stdout; stderr)"
		// sentinel (see runScript) — that is diagnostic noise, not the real data the
		// verification gate is checking for, so it must NOT satisfy needsScriptVerification.
		if !isErr && strings.TrimSpace(result) != "" && !strings.HasPrefix(result, noStdoutSentinel) {
			if h.producedOutput == nil {
				h.producedOutput = map[string]bool{}
			}
			h.producedOutput[key] = true
		}
	}
}

// canonScriptPath normalizes a script path so a write_file("tools/x.py") and a later
// run_script("tools/x.py") map to the same key.
func canonScriptPath(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
}

// isAgentScriptPath reports whether path is an ENTRY-POINT helper script the model authored
// under tools/ (a non-seeded .py that the agent actually runs to do its work) — the only
// kind the build-verification gate should require real output from. The seeded Composio
// helpers are Go-authored, so writing or running them doesn't count.
//
// It deliberately EXCLUDES the multi-file supporting files the testing_rules prompt itself
// tells the model to create — library modules under tools/lib/ (imported, never run
// standalone), test files under tools/tests/ and test_*.py / *_test.py (run separately, not
// the agent's work), and __init__.py / conftest.py (package/pytest scaffolding). Treating
// those as scripts-that-must-produce-output kept needsScriptVerification perpetually
// unsatisfied and produced a false [BLOCKED] on an otherwise-correct multi-file agent.
func isAgentScriptPath(path string) bool {
	p := filepath.ToSlash(strings.TrimSpace(path))
	if !strings.HasSuffix(p, ".py") {
		return false
	}
	if !strings.HasPrefix(p, "tools/") && !strings.Contains(p, "/tools/") {
		return false
	}
	base := filepath.Base(p)
	if composioassets.IsSeededFilename(base) {
		return false
	}
	if base == "__init__.py" || base == "conftest.py" ||
		strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") {
		return false
	}
	// A library module or test lives under a lib/ or tests/ segment of the tools tree —
	// not a standalone entry script.
	for _, seg := range strings.Split(p, "/") {
		if seg == "lib" || seg == "tests" {
			return false
		}
	}
	return true
}

// needsScriptVerification is true when the model authored a helper script this build
// that has NEVER once returned real output — i.e. it may be about to ship a script that
// silently does nothing. A seeded helper running does not clear this (see
// trackScriptProgress), so a Composio agent's own fetch/send script is still checked.
func (h *hostToolSet) needsScriptVerification() bool {
	for s := range h.authoredScripts {
		if !h.producedOutput[s] {
			return true
		}
	}
	return false
}

// verifyFinishNudge is consulted when the model tries to end a BUILD (emits a final
// answer with no tool calls). While the model has an unverified script and the nudge
// budget isn't spent, it returns a message that keeps the loop going — telling the model
// to actually run, inspect, and FIX the script (and to keep scripts thin, doing the
// reasoning itself), not to stop. On the final nudge it flips: stop fixing, and report
// what couldn't be done to the user in PLAIN language with an alternative. Returns "" to
// allow the finish (no unverified script, verification not applicable, or budget spent).
func (h *hostToolSet) verifyFinishNudge() string {
	if !h.verifyBuild || !h.needsScriptVerification() || h.verifyNudges >= maxVerifyNudges {
		return ""
	}
	h.verifyNudges++
	if h.verifyNudges >= maxVerifyNudges {
		// Last nudge: this IS the model's chance to stop. The engine allows the next
		// finish regardless (budget spent), so give BOTH honest exits — a plain-language
		// failure report, or an explicit "legitimately nothing to report" — so a valid
		// but empty-result agent is NOT forced into a false [BLOCKED].
		return "You have tried several times and the helper script still isn't returning real data. " +
			"Stop trying to fix it now and finish. Choose the honest option:\n" +
			"- If this genuinely cannot be done, emit a [BLOCKED] block explaining in PLAIN, NON-TECHNICAL " +
			"language what could not be done (for example: \"I wasn't able to read your emails\") and suggest " +
			"ONE alternative — no code, no file names, no technical terms.\n" +
			"- If the empty result is actually CORRECT right now (there truly is nothing to report), say that " +
			"plainly and finish normally.\n" +
			"- Or, if you can accomplish the goal WITHOUT that script (doing the work yourself from data you can " +
			"already obtain with a minimal fetch), do that now."
	}
	return "Before you finish: you wrote a helper script but it has not yet returned any real data. " +
		"An empty result almost always means it is BROKEN — do not ship it. Run it (run_script), read exactly " +
		"what it prints, and fix the cause (print the raw API response, check the field names, correct the " +
		"logic), then run it again — repeat until it returns real data. Keep the script THIN: it should mainly " +
		"load its secret from the environment, make the request, and print the raw result; do the parsing, " +
		"decisions, and formatting YOURSELF in your reasoning from what it printed, rather than cramming all " +
		"the logic into the script. Never print, log, or return a secret value."
}

// execute runs one tool call and returns the result text the engine feeds back to
// the model (or an error, which is also surfaced as the tool result).
func (h *hostToolSet) execute(ctx context.Context, call llm.ToolCall) string {
	var args struct {
		Path      string   `json:"path"`
		Content   string   `json:"content"`
		OldString string   `json:"old_string"`
		NewString string   `json:"new_string"`
		Args      []string `json:"args"`
		Stdin     string   `json:"stdin"`
	}
	_ = json.Unmarshal(call.Args, &args) // tolerate missing fields

	switch call.Name {
	case "read_file":
		data, err := h.readFile(args.Path)
		if err != nil {
			return "error: " + err.Error()
		}
		return truncate(string(data))
	case "write_file":
		if err := h.writeFile(args.Path, args.Content); err != nil {
			return "error: " + err.Error()
		}
		return "ok: wrote " + args.Path
	case "edit_file":
		if err := h.editFile(args.Path, args.OldString, args.NewString); err != nil {
			return "error: " + err.Error()
		}
		return "ok: edited " + args.Path
	case "list_dir":
		return h.listDir(args.Path)
	case "run_script":
		if !h.includeRunScript {
			return "error: run_script is not available"
		}
		out, err := h.runScript(ctx, args.Path, args.Args, args.Stdin)
		if err != nil {
			return "error: " + err.Error()
		}
		return truncate(out)
	default:
		return "error: unknown tool " + call.Name
	}
}

// resolveVault turns a relative or absolute-within-vault path into an absolute
// path, rejecting anything that escapes the vault root.
//
// Relative paths resolve against the working directory — exactly the CWD
// semantic a CLI coder (claude-code, opencode) has when its subprocess runs with
// --dir=<workDir>. This matters for generation: the implementation prompt says
// "create AGENT.md in the current directory", and workDir is the agent's own
// dir, so write_file("AGENT.md") must land at <workDir>/AGENT.md (not the vault
// root, which is where the previous vault-root-relative resolution sent it —
// that's why the coder "didn't create AGENT.md" when in fact it did, just to the
// wrong place). For chat, workDir == vaultRoot so relative paths still resolve
// to the vault root. For runs, workDir is the agent dir; the run prompt gives
// the absolute vault-root path for the USER's notes/memory, and the agent writes
// its OWN files (tools/, logs/) via relative paths against the agent dir —
// again matching the CLI coder.
//
// The resolved path must stay within the vault root in all cases, so a relative
// path with ".." that would escape the vault (e.g. "../../etc/passwd") is
// rejected the same way an absolute out-of-vault path is.
func (h *hostToolSet) resolveVault(path string) (string, error) {
	root := h.vlt.Root(h.workspaceID)
	if filepath.IsAbs(path) {
		abs := filepath.Clean(path)
		if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return "", fmt.Errorf("path outside vault: %q", path)
		}
		return abs, nil
	}
	base := h.workDir
	if base == "" {
		base = root
	}
	abs := filepath.Clean(filepath.Join(base, path))
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path outside vault: %q", path)
	}
	return abs, nil
}

func (h *hostToolSet) readFile(path string) ([]byte, error) {
	abs, err := h.resolveVault(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

func (h *hostToolSet) writeFile(path, content string) error {
	abs, err := h.resolveVault(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return err
	}
	return writeFileAtomic(abs, []byte(content), 0o640)
}

func (h *hostToolSet) editFile(path, oldStr, newStr string) error {
	if oldStr == "" {
		return fmt.Errorf("old_string is required")
	}
	abs, err := h.resolveVault(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	count := strings.Count(string(data), oldStr)
	if count == 0 {
		return fmt.Errorf("old_string not found in %s", path)
	}
	if count > 1 {
		return fmt.Errorf("old_string appears %d times in %s; it must be unique", count, path)
	}
	return writeFileAtomic(abs, []byte(strings.Replace(string(data), oldStr, newStr, 1)), 0o640)
}

func (h *hostToolSet) listDir(path string) string {
	if path == "" {
		path = "."
	}
	abs, err := h.resolveVault(path)
	if err != nil {
		return "error: " + err.Error()
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "error: " + err.Error()
	}
	var sb strings.Builder
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && e.Name() != ".kb" {
			// .kb stays visible (it's where sidecars live); hide other dotfiles
			// (.git, .staging-* skill-creator scratch dirs, etc.) — noise the model
			// has no reason to see or touch.
			continue
		}
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		sb.WriteString(fmt.Sprintf("%s\t%s\n", kind, e.Name()))
	}
	if sb.Len() == 0 {
		return "(empty)"
	}
	return truncate(sb.String())
}

// runScript runs `python3 <workDir>/<path> [args...]` with the agent's secrets
// in env (the provider API key is stripped) and optional stdin, sandboxed via
// Landlock when enabled — the same confinement pattern the CLI coder uses, so
// agent helper scripts can't reach the DB, config, or other workspaces' vaults.
// scriptArgs are passed as argv (no shell), matching how a host CLI coder invokes
// the script (e.g. `python3 tools/foo.py tools/payload.json`); many Composio helper
// scripts read their payload from sys.argv[1].
func (h *hostToolSet) runScript(ctx context.Context, rel string, scriptArgs []string, stdin string) (string, error) {
	clean := filepath.Clean(strings.TrimPrefix(filepath.ToSlash(rel), "/"))
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid script path: %q", rel)
	}
	scriptAbs := filepath.Join(h.workDir, clean)
	if fi, err := os.Stat(scriptAbs); err != nil || fi.IsDir() {
		return "", fmt.Errorf("script not found: %q", rel)
	}

	// Isolate the script's temp files to the per-workspace home (same as the CLI
	// coder's buildEnv) instead of the shared system /tmp, which the sandbox
	// deliberately does not grant RW on.
	homeDir := h.userHomeDir()
	tmpDir := filepath.Join(homeDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", fmt.Errorf("prepare script tmp dir: %w", err)
	}
	env := buildEnvList(h.subprocessEnv, homeDir, tmpDir)
	command := append([]string{"python3", scriptAbs}, scriptArgs...)
	cmd := h.buildScriptCommand(ctx, command, env, h.workDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("script timed out")
		}
		// Report BOTH streams. Many agent scripts print their failure reason to
		// stdout (e.g. a JSON `{"success":false,"error":...}` line) and then
		// sys.exit(1); if we only surfaced stderr (often empty), the model would
		// get no diagnostic, fail to self-correct, and re-run the same script in
		// a loop until ErrMaxTurns.
		return "", fmt.Errorf("script failed: %w\nstdout: %s\nstderr: %s", err, truncate(stdout.String()), truncate(stderr.String()))
	}
	out := stdout.String()
	if strings.TrimSpace(out) == "" && stderr.Len() > 0 {
		// A script that only logged to stderr is still useful context.
		return noStdoutSentinel + "\n" + truncate(stderr.String()), nil
	}
	return out, nil
}

// userHomeDir returns the per-workspace isolated home dir, matching
// Coder.UserHomeDir — used to grant run_script the same RW home access (and
// isolated TMPDIR) that the CLI coder subprocess gets.
func (h *hostToolSet) userHomeDir() string {
	return filepath.Join(h.homesDir, safeID(h.workspaceID))
}

// buildScriptCommand mirrors Coder.buildCommand's sandbox wrapping but for an
// arbitrary command vector (the python interpreter + script). The CLI path is left
// untouched; this is used only by the API engine's run_script tool.
func (h *hostToolSet) buildScriptCommand(ctx context.Context, command []string, env []string, runDir string) *exec.Cmd {
	var cmd *exec.Cmd
	if h.sandbox && h.selfExe != "" && sandbox.Supported() {
		rw := []string{runDir, h.userHomeDir()}
		if h.dataDir != "" {
			rw = append(rw, filepath.Join(h.dataDir, "vaults", h.workspaceID))
		}
		ro := sandbox.SystemReadOnlyPaths()
		// Keep the python interpreter's install dir readable+executable.
		if p, err := exec.LookPath(command[0]); err == nil {
			ro = append(ro, filepath.Dir(p))
		}
		spec := sandbox.Spec{
			Command:        command,
			Dir:            runDir,
			Env:            env,
			ReadWritePaths: dedupePaths(rw...),
			ReadOnlyPaths:  dedupePaths(ro...),
			ReadWriteFiles: sandbox.SystemReadWriteFiles(),
			NoFile:         8192,
		}
		if wargv, err := sandbox.Wrap(h.selfExe, spec); err == nil {
			cmd = exec.CommandContext(ctx, wargv[0], wargv[1:]...)
		}
	}
	if cmd == nil {
		cmd = exec.CommandContext(ctx, command[0], command[1:]...)
	}
	cmd.Dir = runDir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// buildEnvList constructs the run_script subprocess environment. HOME/TMPDIR
// are isolated to the per-workspace home (matching Coder.buildEnv) so temp
// files don't need — and don't leak through — the shared system /tmp, which
// the sandbox deliberately does not grant. System overrides always take
// precedence over extra (the agent's own secrets).
func buildEnvList(extra map[string]string, homeDir, tmpDir string) []string {
	overrides := map[string]string{
		"HOME":   homeDir,
		"TMPDIR": tmpDir,
		"TMP":    tmpDir,
		"TEMP":   tmpDir,
	}
	for k, v := range extra {
		if _, exists := overrides[k]; !exists {
			overrides[k] = v
		}
	}
	return overrideEnv(os.Environ(), overrides)
}

func truncate(s string) string {
	if len(s) <= maxToolResult {
		return s
	}
	return s[:maxToolResult] + "\n…[truncated]"
}

// writeFileAtomic mirrors the vault's own unexported atomic-write helper.
// Needed locally only for the absolute-within-vault edge case in writeFile/
// editFile, where the path is already resolved to an abs on-disk location and
// vault.WriteNote (which re-resolves from a vault-relative path) doesn't apply.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
