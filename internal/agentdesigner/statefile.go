package agentdesigner

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// fenceLoc describes where (if anywhere) the state fence lives.
type fenceLoc struct {
	Open, Close int // line indices of the ```json and ``` lines; valid only when OK
	OK          bool
	OrphanOpen  int // index of the first ```json line when OK is false; -1 when there is none
}

// findStateFence locates the FIRST well-formed json fence: an opener line
// (trimmed == "```json") terminated by a closer line (trimmed == "```") with no
// other fence-opener line in between.
//
// If the first opener is not cleanly terminated — because the file ends, or
// because another fence opener appears first — the file is damaged and we report
// OK=false rather than searching on. The state fence is by construction the FIRST
// fence; if it is malformed, nothing later in the document may be mistaken for it.
func findStateFence(lines []string) fenceLoc {
	openIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "```json" {
			openIdx = i
			break
		}
	}
	if openIdx == -1 {
		return fenceLoc{OK: false, OrphanOpen: -1}
	}
	for i := openIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "```" {
			return fenceLoc{Open: openIdx, Close: i, OK: true}
		}
		if strings.HasPrefix(trimmed, "```") {
			// Another fence opener before this one closed: damaged.
			return fenceLoc{OK: false, OrphanOpen: openIdx}
		}
	}
	// Ran off the end without a closer: damaged.
	return fenceLoc{OK: false, OrphanOpen: openIdx}
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
	lines := strings.Split(string(raw), "\n")
	loc := findStateFence(lines)
	if !loc.OK {
		return map[string]any{}, nil
	}
	body := strings.TrimSpace(strings.Join(lines[loc.Open+1:loc.Close], "\n"))
	if len(body) == 0 {
		return map[string]any{}, nil
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		return nil, fmt.Errorf("state.md json block: %w", err)
	}
	if st == nil {
		st = map[string]any{}
	}
	return st, nil
}

// WriteState replaces only the first json fence, leaving the heading, intro and
// any agent-written prose untouched. A file with no fence gains one; a missing
// file is created from the template. An orphaned (unterminated) json-fence
// opener is deleted — and only that one line — so a legitimate later fence
// (e.g. in an agent-written "## Notes" section) is never touched.
func WriteState(path, agentName string, state map[string]any) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	fenceLines := []string{"```json", string(body), "```"}

	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		// Only treat missing file as create-fresh; propagate other errors.
		if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		return os.WriteFile(path, []byte(RenderStateTemplate(agentName, string(body))), 0o640)
	}

	if len(strings.TrimSpace(string(raw))) == 0 {
		return os.WriteFile(path, []byte(RenderStateTemplate(agentName, string(body))), 0o640)
	}

	lines := strings.Split(string(raw), "\n")
	loc := findStateFence(lines)

	var out []string
	switch {
	case loc.OK:
		// Line splice: replace lines[Open..Close] inclusive. Everything
		// before Open and after Close survives byte-for-byte.
		out = make([]string, 0, len(lines)-(loc.Close-loc.Open+1)+len(fenceLines))
		out = append(out, lines[:loc.Open]...)
		out = append(out, fenceLines...)
		out = append(out, lines[loc.Close+1:]...)
	case loc.OrphanOpen >= 0:
		// Replace only the one orphaned opener line, in place, with the new
		// fence. Never strip any other ```json line — that is what protects
		// a legitimate fence further down (e.g. in Notes).
		//
		// The new fence goes HERE, not appended at the end of the file: if a
		// legitimate fence already exists later in the document (e.g. in
		// Notes), appending after it would make that later fence the new
		// "first" fence. ReadState would then return the Notes fence's data
		// instead of the state we just wrote, and a SECOND WriteState call
		// would hit the loc.OK branch and splice over the Notes fence —
		// destroying it. Writing in place keeps the new fence first and
		// leaves everything after it byte-for-byte untouched.
		out = make([]string, 0, len(lines)+len(fenceLines))
		out = append(out, lines[:loc.OrphanOpen]...)
		out = append(out, fenceLines...)
		out = append(out, lines[loc.OrphanOpen+1:]...)
	default:
		out = appendFence(append([]string{}, lines...), fenceLines)
	}

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o640)
}

// appendFence appends a blank separator line and the fence lines to the end
// of the document, trimming any trailing blank lines first so spacing is
// consistent regardless of how much trailing whitespace the source had.
func appendFence(lines []string, fenceLines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	lines = append(lines, "")
	lines = append(lines, fenceLines...)
	lines = append(lines, "")
	return lines
}
