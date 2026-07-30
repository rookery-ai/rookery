package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/secrets"
)

func secretsEncryptHelper(v string, key []byte) (string, error) {
	return secrets.EncryptWithSystemKey(v, key)
}

func readAllBody(r *http.Request) (string, error) {
	b := make([]byte, r.ContentLength)
	if r.ContentLength <= 0 {
		return "", nil
	}
	_, err := r.Body.Read(b)
	return string(b), err
}

func dbServiceConn(ws, id, provider, encTok string) db.ServiceConnection {
	return db.ServiceConnection{
		ID: id, WorkspaceID: ws, Provider: provider, AccountLabel: "acct",
		EncryptedAccessToken: encTok, ExpiresAt: "", Status: "ACTIVE",
	}
}

func TestTokenAuthBasicAndStaticHeaders(t *testing.T) {
	var gotAuth, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			gotAuth = r.Header.Get("Authorization")
			// client creds must NOT be in the body when token_auth=basic
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "" {
				t.Errorf("client_id leaked into body under basic auth")
			}
			w.Write([]byte(`{"access_token":"AT","expires_in":3600}`))
		default:
			gotHeader = r.Header.Get("Notion-Version")
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()

	p := Provider{Name: "notion", TokenURL: srv.URL + "/token", TokenAuth: "basic",
		StaticHeaders: map[string]string{"Notion-Version": "2022-06-28"}}
	ts, err := OAuthClient{HTTP: srv.Client()}.ExchangeCode(context.Background(), p, "cid", "csec", "code", "https://cb")
	if err != nil || ts.AccessToken != "AT" {
		t.Fatalf("exchange: %+v %v", ts, err)
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("token endpoint should get Basic auth, got %q", gotAuth)
	}

	// static header must be applied to action requests
	reg := &Registry{providers: map[string]Provider{"notion": p}, actions: map[string][]Action{
		"notion": {{Name: "notion_ping", Request: RequestTemplate{Method: "GET", URL: srv.URL + "/v1/x"}}},
	}}
	_, err = Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(), ConnRef{Provider: "notion"}, "notion_ping", map[string]any{}, Policy{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotHeader != "2022-06-28" {
		t.Fatalf("static header not applied, got %q", gotHeader)
	}
}

func TestTokenContentTypeJSON(t *testing.T) {
	var gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		b, _ := readAllBody(r)
		gotBody = b
		w.Write([]byte(`{"access_token":"AT"}`))
	}))
	defer srv.Close()
	p := Provider{TokenURL: srv.URL + "/token", TokenAuth: "basic", TokenContentType: "json"}
	_, err := OAuthClient{HTTP: srv.Client()}.ExchangeCode(context.Background(), p, "cid", "csec", "code", "https://cb")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q, want application/json", gotCT)
	}
	if !strings.Contains(gotBody, `"grant_type":"authorization_code"`) {
		t.Fatalf("body should be JSON with grant_type: %s", gotBody)
	}
}

func TestConnVarTemplating(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	reg := &Registry{providers: map[string]Provider{"jira": {Name: "jira"}}, actions: map[string][]Action{
		"jira": {{Name: "jira_myself", Request: RequestTemplate{Method: "GET", URL: srv.URL + "/ex/jira/{{conn.cloudid}}/rest/api/3/myself"}}},
	}}
	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{Provider: "jira", Extra: map[string]string{"cloudid": "CLOUD123"}}, "jira_myself", map[string]any{}, Policy{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(gotPath, "/ex/jira/CLOUD123/rest/api/3/myself") {
		t.Fatalf("conn var not substituted into URL: %s", gotPath)
	}
}

func TestNonExpiringProviderNeverRefreshes(t *testing.T) {
	// A non-expiring provider with an empty expires_at must return the stored token, not
	// try to refresh (which would fail — no token endpoint wired).
	d, ws := storeTestDB(t)
	key := mkKey()
	ctx := context.Background()
	encTok, _ := secretsEncryptHelper("GH_TOKEN", key)
	d.InsertServiceConnection(ctx, dbServiceConn(ws, "gh1", "github", encTok))
	reg := &Registry{providers: map[string]Provider{"github": {Name: "github", TokenExpiry: "never"}}, actions: map[string][]Action{}}
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{}}
	tok, err := store.AccessToken(ctx, ConnRef{ID: "gh1", Provider: "github"})
	if err != nil || tok != "GH_TOKEN" {
		t.Fatalf("non-expiring token should be returned as-is, got %q %v", tok, err)
	}
}
