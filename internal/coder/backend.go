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
	// Claude authenticates via the seeded CLAUDE_CONFIG_DIR/.credentials.json. Do
	// NOT inject ANTHROPIC_API_KEY here: putting it in overrides would let a
	// host-env key win over a same-named workspace secret (a precedence flip vs
	// the pre-refactor behavior). The host env still reaches the subprocess via
	// os.Environ(); workspace secrets still win via the gap-fill in buildEnv.
	return map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(workspaceHome, ".claude"),
	}
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
	if strings.TrimSpace(stdout) == "" && strings.TrimSpace(stderr) == "" {
		return true
	}
	return containsLimitKeyword(stdout + " " + stderr)
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
	return containsLimitKeyword(stdout + " " + stderr)
}

// containsLimitKeyword reports whether the combined output text contains a
// known usage/rate-limit signal, shared across backends.
func containsLimitKeyword(s string) bool {
	combined := strings.ToLower(s)
	for _, kw := range []string{"usage limit", "rate limit", "rate_limit", "quota exceeded", "limit reached", "429"} {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

// ─── OpenCode backend ──────────────────────────────────────────────────────────
// Verified end-to-end on this host. Invocation: `opencode run <prompt> --format json`.
// NOTE: opencode's -p flag is basic-auth PASSWORD, not prompt — the prompt is a
// positional arg after the `run` subcommand.
type opencodeBackend struct {
	model string // provider/model, from workspace CoderModel; passed as -m when set
}

func (b *opencodeBackend) buildArgs(prompt string, _ bool, _ string) []string {
	args := []string{"run", prompt, "--format", "json"}
	if b.model != "" {
		args = append(args, "-m", b.model)
	}
	return args
}

func (b *opencodeBackend) parseOutput(stdout []byte) (string, bool, error) {
	return parseNDJSONEvents(stdout)
}

func (b *opencodeBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv(knownAuthEnvVars...)
	// opencode resolves auth/state under XDG_DATA_HOME and config under XDG_CONFIG_HOME.
	env["XDG_DATA_HOME"] = filepath.Join(workspaceHome, ".local", "share")
	env["XDG_CONFIG_HOME"] = filepath.Join(workspaceHome, ".config")
	return env
}

func (b *opencodeBackend) seedFiles(workspaceHome string) []seedSpec {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	// Operator auth lives at ~/.local/share/opencode/auth.json (XDG_DATA_HOME default).
	src := opencodeAuthPath(home)
	return []seedSpec{{
		From: src,
		To:   filepath.Join(workspaceHome, ".local", "share", "opencode", "auth.json"),
		Mode: 0o600,
	}}
}

func (b *opencodeBackend) looksLikeLimit(stdout, stderr string) bool {
	return containsLimitKeyword(stdout + " " + stderr)
}

// opencodeAuthPath returns the operator's opencode auth file, honoring an explicit
// XDG_DATA_HOME override, else ~/.local/share.
func opencodeAuthPath(home string) string {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "opencode", "auth.json")
}

// ─── Codex backend ─────────────────────────────────────────────────────────────
// AUTHORED, UNVERIFIED — not installed on the build host. Invocation from current
// docs: `codex exec <prompt> --json`; exec mode auto-downgrades approval to
// `never` (no TTY), so it will not hang. Isolation via CODEX_HOME. Must pass
// Coder.Smoke on a host with `codex` before being relied upon.
type codexBackend struct{}

func (b *codexBackend) buildArgs(prompt string, _ bool, _ string) []string {
	return []string{"exec", prompt, "--json"}
}

func (b *codexBackend) parseOutput(stdout []byte) (string, bool, error) {
	return parseNDJSONEvents(stdout)
}

func (b *codexBackend) configEnv(workspaceHome string) map[string]string {
	env := forwardEnv("OPENAI_API_KEY", "CODEX_API_KEY")
	env["CODEX_HOME"] = filepath.Join(workspaceHome, ".codex")
	return env
}

func (b *codexBackend) seedFiles(workspaceHome string) []seedSpec {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	src := os.Getenv("CODEX_HOME")
	if src == "" {
		src = filepath.Join(home, ".codex")
	}
	return []seedSpec{{
		From: filepath.Join(src, "auth.json"),
		To:   filepath.Join(workspaceHome, ".codex", "auth.json"),
		Mode: 0o600,
	}}
}

func (b *codexBackend) looksLikeLimit(stdout, stderr string) bool {
	return containsLimitKeyword(stdout + " " + stderr)
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

// parseSingleJSONField extracts the first present string field from a single JSON
// object emitted by coders that print one final object (Gemini: "response",
// Cursor: "result"). If the object is not valid JSON, the raw trimmed text is
// returned (best-effort for plain-text stragglers).
func parseSingleJSONField(stdout []byte, fields ...string) (string, bool, error) {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return "", false, fmt.Errorf("coder produced no output")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(trimmed, &obj) != nil {
		return string(trimmed), false, nil
	}
	for _, f := range fields {
		raw, ok := obj[f]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s), false, nil
		}
	}
	return "", false, fmt.Errorf("no text field %v in response", fields)
}

// ndjsonEvent is the minimal shape shared by OpenCode/Codex event streams.
type ndjsonEvent struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Delta string `json:"delta"`
	Error struct {
		Message string `json:"message"`
		Data    struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
		} `json:"data"`
	} `json:"error"`
}

// parseNDJSONEvents scans newline-delimited JSON events, accumulating assistant
// text and reporting a terminal error event. Returns isError=true (not a Go
// error) for a coder-reported error so looksLikeLimit can classify it.
func parseNDJSONEvents(stdout []byte) (string, bool, error) {
	lines := bytes.Split(bytes.TrimSpace(stdout), []byte("\n"))
	var text strings.Builder
	var errMsg string
	var sawError bool
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev ndjsonEvent
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		switch {
		case ev.Type == "error":
			sawError = true
			m := ev.Error.Data.Message
			if m == "" {
				m = ev.Error.Message
			}
			if ev.Error.Data.StatusCode != 0 {
				if m == "" {
					m = fmt.Sprintf("status %d", ev.Error.Data.StatusCode)
				} else {
					m = fmt.Sprintf("%s (status %d)", m, ev.Error.Data.StatusCode)
				}
			}
			errMsg = m
		case ev.Text != "":
			text.WriteString(ev.Text)
		case ev.Delta != "":
			text.WriteString(ev.Delta)
		}
	}
	if sawError {
		if errMsg == "" {
			errMsg = "coder reported an error with no message"
		}
		return errMsg, true, nil
	}
	if text.Len() == 0 {
		return "", false, fmt.Errorf("no assistant text in event stream")
	}
	return strings.TrimSpace(text.String()), false, nil
}
