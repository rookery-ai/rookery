// Package coder wraps the claude CLI subprocess as the code generation engine.
// Each user gets an isolated config directory so their sessions are completely
// separate from the server operator's settings, CLAUDE.md files, and history.
//
// When Firejail is available, every invocation runs inside a per-user sandbox:
// the user's persistent home dir is bind-mounted as the sandbox home, the rest
// of the data directory is blacklisted, and no other user's files are visible.
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
// as the working directory instead of the per-user home. HOME and CLAUDE_CONFIG_DIR
// still point to the user's isolated home so credentials are accessible.
func (c *Coder) WithDir(dir string) *Coder {
	c2 := *c
	c2.workDir = dir
	return &c2
}

// WithAllowedTools returns a shallow copy of the Coder that passes
// --allowedTools <tools> to the claude CLI. Use this when running with full
// tools (not WithNoTools) so the subprocess doesn't block on permission prompts.
// Example: c.WithAllowedTools("Bash,Write,Edit,Read")
func (c *Coder) WithAllowedTools(tools string) *Coder {
	c2 := *c
	c2.allowedTools = tools
	return &c2
}

// New creates a Coder.
// homesDir should be cfg.Data.Dir + "/claude-homes".
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
	userDir, err := c.ensureUserHome(userID)
	if err != nil {
		return nil, fmt.Errorf("ensure user home: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()

	args := []string{"-p", prompt, "--output-format", "json"}
	if c.isClaude() {
		args = append(args, "--setting-sources", "")
		switch {
		case c.noTools:
			args = append(args, "--allowedTools", "")
		case c.allowedTools != "":
			args = append(args, "--allowedTools", c.allowedTools)
		}
	}

	// The coder binary (claude CLI) is installed in the operator's real home
	// (e.g. ~/.local/share/claude/). Firejail's --private would replace that
	// home and break the binary's own installation. Per-user isolation for the
	// coder is handled via CLAUDE_CONFIG_DIR, HOME override, and --setting-sources "".
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = userDir
	if c.workDir != "" {
		cmd.Dir = c.workDir
	}
	cmd.Env = c.buildEnv(userDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("coder timed out after %s", c.timeout)
		}
		return nil, fmt.Errorf("coder exited with error: %w\nstderr: %s", err, stderr.String())
	}
	stdoutBytes := stdout.Bytes()

	text, isError, err := extractText(stdoutBytes)
	if err != nil {
		text = strings.TrimSpace(string(stdoutBytes))
	} else if isError {
		return nil, fmt.Errorf("coder error: %s", text)
	}

	return &Result{Text: text, Duration: time.Since(start)}, nil
}

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

func (c *Coder) ensureUserHome(userID string) (string, error) {
	dir := c.UserHomeDir(userID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	if c.isClaude() {
		claudeDir := filepath.Join(dir, ".claude")
		if err := os.MkdirAll(claudeDir, 0o700); err != nil {
			return "", err
		}
		if c.sysClaudeDir != "" {
			src := filepath.Join(c.sysClaudeDir, ".credentials.json")
			if data, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), data, 0o600)
			}
		}
	}

	return dir, nil
}

func (c *Coder) isClaude() bool {
	return strings.Contains(strings.ToLower(filepath.Base(c.bin)), "claude")
}

// buildEnv constructs the subprocess environment for non-sandboxed execution.
func (c *Coder) buildEnv(homeDir string) []string {
	overrides := map[string]string{
		"HOME": homeDir,
	}
	if c.isClaude() {
		overrides["CLAUDE_CONFIG_DIR"] = filepath.Join(homeDir, ".claude")
	} else {
		for _, key := range knownAuthEnvVars {
			if val := os.Getenv(key); val != "" {
				overrides[key] = val
			}
		}
	}
	// Merge extra env vars, but never override system keys (HOME, CLAUDE_CONFIG_DIR, etc.).
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

type claudeJSONResponse struct {
	Type     string `json:"type"`
	Subtype  string `json:"subtype"`
	IsError  bool   `json:"is_error"`
	Result   string `json:"result"`
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
}

func extractText(data []byte) (text string, isError bool, err error) {
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))

	var lastText string
	var lastIsError bool
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var resp claudeJSONResponse
		if json.Unmarshal(line, &resp) != nil {
			continue
		}
		if resp.Result != "" {
			lastText = resp.Result
			lastIsError = resp.IsError
		}
		for _, msg := range resp.Messages {
			if msg.Role == "assistant" {
				for _, part := range msg.Content {
					if part.Type == "text" && part.Text != "" {
						lastText = part.Text
						lastIsError = false
					}
				}
			}
		}
	}

	if lastText == "" {
		return "", false, fmt.Errorf("no assistant text found in response")
	}
	return strings.TrimSpace(lastText), lastIsError, nil
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
