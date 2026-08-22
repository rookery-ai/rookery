// Package coder wraps any compatible coder CLI as the code generation engine.
// Each user gets an isolated home directory so their sessions are completely
// separate from the server operator's settings and history.
//
// The concrete CLI behaviour (flags, output parsing, credential setup) is
// abstracted behind the CoderBackend interface in backend.go. Two implementations
// are provided: claudeBackend (Claude CLI) and genericCLIBackend (everything else).
package coder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rookery-ai/rookery/internal/connectors"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/mcp"
	"github.com/rookery-ai/rookery/internal/sandbox"
	"github.com/rookery-ai/rookery/internal/vault"
)

// knownAuthEnvVars are env var names that carry LLM provider credentials.
var knownAuthEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"GOOGLE_API_KEY",
}

// Result holds the output from a coder invocation.
// ToolCallStat is one tool call and the size of what it returned. Deliberately
// not the arguments or the payload: this is written to logs and to a run note in
// the user's vault, and a tool result can carry their data.
type ToolCallStat struct {
	Name  string
	Turn  int
	Bytes int
	Error bool // the result began with the engine's "error:" prefix
}

type Result struct {
	Text     string
	Duration time.Duration
	Usage    Usage // populated by the API engine; zero for CLI coders

	// ScriptVerified / ScriptOutput carry the API build engine's ground truth: whether an
	// authored helper script actually RAN with real output this build, and that captured
	// stdout (secret-redacted). Set only by the API engine during a build (ROOKERY_BUILD_PHASE=
	// generation); zero for CLI coders and for runs/chat. The agent designer uses these so a
	// build the engine confirmed runs isn't falsely flagged "couldn't confirm the helper".
	ScriptVerified bool
	ScriptOutput   string

	// ScriptRan reports whether an authored helper script was executed at least once during a
	// build (regardless of output). Observability only: it discriminates "ran but produced
	// nothing" from "never ran" behind a "couldn't confirm" outcome. Zero for CLI/runs/chat.
	ScriptRan bool

	// UsedConnectionIDs lists the service-connection IDs whose connector tools the API engine
	// actually invoked this build (deduped). The agent designer auto-binds these when the model
	// omitted the `# Connections:` header. Set only by the API engine; zero for CLI/runs’ callers
	// that don’t consume it.
	UsedConnectionIDs []string

	// UsedMCPServerIDs lists the MCP server IDs whose tools the API engine invoked during
	// this call, the sibling of UsedConnectionIDs and consumed by the same auto-bind path.
	UsedMCPServerIDs []string

	// OfferedTools names the tools this run offered the model. Empty for a CLI coder.
	// The runner uses it to recognise the model's own tool-call machinery leaking into
	// a message, without needing to know any provider's markup dialect.
	OfferedTools []string

	// ToolTrace records what the model actually DID: one entry per tool call, in
	// order, with the size of the result fed back.
	//
	// It exists because three separate diagnoses of one failing agent were made
	// by INFERRING the tool calls from token counts, and all three were wrong.
	// A run that produces nothing records its outcome and its cost, and until
	// now nothing about the path it took — which is the only part that explains
	// either. Compact by construction: names and byte counts, never payloads,
	// so it is safe to log and cheap to keep.
	ToolTrace []ToolCallStat

	// StopReason is why the tool loop ended: "" for a normal finish, otherwise
	// "budget", "unproductive" or "hard-ceiling". Non-empty means the run was cut
	// short, which the agent designer uses to caveat a build rather than presenting
	// it as complete. It does NOT depend on the model remembering to emit a marker —
	// that dependency is what let a truncated build read as a finished one.
	StopReason string
}

// Usage is a best-effort token accounting for API coders (zero for CLI coders).
// Aliased to llm.Usage so the API engine's provider-reported usage needs no
// conversion shim between the two packages.
type Usage = llm.Usage

// ErrAPIAuth indicates the API coder could not authenticate (bad/missing API key).
// This is a configuration error, not a transient failure — distinct from ErrUsageLimit.
var ErrAPIAuth = errors.New("coder api auth failed")

// ErrMaxTurns indicates the API coder's tool-calling loop exceeded its turn budget
// without producing a final answer. Surfaced as a normal run error (not ErrUsageLimit).
var ErrMaxTurns = errors.New("coder exceeded max tool-calling turns")

// Coder generates code or text by invoking a configured CLI tool.
type Coder struct {
	bin          string
	timeout      time.Duration
	homesDir     string            // root dir for per-user isolated HOME directories
	dataDir      string            // root data dir; used to derive the per-user vault root for sandbox RO access
	sysClaudeDir string            // operator's ~/.claude — for credential copying
	selfExe      string            // path to this binary, for re-exec into the sandbox helper
	sandbox      bool              // when true, confine subprocesses via Landlock (Linux only)
	extraEnv     map[string]string // additional env vars merged into the subprocess environment
	noTools      bool              // when true, passes --allowedTools "" to disable all tools
	workDir      string            // when non-empty, overrides cmd.Dir (default: per-user home)
	agentName    string            // name for the API engine's state.md tools (cosmetic; see WithAgentName)
	allowedTools string            // when non-empty, passed as --allowedTools <value>
	backendType  string            // '' = auto-detect by binary name, 'claude', 'generic', or 'api"
	cliModel     string            // provider/model for CLI coders that accept -m/--model (opencode, cursor)

	// ── API coder (coder_kind == "api") ──────────────────────────────────────
	// When api is non-nil, Generate/Ping dispatch to the in-process tool-calling
	// engine (api_engine.go) instead of spawning a CLI subprocess. The CLI path
	// stays byte-identical when api is nil.
	api           *apiConfig
	secretsLookup SecretsLookup // resolves the provider API key by secret name at run time
	vlt           *vault.Vault  // vault for host-tool file operations (read/write/edit/list/run_script)
	progress      func(string)  // optional live-progress sink (per tool-call milestone) for the API engine
	buildSpec     BuildSpec     // what a BUILD must produce; zero value = AgentBuildSpec

	// Self-managed OAuth connectors: when an agent is bound to service connections,
	// the API engine offers each connection's curated actions as native typed tools.
	mcpCaller mcp.Caller
	mcpParker mcp.Parker
	boundMCP  []mcp.BoundServer

	connReg    *connectors.Registry
	connStore  connectors.TokenStore
	boundConns []connectors.BoundConn
	// connParker gates public_write connector actions; nil means no gate.
	connParker connectors.Parker

	// disabled, when non-nil, is returned by every entry point instead of
	// running anything. Set when the build cannot honour the workspace's coder
	// kind — see ForWorkspace and ErrLocalCoderDisabled.
	disabled error
}

// withDisabled returns a shallow copy whose entry points all fail with err.
// Unexported: only ForWorkspace decides a coder is unusable.
func (c *Coder) withDisabled(err error) *Coder {
	c2 := *c
	c2.disabled = err
	return &c2
}

// WithConnectors returns a shallow copy of the Coder that offers the given bound
// service connections as native typed tools in the API engine. reg + store are the
// registry and token store; bound is the set of connections the agent may use.
func (c *Coder) WithConnectors(reg *connectors.Registry, store connectors.TokenStore, bound []connectors.BoundConn) *Coder {
	c2 := *c
	c2.connReg, c2.connStore, c2.boundConns = reg, store, bound
	return &c2
}

// WithMCP returns a shallow copy of the Coder that offers the given bound MCP
// servers' tools as native typed tools in the API engine.
//
// caller performs the actual tools/call (an *mcp.Client in production, a fake in
// tests). Passing no servers leaves the coder exactly as it was, so a workspace with
// no MCP servers pays nothing.
func (c *Coder) WithMCP(caller mcp.Caller, bound []mcp.BoundServer) *Coder {
	c2 := *c
	c2.mcpCaller, c2.boundMCP = caller, bound
	return &c2
}

// WithMCPParker attaches the approval gate for MCP tools marked for approval. Nil
// (the default) means no gate.
//
// It is separate from WithParker because the two layers have different Parker
// interfaces — a connector call is identified by (connection, action) and an MCP call
// by (server, tool) — but the semantics are identical, and both must be wired for the
// same agent or changing which layer a capability comes from would change whether the
// owner's approval requirement applies.
func (c *Coder) WithMCPParker(p mcp.Parker) *Coder {
	c2 := *c
	c2.mcpParker = p
	return &c2
}

// WithParker attaches the approval gate for public_write connector actions. Nil (the
// default) means no gate, which is what chat, builds, and any agent with no gated
// binding all want — see approval.Service.ParkerFor.
func (c *Coder) WithParker(p connectors.Parker) *Coder {
	c2 := *c
	c2.connParker = p
	return &c2
}

// WithExtraEnv returns a shallow copy of the Coder with additional environment variables
// injected into every subprocess invocation. Existing system overrides (HOME, CLAUDE_CONFIG_DIR)
// take precedence over any keys in env.
func (c *Coder) WithExtraEnv(env map[string]string) *Coder {
	c2 := *c
	c2.extraEnv = env
	return &c2
}

// WithNoTools returns a shallow copy of the Coder with all tools disabled.
// Use this for design/generation calls where output must be plain text only.
func (c *Coder) WithNoTools() *Coder {
	c2 := *c
	c2.noTools = true
	return &c2
}

// WithDir returns a shallow copy of the Coder that runs the subprocess with dir
// as the working directory instead of the per-user home. HOME and any backend
// config-dir overrides still point to the user's isolated home.
func (c *Coder) WithDir(dir string) *Coder {
	c2 := *c
	c2.workDir = dir
	return &c2
}

// WithAgentName returns a shallow copy of the Coder that names the agent for the API
// engine's get_state/set_state tools (statetools.go). Only cosmetic: it labels a
// freshly-created state.md's heading (agentstate.RenderTemplate) — a run or build
// always has an existing state.md written with the real name at build/edit time, so
// an unset name never surfaces as wrong data.
func (c *Coder) WithAgentName(name string) *Coder {
	c2 := *c
	c2.agentName = name
	return &c2
}

// WithAllowedTools returns a shallow copy of the Coder that pre-approves specific
// tools so the subprocess doesn't block on permission prompts. The format is
// backend-specific; for the Claude backend use comma-separated tool names.
// Example: c.WithAllowedTools("Bash,Write,Edit,Read")
func (c *Coder) WithAllowedTools(tools string) *Coder {
	c2 := *c
	c2.allowedTools = tools
	return &c2
}

// WithBackendType returns a shallow copy of the Coder with the backend explicitly
// set, overriding name-based auto-detection. Valid values: "claude", "generic", "".
func (c *Coder) WithBackendType(t string) *Coder {
	c2 := *c
	c2.backendType = t
	return &c2
}

// BackendType returns the configured backend type ("claude", "generic", or "" for
// auto-detect). Callers map this to a prompts-level backend capability so the agent
// prompts can describe how the coder can act on files (full coder vs basic model).
func (c *Coder) BackendType() string {
	return c.backendType
}

// apiModelForCLI returns the workspace-configured model for CLI coders that
// accept one (opencode -m, cursor --model). Empty means "use the coder's default".
func (c *Coder) apiModelForCLI() string { return c.cliModel }

// WithSandbox returns a shallow copy of the Coder with Landlock confinement
// toggled. When enabled (and supported by the kernel), every subprocess is
// re-executed through the sandbox helper so it can only read/write the calling
// user's own files. When disabled or unsupported, the subprocess runs directly
// and the detective vault.Guard remains the boundary.
func (c *Coder) WithSandbox(enabled bool) *Coder {
	c2 := *c
	c2.sandbox = enabled
	return &c2
}

// WithBuildSpec returns a shallow copy of the Coder whose API-engine build gates check
// the given deliverable and script shape. Unset means the agent build (see BuildSpec).
func (c *Coder) WithBuildSpec(spec BuildSpec) *Coder {
	c2 := *c
	c2.buildSpec = spec
	return &c2
}

// Name returns a short identifier for the underlying CLI binary (e.g. "claude"),
// suitable for user-facing messages. The system supports multiple coder profiles
// with different binaries, so callers should never hardcode a specific name.
func (c *Coder) Name() string {
	if c.api != nil {
		model := c.api.model
		if model == "" {
			model = "?"
		}
		return c.api.provider + "/" + model
	}
	return filepath.Base(c.bin)
}

// DefaultTimeout is the fallback when neither the workspace nor the config
// names one. It matches config.defaults() deliberately — two different
// fallbacks would mean the effective timeout depended on which construction
// path a caller happened to take.
const DefaultTimeout = 30 * time.Minute

// RetryTimeoutBelow is the ceiling under which a timed-out agent build is
// retried once automatically.
//
// The point is not that retrying helps in general — at the 30-minute default a
// timeout means something is genuinely wrong, and spending another 30 minutes
// on it wastes the coder and delays the report. It is that a workspace can
// still be carrying a small timeout, and at two minutes a build is cut off
// mid-repair often enough that one retry converts a routine failure into a
// success. So the retry is scoped to exactly the installs that need it and
// costs nothing on the ones that do not.
const RetryTimeoutBelow = 10 * time.Minute

// ErrTimeout reports that the coder exceeded its deadline. Callers used to
// detect this by matching "timed out" in the error text; the sentinel makes the
// check exact, and the wrapped message is byte-identical so the older string
// check keeps working wherever it survives.
var ErrTimeout = errors.New("coder timed out")

// Timeout returns the deadline this coder applies to one call. Exported for the
// retry decision in the agent designer, which is scoped by how small the
// configured timeout is.
func (c *Coder) Timeout() time.Duration { return c.timeout }

// New creates a Coder.
// homesDir is the root directory for per-user isolated HOME directories
// (typically cfg.Data.Dir + "/claude-homes" for historical reasons).
// dataDir is the root data directory; it is used to derive the per-user vault
// root that the sandbox grants read-only when confinement is enabled.
func New(bin string, timeout time.Duration, homesDir, dataDir string) *Coder {
	if bin == "" {
		bin = "claude"
	}
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	sysHome, _ := os.UserHomeDir()
	selfExe, _ := os.Executable()
	return &Coder{
		bin:          bin,
		timeout:      timeout,
		homesDir:     homesDir,
		dataDir:      dataDir,
		sysClaudeDir: filepath.Join(sysHome, ".claude"),
		selfExe:      selfExe,
	}
}

// Generate sends prompt to the coder binary and returns the text response.
func (c *Coder) Generate(ctx context.Context, workspaceID, prompt string) (*Result, error) {
	if c.disabled != nil {
		return nil, c.disabled
	}
	// API coder: run the in-process tool-calling loop instead of spawning a CLI.
	if c.api != nil {
		return c.runAPI(ctx, workspaceID, prompt)
	}

	backend := c.selectBackend()

	userDir, err := c.ensureUserHome(workspaceID, backend)
	if err != nil {
		return nil, fmt.Errorf("ensure user home: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()

	args := backend.buildArgs(prompt, c.noTools, c.allowedTools)
	env := c.buildEnv(userDir, backend)
	runDir := userDir
	if c.workDir != "" {
		runDir = c.workDir
	}

	// The coder binary is installed in the operator's real home directory.
	// Per-user isolation is handled by the backend via HOME + config-dir overrides,
	// and (when enabled) hardened by Landlock filesystem confinement.
	cmd := c.buildCommand(ctx, workspaceID, args, env, runDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%w after %s", ErrTimeout, c.timeout)
		}
		if backend.looksLikeLimit(stdout.String(), stderr.String()) {
			return nil, ErrUsageLimit
		}
		return nil, fmt.Errorf("coder exited with error: %w\nstdout: %.500s\nstderr: %.500s", err, stdout.String(), stderr.String())
	}
	stdoutBytes := stdout.Bytes()

	text, isError, err := backend.parseOutput(stdoutBytes)
	if err != nil {
		text = strings.TrimSpace(string(stdoutBytes))
	} else if isError {
		if backend.looksLikeLimit(text, "") {
			return nil, ErrUsageLimit
		}
		return nil, fmt.Errorf("coder error: %s", text)
	}

	return &Result{Text: text, Duration: time.Since(start)}, nil
}

// ErrUsageLimit indicates the coder failed because the underlying account ran
// out of credits/quota (e.g. a 402 payment-required), not because of an agent
// bug. Callers should surface this distinctly (e.g. "retrying next scheduled
// run") rather than treating it as a generic execution failure.
var ErrUsageLimit = errors.New("coder usage limit reached")

// ErrRateLimited indicates the API coder was throttled by the provider with a
// transient 429 (RPM/TPM window) that did not clear within the retry budget. This
// is NOT quota exhaustion — the run would likely succeed if retried shortly
// after. Distinct from ErrUsageLimit so the user-facing message can say "try
// again in a moment" instead of "you're out of quota".
var ErrRateLimited = errors.New("coder rate-limited by provider")

// ErrProviderEmpty indicates the provider answered 2xx with no body at all, on
// every attempt of the retry budget. An outage at the provider, not a property
// of the request: the run reached no model, so it has no tokens, no tool calls
// and no partial work to salvage.
//
// Distinct from ErrRateLimited because the remedy differs in kind — throttling
// clears on a timer and this may not — and distinct from a generic failure
// because it is the one transient case that used to reach the user as a raw
// internal string after burning the full retry budget.
var ErrProviderEmpty = errors.New("coder got an empty response from the provider")

// Chat sends a conversational message to the coder with optional history. It is
// used by the text-only design conversations (agent designer, skill designer,
// skill vetter) — never the agentic tool loop, which uses Generate.
func (c *Coder) Chat(ctx context.Context, workspaceID string, history []db.ChatMessage, systemContext, userMessage string) (*Result, error) {
	if c.disabled != nil {
		return nil, c.disabled
	}
	// API coders need real alternating user/assistant message turns. Flattening
	// the history into the system prompt (as the CLI path below does) makes the
	// model treat each turn as a fresh single-turn request and re-ask the
	// opening question every time — the design-conversation loop.
	//
	// Two API-coder chat flavours, split by noTools:
	//   - WithNoTools (design conversations: agent/skill designer, skill vetter):
	//     text-only single completion, no tools offered → chatAPI.
	//   - without WithNoTools (one-off chat): the chat can retrieve and edit the
	//     user's knowledge base on demand, so it runs the tool-calling loop with
	//     the host file tools → chatToolsAPI.
	if c.api != nil {
		if c.noTools {
			return c.chatAPI(ctx, workspaceID, history, systemContext, userMessage)
		}
		return c.chatToolsAPI(ctx, workspaceID, history, systemContext, userMessage)
	}
	var sb strings.Builder
	if systemContext != "" {
		sb.WriteString("[Persistent user context]\n")
		sb.WriteString(systemContext)
		sb.WriteByte('\n')
	}
	if len(history) > 0 {
		sb.WriteString("[Previous conversation]\n")
		for _, m := range history {
			switch m.Role {
			case "user":
				sb.WriteString("Human: ")
			case "assistant":
				sb.WriteString("Assistant: ")
			}
			sb.WriteString(m.Content)
			sb.WriteByte('\n')
		}
		sb.WriteString("\nCurrent message: ")
		sb.WriteString(userMessage)
	} else {
		sb.WriteString(userMessage)
	}
	return c.Generate(ctx, workspaceID, sb.String())
}

// Ping checks that the coder is reachable and returns a short identifying
// string. For a CLI coder this runs `<bin> --version`; workspaceID is unused.
// For an API coder, workspaceID is required — it's used to resolve the
// provider API key via the secrets lookup, exactly as Generate does.
func (c *Coder) Ping(ctx context.Context, workspaceID string) (string, error) {
	if c.disabled != nil {
		return "", c.disabled
	}
	if c.api != nil {
		return c.pingAPI(ctx, workspaceID)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, "--version")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude not found at %q: %w", c.bin, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Smoke runs a trivial prompt through the full isolated pipeline (seed → env →
// invoke → parse) and validates a sane structured reply. It is the fail-loud
// gate for the coder-settings UI: a wrong CLI convention or bad/expired operator
// auth returns a descriptive error instead of silently feeding garbage into a
// run. For API coders it delegates to Ping (which resolves the provider key).
func (c *Coder) Smoke(ctx context.Context, workspaceID string) (string, error) {
	if c.disabled != nil {
		return "", c.disabled
	}
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

// UserHomeDir returns the per-user HOME path without creating it.
func (c *Coder) UserHomeDir(workspaceID string) string {
	return filepath.Join(c.homesDir, safeID(workspaceID))
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// buildCommand constructs the subprocess to run. When sandboxing is enabled and
// the kernel supports Landlock, the real command is wrapped in the in-binary
// sandbox helper so it (and its children) can only touch the calling user's own
// files. Otherwise it runs directly and the detective vault.Guard is the boundary.
//
// In both cases the child is placed in its own process group and killed
// group-wide on ctx cancel/timeout, so the agent's descendants (bash, python, …)
// are not orphaned when a run is aborted.
func (c *Coder) buildCommand(ctx context.Context, workspaceID string, args, env []string, runDir string) *exec.Cmd {
	var cmd *exec.Cmd

	if c.sandbox && c.selfExe != "" && sandbox.Supported() {
		rw := []string{c.UserHomeDir(workspaceID), runDir}
		// The user's whole vault is read+write for agent runs and chat: agents
		// record/persist knowledge into the KB (notes, memory, user files), and
		// the chat edits notes on demand. Landlock is additive, so a path present
		// in both ReadWritePaths and ReadOnlyPaths (see sandboxReadOnlyPaths) is
		// net read+write. Still confined to this user's vault + HOME — the DB,
		// config, and other users' vaults stay out of reach.
		if c.dataDir != "" {
			rw = append(rw, filepath.Join(c.dataDir, "vaults", workspaceID))
		}
		spec := sandbox.Spec{
			Command:        append([]string{c.bin}, args...),
			Dir:            runDir,
			Env:            env,
			ReadWritePaths: dedupePaths(rw...),
			ReadOnlyPaths:  c.sandboxReadOnlyPaths(workspaceID),
			ReadWriteFiles: sandbox.SystemReadWriteFiles(),
			NoFile:         8192,
		}
		if wargv, err := sandbox.Wrap(c.selfExe, spec); err == nil {
			cmd = exec.CommandContext(ctx, wargv[0], wargv[1:]...)
		}
	}
	if cmd == nil {
		cmd = exec.CommandContext(ctx, c.bin, args...)
	}
	cmd.Dir = runDir
	cmd.Env = env

	// Own process group + tree-wide kill on cancel so child processes are never
	// orphaned (CommandContext otherwise signals only the direct child).
	setProcGroup(cmd)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// sandboxReadOnlyPaths returns the read-only roots a confined coder subprocess
// needs: the system locations the CLI runtime requires, the resolved coder
// binary's own install directory (often under the operator's HOME, e.g.
// ~/.local/share/claude/...), and the user's own vault root (so an agent can
// READ its whole knowledge base while only its own agent dir is writable).
func (c *Coder) sandboxReadOnlyPaths(workspaceID string) []string {
	ro := sandbox.SystemReadOnlyPaths()
	ro = append(ro, c.sandboxBinaryDirs()...)
	// The rookery binary's own dir must be RO+execute so a confined CLI coder can run
	// `rookery connector exec …` (the connector bridge client). Without this the child
	// gets EACCES on exec even though the loopback TCP call itself is allowed.
	if c.selfExe != "" {
		ro = append(ro, filepath.Dir(c.selfExe))
		if real, err := filepath.EvalSymlinks(c.selfExe); err == nil {
			ro = append(ro, filepath.Dir(real))
		}
	}
	if c.dataDir != "" {
		ro = append(ro, filepath.Join(c.dataDir, "vaults", workspaceID))
	}
	return ro
}

// sandboxBinaryDirs resolves the coder binary (following symlinks) and returns
// the directories that must be readable+executable for it to run. The launcher
// is frequently a symlink into a versioned install dir; both are granted.
func (c *Coder) sandboxBinaryDirs() []string {
	resolved, err := exec.LookPath(c.bin)
	if err != nil {
		return nil
	}
	out := []string{filepath.Dir(resolved)}
	if real, err := filepath.EvalSymlinks(resolved); err == nil && real != resolved {
		if fi, statErr := os.Stat(real); statErr == nil && fi.IsDir() {
			out = append(out, real)
		} else {
			out = append(out, filepath.Dir(real))
		}
	}
	return out
}

// dedupePaths returns the non-empty inputs with duplicates removed, preserving order.
func dedupePaths(paths ...string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// selectBackend returns the CoderBackend for this invocation. An explicit
// backendType field takes precedence; otherwise detection falls back to the
// binary name (any name containing "claude" → claudeBackend).
func (c *Coder) selectBackend() CoderBackend {
	switch c.backendType {
	case "claude":
		return &claudeBackend{sysClaudeDir: c.sysClaudeDir}
	case "generic":
		return &genericCLIBackend{}
	case "opencode":
		return &opencodeBackend{model: c.apiModelForCLI()}
	case "codex":
		return &codexBackend{model: c.apiModelForCLI()}
	case "gemini":
		return &geminiBackend{model: c.apiModelForCLI()}
	case "cursor":
		return &cursorBackend{model: c.cliModel}
	}
	// Auto-detect by binary name.
	if strings.Contains(strings.ToLower(filepath.Base(c.bin)), "claude") {
		return &claudeBackend{sysClaudeDir: c.sysClaudeDir}
	}
	return &genericCLIBackend{}
}

func (c *Coder) ensureUserHome(workspaceID string, backend CoderBackend) (string, error) {
	dir := c.UserHomeDir(workspaceID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Per-user private temp dir (under the RW-granted HOME) so tmp files don't
	// need — and don't leak through — the shared, world-writable /tmp, which the
	// sandbox deliberately does not grant. TMPDIR below points subprocesses here.
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

// buildEnv constructs the subprocess environment. System overrides (HOME plus
// any backend-specific vars) always take precedence over c.extraEnv.
func (c *Coder) buildEnv(homeDir string, backend CoderBackend) []string {
	tmpDir := filepath.Join(homeDir, "tmp")
	overrides := map[string]string{
		"HOME":   homeDir,
		"TMPDIR": tmpDir,
		"TMP":    tmpDir,
		"TEMP":   tmpDir,
	}
	for k, v := range backend.configEnv(homeDir) {
		overrides[k] = v
	}
	// Extra env (e.g. decrypted secrets) may not override system keys.
	for k, v := range c.extraEnv {
		if _, exists := overrides[k]; !exists {
			overrides[k] = v
		}
	}
	return overrideEnv(os.Environ(), overrides)
}

func overrideEnv(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]bool, len(overrides))
	for _, kv := range base {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			result = append(result, kv)
			continue
		}
		k := kv[:idx]
		if v, ok := overrides[k]; ok {
			result = append(result, k+"="+v)
			seen[k] = true
		} else {
			result = append(result, kv)
		}
	}
	for k, v := range overrides {
		if !seen[k] {
			result = append(result, k+"="+v)
		}
	}
	return result
}

func safeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
