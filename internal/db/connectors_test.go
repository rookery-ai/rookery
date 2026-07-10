package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

// connTestDB opens a fresh migrated DB with one workspace + one agent (FK targets)
// and returns the DB, agentID, and workspaceID.
func connTestDB(t *testing.T) (*db.DB, string, string) {
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
	agentID := "agent-1"
	if err := database.CreateAgent(&db.Agent{ID: agentID, WorkspaceID: workspaceID, Name: "A", Active: true}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return database, agentID, workspaceID
}

func TestServiceConnectionRoundTrip(t *testing.T) {
	d, _, ws := connTestDB(t)
	ctx := context.Background()

	conn := db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		AccountIdentity: "ilija@x.com", Scopes: "gmail.send",
		EncryptedAccessToken: "enc-a", EncryptedRefreshToken: "enc-r",
		ExpiresAt: "2999-01-01T00:00:00Z", Status: "ACTIVE",
	}
	if err := d.InsertServiceConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetServiceConnection(ctx, "c1")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.AccountIdentity != "ilija@x.com" {
		t.Fatalf("identity: %q", got.AccountIdentity)
	}
	list, err := d.ListServiceConnections(ctx, ws)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}

	if err := d.UpdateConnectionTokens(ctx, "c1", "enc-a2", "3000-01-01T00:00:00Z", "ACTIVE"); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetServiceConnection(ctx, "c1")
	if got.EncryptedAccessToken != "enc-a2" {
		t.Fatalf("token not updated: %q", got.EncryptedAccessToken)
	}
}

func TestProviderConfigUpsert(t *testing.T) {
	d, _, ws := connTestDB(t)
	ctx := context.Background()
	if err := d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: "a", EncryptedClientSecret: "b"}); err != nil {
		t.Fatal(err)
	}
	// Upsert again (same ws+provider) updates in place.
	if err := d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{ID: "pc2", WorkspaceID: ws, Provider: "google", EncryptedClientID: "a2", EncryptedClientSecret: "b2"}); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetServiceProviderConfig(ctx, ws, "google")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.EncryptedClientID != "a2" {
		t.Fatalf("upsert did not update: %q", got.EncryptedClientID)
	}
	if none, _ := d.GetServiceProviderConfig(ctx, ws, "github"); none != nil {
		t.Fatal("missing config must be nil")
	}
}

func TestConnectionsNearExpiry(t *testing.T) {
	d, _, ws := connTestDB(t)
	ctx := context.Background()
	// One soon-expiring with a refresh token, one far-off, one without a refresh token.
	d.InsertServiceConnection(ctx, db.ServiceConnection{ID: "soon", WorkspaceID: ws, Provider: "google", AccountLabel: "a", EncryptedRefreshToken: "r", ExpiresAt: "2000-01-01T00:00:00Z", Status: "ACTIVE"})
	d.InsertServiceConnection(ctx, db.ServiceConnection{ID: "far", WorkspaceID: ws, Provider: "google", AccountLabel: "b", EncryptedRefreshToken: "r", ExpiresAt: "2999-01-01T00:00:00Z", Status: "ACTIVE"})
	d.InsertServiceConnection(ctx, db.ServiceConnection{ID: "norefresh", WorkspaceID: ws, Provider: "google", AccountLabel: "c", ExpiresAt: "2000-01-01T00:00:00Z", Status: "ACTIVE"})

	due, err := d.ConnectionsNearExpiry(ctx, "2100-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != "soon" {
		t.Fatalf("expected only 'soon', got %+v", due)
	}
}

func TestAgentConnectionsReplaceAll(t *testing.T) {
	d, ag, ws := connTestDB(t)
	ctx := context.Background()
	for _, id := range []string{"c1", "c2"} {
		if err := d.InsertServiceConnection(ctx, db.ServiceConnection{ID: id, WorkspaceID: ws, Provider: "google", AccountLabel: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetAgentConnections(ctx, ag, []string{"c1", "c2"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAgentConnections(ctx, ag, []string{"c1"}); err != nil { // replace-all
		t.Fatal(err)
	}
	got, err := d.ListAgentConnections(ctx, ag)
	if err != nil || len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("expected only c1, got %v (%v)", got, err)
	}
}
