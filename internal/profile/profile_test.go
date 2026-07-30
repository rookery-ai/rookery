package profile

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var errNotFound = errors.New("not found")

// fakeStore is a minimal map-backed Getter/Setter, proving the interfaces
// profile depends on are genuinely minimal (no real DB needed).
type fakeStore struct{ m map[string]string }

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]string{}} }

func (f *fakeStore) GetSetting(workspaceID, key string) (string, error) {
	v, ok := f.m[workspaceID+"|"+key]
	if !ok {
		return "", errNotFound
	}
	return v, nil
}

func (f *fakeStore) SetSetting(workspaceID, key, value string) error {
	f.m[workspaceID+"|"+key] = value
	return nil
}

func TestSaveLoadRoundTrip(t *testing.T) {
	store := newFakeStore()
	const workspaceID = "u1"

	p := Profile{
		DisplayName: "Ilija",
		Email:       "ilija@example.com",
		Location:    "Skopje, North Macedonia",
		Timezone:    "Europe/Skopje",
		Tone:        "Short & concise",
		Language:    "English",
		Notes:       "Prefers technical detail",
	}
	if err := Save(store, workspaceID, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := Load(store, workspaceID)
	if got != p {
		t.Fatalf("Load mismatch: got %+v, want %+v", got, p)
	}

	// Clearing a field: Save again with Email blank must actually clear it,
	// not leave the old value in place.
	p.Email = ""
	if err := Save(store, workspaceID, p); err != nil {
		t.Fatalf("Save (clear): %v", err)
	}
	got = Load(store, workspaceID)
	if got.Email != "" {
		t.Fatalf("Email not cleared, got %q", got.Email)
	}
}

func TestSaveClampsNotes(t *testing.T) {
	store := newFakeStore()
	long := strings.Repeat("a", 400)
	if err := Save(store, "u1", Profile{Notes: long}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got := Load(store, "u1")
	if len([]rune(got.Notes)) != notesMaxRunes {
		t.Fatalf("expected notes clamped to %d runes, got %d", notesMaxRunes, len([]rune(got.Notes)))
	}
}

func TestRuntimeContextString(t *testing.T) {
	store := newFakeStore()
	store.m["u1|profile_timezone"] = "Europe/Skopje"
	now := time.Date(2026, 7, 30, 12, 32, 0, 0, time.UTC)
	got := RuntimeContextString(store, "u1", now)

	for _, want := range []string{"[Current context]", "Europe/Skopje", "2026", "14:32"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Identity lives in memory/ABOUT.md now; duplicating it here would recreate
	// the two-sources-of-truth problem this replaced.
	for _, banned := range []string{"[User profile]", "Preferred tone", "Email"} {
		if strings.Contains(got, banned) {
			t.Errorf("runtime context must not carry identity (%q):\n%s", banned, got)
		}
	}
}

// profile.Timezone is free text: "", "CEST" and "UTC+2" all fail
// time.LoadLocation. None may panic or blank the block.
func TestRuntimeContextStringBadTimezones(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	for _, tz := range []string{"", "CEST", "UTC+2", "Mars/Olympus"} {
		store := newFakeStore()
		store.m["u1|profile_timezone"] = tz
		got := RuntimeContextString(store, "u1", now)
		if !strings.Contains(got, "[Current context]") {
			t.Errorf("tz %q produced no block:\n%s", tz, got)
		}
		if !strings.Contains(got, "UTC") {
			t.Errorf("tz %q should fall back to UTC:\n%s", tz, got)
		}
	}
}

func TestIsCompleteFalseUntilMarked(t *testing.T) {
	store := newFakeStore()
	if IsComplete(store, "u1") {
		t.Fatal("expected IsComplete false before MarkComplete")
	}
	if err := MarkComplete(store, "u1"); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	if !IsComplete(store, "u1") {
		t.Fatal("expected IsComplete true after MarkComplete")
	}
}

func TestLoadLocationFallsBackToUTC(t *testing.T) {
	store := newFakeStore()

	if loc := LoadLocation(store, "u1"); loc != time.UTC {
		t.Fatalf("unset timezone: expected UTC, got %v", loc)
	}

	if err := Save(store, "u1", Profile{Timezone: "Not/AZone"}); err != nil {
		t.Fatal(err)
	}
	if loc := LoadLocation(store, "u1"); loc != time.UTC {
		t.Fatalf("invalid timezone: expected UTC fallback, got %v", loc)
	}

	if err := Save(store, "u1", Profile{Timezone: "Europe/Skopje"}); err != nil {
		t.Fatal(err)
	}
	loc := LoadLocation(store, "u1")
	if loc == nil || loc.String() != "Europe/Skopje" {
		t.Fatalf("expected Europe/Skopje, got %v", loc)
	}
}
