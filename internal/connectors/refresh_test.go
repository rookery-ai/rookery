package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

func TestRefreshDueRefreshesSoonExpiring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"REFRESHED","expires_in":3600}`))
	}))
	defer srv.Close()
	d, ws := storeTestDB(t)
	key := mkKey()
	ctx := context.Background()
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: encID, EncryptedClientSecret: encSec})
	encR, _ := secrets.EncryptWithSystemKey("RT", key)
	soon := time.Now().Add(3 * time.Minute).UTC().Format(time.RFC3339) // within the 10-min cutoff
	d.InsertServiceConnection(ctx, db.ServiceConnection{ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "w", EncryptedRefreshToken: encR, ExpiresAt: soon, Status: "ACTIVE"})

	reg := testRegistry(t)
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}
	if n := refreshDue(ctx, store); n != 1 {
		t.Fatalf("want 1 refreshed, got %d", n)
	}
	got, _ := d.GetServiceConnection(ctx, "c1")
	if dec, _ := secrets.DecryptWithSystemKey(got.EncryptedAccessToken, key); dec != "REFRESHED" {
		t.Fatalf("not refreshed: %q", dec)
	}
}
