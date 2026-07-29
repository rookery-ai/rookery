package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/secrets"
)

func TestBlueskyUsesSessionExchange(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := reg.ProviderByName("bluesky")
	if !ok {
		t.Fatal("bluesky provider not loaded")
	}
	if !p.UsesSessionExchange() {
		t.Errorf("auth kind = %q, want session_exchange", p.Auth.Kind)
	}
	// It is NOT an api_key: the stored value must never be sent verbatim as the bearer.
	if p.IsAPIKey() {
		t.Error("session_exchange must not report as api_key — the app password is not a bearer token")
	}
	// But the connect UI is the same paste-a-credential form.
	if !p.PastesCredential() {
		t.Error("session_exchange must render the paste-credential form, or it is unconnectable")
	}
	if p.Auth.SessionURL == "" || p.Auth.SessionIdentityKey == "" {
		t.Error("session_url and session_identity_key are both required for the exchange")
	}
	// The handle is collected at connect; without it createSession has no identifier.
	var hasHandle bool
	for _, ci := range p.ConnectInputs {
		if ci.Key == "handle" && ci.Required {
			hasHandle = true
		}
	}
	if !hasHandle {
		t.Error("bluesky must require a handle connect_input")
	}
}

// Posting addresses the repo by the handle collected at connect, not by an argument —
// the model must not be able to write to another account's repo.
func TestBlueskyPostAddressesOwnRepo(t *testing.T) {
	reg, _ := LoadBundled()
	a, ok := reg.Action("bluesky", "bluesky_create_post")
	if !ok {
		t.Fatal("bluesky_create_post not found")
	}
	if !a.PublicWrite || !a.Mutating {
		t.Error("posting must be public_write and mutating")
	}
	_, u, body, _, err := renderRequest(a, map[string]any{
		"text": "hello", "created_at": "2026-07-27T12:00:00Z",
	}, map[string]string{"handle": "me.bsky.social"})
	if err != nil {
		t.Fatalf("renderRequest: %v", err)
	}
	if u != "https://bsky.social/xrpc/com.atproto.repo.createRecord" {
		t.Errorf("unexpected URL: %s", u)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v — %s", err, body)
	}
	if got["repo"] != "me.bsky.social" {
		t.Errorf("repo = %v, want the connection's handle", got["repo"])
	}
	rec, ok := got["record"].(map[string]any)
	if !ok {
		t.Fatalf("record missing: %v", got)
	}
	// $type is required by the AT Protocol; without it createRecord rejects the write.
	if rec["$type"] != "app.bsky.feed.post" {
		t.Errorf("record.$type = %v, want app.bsky.feed.post", rec["$type"])
	}
	if rec["text"] != "hello" || rec["createdAt"] != "2026-07-27T12:00:00Z" {
		t.Errorf("record did not render: %v", rec)
	}
}

// The stored app password must be exchanged for a short-lived JWT, and that JWT — not
// the password — is what reaches the provider.
func TestSessionExchangeSwapsCredentialAndCaches(t *testing.T) {
	var sessionCalls int
	var gotIdentifier, gotPassword string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionCalls++
		var in struct{ Identifier, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		gotIdentifier, gotPassword = in.Identifier, in.Password
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessJwt":"JWT-1","did":"did:plc:abc"}`))
	}))
	defer srv.Close()

	prov := Provider{
		Name: "bluesky", Label: "Bluesky",
		Auth: AuthConfig{Kind: "session_exchange", SessionURL: srv.URL, SessionIdentityKey: "handle"},
	}
	store := &DBTokenStore{HTTP: srv.Client(), SystemKey: bytes.Repeat([]byte{7}, 32), Now: func() time.Time { return time.Unix(1000, 0) }}
	row := fakeRow(t, "me.bsky.social")

	tok, err := store.sessionToken(context.Background(), prov, row)
	if err != nil {
		t.Fatalf("sessionToken: %v", err)
	}
	if tok != "JWT-1" {
		t.Errorf("token = %q, want the exchanged JWT", tok)
	}
	if gotPassword == "" || gotPassword == tok {
		t.Errorf("the app password must be sent to createSession, not returned as the bearer")
	}
	if gotIdentifier != "me.bsky.social" {
		t.Errorf("identifier = %q, want the handle from extra", gotIdentifier)
	}

	// A second call inside the cache window must not re-exchange: an agent making ten
	// calls should not open ten sessions.
	if _, err := store.sessionToken(context.Background(), prov, row); err != nil {
		t.Fatalf("second sessionToken: %v", err)
	}
	if sessionCalls != 1 {
		t.Errorf("createSession called %d times, want 1 (cached)", sessionCalls)
	}
}

// A revoked app password must read as needs-reauth, not as a generic failure, or the
// user is told nothing actionable.
func TestSessionExchangeRevokedCredentialIsNeedsReauth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"AuthenticationRequired"}`))
	}))
	defer srv.Close()

	prov := Provider{
		Name: "bluesky", Label: "Bluesky",
		Auth: AuthConfig{Kind: "session_exchange", SessionURL: srv.URL, SessionIdentityKey: "handle"},
	}
	store := &DBTokenStore{HTTP: srv.Client(), SystemKey: bytes.Repeat([]byte{7}, 32), Now: time.Now}

	_, err := store.sessionToken(context.Background(), prov, fakeRow(t, "me.bsky.social"))
	if err == nil {
		t.Fatal("a rejected credential must error")
	}
	var ce *ConnectorError
	if !asConnectorError(err, &ce) || ce.Kind != KindNeedsReauth {
		t.Errorf("want KindNeedsReauth, got %v", err)
	}
}

// fakeRow builds a service_connections row whose credential is really encrypted, so
// sessionToken exercises the same decrypt path production does.
func fakeRow(t *testing.T, handle string) *db.ServiceConnection {
	t.Helper()
	key := bytes.Repeat([]byte{7}, 32)
	enc, err := secrets.EncryptWithSystemKey("app-pass-1234", key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	extra, _ := json.Marshal(map[string]string{"handle": handle})
	return &db.ServiceConnection{
		ID: "conn-bsky", WorkspaceID: "w1", Provider: "bluesky",
		AccountLabel: handle, AccountIdentity: handle,
		EncryptedAccessToken: enc, Status: "ACTIVE", Extra: string(extra),
	}
}

func asConnectorError(err error, target **ConnectorError) bool {
	return errors.As(err, target)
}

// ── Mastodon: per-instance OAuth endpoints ───────────────────────────────────

// Every Mastodon instance is its own OAuth server, so the endpoints are TEMPLATES.
// Unresolved, the consent URL would point at a literal "{{conn.instance}}" host.
func TestMastodonResolvesPerInstanceEndpoints(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := reg.ProviderByName("mastodon")
	if !ok {
		t.Fatal("mastodon provider not loaded")
	}
	if !contains(p.AuthorizeURL, "{{conn.instance}}") {
		t.Fatalf("authorize_url should be a template, got %q", p.AuthorizeURL)
	}

	resolved := p.WithConnVars(map[string]string{"instance": "https://mastodon.social"})
	if resolved.AuthorizeURL != "https://mastodon.social/oauth/authorize" {
		t.Errorf("authorize_url = %q", resolved.AuthorizeURL)
	}
	if resolved.TokenURL != "https://mastodon.social/oauth/token" {
		t.Errorf("token_url = %q", resolved.TokenURL)
	}
	if resolved.UserinfoURL != "https://mastodon.social/api/v1/accounts/verify_credentials" {
		t.Errorf("userinfo_url = %q", resolved.UserinfoURL)
	}

	u := resolved.ConsentURL("cid", "https://example.com/cb", "st", resolved.DefaultScopes)
	if !contains(u, "https://mastodon.social/oauth/authorize?") {
		t.Errorf("consent URL did not resolve: %s", u)
	}
	// Mastodon tokens do not expire, so nothing should try to refresh them.
	if !p.NonExpiring() {
		t.Error("mastodon tokens do not expire — token_expiry should be never")
	}
}

// WithConnVars must be a no-op for every provider with literal URLs, or resolving it
// on the shared code path would corrupt them.
func TestWithConnVarsLeavesLiteralURLsAlone(t *testing.T) {
	reg, _ := LoadBundled()
	for _, name := range []string{"github", "google", "linkedin", "x"} {
		p, ok := reg.ProviderByName(name)
		if !ok {
			continue
		}
		got := p.WithConnVars(map[string]string{"instance": "https://evil.example"})
		if got.AuthorizeURL != p.AuthorizeURL || got.TokenURL != p.TokenURL {
			t.Errorf("%s: literal endpoints were altered: %q / %q", name, got.AuthorizeURL, got.TokenURL)
		}
	}
	// Empty vars must also be a no-op.
	p, _ := reg.ProviderByName("mastodon")
	if p.WithConnVars(nil).AuthorizeURL != p.AuthorizeURL {
		t.Error("nil vars should leave the provider untouched")
	}
}

// Every action addresses the user's own instance from conn.extra, so the model cannot
// direct a post at an arbitrary server.
func TestMastodonActionsUseInstanceFromExtra(t *testing.T) {
	reg, _ := LoadBundled()
	for _, name := range []string{"mastodon_me", "mastodon_timeline", "mastodon_search", "mastodon_create_post"} {
		a, ok := reg.Action("mastodon", name)
		if !ok {
			t.Fatalf("%s not found", name)
		}
		if !contains(a.Request.URL, "{{conn.instance}}") {
			t.Errorf("%s must address the connected instance, got %s", name, a.Request.URL)
		}
	}
	a, _ := reg.Action("mastodon", "mastodon_create_post")
	if !a.PublicWrite || !a.Mutating {
		t.Error("posting to Mastodon must be public_write and mutating")
	}
	_, u, _, _, err := renderRequest(a, map[string]any{"status": "hi"},
		map[string]string{"instance": "https://mastodon.social"})
	if err != nil {
		t.Fatalf("renderRequest: %v", err)
	}
	if u != "https://mastodon.social/api/v1/statuses" {
		t.Errorf("unexpected URL: %s", u)
	}
}
