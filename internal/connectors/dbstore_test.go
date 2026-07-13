package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

func mkKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

// storeTestDB opens a fresh migrated DB with one workspace.
func storeTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), "../../migrations")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	ws := uuid.New().String()
	if err := d.CreateWorkspace(&db.Workspace{ID: ws, Name: "tester"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return d, ws
}

func TestAccessTokenRefreshesNearExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"NEW","expires_in":3600}`))
	}))
	defer srv.Close()

	d, ws := storeTestDB(t)
	key := mkKey()
	ctx := context.Background()
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: encID, EncryptedClientSecret: encSec})
	encRefresh, _ := secrets.EncryptWithSystemKey("RT", key)
	encOld, _ := secrets.EncryptWithSystemKey("OLD", key)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		EncryptedAccessToken: encOld, EncryptedRefreshToken: encRefresh, ExpiresAt: past, Status: "ACTIVE"})

	reg := testRegistry(t)
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}

	tok, err := store.AccessToken(ctx, ConnRef{ID: "c1", Provider: "google"})
	if err != nil || tok != "NEW" {
		t.Fatalf("want refreshed NEW, got %q %v", tok, err)
	}
	got, _ := d.GetServiceConnection(ctx, "c1")
	if dec, _ := secrets.DecryptWithSystemKey(got.EncryptedAccessToken, key); dec != "NEW" {
		t.Fatalf("new token not persisted: %q", dec)
	}
}

func TestAccessTokenValidReturnsStored(t *testing.T) {
	d, ws := storeTestDB(t)
	key := mkKey()
	ctx := context.Background()
	encTok, _ := secrets.EncryptWithSystemKey("STILL_GOOD", key)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	d.InsertServiceConnection(ctx, db.ServiceConnection{ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "w", EncryptedAccessToken: encTok, ExpiresAt: future, Status: "ACTIVE"})
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: testRegistry(t), OAuth: OAuthClient{}}
	tok, err := store.AccessToken(ctx, ConnRef{ID: "c1", Provider: "google"})
	if err != nil || tok != "STILL_GOOD" {
		t.Fatalf("valid token should be returned unchanged, got %q %v", tok, err)
	}
}

func TestAccessTokenAPIKeyReturnsStoredKeyNoRefresh(t *testing.T) {
	d, ws := storeTestDB(t)
	key := mkKey()
	reg := testRegistry(t)
	ctx := context.Background()

	enc, _ := secrets.EncryptWithSystemKey("sk-secret", key)
	// api_key connection: empty refresh + empty expiry (would normally be treated as expired).
	d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "k1", WorkspaceID: ws, Provider: "openai", AccountLabel: "default",
		EncryptedAccessToken: enc, ExpiresAt: "", Status: "ACTIVE",
	})

	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{}}
	got, err := store.AccessToken(ctx, ConnRef{ID: "k1", Provider: "openai"})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "sk-secret" {
		t.Fatalf("got %q, want sk-secret", got)
	}
}

func TestAccessTokenRefreshFailureFlipsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	d, ws := storeTestDB(t)
	key := mkKey()
	ctx := context.Background()
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: encID, EncryptedClientSecret: encSec})
	encR, _ := secrets.EncryptWithSystemKey("RT", key)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	d.InsertServiceConnection(ctx, db.ServiceConnection{ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "w", EncryptedRefreshToken: encR, ExpiresAt: past, Status: "ACTIVE"})
	reg := testRegistry(t)
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}

	_, err := store.AccessToken(ctx, ConnRef{ID: "c1", Provider: "google"})
	if ce, ok := err.(*ConnectorError); !ok || ce.Kind != KindNeedsReauth {
		t.Fatalf("expected KindNeedsReauth, got %v", err)
	}
	got, _ := d.GetServiceConnection(ctx, "c1")
	if got.Status != "NEEDS_REAUTH" {
		t.Fatalf("status should flip to NEEDS_REAUTH, got %q", got.Status)
	}
}
