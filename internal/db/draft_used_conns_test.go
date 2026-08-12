package db_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
)

// draftTestDB opens a fresh migrated DB with one workspace (FK target for
// agent_drafts) and returns it.
func draftTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	workspaceID := uuid.New().String()
	if err := database.CreateWorkspace(&db.Workspace{ID: workspaceID, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return database, workspaceID
}

// TestAgentDraftPersistsUsedConnections verifies the build-used connection IDs
// round-trip through UpsertAgentDraft/GetAgentDraft, so auto-bind survives a
// server restart / resumed "keep-as-is" draft instead of relying solely on the
// in-memory DesignSession.PendingUsedConnections.
func TestAgentDraftPersistsUsedConnections(t *testing.T) {
	database, workspaceID := draftTestDB(t)

	err := database.UpsertAgentDraft(&db.AgentDraft{
		WorkspaceID:                workspaceID,
		AgentID:                    uuid.New().String(),
		AgentName:                  "price-tracker",
		State:                      "verifying",
		PendingAgentMD:             "# x",
		PendingToolsJSON:           "{}",
		PendingUsedConnectionsJSON: `["conn-1","conn-2"]`,
		ExpiresAt:                  time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertAgentDraft: %v", err)
	}

	got, err := database.GetAgentDraft(workspaceID)
	if err != nil {
		t.Fatalf("GetAgentDraft: %v", err)
	}
	if got.PendingUsedConnectionsJSON != `["conn-1","conn-2"]` {
		t.Fatalf("used-conns not round-tripped: %q", got.PendingUsedConnectionsJSON)
	}
}
