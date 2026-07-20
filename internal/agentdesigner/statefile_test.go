package agentdesigner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if err := WriteState(p, "Gmail Digest", map[string]any{"last_seen_id": "abc", "count": float64(4)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["last_seen_id"] != "abc" || got["count"] != float64(4) {
		t.Fatalf("round-trip mismatch: %#v", got)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "# State — Gmail Digest") {
		t.Fatalf("template heading missing:\n%s", raw)
	}
}

func TestWriteStatePreservesProseOutsideFence(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n_intro_\n\n```json\n{\"a\":1}\n```\n\n## Notes\n\nAgent wrote this.\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"a": float64(2)}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "## Notes") || !strings.Contains(string(raw), "Agent wrote this.") {
		t.Fatalf("prose lost:\n%s", raw)
	}
	if !strings.Contains(string(raw), "_intro_") {
		t.Fatalf("intro lost:\n%s", raw)
	}
	got, _ := ReadState(p)
	if got["a"] != float64(2) {
		t.Fatalf("state not updated: %#v", got)
	}
}

func TestReadStateMissingFileAndMissingFence(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadState(filepath.Join(dir, "nope.md"))
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: %#v %v", got, err)
	}

	p := filepath.Join(dir, "state.md")
	os.WriteFile(p, []byte("# State — X\n\nno fence here\n"), 0o640)
	got, err = ReadState(p)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing fence should self-heal to empty: %#v %v", got, err)
	}
	// Next write appends a fence and keeps the existing prose.
	if err := WriteState(p, "X", map[string]any{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "no fence here") || !strings.Contains(string(raw), "```json") {
		t.Fatalf("append failed:\n%s", raw)
	}
}

func TestWriteStateDollarSignSafe(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.md")
	if err := WriteState(p, "X", map[string]any{"cmd": "$1 and $0 and ${x}"}); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadState(p)
	if got["cmd"] != "$1 and $0 and ${x}" {
		t.Fatalf("dollar signs corrupted: %#v", got)
	}
}

func TestReadStateUnterminatedFenceNoMatch(t *testing.T) {
	// A fence with no closing backticks must NOT match, so damaged files degrade to empty.
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n```json\n{\"stale\": \"data\"}\n\nno close fence\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(p)
	if err != nil {
		t.Fatalf("unterminated fence should degrade to empty, not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("unterminated fence returned stale state: %#v", got)
	}
}

func TestWriteStateStripsOrphanedOpener(t *testing.T) {
	// An unterminated fence opener must be stripped when writing a new fence,
	// so a later ReadState returns only the new data, not stale+new confusion.
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n_intro_\n\n```json\n{\"stale\": \"value\"}\n\n## Notes\n\nAgent prose.\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"fresh": "data"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, _ := os.ReadFile(p)
	// The orphaned opener should be gone.
	if strings.Count(string(raw), "```json") != 1 {
		t.Fatalf("orphaned ```json opener not stripped, found %d fences:\n%s",
			strings.Count(string(raw), "```json"), raw)
	}
	// Agent prose must be preserved.
	if !strings.Contains(string(raw), "Agent prose") {
		t.Fatalf("agent prose lost:\n%s", raw)
	}
	// New state must be readable.
	got, _ := ReadState(p)
	if got["fresh"] != "data" {
		t.Fatalf("new state not readable: %#v", got)
	}
}

func TestReadStateUnmarshalError(t *testing.T) {
	// Invalid JSON in a well-formed fence must return a wrapped error, not degrade to empty.
	p := filepath.Join(t.TempDir(), "state.md")
	seed := "# State — X\n\n```json\nnot valid json at all\n```\n"
	if err := os.WriteFile(p, []byte(seed), 0o640); err != nil {
		t.Fatal(err)
	}
	_, err := ReadState(p)
	if err == nil {
		t.Fatalf("invalid JSON should return an error, not degrade to empty")
	}
	if !strings.Contains(err.Error(), "state.md json block") {
		t.Fatalf("error should mention state.md json block: %v", err)
	}
}

func TestWriteStateWhitespaceOnlyFile(t *testing.T) {
	// A whitespace-only existing file should be treated as empty and replaced with a fresh template.
	p := filepath.Join(t.TempDir(), "state.md")
	if err := os.WriteFile(p, []byte("   \n\n  \t  \n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(p, "X", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("write to whitespace-only file: %v", err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "# State — X") {
		t.Fatalf("template not created for whitespace-only file:\n%s", raw)
	}
}

func TestWriteStateDirectoryError(t *testing.T) {
	// Attempting to write to a path that is a directory must return an error.
	dir := t.TempDir()
	if err := WriteState(dir, "X", map[string]any{"k": "v"}); err == nil {
		t.Fatalf("writing to a directory should return an error, not silently fail")
	}
}
