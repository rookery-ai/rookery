package agentdesigner

import (
	"strings"
	"testing"
)

// TestLoadUserProfile_NoDB confirms loadUserProfile is safe to call before
// WithDB is attached (e.g. a Flow constructed without WithDB).
func TestLoadUserProfile_NoDB(t *testing.T) {
	f := NewFlow(nil, nil)
	if got := f.loadUserProfile("any-user"); got != "" {
		t.Fatalf("expected empty string with no db attached, got %q", got)
	}
}

// TestLoadUserProfile_RendersSavedFields proves the dbDesignStore.GetSetting
// wiring works end-to-end against a real DB, without needing a live coder
// subprocess.
func TestLoadUserProfile_RendersSavedFields(t *testing.T) {
	database, userID := testDB(t)

	if err := database.SetSetting(userID, "display_name", "Ilija"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetSetting(userID, "profile_timezone", "Europe/Skopje"); err != nil {
		t.Fatal(err)
	}

	f := NewFlow(nil, nil).WithDB(database)
	got := f.loadUserProfile(userID)

	if !strings.Contains(got, "[User profile]") {
		t.Fatalf("missing header, got %q", got)
	}
	if !strings.Contains(got, "Ilija") {
		t.Fatalf("missing display name, got %q", got)
	}
	if !strings.Contains(got, "Europe/Skopje") {
		t.Fatalf("missing timezone, got %q", got)
	}
}

// TestLoadUserProfile_EmptyWhenNothingSaved confirms a brand-new user with no
// profile fields set yields an empty block (no header-only noise injected
// into the system prompt).
func TestLoadUserProfile_EmptyWhenNothingSaved(t *testing.T) {
	database, userID := testDB(t)

	f := NewFlow(nil, nil).WithDB(database)
	if got := f.loadUserProfile(userID); got != "" {
		t.Fatalf("expected empty string for unset profile, got %q", got)
	}
}
