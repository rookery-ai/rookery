package agentdesigner

import (
	"strings"
	"testing"
)

// TestLoadRuntimeContext_NoDB confirms loadRuntimeContext is safe to call
// before WithDB is attached (e.g. a Flow constructed without WithDB).
func TestLoadRuntimeContext_NoDB(t *testing.T) {
	f := NewFlow(nil, nil)
	if got := f.loadRuntimeContext("any-user"); got != "" {
		t.Fatalf("expected empty string with no db attached, got %q", got)
	}
}

// TestLoadRuntimeContext_UsesSavedTimezone proves the dbDesignStore.GetSetting
// wiring works end-to-end against a real DB, without needing a live coder
// subprocess.
func TestLoadRuntimeContext_UsesSavedTimezone(t *testing.T) {
	database, workspaceID := testDB(t)

	if err := database.SetSetting(workspaceID, "profile_timezone", "Europe/Skopje"); err != nil {
		t.Fatal(err)
	}

	f := NewFlow(nil, nil).WithDB(database)
	got := f.loadRuntimeContext(workspaceID)

	if !strings.Contains(got, "[Current context]") {
		t.Fatalf("missing header, got %q", got)
	}
	if !strings.Contains(got, "Europe/Skopje") {
		t.Fatalf("missing timezone, got %q", got)
	}
	// Identity moved to memory/ABOUT.md — it must not be duplicated here, or
	// the designer would carry two descriptions of the same person.
	if strings.Contains(got, "[User profile]") {
		t.Fatalf("runtime context must not carry the old identity block: %q", got)
	}
}

// TestLoadRuntimeContext_PresentWithNothingSaved: unlike the identity block it
// replaced, this one is never empty. A brand-new workspace still needs to be
// told what day it is; the timezone simply falls back to UTC.
func TestLoadRuntimeContext_PresentWithNothingSaved(t *testing.T) {
	database, workspaceID := testDB(t)

	f := NewFlow(nil, nil).WithDB(database)
	got := f.loadRuntimeContext(workspaceID)
	if !strings.Contains(got, "[Current context]") {
		t.Fatalf("expected a runtime context block, got %q", got)
	}
	if !strings.Contains(got, "UTC") {
		t.Fatalf("expected the UTC fallback, got %q", got)
	}
}
