// Package coder wraps the claude CLI subprocess as the code generation engine.
// Each user gets an isolated config directory so their sessions are completely
// separate from the server operator's settings, CLAUDE.md files, and history.
//
// Security: the subprocess runs WITHOUT --sandbox (firejail handles Python execution;
// the coder only generates code, never runs it). Per-user isolation is enforced by
// CLAUDE_CONFIG_DIR and --setting-sources "".
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
// These are always forwarded to non-claude subprocesses so future tool types
// (opencode, cursor, etc.) can authenticate without a credentials file.
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
	bin          string        // path to the coder binary (claude, opencode, …)
	timeout      time.Duration // per-request timeout
	homesDir     string        // root dir for per-user isolated HOME directories
	sysClaudeDir string        // path to the operator's ~/.claude — for credential copying
}

// New creates a Coder.
// homesDir should be cfg.Data.Dir + "/claude-homes".
func New(bin string, timeout time.Duration, homesDir string) *Coder {
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
// userID is used to select the per-user isolated home directory.
func (c *Coder) Generate(ctx context.Context, userID, prompt string) (*Result, error) {
	homeDir, err := c.ensureUserHome(userID)
	if err != nil {
		return nil, fmt.Errorf("ensure user home: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()

	args := []string{"-p", prompt, "--output-format", "json"}
	if c.isClaude() {
		// Suppress all settings files and CLAUDE.md directory traversal so the
		// operator's global config does not bleed into user sessions.
		args = append(args, "--setting-sources", "")
	}

	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = homeDir
	cmd.Env = c.buildEnv(homeDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("coder timed out after %s", c.timeout)
		}
		return nil, fmt.Errorf("coder exited with error: %w\nstderr: %s", err, stderr.String())
	}

	text, isError, err := extractText(stdout.Bytes())
	if err != nil {
		// Fall back to raw stdout if JSON parsing fails.
		text = strings.TrimSpace(stdout.String())
	} else if isError {
		return nil, fmt.Errorf("coder error: %s", text)
	}

	return &Result{Text: text, Duration: time.Since(start)}, nil
}

// Chat sends a conversational message to claude with optional history for multi-turn sessions.
// systemContext is prepended before history (use it for persistent user memory / facts).
// When history is non-empty, prior turns are formatted into the prompt so the model has context.
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
		// Copy credentials on every call so refreshed OAuth tokens are available.
		// The file is small (< 1 KB) and copying is cheaper than stale auth errors.
		if c.sysClaudeDir != "" {
			src := filepath.Join(c.sysClaudeDir, ".credentials.json")
			if data, err := os.ReadFile(src); err == nil {
				_ = os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), data, 0o600)
			}
		}
	}

	return dir, nil
}

// isClaude returns true when the configured binary is the claude CLI.
// This gates claude-specific isolation flags and credential handling.
func (c *Coder) isClaude() bool {
	return strings.Contains(strings.ToLower(filepath.Base(c.bin)), "claude")
}

// buildEnv constructs the subprocess environment with per-user isolation applied.
// For claude: redirects CLAUDE_CONFIG_DIR and HOME to the per-user home.
// For other tools: passes through known auth env vars; HOME is still overridden.
func (c *Coder) buildEnv(homeDir string) []string {
	overrides := map[string]string{
		"HOME": homeDir,
	}
	if c.isClaude() {
		overrides["CLAUDE_CONFIG_DIR"] = filepath.Join(homeDir, ".claude")
	} else {
		// For non-claude tools, preserve any API key env vars already set in the
		// parent process so the tool can authenticate to its LLM provider.
		for _, key := range knownAuthEnvVars {
			if val := os.Getenv(key); val != "" {
				overrides[key] = val
			}
		}
	}
	return overrideEnv(os.Environ(), overrides)
}

// overrideEnv returns base with the given key=value pairs replaced or appended.
// All keys in base that are not in overrides are preserved unchanged.
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

// claudeJSONResponse is the JSON envelope returned by `claude --output-format json`.
type claudeJSONResponse struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
}

// extractText parses claude's JSON output and returns (text, isError, err).
// isError is true when the JSON contains is_error:true (e.g. auth failure).
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

// safeID strips characters that are unsafe in directory names.
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
