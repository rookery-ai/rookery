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

	"github.com/ilijad1/simple-agents/internal/db"
)

// knownAuthEnvVars are env var names that carry LLM provider credentials.
var knownAuthEnvVars = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"GOOGLE_API_KEY",
}

// Result holds the output from a claude invocation.
type Result struct {
	Text     string
	Duration time.Duration
}

// Coder generates code or text by invoking a configured CLI tool.
type Coder struct {
	bin          string
	timeout      time.Duration
	homesDir     string            // root dir for per-user isolated HOME directories
	sysClaudeDir string            // operator's ~/.claude — for credential copying
	extraEnv     map[string]string // additional env vars merged into the subprocess environment
	noTools      bool              // when true, passes --allowedTools "" to disable all tools
	workDir      string            // when non-empty, overrides cmd.Dir (default: per-user home)
	allowedTools string            // when non-empty, passed as --allowedTools <value>
	backendType  string            // '' = auto-detect by binary name, 'claude', or 'generic'
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

// Name returns a short identifier for the underlying CLI binary (e.g. "claude"),
// suitable for user-facing messages. The system supports multiple coder profiles
// with different binaries, so callers should never hardcode a specific name.
func (c *Coder) Name() string {
	return filepath.Base(c.bin)
}

// New creates a Coder.
// homesDir is the root directory for per-user isolated HOME directories
// (typically cfg.Data.Dir + "/claude-homes" for historical reasons).
// dataDir is accepted for API compatibility but is not used by the coder itself
// (it is used by the agent runner's sandbox).
func New(bin string, timeout time.Duration, homesDir, dataDir string) *Coder {
	if bin == "" {
		bin = "claude"
	}
	if timeout == 0 {
		timeout = 20 * time.Minute
	}
	sysHome, _ := os.UserHomeDir()
	return &Coder{
		bin:          bin,
		timeout:      timeout,
		homesDir:     homesDir,
		sysClaudeDir: filepath.Join(sysHome, ".claude"),
	}
}

// Generate sends prompt to the coder binary and returns the text response.
func (c *Coder) Generate(ctx context.Context, userID, prompt string) (*Result, error) {
	backend := c.selectBackend()

	userDir, err := c.ensureUserHome(userID, backend)
	if err != nil {
		return nil, fmt.Errorf("ensure user home: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()

	args := backend.buildArgs(prompt, c.noTools, c.allowedTools)

	// The coder binary is installed in the operator's real home directory.
	// Per-user isolation is handled by the backend via HOME + config-dir overrides.
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = userDir
	if c.workDir != "" {
		cmd.Dir = c.workDir
	}
	cmd.Env = c.buildEnv(userDir, backend)

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

// ErrUsageLimit indicates the coder subprocess failed because the underlying
// account/session hit its usage limit, not because of an agent bug. Callers
// should surface this distinctly (e.g. "retrying next scheduled run") rather
// than treating it as a generic execution failure.
var ErrUsageLimit = errors.New("coder usage limit reached")

// Chat sends a conversational message to claude with optional history.
func (c *Coder) Chat(ctx context.Context, userID string, history []db.ChatMessage, systemContext, userMessage string) (*Result, error) {
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
	return c.Generate(ctx, userID, sb.String())
}

// Ping checks that the claude binary is reachable and returns its version string.
func (c *Coder) Ping(ctx context.Context) (string, error) {
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
func (c *Coder) UserHomeDir(userID string) string {
	return filepath.Join(c.homesDir, safeID(userID))
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// selectBackend returns the CoderBackend for this invocation. An explicit
// backendType field takes precedence; otherwise detection falls back to the
// binary name (any name containing "claude" → claudeBackend).
func (c *Coder) selectBackend() CoderBackend {
	switch c.backendType {
	case "claude":
		return &claudeBackend{sysClaudeDir: c.sysClaudeDir}
	case "generic":
		return &genericCLIBackend{}
	}
	// Auto-detect by binary name.
	if strings.Contains(strings.ToLower(filepath.Base(c.bin)), "claude") {
		return &claudeBackend{sysClaudeDir: c.sysClaudeDir}
	}
	return &genericCLIBackend{}
}

func (c *Coder) ensureUserHome(userID string, backend CoderBackend) (string, error) {
	dir := c.UserHomeDir(userID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := backend.setupHome(dir, c.sysClaudeDir); err != nil {
		return "", err
	}
	return dir, nil
}

// buildEnv constructs the subprocess environment. System overrides (HOME plus
// any backend-specific vars) always take precedence over c.extraEnv.
func (c *Coder) buildEnv(homeDir string, backend CoderBackend) []string {
	overrides := map[string]string{"HOME": homeDir}
	for k, v := range backend.extraEnvForUser(homeDir) {
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
