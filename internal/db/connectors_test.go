package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
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

// TestInsertServiceConnectionReconnectUpsertsPreservingID is the SP5 final
// review fix: reconnecting under the same (workspace_id, provider,
// account_label) — e.g. re-consenting to OAuth or re-pasting an API key with
// the same label — must UPDATE the existing row (new token, status back to
// ACTIVE) rather than fail the UNIQUE constraint, and it must KEEP THE SAME
// id (agent_connections bindings reference connections by id).
func TestInsertServiceConnectionReconnectUpsertsPreservingID(t *testing.T) {
	d, _, ws := connTestDB(t)
	ctx := context.Background()

	first := db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		AccountIdentity: "old@x.com", EncryptedAccessToken: "enc-old",
		EncryptedRefreshToken: "enc-r-old", ExpiresAt: "2000-01-01T00:00:00Z",
		Status: "REVOKED",
	}
	if err := d.InsertServiceConnection(ctx, first); err != nil {
		t.Fatalf("initial insert: %v", err)
	}

	// Reconnect with the SAME workspace+provider+label but a different row
	// id (mirrors handleOAuthCallback/connectAPIKeyCore, which always mint a
	// fresh uuid before inserting).
	reconnect := db.ServiceConnection{
		ID: "c2-fresh-uuid", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		AccountIdentity: "new@x.com", EncryptedAccessToken: "enc-new",
		EncryptedRefreshToken: "enc-r-new", ExpiresAt: "2999-01-01T00:00:00Z",
		Status: "ACTIVE",
	}
	if err := d.InsertServiceConnection(ctx, reconnect); err != nil {
		t.Fatalf("reconnect must upsert, not error on UNIQUE constraint: %v", err)
	}

	// Still exactly one row for this workspace, under the ORIGINAL id.
	list, err := d.ListServiceConnections(ctx, ws)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected exactly 1 connection after reconnect, got %d (%v)", len(list), err)
	}
	got, err := d.GetServiceConnection(ctx, "c1")
	if err != nil || got == nil {
		t.Fatalf("original id c1 must still resolve: %v %v", got, err)
	}
	if got.EncryptedAccessToken != "enc-new" || got.AccountIdentity != "new@x.com" || got.Status != "ACTIVE" {
		t.Fatalf("reconnect did not refresh token/identity/status: %+v", got)
	}
	if none, _ := d.GetServiceConnection(ctx, "c2-fresh-uuid"); none != nil {
		t.Fatalf("the fresh uuid from the reconnect attempt must NOT become a second row: %+v", none)
	}
}

// TestInsertServiceConnectionReconnectWithEmptyRefreshTokenKeepsOld covers
// the SP3-5 ledger fix: a provider that doesn't re-issue a refresh token on
// every OAuth consent (or an API-key reconnect that never carries one) must
// not blank out the existing refresh token on reconnect — that would brick
// background token refresh the moment the current access token expires.
func TestInsertServiceConnectionReconnectWithEmptyRefreshTokenKeepsOld(t *testing.T) {
	d, _, ws := connTestDB(t)
	ctx := context.Background()

	first := db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		AccountIdentity: "old@x.com", EncryptedAccessToken: "enc-old",
		EncryptedRefreshToken: "enc-r-old", ExpiresAt: "2000-01-01T00:00:00Z",
		Status: "ACTIVE",
	}
	if err := d.InsertServiceConnection(ctx, first); err != nil {
		t.Fatalf("initial insert: %v", err)
	}

	// Reconnect with a fresh access token but an EMPTY refresh token.
	reconnect := db.ServiceConnection{
		ID: "c2-fresh-uuid", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		AccountIdentity: "new@x.com", EncryptedAccessToken: "enc-new",
		EncryptedRefreshToken: "", ExpiresAt: "2999-01-01T00:00:00Z",
		Status: "ACTIVE",
	}
	if err := d.InsertServiceConnection(ctx, reconnect); err != nil {
		t.Fatalf("reconnect: %v", err)
	}

	got, err := d.GetServiceConnection(ctx, "c1")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.EncryptedAccessToken != "enc-new" {
		t.Fatalf("access token should still refresh: %q", got.EncryptedAccessToken)
	}
	if got.EncryptedRefreshToken != "enc-r-old" {
		t.Fatalf("expected the OLD refresh token to be preserved, got %q", got.EncryptedRefreshToken)
	}

	// A reconnect that DOES carry a new refresh token still overwrites it.
	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "c3", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		EncryptedAccessToken: "enc-new2", EncryptedRefreshToken: "enc-r-new2",
		ExpiresAt: "2999-01-01T00:00:00Z", Status: "ACTIVE",
	}); err != nil {
		t.Fatalf("reconnect with new refresh token: %v", err)
	}
	got, _ = d.GetServiceConnection(ctx, "c1")
	if got.EncryptedRefreshToken != "enc-r-new2" {
		t.Fatalf("expected a non-empty new refresh token to overwrite the old one, got %q", got.EncryptedRefreshToken)
	}
}

// TestReconnectPreservesAgentBinding verifies an agent bound to a connection
// (agent_connections, keyed by connection id) stays bound after that
// connection is reconnected — a naive delete+insert would mint a new id and
// silently orphan the binding.
func TestReconnectPreservesAgentBinding(t *testing.T) {
	d, agentID, ws := connTestDB(t)
	ctx := context.Background()

	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		EncryptedAccessToken: "enc-old", ExpiresAt: "2000-01-01T00:00:00Z", Status: "REVOKED",
	}); err != nil {
		t.Fatalf("initial insert: %v", err)
	}
	if err := d.SetAgentConnections(ctx, agentID, []string{"c1"}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Reconnect (fresh uuid, same natural key).
	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "new-uuid-on-reconnect", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		EncryptedAccessToken: "enc-new", ExpiresAt: "2999-01-01T00:00:00Z", Status: "ACTIVE",
	}); err != nil {
		t.Fatalf("reconnect: %v", err)
	}

	bound, err := d.ListAgentConnections(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) != 1 || bound[0].ID != "c1" {
		t.Fatalf("expected the agent to remain bound to c1 after reconnect, got %+v", bound)
	}
	if bound[0].EncryptedAccessToken != "enc-new" {
		t.Fatalf("bound connection should reflect the refreshed token: %+v", bound[0])
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
