package coder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CoderBackend abstracts the coder-specific behaviour so that the Coder struct
// can drive any compatible CLI tool — not just the Claude CLI.
//
// Implementations must be stateless (no mutable fields) so that selectBackend()
// can construct them on every Generate() call without races.
type CoderBackend interface {
	// buildArgs returns the CLI flags to pass to the binary for this invocation.
	buildArgs(prompt string, noTools bool, allowedTools string) []string

	// parseOutput extracts the assistant's plain-text response from raw subprocess stdout.
	// isError is true when the coder itself reported a non-fatal error in its output.
	parseOutput(stdout []byte) (text string, isError bool, err error)

	// setupHome performs any one-time per-user setup inside homeDir
	// (e.g. creating config directories, copying credentials).
	setupHome(homeDir, sysConfigDir string) error

	// extraEnvForUser returns env vars that this backend needs injected for
	// per-user isolation (e.g. CLAUDE_CONFIG_DIR for the Claude backend).
	extraEnvForUser(homeDir string) map[string]string

	// looksLikeLimit reports whether the subprocess failure looks like a
	// usage/rate-limit hit rather than a genuine agent or system error.
	looksLikeLimit(stdout, stderr string) bool
}

// ─── Claude backend ───────────────────────────────────────────────────────────

// claudeBackend drives the Claude CLI (claude or any binary whose name contains
// "claude"). It uses JSON output format, per-user CLAUDE_CONFIG_DIR isolation,
// and the --allowedTools / --setting-sources flags.
type claudeBackend struct {
	sysClaudeDir string // operator's ~/.claude — for credential copying
}

func (b *claudeBackend) buildArgs(prompt string, noTools bool, allowedTools string) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--setting-sources", ""}
	switch {
	case noTools:
		args = append(args, "--allowedTools", "")
	case allowedTools != "":
		args = append(args, "--allowedTools", allowedTools)
	}
	return args
}

func (b *claudeBackend) parseOutput(stdout []byte) (string, bool, error) {
	return extractClaudeJSON(stdout)
}

func (b *claudeBackend) setupHome(homeDir, _ string) error {
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		return err
	}
	if b.sysClaudeDir != "" {
		src := filepath.Join(b.sysClaudeDir, ".credentials.json")
		if data, err := os.ReadFile(src); err == nil {
			_ = os.WriteFile(filepath.Join(claudeDir, ".credentials.json"), data, 0o600)
		}
	}
	return nil
}

func (b *claudeBackend) extraEnvForUser(homeDir string) map[string]string {
	return map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(homeDir, ".claude"),
	}
}

func (b *claudeBackend) looksLikeLimit(stdout, stderr string) bool {
	// Claude CLI's signature for hitting the usage limit: non-zero exit with
	// completely empty stdout and stderr (verified empirically).
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if stdout == "" && stderr == "" {
		return true
	}
	combined := strings.ToLower(stdout + " " + stderr)
	for _, kw := range []string{"usage limit", "rate limit", "rate_limit", "quota exceeded", "limit reached"} {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

// ─── Generic CLI backend ──────────────────────────────────────────────────────

// genericCLIBackend drives any coder CLI that is not the Claude CLI — e.g.
// Gemini-CLI, opencode, qwen-code, Cursor background agent, etc.
//
// It assumes:
//   - The binary accepts a prompt via -p <prompt> (or the equivalent)
//   - It writes the plain-text response to stdout
//   - Auth is provided via well-known env vars (GOOGLE_API_KEY, OPENAI_API_KEY, …)
//
// If a specific coder uses a different flag convention, add a dedicated backend.
type genericCLIBackend struct{}

func (b *genericCLIBackend) buildArgs(prompt string, _ bool, _ string) []string {
	// Generic coders receive the prompt via -p; tool-permission and isolation
	// flags are not used because each coder has its own convention.
	return []string{"-p", prompt}
}

func (b *genericCLIBackend) parseOutput(stdout []byte) (string, bool, error) {
	text := strings.TrimSpace(string(stdout))
	if text == "" {
		return "", false, fmt.Errorf("coder produced no output")
	}
	return text, false, nil
}

func (b *genericCLIBackend) setupHome(homeDir, _ string) error {
	return os.MkdirAll(homeDir, 0o700)
}

func (b *genericCLIBackend) extraEnvForUser(_ string) map[string]string {
	// Forward any known auth env vars that are set in the system environment.
	env := make(map[string]string)
	for _, key := range knownAuthEnvVars {
		if val := os.Getenv(key); val != "" {
			env[key] = val
		}
	}
	return env
}

func (b *genericCLIBackend) looksLikeLimit(stdout, stderr string) bool {
	// For generic coders we can only rely on keywords — the empty-stdout
	// heuristic is Claude-specific.
	combined := strings.ToLower(stdout + " " + stderr)
	for _, kw := range []string{"usage limit", "rate limit", "rate_limit", "quota exceeded", "limit reached"} {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

// ─── Claude JSON output parsing ───────────────────────────────────────────────

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

func extractClaudeJSON(data []byte) (text string, isError bool, err error) {
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
