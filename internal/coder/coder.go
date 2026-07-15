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
	"syscall"
	"time"

	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/llm"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/vault"
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
type Result struct {
	Text     string
	Duration time.Duration
	Usage    Usage // populated by the API engine; zero for CLI coders

	// ScriptVerified / ScriptOutput carry the API build engine's ground truth: whether an
	// authored helper script actually RAN with real output this build, and that captured
	// stdout (secret-redacted). Set only by the API engine during a build (SA_BUILD_PHASE=
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

	// Self-managed OAuth connectors: when an agent is bound to service connections,
	// the API engine offers each connection's curated actions as native typed tools.
	connReg    *connectors.Registry
	connStore  connectors.TokenStore
	boundConns []connectors.BoundConn
}

// WithConnectors returns a shallow copy of the Coder that offers the given bound
// service connections as native typed tools in the API engine. reg + store are the
// registry and token store; bound is the set of connections the agent may use.
func (c *Coder) WithConnectors(reg *connectors.Registry, store connectors.TokenStore, bound []connectors.BoundConn) *Coder {
	c2 := *c
	c2.connReg, c2.connStore, c2.boundConns = reg, store, bound
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
		timeout = 20 * time.Minute
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
			return nil, fmt.Errorf("coder timed out after %s", c.timeout)
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

// Chat sends a conversational message to the coder with optional history. It is
// used by the text-only design conversations (agent designer, skill designer,
// skill vetter) — never the agentic tool loop, which uses Generate.
func (c *Coder) Chat(ctx context.Context, workspaceID string, history []db.ChatMessage, systemContext, userMessage string) (*Result, error) {
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

	// Own process group + group-wide SIGKILL on cancel so child processes are
	// never orphaned (CommandContext otherwise signals only the direct child).
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

// sandboxReadOnlyPaths returns the read-only roots a confined coder subprocess
// needs: the system locations the CLI runtime requires, the resolved coder
// binary's own install directory (often under the operator's HOME, e.g.
// ~/.local/share/claude/...), and the user's own vault root (so an agent can
// READ its whole knowledge base while only its own agent dir is writable).
func (c *Coder) sandboxReadOnlyPaths(workspaceID string) []string {
	ro := sandbox.SystemReadOnlyPaths()
	ro = append(ro, c.sandboxBinaryDirs()...)
	// The simple-agents binary's own dir must be RO+execute so a confined CLI coder can run
	// `simple-agents connector exec …` (the connector bridge client). Without this the child
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
		return &codexBackend{}
	case "gemini":
		return &geminiBackend{}
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
