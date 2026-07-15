package coder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// seedSpec describes one operator credential/config file to copy from the host
// into a workspace's isolated coder home before each invocation.
type seedSpec struct {
	From string      // absolute path in the operator's real config
	To   string      // absolute path inside the per-workspace isolated home
	Mode os.FileMode // permissions for the copied file
}

// forwardEnv returns the subset of the given env var names that are currently
// set in the host environment (used to pass operator-provided API keys through).
func forwardEnv(keys ...string) map[string]string {
	out := make(map[string]string)
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			out[k] = v
		}
	}
	return out
}

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

	// configEnv returns env vars that redirect this coder's config/state dir into
	// the per-workspace home (cross-platform: prefers explicit dir env vars over HOME).
	configEnv(workspaceHome string) map[string]string

	// seedFiles returns operator credential/config file(s) to copy into the isolated
	// dir on each invocation. Auth only — never session DBs, history, or logs.
	seedFiles(workspaceHome string) []seedSpec

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

func (b *claudeBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN")
	env["CLAUDE_CONFIG_DIR"] = filepath.Join(workspaceHome, ".claude")
	return env
}

func (b *claudeBackend) seedFiles(workspaceHome string) []seedSpec {
	if b.sysClaudeDir == "" {
		return nil
	}
	return []seedSpec{{
		From: filepath.Join(b.sysClaudeDir, ".credentials.json"),
		To:   filepath.Join(workspaceHome, ".claude", ".credentials.json"),
		Mode: 0o600,
	}}
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

func (b *genericCLIBackend) configEnv(_ string) map[string]string {
	return forwardEnv(knownAuthEnvVars...)
}

func (b *genericCLIBackend) seedFiles(_ string) []seedSpec { return nil }

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
