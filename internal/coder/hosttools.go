package coder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/convert"
	"github.com/ilijad1/simple-agents/internal/llm"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/vault"
	"github.com/ilijad1/simple-agents/internal/websearch"
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
	includeExecTools bool // gates the powerful tools (run_script, bash, web_fetch); off for chat

	// web_fetch tuning (both optional; zero values use sane defaults). Injected by tests so
	// the transient-retry path doesn't sleep for real.
	httpClient   *http.Client  // nil → a default 30s client
	webRetryBase time.Duration // 0 → default base backoff for transient (429/5xx/network) retries

	// ddgBaseURL, when set, overrides the search endpoint AND collapses the
	// provider cascade to that single endpoint. Tests point it at an httptest
	// server so the scraper is exercised deterministically and offline; in
	// production it is empty and the full cascade (see websearch.DefaultProviders)
	// applies.
	ddgBaseURL string

	// allowPrivateHosts disables web_fetch's private-address guard. It exists
	// ONLY for tests, which serve fixtures from httptest servers bound to
	// 127.0.0.1. It is never set in production: the guard is what stops a chat
	// coder from reaching the loopback connector bridge and its bearer tokens.
	allowPrivateHosts bool

	// fetchMemo caches web_fetch results within this toolset (one run/loop).
	// A weak model re-fetches the same URL repeatedly; the memo makes that free
	// and bounded, with no cross-run invalidation problem to get wrong.
	fetchMemo map[string]string

	// Build-time script verification. verifyBuild is set ONLY during agent generation
	// (SA_BUILD_PHASE=generation) on the API/tool-calling backend — the weaker-model path
	// that, unlike a full CLI coder, does not reliably run-and-fix its own scripts. The
	// engine uses these to refuse to "finish" a build while the model has authored a
	// helper script it never once got real output from (see verifyFinishNudge +
	// runToolLoop), driving it to actually run, inspect, and fix the script — or, after a
	// bounded number of attempts, report the failure to the user in plain language.
	verifyBuild bool
	// spec describes what this build must produce (deliverable file + which paths are
	// entry-point scripts + the nudge wording). The zero value is treated as
	// AgentBuildSpec, so an unset spec keeps the historical agent behaviour.
	spec            BuildSpec
	authoredScripts map[string]bool // non-seeded tools/*.py the model WROTE this build (by canonical path)
	producedOutput  map[string]bool // authored scripts that RAN with real (non-empty) output
	verifyNudges    int             // how many finish-verification nudges have fired

	// lastVerifiedOutput is the most recent real, non-empty stdout captured from an authored
	// helper script that RAN successfully this build (set alongside producedOutput). It is
	// surfaced up to the agent designer (via coder.Result) as the review sample the user sees
	// before approve — so a build the engine already confirmed runs shows real output even
	// when the model forgot to wrap it in a [TEST_OUTPUT] marker. Secret values are redacted.
	lastVerifiedOutput string

	// ranAuthoredScript records whether the model EXECUTED an authored helper script at least
	// once this build (regardless of whether it produced output). It discriminates the two
	// "couldn't confirm" causes: the model ran its script but got nothing (broken/blocked) vs.
	// it never ran the script at all — surfaced via coder.Result.ScriptRan and logged at the
	// weak-backend gate so a real failure tells us which lever to pull next.
	ranAuthoredScript bool

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

	// Self-managed OAuth connector tools. When an agent is bound to service
	// connections (agent_connections), each connection's curated actions are offered
	// as native typed tools (connectorTools) and dispatched through connectors.Execute.
	// Empty for chat / unbound agents. See connectortools.go.
	connReg    *connectors.Registry
	connStore  connectors.TokenStore
	boundConns []connectors.BoundConn

	// usedConnIDs records connection IDs whose connector tools were invoked (for build auto-bind).
	usedConnIDs map[string]bool
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
		{Name: "read_file", Description: "Read a file from the user's knowledge base (vault). Path is relative to the vault root, or an absolute path inside the vault. Large files are capped; to page through one, pass offset (byte to start at) and optionally limit (max bytes) — the result tells you the next offset when more remains.", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string","description":"vault-relative or absolute-within-vault path"},"offset":{"type":"integer","description":"byte offset to start reading from (default 0)"},"limit":{"type":"integer","description":"max bytes to return from offset (default: the per-result cap)"}},"required":["path"]}`)},
		{Name: "write_file", Description: "Create or overwrite a file in the vault (creates parent folders). Path is relative to the vault root, or absolute within the vault.", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string","description":"full file contents"}},"required":["path","content"]}`)},
		{Name: "edit_file", Description: "Replace a unique substring in a vault file. old_string must appear exactly once.", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"}},"required":["path","old_string","new_string"]}`)},
		{Name: "list_dir", Description: "List entries in a vault directory. Path is relative to the vault root (default \".\" lists the vault root).", Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string","description":"vault-relative directory; defaults to vault root"}}}`)},
		{Name: "search_files", Description: "Search the user's whole knowledge base (vault) for literal text and return the matching lines as `path:line: snippet` entries. Case-insensitive. Use this to find a note by its CONTENT instead of read_file-ing your way through folders — " +
			`e.g. search_files with query "dentist appointment". Returns up to a few dozen matches across all notes/memory/agents files (not the hidden .kb sidecars).`,
			Parameters: rawSchema(`{"type":"object","properties":{"query":{"type":"string","description":"the literal text to search for across the vault (case-insensitive)"}},"required":["query"]}`)},
		{Name: "glob", Description: "Find files in the vault by name/pattern and return their vault-relative paths (one per line). Supports * (within one folder), ? (one char), and ** (any depth, crosses folders) — " +
			`e.g. glob with pattern "notes/*-meeting.md" or "**/*.py". Use this to locate files by NAME instead of listing folders one at a time.`,
			Parameters: rawSchema(`{"type":"object","properties":{"pattern":{"type":"string","description":"glob pattern matching vault-relative paths (supports *, ?, and **)"}},"required":["pattern"]}`)},
	}
	if h.includeExecTools {
		tools = append(tools,
			llm.Tool{
				Name: "web_fetch",
				Description: "Fetch a PUBLIC URL over HTTP(S) and return its content as text (HTML is reduced to readable text; JSON/text is returned as-is), " +
					`e.g. web_fetch with url "https://api.open-meteo.com/v1/forecast?latitude=42.0&longitude=21.4&current=temperature_2m". ` +
					"Use this for a simple read of a PUBLIC endpoint — a weather API, an RSS/JSON feed, a web page. " +
					"It CANNOT send secrets: you do not have secret values (they are environment variables), so any call that needs an API key, token, or auth header must use run_script or bash instead, where secrets are available in the environment. " +
					"Optional: method (GET or POST; default GET).",
				Parameters: rawSchema(`{"type":"object","properties":{"url":{"type":"string","description":"the public http/https URL to fetch"},"method":{"type":"string","enum":["GET","POST"],"description":"HTTP method (default GET)"}},"required":["url"]}`),
			},
			llm.Tool{
				Name: "web_search",
				Description: "Search the public web (DuckDuckGo) and return a few results as numbered `title / url / snippet` entries — " +
					`e.g. web_search with query "weather Skopje today". Use it to FIND a URL when you don't have one yet; then call web_fetch to READ the page you chose. ` +
					"It is query-only and CANNOT carry secrets — there is nothing to authenticate, so it needs no key/token.",
				Parameters: rawSchema(`{"type":"object","properties":{"query":{"type":"string","description":"the web search query"}},"required":["query"]}`),
			},
			llm.Tool{
				Name: "run_script",
				Description: "Run a Python helper script under your working directory's tools/ folder (e.g. \"tools/foo.py\") and return its stdout. " +
					"Pass command-line arguments via `args` (e.g. [\"tools/payload.json\"] for a script that reads sys.argv[1]) and/or pipe input via `stdin` — " +
					"this matches how scripts are invoked on a host CLI coder (python3 tools/foo.py tools/payload.json). " +
					"The script runs with your working directory as CWD, sandboxed; secrets are available as env vars. " +
					"For LARGE or many-item output, have the script write its results to files itself and print only a " +
					"short summary — a big stdout dump gets truncated and can't be relayed back through you reliably.",
				Parameters: rawSchema(`{"type":"object","properties":{"path":{"type":"string","description":"path to the .py file, relative to your working directory"},"args":{"type":"array","items":{"type":"string"},"description":"command-line arguments to pass to the script (e.g. a payload file path the script reads via sys.argv)"},"stdin":{"type":"string","description":"text to feed to the script's stdin"}},"required":["path"]}`),
			},
			llm.Tool{
				Name: "bash",
				Description: "Run a bash command with your working directory as CWD, sandboxed, and return its stdout. " +
					"Secrets are available as environment variables (e.g. curl -H \"Authorization: Bearer $MY_TOKEN\" ...), so use this (or run_script) for any call that needs a secret. " +
					"On failure both stdout and stderr are returned so you can see what went wrong. Do NOT install packages (no internet-backed pip/apt) — use tools that are already present.",
				Parameters: rawSchema(`{"type":"object","properties":{"command":{"type":"string","description":"the bash command line to run"}},"required":["command"]}`),
			},
		)
	}
	tools = append(tools, h.connectorTools()...)
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
// single flag) matters because a build runs a helper script
// early — that must NOT count as verifying the model's OWN fetch/send script — and
// because one working script must not mask a second, broken one. Cheap, side-effect free.
func (h *hostToolSet) trackScriptProgress(call llm.ToolCall, result string, isErr bool) {
	var a struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(call.Args, &a)
	if !h.buildSpec().IsScript(a.Path) { // ignores non-.py, non-tools, and seeded helpers
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
		// The model executed an authored script this build (output tracked separately below).
		h.ranAuthoredScript = true
		// Only real stdout counts as "the script produced output". A run that printed
		// nothing to stdout and only logged to stderr returns the "(no stdout; stderr)"
		// sentinel (see runScript) — that is diagnostic noise, not the real data the
		// verification gate is checking for, so it must NOT satisfy needsScriptVerification.
		if !isErr && strings.TrimSpace(result) != "" && !strings.HasPrefix(result, noStdoutSentinel) {
			if h.producedOutput == nil {
				h.producedOutput = map[string]bool{}
			}
			h.producedOutput[key] = true
			// Keep the real stdout as the review sample shown to the user before approve
			// (see coder.Result.ScriptOutput). Redact secret values first — we now surface
			// raw stdout automatically, so we can't rely on the model-curated [TEST_OUTPUT]
			// path's "never print a secret" nudge to keep credentials off the screen.
			h.lastVerifiedOutput = h.redactSecrets(result)
		}
	}
}

// redactSecrets replaces any exact secret VALUE from the run env with a placeholder, so an
// authored script that echoes a token doesn't leak it into the review sample surfaced to the
// user. Only non-trivial values are matched (short/empty values would over-redact benign text).
func (h *hostToolSet) redactSecrets(s string) string {
	for _, v := range h.subprocessEnv {
		if len(v) < 6 {
			continue
		}
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	return s
}

// scriptVerified reports whether the model authored at least one helper script this build AND
// every authored script ran with real output — the engine's ground truth that the build's
// scripts actually work. Consumed by the agent designer's decideBuildOutcome so it agrees with
// the finish gate (verifyFinishNudge) instead of re-deriving verification from a [TEST_OUTPUT]
// marker the weak model may have forgotten.
func (h *hostToolSet) scriptVerified() bool {
	return len(h.authoredScripts) > 0 && !h.needsScriptVerification()
}

// verifiedOutput returns the captured real stdout from a verified authored-script run (empty
// when nothing was verified).
func (h *hostToolSet) verifiedOutput() string {
	return h.lastVerifiedOutput
}

// authoredScriptRan reports whether the model executed an authored helper script at least once
// this build (used only for observability — see hostToolSet.ranAuthoredScript).
func (h *hostToolSet) authoredScriptRan() bool {
	return h.ranAuthoredScript
}

// usedConnectionIDs returns the sorted, deduped connection IDs whose connector tools were invoked.
func (h *hostToolSet) usedConnectionIDs() []string {
	if len(h.usedConnIDs) == 0 {
		return nil
	}
	out := make([]string, 0, len(h.usedConnIDs))
	for id := range h.usedConnIDs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// canonScriptPath normalizes a script path so a write_file("tools/x.py") and a later
// run_script("tools/x.py") map to the same key.
func canonScriptPath(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
}

// isAgentScriptPath reports whether path is an ENTRY-POINT helper script the model authored
// under tools/ (a non-seeded .py that the agent actually runs to do its work) — the only
// kind the build-verification gate should require real output from. A seeded
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
// trackScriptProgress), so an agent's own fetch/send script is still checked.
func (h *hostToolSet) needsScriptVerification() bool {
	for s := range h.authoredScripts {
		if !h.producedOutput[s] {
			return true
		}
	}
	return false
}

// buildSpec returns the build spec in force, defaulting to the agent shape so a caller
// that never sets one behaves exactly as before.
func (h *hostToolSet) buildSpec() BuildSpec {
	if h.spec.Deliverable == "" {
		return AgentBuildSpec
	}
	return h.spec
}

// verifyFinishNudge is consulted when the model tries to end a BUILD (emits a final
// answer with no tool calls). It keeps the loop going — returning a message the model must
// respond to — until the build is actually finishable. Two gates, in priority order:
//
//  1. the build's deliverable must exist. A build with no deliverable is useless
//     regardless of the helper script, and the common trap on a weak tool-calling backend
//     is the model burning its whole turn budget trying to verify a helper script that
//     can't reach the live service at build time (SA_BUILD_PHASE blocks outbound) — and
//     never writing the deliverable. So the FIRST thing the nudge demands is: write it,
//     don't keep fixing the script.
//
//  2. Once the deliverable exists, the script-verification gate applies: don't ship a
//     helper script that silently does nothing. At build time the helper usually CAN'T
//     return real data (outbound blocked), so this gate is expected to top out — which is
//     fine: the agent designer then presents the build as "built but not confirmed to run"
//     with a keep-it-as-is escape hatch (see decideBuildOutcome), rather than looping
//     forever.
//
// Returns "" to allow the finish (not a build, deliverable present + no unverified script,
// or the nudge budget is spent — the last resort so a stuck build can still end).
func (h *hostToolSet) verifyFinishNudge() string {
	if !h.verifyBuild || h.verifyNudges >= maxVerifyNudges {
		return ""
	}
	spec := h.buildSpec()

	// Gate 1: the deliverable must exist before the build may finish.
	if md, err := os.Stat(filepath.Join(h.workDir, spec.Deliverable)); err != nil || md.Size() == 0 {
		h.verifyNudges++
		return spec.MissingDeliverableNudge(h.verifyNudges >= maxVerifyNudges)
	}

	// Gate 2: refuse to ship an authored entry-point script that never returned real output.
	if !h.needsScriptVerification() {
		return ""
	}
	h.verifyNudges++
	return spec.UnverifiedScriptNudge(h.verifyNudges >= maxVerifyNudges)
}

// execute runs one tool call and returns the result text the engine feeds back to
// the model (or an error, which is also surfaced as the tool result).
func (h *hostToolSet) execute(ctx context.Context, call llm.ToolCall) string {
	var args struct {
		Path      string            `json:"path"`
		Content   string            `json:"content"`
		OldString string            `json:"old_string"`
		NewString string            `json:"new_string"`
		Args      []string          `json:"args"`
		Stdin     string            `json:"stdin"`
		URL       string            `json:"url"`
		Method    string            `json:"method"`
		Headers   map[string]string `json:"headers"`
		Body      string            `json:"body"`
		Command   string            `json:"command"`
		Query     string            `json:"query"`
		Pattern   string            `json:"pattern"`
		Offset    int               `json:"offset"`
		Limit     int               `json:"limit"`
	}
	_ = json.Unmarshal(call.Args, &args) // tolerate missing fields

	switch call.Name {
	case "read_file":
		data, err := h.readFile(args.Path)
		if err != nil {
			return "error: " + err.Error()
		}
		return readFileSlice(string(data), args.Offset, args.Limit)
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
	case "search_files":
		out, err := h.searchFiles(ctx, args.Query)
		if err != nil {
			return "error: " + err.Error()
		}
		return out
	case "glob":
		out, err := h.glob(args.Pattern)
		if err != nil {
			return "error: " + err.Error()
		}
		return out
	case "run_script":
		if !h.includeExecTools {
			return "error: run_script is not available"
		}
		out, err := h.runScript(ctx, args.Path, args.Args, args.Stdin)
		if err != nil {
			return "error: " + err.Error()
		}
		return h.spillLargeOutput(out, "run_script")
	case "web_fetch":
		if !h.includeExecTools {
			return "error: web_fetch is not available"
		}
		out, err := h.webFetch(ctx, args.URL, args.Method, args.Headers, args.Body)
		if err != nil {
			return "error: " + err.Error()
		}
		return truncate(out)
	case "web_search":
		if !h.includeExecTools {
			return "error: web_search is not available"
		}
		out, err := h.webSearch(ctx, args.Query)
		if err != nil {
			return "error: " + err.Error()
		}
		return truncate(out)
	case "bash":
		if !h.includeExecTools {
			return "error: bash is not available"
		}
		out, err := h.runBash(ctx, args.Command)
		if err != nil {
			return "error: " + err.Error()
		}
		return h.spillLargeOutput(out, "bash")
	default:
		if _, _, ok := h.resolveConnectorTool(call.Name); ok {
			var cargs map[string]any
			_ = json.Unmarshal(call.Args, &cargs)
			return h.executeConnectorTool(ctx, call.Name, cargs)
		}
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

// ── search_files / glob ───────────────────────────────────────────────────────

// maxSearchHits caps how many search_files matches are returned to the model, so a
// query that hits dozens of notes doesn't blow the context. The Searcher already
// caps at 5 matches per file; this bounds the total across all files.
const maxSearchHits = 50

// maxGlobMatches caps how many file paths glob returns.
const maxGlobMatches = 200

// searchFiles exposes the existing vault.Searcher (ripgrep + pure-Go fallback,
// case-insensitive fixed-string, 5 matches/file) to the model as a TIER-1 read —
// "find the note where I mentioned the dentist" without read_file-ing everything.
// It searches the WHOLE vault root (not workDir), matching the web KB search and
// the user's intent. No matches is a valid empty result (NOT an error:) so it never
// trips the oscillation guard. The Searcher excludes the hidden .kb sidecars.
func (h *hostToolSet) searchFiles(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	if h.vlt == nil {
		return "", fmt.Errorf("search_files unavailable: no vault")
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	hits, err := h.vlt.NewSearcher().Search(sctx, h.workspaceID, query)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return fmt.Sprintf("(no matches for %q)", query), nil
	}
	if len(hits) > maxSearchHits {
		hits = hits[:maxSearchHits]
	}
	var sb strings.Builder
	for _, hit := range hits {
		fmt.Fprintf(&sb, "%s:%d: %s\n", hit.Path, hit.Line, hit.Snippet)
	}
	return truncate(sb.String()), nil
}

// glob finds files by name/pattern across the whole vault and returns their
// vault-relative paths (one per line), supporting *, ?, and ** (recursive). It
// skips dotfiles and the internal .kb dir (mirror listDir's dotfile rule + the
// Searcher's .kb exclusion), so the model never sees or touches internal data.
// No matches is a valid empty result, not an error.
func (h *hostToolSet) glob(pattern string) (string, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if h.vlt == nil {
		return "", fmt.Errorf("glob unavailable: no vault")
	}
	root := h.vlt.Root(h.workspaceID)
	// A weak model sometimes passes an ABSOLUTE vault path as the pattern
	// (e.g. "/home/.../vaults/<ws>/notes/*.md") instead of a vault-relative
	// glob. Relativize it first — mirror read_file/resolveVault — so the call
	// still matches instead of no-op'ing (an absolute string anchored+quoted by
	// compileGlob can never match a vault-relative path). An absolute path that
	// escapes the vault root is rejected.
	if filepath.IsAbs(pattern) {
		rel, err := h.vlt.Rel(h.workspaceID, filepath.Clean(pattern))
		if err != nil {
			return "", fmt.Errorf("pattern outside vault: %q", pattern)
		}
		pattern = rel
	}
	matcher, err := compileGlob(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %w", err)
	}
	var matches []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			// Skip dotfile dirs and the internal .kb dir entirely — their
			// contents never match, and descending into .kb would leak sidecars.
			name := d.Name()
			if name == vault.InternalDir || (strings.HasPrefix(name, ".") && name != "." && name != "..") {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip dotfiles (e.g. .secret.md, .staging scratch).
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, rerr := h.vlt.Rel(h.workspaceID, path)
		if rerr != nil {
			return nil
		}
		if matcher.MatchString(rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return fmt.Sprintf("(no files matched %q)", pattern), nil
	}
	sort.Strings(matches)
	if len(matches) > maxGlobMatches {
		matches = matches[:maxGlobMatches]
	}
	return truncate(strings.Join(matches, "\n")), nil
}

// compileGlob converts a glob pattern into an anchored regexp. It supports the
// three forms a host coder's Glob offers: `*` matches within one folder (no
// separator), `?` matches one non-separator char, and `**` matches any depth
// (crosses separators). Everything else is regex-quoted literally. The result
// is anchored (^...$) so "notes/*.md" doesn't also match "x/notes/a.md".
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				sb.WriteString(".*") // ** crosses separators
				i++
			} else {
				sb.WriteString("[^/]*") // * stays within one folder
			}
		case '?':
			sb.WriteString("[^/]")
		default:
			sb.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}

// runScript runs `python3 <workDir>/<path> [args...]` with the agent's secrets
// in env (the provider API key is stripped) and optional stdin, sandboxed via
// Landlock when enabled — the same confinement pattern the CLI coder uses, so
// agent helper scripts can't reach the DB, config, or other workspaces' vaults.
// scriptArgs are passed as argv (no shell), matching how a host CLI coder invokes
// the script (e.g. `python3 tools/foo.py tools/payload.json`); many helper
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

// ── web_fetch ────────────────────────────────────────────────────────────────

// maxWebBody bounds how many bytes web_fetch reads from a response before the result is
// further truncated to maxToolResult for the model context.
const maxWebBody = 2 << 20 // 2 MiB

// webFetchMaxAttempts bounds the internal transient-retry loop (429/5xx/network/timeout).
const webFetchMaxAttempts = 4

// webFetch performs an HTTP(S) request and returns the response body as text (HTML reduced
// to readable text; JSON/text passed through), prefixed with a short status line. Transient
// failures (429, 5xx, network, timeout) are retried INTERNALLY with backoff and are NEVER
// surfaced as an "error:" result — so a blip that clears on its own does not trip the
// tool-loop's oscillation guard (executeOrNudge treats every "error:" as a repeat-worthy
// failure). A non-retryable outcome (bad URL, 4xx other than 429) returns an error, which the
// caller surfaces as "error: ...". It runs in the HOST process (not the sandbox): it is only
// an HTTP client, and agents already reach the network via run_script/bash, so it adds
// ergonomics, not capability. It cannot inject secrets (the model has no secret values) — an
// authenticated call must use run_script/bash where secrets are env vars.
func (h *hostToolSet) webFetch(ctx context.Context, rawURL, method string, headers map[string]string, body string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid url %q: must be an http/https URL", rawURL)
	}
	if method == "" {
		method = http.MethodGet
	}

	// Memo: an identical GET within one toolset costs one request.
	//
	// The key deliberately omits headers, which is safe ONLY because the tool's
	// JSON schema exposes just url+method to the model — headers/body are
	// plumbing it cannot populate. If a future change lets the model set
	// headers, they must join this key or two different requests will collide.
	memoKey := method + " " + u.String() + " " + body
	if h.fetchMemo == nil {
		h.fetchMemo = map[string]string{}
	}
	if cached, ok := h.fetchMemo[memoKey]; ok {
		return cached, nil
	}

	client := h.httpClient
	if client == nil {
		// The guarded client refuses to dial private/loopback/link-local space,
		// enforced at dial time so it also covers a hostname that resolves into
		// private space and every redirect hop.
		if h.allowPrivateHosts {
			client = &http.Client{Timeout: 30 * time.Second}
		} else {
			client = guardedHTTPClient(30 * time.Second)
		}
	}
	base := h.webRetryBase
	if base <= 0 {
		base = 500 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt < webFetchMaxAttempts; attempt++ {
		if attempt > 0 {
			if !ctxSleep(ctx, base<<(attempt-1)) {
				return "", ctx.Err()
			}
		}
		text, retryable, err := h.webFetchOnce(ctx, client, method, u.String(), headers, body)
		if err == nil {
			h.fetchMemo[memoKey] = text
			return text, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}
	return "", fmt.Errorf("web_fetch failed after %d attempts: %w", webFetchMaxAttempts, lastErr)
}

// webFetchOnce performs a single request. retryable is true for transient conditions the
// caller should retry (429, 5xx, network/timeout); false for definitive outcomes (2xx or a
// non-retryable 4xx).
func (h *hostToolSet) webFetchOnce(ctx context.Context, client *http.Client, method, u string, headers map[string]string, body string) (string, bool, error) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return "", false, fmt.Errorf("build request: %v", err)
	}
	req.Header.Set("User-Agent", "simple-agents/1.0 (+web_fetch)")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", true, fmt.Errorf("request failed: %v", err) // network/timeout → transient
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", true, fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxWebBody))
	if resp.StatusCode >= 400 {
		return "", false, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, u, snippetBytes(data))
	}
	ct := resp.Header.Get("Content-Type")
	header := fmt.Sprintf("[web_fetch %d %s %s]\n", resp.StatusCode, contentTypeMain(ct), u)
	return header + renderWebBody(ct, u, data), false, nil
}

// renderWebBody turns a response body into text the model can use. HTML and any
// convertible document format go through internal/convert — so a fetched page
// keeps its headings, lists, links and tables instead of collapsing into one
// whitespace-run, and a PDF/DOCX URL yields readable text instead of a dead end.
// A format convert cannot handle degrades to a short note naming the type rather
// than dumping raw bytes into the model context.
func renderWebBody(contentType, sourceURL string, data []byte) string {
	res, err := convert.ToMarkdown(data, convert.Options{MIME: contentType, SourceURL: sourceURL})
	if err == nil && strings.TrimSpace(res.Markdown) != "" {
		return res.Markdown
	}
	// convert could not handle this type. If the body is textual, hand it back
	// AS-IS rather than discarding it: a JSON API response is the single most
	// common web_fetch target, and returning "no text could be extracted" for
	// one would be a regression. This branch also keeps Phase 1 shippable on
	// its own — the JSON/CSV/PDF converters land in Phase 2, and until they do
	// every textual body still flows through here unchanged.
	if convert.IsTextual(data, contentType) {
		return string(data)
	}
	kind := convert.Detect(data, "", contentType)
	return fmt.Sprintf("[web_fetch: %s response (%s), %d bytes — no text could be extracted; if you need to process it, use run_script or bash]",
		contentTypeMain(contentType), kind, len(data))
}

func contentTypeMain(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	if ct = strings.TrimSpace(ct); ct == "" {
		return "?"
	}
	return ct
}

func snippetBytes(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// ctxSleep waits d, returning false if the context is cancelled first.
func ctxSleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// ── web_search ───────────────────────────────────────────────────────────────

// maxWebSearchResults bounds how many results web_search returns to the model.
const maxWebSearchResults = 6

// webSearch runs the provider cascade and renders numbered title/url/snippet
// entries. Reliability comes from the cascade, not from this function: a single
// engine returning a JS-challenge page (200 OK, zero parseable results) is
// indistinguishable from "no results", so websearch treats it as a reason to try
// the next engine. Exhausting every engine still yields a NON-error empty notice
// so the model can fall back to web_fetch without tripping the oscillation guard.
func (h *hostToolSet) webSearch(ctx context.Context, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	client := h.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	base := h.webRetryBase
	if base <= 0 {
		base = 500 * time.Millisecond
	}

	results, err := (&websearch.Client{
		HTTP:      client,
		RetryBase: base,
		Providers: h.searchProviders(),
	}).Search(ctx, query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "(no search results)", nil
	}
	if len(results) > maxWebSearchResults {
		results = results[:maxWebSearchResults]
	}
	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return strings.TrimSpace(sb.String()), nil
}

// searchProviders builds the provider list for this toolset. A workspace that
// has stored a search API key (as an ordinary encrypted secret, injected into
// subprocessEnv alongside the agent's other secrets) gets that provider FIRST
// and skips scraping entirely; otherwise the keyless cascade applies. When
// ddgBaseURL is set (tests only) the cascade collapses to that single endpoint.
func (h *hostToolSet) searchProviders() []websearch.Provider {
	if h.ddgBaseURL != "" {
		return websearch.DefaultProviders(map[string]string{"ddg-html": h.ddgBaseURL})[:1]
	}
	var out []websearch.Provider
	if p := websearch.KeyedProvider("brave", h.subprocessEnv["SEARCH_KEY_BRAVE"], ""); p != nil {
		out = append(out, p)
	}
	if p := websearch.KeyedProvider("tavily", h.subprocessEnv["SEARCH_KEY_TAVILY"], ""); p != nil {
		out = append(out, p)
	}
	return append(out, websearch.DefaultProviders(nil)...)
}

// ── bash ─────────────────────────────────────────────────────────────────────

// runBash runs `bash -c <command>` with the agent's secrets in env (provider key stripped),
// CWD = the agent working directory, sandboxed via Landlock when enabled — the SAME
// confinement, env, and isolated TMPDIR runScript uses (via buildScriptCommand). `bash -c`
// (not a login shell) keeps the environment clean and deterministic. On non-zero exit BOTH
// stdout and stderr are returned so the model can diagnose and self-correct, exactly like
// runScript. NOTE: unlike an authored tools/*.py (AST-scanned by build guardrails), an
// arbitrary bash string is not statically vetted — it is sandboxed identically but unvetted.
func (h *hostToolSet) runBash(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	homeDir := h.userHomeDir()
	tmpDir := filepath.Join(homeDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return "", fmt.Errorf("prepare tmp dir: %w", err)
	}
	env := buildEnvList(h.subprocessEnv, homeDir, tmpDir)
	cmd := h.buildScriptCommand(ctx, []string{"bash", "-c", command}, env, h.workDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("command timed out")
		}
		return "", fmt.Errorf("command failed: %w\nstdout: %s\nstderr: %s", err, truncate(stdout.String()), truncate(stderr.String()))
	}
	out := stdout.String()
	if strings.TrimSpace(out) == "" && stderr.Len() > 0 {
		return noStdoutSentinel + "\n" + truncate(stderr.String()), nil
	}
	return out, nil
}

// spillHeadBytes is how much of an over-cap exec-tool output is shown inline alongside the
// spill-file pointer — enough to see the shape of the data (is it the expected JSON? an error
// trace?) without pulling the whole payload into the model context.
const spillHeadBytes = 2 * 1024

// spillDirName is the dot-prefixed directory (under the agent workDir) where large run_script /
// bash output is persisted so it can reach the filesystem without transiting the model context.
// Dot-prefixed on purpose: ReadToolsTree only walks tools/ and skips dot-dirs, cleanupTestArtifacts
// removes any dot-dir post-save, and vault.List hides dotfiles — so a spill never ships with a
// built agent nor shows in the KB browser.
const spillDirName = ".sa_out"

// spillLargeOutput persists an over-cap exec-tool output (run_script / bash stdout) to a file under
// the agent workDir and returns a compact, STEERING notice: a head of the data plus the file path
// and an explicit instruction to process that file WITH A SCRIPT rather than reading it all back
// inline (otherwise the model just read_file's it and we're back to routing the payload through the
// context). This is the primary fix for "a script's large output has no path to the filesystem
// except through the model." Falls back to plain truncate() when it can't write the file (e.g. no
// workDir, or an IO error) so behavior degrades gracefully rather than losing the output. The
// returned string is non-empty and real, so the build verification bridge (trackScriptProgress)
// still latches producedOutput/lastVerifiedOutput on it.
// clearSpillDir removes the spill directory under the agent workDir. Called at the start of a run
// so spill files from a previous run don't accumulate (the live agent dir is never GC'd the way a
// build dir is). A no-op when workDir is unset or the dir doesn't exist.
func (h *hostToolSet) clearSpillDir() {
	if h.workDir == "" {
		return
	}
	_ = os.RemoveAll(filepath.Join(h.workDir, spillDirName))
}

func (h *hostToolSet) spillLargeOutput(out, toolName string) string {
	if len(out) <= maxToolResult || h.workDir == "" {
		return truncate(out)
	}
	dir := filepath.Join(h.workDir, spillDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return truncate(out)
	}
	name := fmt.Sprintf("%s_%s.txt", toolName, time.Now().Format("20060102_150405.000"))
	abs := filepath.Join(dir, name)
	if err := os.WriteFile(abs, []byte(out), 0o640); err != nil {
		return truncate(out)
	}
	rel := filepath.ToSlash(filepath.Join(spillDirName, name))
	head := out
	if len(head) > spillHeadBytes {
		head = head[:spillHeadBytes]
	}
	return fmt.Sprintf("%s\n…[output is %d bytes — saved in full to %s. Do NOT read it all back into "+
		"context. Write a small Python script that reads that file and does the work directly (e.g. writes "+
		"each item to its destination path), then print only a short summary.]", head, len(out), rel)
}

// readFileSlice returns the model-facing view of a file's contents, honoring optional byte-range
// paging. With offset==0 and limit==0 (what every caller that sends neither produces) it is
// byte-identical to the previous behavior — a full read followed by truncate(). When offset/limit
// are given it returns that byte window and, if more bytes remain past the window, appends an
// explicit next-offset hint so a weak model can deterministically page a large file instead of
// hitting the flat 8 KiB wall. limit==0 means "no explicit limit" (fall through to the cap), never
// "read zero bytes". Out-of-range offsets are clamped rather than erroring.
func readFileSlice(content string, offset, limit int) string {
	if offset <= 0 && limit <= 0 {
		return truncate(content)
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	window := content[offset:]
	// A positive limit caps the window; otherwise the shared per-result cap applies.
	winCap := limit
	if winCap <= 0 || winCap > maxToolResult {
		winCap = maxToolResult
	}
	remainderNote := ""
	if len(window) > winCap {
		next := offset + winCap
		window = window[:winCap]
		remainderNote = fmt.Sprintf("\n…[%d more bytes; call read_file again with offset=%d]", len(content)-next, next)
	}
	if window == "" {
		return fmt.Sprintf("(no bytes at offset %d; file is %d bytes)", offset, len(content))
	}
	return window + remainderNote
}

func truncate(s string) string {
	if len(s) <= maxToolResult {
		return s
	}
	// State the true total and the escape hatch so a weak model doesn't silently reason over
	// incomplete data: it can page the rest (read_file offset/limit) or process it with a
	// script instead of pulling it inline. Kept short (< maxToolResult+512, see the web_fetch
	// truncation test) and marker-free of anything parsed for logic elsewhere.
	return fmt.Sprintf("%s\n…[truncated: showing first %d of %d bytes. To get the rest, request a byte "+
		"range (read_file offset/limit) or process the data with a script instead of reading it inline.]",
		s[:maxToolResult], maxToolResult, len(s))
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
