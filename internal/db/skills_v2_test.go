package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

// TestSkillLibraryMigrationDropped verifies migration 009: the skill_library
// table and the skills.library_slug/library_version columns are gone, and the
// skill_drafts table exists and round-trips.
func TestSkillLibraryMigrationDropped(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	// skill_library must be gone.
	var name string
	err = database.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='skill_library'").Scan(&name)
	if err == nil {
		t.Fatal("skill_library table should have been dropped")
	}

	// skills must no longer have the library provenance columns.
	rows, err := database.Query("PRAGMA table_info(skills)")
	if err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "library_slug" || name == "library_version" {
			t.Fatalf("skills still has dropped column %q", name)
		}
	}

	// skill_drafts must exist and round-trip.
	userID := uuid.New().String()
	if _, err := database.Exec("INSERT INTO users(id, username, password_hash, role, created_at) VALUES(?,?,?,'user',datetime('now'))",
		userID, "skilldraft-tester", "x"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	draft := &db.SkillDraft{
		UserID:         userID,
		SkillName:      "my-skill",
		State:          "designing",
		HistoryJSON:    "[]",
		PendingSkillMD: "---\nname: my-skill\n---",
		VettingReport:  "",
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}
	if err := database.UpsertSkillDraft(draft); err != nil {
		t.Fatalf("upsert draft: %v", err)
	}
	got, err := database.GetSkillDraft(userID)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if got.SkillName != "my-skill" {
		t.Fatalf("skill name = %q", got.SkillName)
	}
	if err := database.DeleteSkillDraft(userID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, err := database.GetSkillDraft(userID); err == nil {
		t.Fatal("draft should be gone after delete")
	}
}