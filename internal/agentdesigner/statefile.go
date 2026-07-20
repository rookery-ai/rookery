package agentdesigner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// stateFenceRE matches a fenced json block. Non-greedy so the FIRST block wins:
// an agent's own "## Notes" prose may legitimately contain another fence.
var stateFenceRE = regexp.MustCompile("(?s)```json\\s*\\n(.*?)```")

// StateFilePath returns the path to an agent's state.md — its memory between
// runs, kept as a markdown document so it is readable in the knowledge base.
func StateFilePath(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "state.md")
}

// RenderStateTemplate builds a fresh state.md. The intro is italic prose, never
// an HTML comment: comments do not round-trip through the KB editor and would
// pin the file in raw mode forever.
func RenderStateTemplate(agentName, jsonBody string) string {
	return fmt.Sprintf(`# State — %s

_Managed by Simple Agents. The block below is this agent's memory between runs —
edit it if you need to fix something by hand._

`+"```json\n%s\n```"+`
`, agentName, jsonBody)
}

// ReadState returns the state object held in the first json fence of state.md.
// A missing file, a missing fence, or an empty fence all yield an empty map so a
// damaged file degrades to "no memory" instead of failing the run.
func ReadState(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	m := stateFenceRE.FindSubmatch(raw)
	if m == nil {
		return map[string]any{}, nil
	}
	body := bytes.TrimSpace(m[1])
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("state.md json block: %w", err)
	}
	if st == nil {
		st = map[string]any{}
	}
	return st, nil
}

// WriteState replaces only the first json fence, leaving the heading, intro and
// any agent-written prose untouched. A file with no fence gains one; a missing
// file is created from the template.
func WriteState(path, agentName string, state map[string]any) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	fence := "```json\n" + string(body) + "\n```"

	raw, readErr := os.ReadFile(path)
	if readErr != nil || len(bytes.TrimSpace(raw)) == 0 {
		return os.WriteFile(path, []byte(RenderStateTemplate(agentName, string(body))), 0o640)
	}

	loc := stateFenceRE.FindIndex(raw)
	if loc == nil {
		out := strings.TrimRight(string(raw), "\n") + "\n\n" + fence + "\n"
		return os.WriteFile(path, []byte(out), 0o640)
	}

	// Index splice, never ReplaceAll: JSON containing "$1"/"${x}" would be
	// mangled by regexp template expansion.
	out := make([]byte, 0, len(raw)+len(fence))
	out = append(out, raw[:loc[0]]...)
	out = append(out, []byte(fence)...)
	out = append(out, raw[loc[1]:]...)
	return os.WriteFile(path, out, 0o640)
}
