package connectors

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/secrets"
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
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
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

func TestAccessTokenRefreshResolvesParentConfig(t *testing.T) {
	d, ws := storeTestDB(t)
	key := mkKey()
	reg := testRegistry(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"NEW","expires_in":3600}`))
	}))
	defer srv.Close()

	// OAuth app creds stored under the PARENT provider "google" only.
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: encID, EncryptedClientSecret: encSec})

	// A child google_drive connection with a stale token + a refresh token.
	encRefresh, _ := secrets.EncryptWithSystemKey("RT", key)
	encOld, _ := secrets.EncryptWithSystemKey("OLD", key)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google_drive", AccountLabel: "work",
		EncryptedAccessToken: encOld, EncryptedRefreshToken: encRefresh,
		ExpiresAt: past, Status: "ACTIVE",
	})

	// Point the PARENT google provider's token endpoint at the test server. Do NOT
	// override reg.providers["google_drive"] — refresh must resolve it via OAuthProvider.
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}

	tok, err := store.AccessToken(ctx, ConnRef{ID: "c1", Provider: "google_drive"})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "NEW" {
		t.Fatalf("got %q, want NEW (refreshed via parent config)", tok)
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

// A keyless connection has no credential, no expiry and no refresh token. AccessToken
// must hand back an empty string cleanly rather than falling through to the refresh
// path, which would fail with "missing OAuth app credentials" on the first call.
func TestAccessTokenKeylessReturnsEmptyWithoutRefreshing(t *testing.T) {
	d, ws := storeTestDB(t)
	ctx := context.Background()

	id := uuid.New().String()
	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: id, WorkspaceID: ws, Provider: "open_meteo",
		AccountLabel: "Open-Meteo", AccountIdentity: "Open-Meteo",
		Status: "ACTIVE",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	reg := &Registry{
		providers: map[string]Provider{
			"open_meteo": {Name: "open_meteo", Auth: AuthConfig{Kind: "none"}},
		},
		actions: map[string][]Action{},
	}
	// No OAuth client and no HTTP client: if the store attempts a refresh, it fails
	// rather than silently succeeding against a stub.
	s := &DBTokenStore{DB: d, SystemKey: mkKey(), Reg: reg}

	tok, err := s.AccessToken(ctx, ConnRef{ID: id, Provider: "open_meteo"})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "" {
		t.Errorf("token = %q, want empty", tok)
	}
}

// A keyless connection stores no expiry and no refresh token, so the background
// refresh loop must never pick it up. This holds today only because
// ConnectionsNearExpiry filters on both columns being non-empty — pin it, since
// relaxing that query would put keyless rows into a refresh path they cannot survive.
func TestRefreshDueSkipsKeylessConnections(t *testing.T) {
	d, ws := storeTestDB(t)
	ctx := context.Background()

	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: uuid.New().String(), WorkspaceID: ws, Provider: "open_meteo",
		AccountLabel: "Open-Meteo", AccountIdentity: "Open-Meteo", Status: "ACTIVE",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	s := &DBTokenStore{DB: d, SystemKey: mkKey(), Reg: &Registry{
		providers: map[string]Provider{"open_meteo": {Name: "open_meteo", Auth: AuthConfig{Kind: "none"}}},
		actions:   map[string][]Action{},
	}}

	if n := refreshDue(ctx, s); n != 0 {
		t.Errorf("refreshDue refreshed %d keyless connections, want 0", n)
	}
}

// refreshFixture wires one google connection whose token expired a minute ago,
// pointed at srv as its token endpoint.
func refreshFixture(t *testing.T, srv *httptest.Server) (*db.DB, *DBTokenStore, []byte) {
	t.Helper()
	d, ws := storeTestDB(t)
	key := mkKey()
	ctx := context.Background()
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{
		ID: "pc1", WorkspaceID: ws, Provider: "google",
		EncryptedClientID: encID, EncryptedClientSecret: encSec})
	encRefresh, _ := secrets.EncryptWithSystemKey("RT", key)
	encOld, _ := secrets.EncryptWithSystemKey("OLD", key)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		EncryptedAccessToken: encOld, EncryptedRefreshToken: encRefresh,
		ExpiresAt: past, Status: "ACTIVE"})

	reg := testRegistry(t)
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	return d, &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}, key
}

// A transient failure must not cost the user the connection. Once a row is
// NEEDS_REAUTH it drops out of ConnectionsNearExpiry's status='ACTIVE' filter
// and the background loop never renews it again, so treating a 500 as fatal
// turns a momentary outage into a permanent breakage.
func TestRefreshKeepsConnectionActiveOnTransientFailure(t *testing.T) {
	for _, status := range []int{429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer srv.Close()
			d, store, _ := refreshFixture(t, srv)

			if _, err := store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"}); err == nil {
				t.Fatal("want an error from a failing token endpoint")
			}
			got, _ := d.GetServiceConnection(context.Background(), "c1")
			if got.Status != "ACTIVE" {
				t.Fatalf("status %d must not brick the connection: got %q, want ACTIVE", status, got.Status)
			}
		})
	}
}

// The transient path must still say WHICH connection failed, and must keep the
// classified kind so callers can tell a retryable failure from a rejection.
func TestTransientRefreshErrorNamesTheAccountAndKeepsItsKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	_, store, _ := refreshFixture(t, srv)

	_, err := store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	var ce *ConnectorError
	if !errors.As(err, &ce) {
		t.Fatalf("want *ConnectorError, got %T (%v)", err, err)
	}
	if ce.Kind != KindServer {
		t.Fatalf("kind = %v, want KindServer (a 503 is not a rejection)", ce.Kind)
	}
	if !strings.Contains(ce.Msg, "work") {
		t.Fatalf("message %q does not name the account", ce.Msg)
	}
}

type recordingNotifier struct {
	calls [][4]string
}

func (r *recordingNotifier) ConnectionNeedsReauth(workspaceID, connectionID, providerLabel, accountLabel string) {
	r.calls = append(r.calls, [4]string{workspaceID, connectionID, providerLabel, accountLabel})
}

func TestRefreshNotifiesOnceOnDefinitiveRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	_, store, _ := refreshFixture(t, srv)
	n := &recordingNotifier{}
	store.WithNotifier(n)

	store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	if len(n.calls) != 1 {
		t.Fatalf("got %d notifications, want 1", len(n.calls))
	}
	if n.calls[0][1] != "c1" || n.calls[0][3] != "work" {
		t.Fatalf("unexpected notification payload: %v", n.calls[0])
	}

	// The row is NEEDS_REAUTH now, so AccessToken short-circuits before refresh
	// and must not notify again. This is what makes fire-once free.
	store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	if len(n.calls) != 1 {
		t.Fatalf("got %d notifications after a second call, want still 1", len(n.calls))
	}
}

func TestRefreshDoesNotNotifyOnTransientFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	_, store, _ := refreshFixture(t, srv)
	n := &recordingNotifier{}
	store.WithNotifier(n)

	store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	if len(n.calls) != 0 {
		t.Fatalf("got %d notifications for a 503, want 0", len(n.calls))
	}
}

func TestRefreshMarksNeedsReauthOnDefinitiveRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	d, store, _ := refreshFixture(t, srv)

	if _, err := store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"}); err == nil {
		t.Fatal("want an error from a rejected refresh token")
	}
	got, _ := d.GetServiceConnection(context.Background(), "c1")
	if got.Status != "NEEDS_REAUTH" {
		t.Fatalf("got %q, want NEEDS_REAUTH", got.Status)
	}
}
