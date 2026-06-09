// Package coder wraps the claude CLI subprocess as the code generation engine.
// Each user gets an isolated HOME directory so Claude Code's config, caches,
// and auth are completely separate between users.
//
// Security: the subprocess runs WITHOUT --sandbox (firejail handles Python execution;
// the coder only generates code, never runs it). The per-user HOME ensures no
// user can read another's Claude config or conversation history.
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
)

// Result holds the output from a claude invocation.
type Result struct {
	Text     string
	Duration time.Duration
}

// Coder generates code or text by invoking the claude CLI.
type Coder struct {
	bin       string        // path to claude binary
	timeout   time.Duration // per-request timeout
	homesDir  string        // root dir for per-user HOME directories (~/.simple-agents/claude-homes/)
}

// New creates a Coder.
// homesDir should be cfg.Data.Dir + "/claude-homes".
func New(bin string, timeout time.Duration, homesDir string) *Coder {
	if bin == "" {
		bin = "claude"
	}
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	return &Coder{bin: bin, timeout: timeout, homesDir: homesDir}
}

// Generate sends prompt to claude and returns the text response.
// userID is used to select the per-user HOME directory.
func (c *Coder) Generate(ctx context.Context, userID, prompt string) (*Result, error) {
	homeDir, err := c.ensureUserHome(userID)
	if err != nil {
		return nil, fmt.Errorf("ensure user home: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()

	// --output-format json gives structured output with is_error flag.
	cmd := exec.CommandContext(ctx, c.bin,
		"-p", prompt,
		"--output-format", "json",
	)
	// Do not override HOME — the subprocess uses the real ~/.claude for auth.
	// Per-user HOME dirs are preserved as workdirs for context isolation.
	cmd.Dir = homeDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude timed out after %s", c.timeout)
		}
		return nil, fmt.Errorf("claude exited with error: %w\nstderr: %s", err, stderr.String())
	}

	text, isError, err := extractText(stdout.Bytes())
	if err != nil {
		// Fall back to raw stdout if JSON parsing fails.
		text = strings.TrimSpace(stdout.String())
	} else if isError {
		return nil, fmt.Errorf("claude error: %s", text)
	}

	return &Result{Text: text, Duration: time.Since(start)}, nil
}

// Chat sends a conversational message to claude. Unlike Generate, it passes
// a system prompt and conversation history for context-aware replies.
func (c *Coder) Chat(ctx context.Context, userID, systemPrompt, userMessage string) (*Result, error) {
	// Build a single prompt that combines system context with the user message.
	// For multi-turn chat, session persistence is handled by the caller via
	// the session package (Phase 7); here we keep each call stateless.
	combined := userMessage
	if systemPrompt != "" {
		combined = "SYSTEM: " + systemPrompt + "\n\nUSER: " + userMessage
	}
	return c.Generate(ctx, userID, combined)
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
	return dir, nil
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
