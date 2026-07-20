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
