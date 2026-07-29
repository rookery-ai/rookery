package backup

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestManifestJSONRoundTrip(t *testing.T) {
	m := Manifest{
		FormatVersion:  FormatVersion,
		AppVersion:     "v0.3.1",
		AppCommit:      "abc1234",
		SchemaVersion:  "011_pending_actions.up.sql",
		SystemKey:      "00112233",
		WorkspaceCount: 7,
		TotalBytes:     123,
		Files:          []FileEntry{{Path: "db/simple-agents.db", Size: 123, SHA256: "deadbeef"}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != m.SchemaVersion || len(got.Files) != 1 || got.Files[0].SHA256 != "deadbeef" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestManifestAcceptsOlderSchema(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "003_agent_runs_usage.up.sql"}
	if err := m.CheckCompatible("011_pending_actions.up.sql"); err != nil {
		t.Fatalf("older schema must be accepted (migrations run forward): %v", err)
	}
}

func TestManifestAcceptsEqualSchema(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "011_pending_actions.up.sql"}
	if err := m.CheckCompatible("011_pending_actions.up.sql"); err != nil {
		t.Fatalf("equal schema must be accepted: %v", err)
	}
}

// The gate that matters: a half-applied restore destroys the data it was meant
// to protect, so a snapshot from a newer binary is refused outright.
func TestManifestRefusesNewerSchema(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion, SchemaVersion: "012_future.up.sql", AppVersion: "v0.9.0"}
	err := m.CheckCompatible("011_pending_actions.up.sql")
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("got %v, want ErrSchemaTooNew", err)
	}
	if !strings.Contains(err.Error(), "v0.9.0") {
		t.Fatalf("error must name the version to upgrade to, got %q", err)
	}
}

func TestManifestRefusesNewerFormat(t *testing.T) {
	m := Manifest{FormatVersion: FormatVersion + 1, SchemaVersion: "001_initial_schema.up.sql"}
	if err := m.CheckCompatible("011_pending_actions.up.sql"); !errors.Is(err, ErrFormatTooNew) {
		t.Fatalf("got %v, want ErrFormatTooNew", err)
	}
}
