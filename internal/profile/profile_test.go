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

func TestContextStringEmptyWhenNoFields(t *testing.T) {
	if s := (Profile{}).ContextString(); s != "" {
		t.Fatalf("expected empty string for zero-value profile, got %q", s)
	}
}

func TestContextStringPartialFields(t *testing.T) {
	p := Profile{DisplayName: "Ilija", Timezone: "Europe/Skopje"}
	s := p.ContextString()
	if !strings.HasPrefix(s, "[User profile]\n") {
		t.Fatalf("missing header, got %q", s)
	}
	if !strings.Contains(s, "- Name: Ilija") {
		t.Fatalf("missing name line, got %q", s)
	}
	if !strings.Contains(s, "- Timezone: Europe/Skopje") {
		t.Fatalf("missing timezone line, got %q", s)
	}
	if strings.Contains(s, "Email") || strings.Contains(s, "Notes") {
		t.Fatalf("unset fields should not render, got %q", s)
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
