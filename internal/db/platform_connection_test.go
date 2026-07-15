package db_test

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

func pcTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
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

func TestPlatformConnectionEncryptedConfigRoundTrip(t *testing.T) {
	d, ws := pcTestDB(t)
	conn := &db.PlatformConnection{
		ID:              "pc1",
		WorkspaceID:     ws,
		Platform:        "slack",
		EncryptedToken:  "tok",
		EncryptedConfig: `{"app_token":"x"}`,
		Active:          true,
	}
	if err := d.UpsertPlatformConnection(conn); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetPlatformConnection(ws, "slack")
	if err != nil {
		t.Fatal(err)
	}
	if got.EncryptedConfig != `{"app_token":"x"}` {
		t.Fatalf("config not persisted: %q", got.EncryptedConfig)
	}
}
