package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
)

// TestSkillLibraryMigrationDropped verifies the schema has no skill_library table
// and no skills.library_slug/library_version columns, and that the skill_drafts
// table exists and round-trips (keyed by workspace_id).
func TestSkillLibraryMigrationDropped(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
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
	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "skilldraft-tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	draft := &db.SkillDraft{
		WorkspaceID:    workspaceID,
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
	got, err := database.GetSkillDraft(workspaceID)
	if err != nil {
		t.Fatalf("get draft: %v", err)
	}
	if got.SkillName != "my-skill" {
		t.Fatalf("skill name = %q", got.SkillName)
	}
	if err := database.DeleteSkillDraft(workspaceID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	if _, err := database.GetSkillDraft(workspaceID); err == nil {
		t.Fatal("draft should be gone after delete")
	}
}
