// Package profile stores and renders per-user personalization data (name,
// location, timezone, communication tone, etc.) collected during onboarding
// and editable later from Settings. It is built on top of the existing
// generic per-user key-value settings table — no dedicated schema.
package profile

import "time"

// Getter is the minimal read capability profile needs. *db.DB satisfies this
// already via GetSetting(workspaceID, key string) (string, error).
type Getter interface {
	GetSetting(workspaceID, key string) (string, error)
}

// Setter is the minimal write capability profile needs. *db.DB satisfies this
// already via SetSetting(workspaceID, key, value string) error.
type Setter interface {
	SetSetting(workspaceID, key, value string) error
}

// Profile holds the structured personalization fields. All fields are
// optional; an empty string means "not set".
type Profile struct {
	DisplayName string
	Email       string
	Location    string
	Timezone    string // IANA name, e.g. "Europe/Skopje"
	Tone        string // descriptive text, e.g. "Short & concise"
	Language    string // e.g. "English"
	Notes       string // free text, clamped to notesMaxRunes by Save
}

const (
	keyDisplayName = "display_name" // pre-existing key, reused as-is
	keyEmail       = "profile_email"
	keyLocation    = "profile_location"
	keyTimezone    = "profile_timezone"
	keyTone        = "profile_tone"
	keyLanguage    = "profile_language"
	keyNotes       = "profile_notes"
	keyCompleted   = "profile_completed"

	notesMaxRunes = 300
)

// Load reads all profile fields for workspaceID. Per-key lookup errors (including
// db.ErrNotFound) are treated as "" — Load never fails.
func Load(g Getter, workspaceID string) Profile {
	get := func(key string) string {
		v, err := g.GetSetting(workspaceID, key)
		if err != nil {
			return ""
		}
		return v
	}
	return Profile{
		DisplayName: get(keyDisplayName),
		Email:       get(keyEmail),
		Location:    get(keyLocation),
		Timezone:    get(keyTimezone),
		Tone:        get(keyTone),
		Language:    get(keyLanguage),
		Notes:       get(keyNotes),
	}
}

// Save writes all profile fields (data only, not the completed sentinel).
// Every field is written even if empty, so callers can clear a previously-set
// field. Notes is clamped to notesMaxRunes before writing.
func Save(s Setter, workspaceID string, p Profile) error {
	if n := []rune(p.Notes); len(n) > notesMaxRunes {
		p.Notes = string(n[:notesMaxRunes])
	}
	fields := []struct{ key, val string }{
		{keyDisplayName, p.DisplayName},
		{keyEmail, p.Email},
		{keyLocation, p.Location},
		{keyTimezone, p.Timezone},
		{keyTone, p.Tone},
		{keyLanguage, p.Language},
		{keyNotes, p.Notes},
	}
	for _, f := range fields {
		if err := s.SetSetting(workspaceID, f.key, f.val); err != nil {
			return err
		}
	}
	return nil
}

// IsComplete reports whether the user has passed (or skipped) the profile
// setup step. Sentinel-only — does not look at any data field.
func IsComplete(g Getter, workspaceID string) bool {
	v, err := g.GetSetting(workspaceID, keyCompleted)
	return err == nil && v == "1"
}

// MarkComplete sets the profile_completed sentinel. Called on both "save and
// continue" and "skip" in the setup wizard.
func MarkComplete(s Setter, workspaceID string) error {
	return s.SetSetting(workspaceID, keyCompleted, "1")
}

// RuntimeContextString renders the "[Current context]" block injected into
// every LLM prompt.
//
// It deliberately carries ONLY what markdown cannot hold without going stale.
// Identity — who the user is, what the workspace is for, how they want to be
// spoken to — lives in memory/ABOUT.md and memory/STYLE.md, which the user
// edits directly and which memory.ContextString injects. This replaced a
// "[User profile]" block built from the settings table, which was the de-facto
// source of truth precisely because the memory files were never populated.
//
// The timezone stays here because it is editable in Settings and is read
// programmatically by LoadLocation for reminder parsing; a copy rendered into
// markdown at setup would silently diverge the moment the user changed it.
//
// The current date and time are here because nothing else supplied them: before
// this, the only time.Now() in prompt construction was the reminder parser, so
// chat and agent runs could not say what day it was.
//
// now is a parameter rather than a call to time.Now() so tests are not
// clock-dependent. Never returns an error: an unparseable timezone degrades to
// UTC exactly as LoadLocation does.
func RuntimeContextString(g Getter, workspaceID string, now time.Time) string {
	loc := LoadLocation(g, workspaceID)
	tz := Load(g, workspaceID).Timezone
	if _, err := time.LoadLocation(tz); tz == "" || err != nil {
		tz = "UTC"
	}
	return "[Current context]\n" +
		"- Current date and time: " + now.In(loc).Format("Monday, 2 January 2006, 15:04") + " (" + tz + ")\n" +
		"- Timezone: " + tz + "\n"
}

// LoadLocation resolves the user's saved timezone to a *time.Location,
// falling back to time.UTC if unset or invalid.
func LoadLocation(g Getter, workspaceID string) *time.Location {
	tz := Load(g, workspaceID).Timezone
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}
