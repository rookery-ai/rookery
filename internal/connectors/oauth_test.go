package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangeAndIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","expires_in":3600}`))
		case strings.HasSuffix(r.URL.Path, "/userinfo"):
			if r.Header.Get("Authorization") != "Bearer AT" {
				t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
			}
			w.Write([]byte(`{"email":"ilija@x.com"}`))
		}
	}))
	defer srv.Close()

	p := Provider{Name: "google", TokenURL: srv.URL + "/token", UserinfoURL: srv.URL + "/userinfo", IdentityPath: "email"}
	c := OAuthClient{HTTP: srv.Client()}
	ts, err := c.ExchangeCode(context.Background(), p, "cid", "csec", "code123", "https://cb")
	if err != nil || ts.AccessToken != "AT" || ts.RefreshToken != "RT" || ts.ExpiresIn != 3600 {
		t.Fatalf("exchange: %+v %v", ts, err)
	}
	id, err := c.FetchIdentity(context.Background(), p, "AT")
	if err != nil || id != "ilija@x.com" {
		t.Fatalf("identity: %q %v", id, err)
	}
}

func TestRefreshKeepsExistingRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"NEW","expires_in":3600}`)) // no refresh_token in response
	}))
	defer srv.Close()
	p := Provider{TokenURL: srv.URL + "/token"}
	ts, err := OAuthClient{HTTP: srv.Client()}.Refresh(context.Background(), p, "cid", "csec", "OLD_RT")
	if err != nil || ts.AccessToken != "NEW" || ts.RefreshToken != "OLD_RT" {
		t.Fatalf("refresh must keep old refresh token: %+v %v", ts, err)
	}
}

func TestConsentURLUsesExplicitScopes(t *testing.T) {
	p := Provider{
		AuthorizeURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		AuthorizeExtra: map[string]string{"include_granted_scopes": "true"},
	}
	u := p.ConsentURL("CID", "https://cb", "STATE", []string{"https://www.googleapis.com/auth/drive"})
	if !strings.Contains(u, "scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdrive") {
		t.Fatalf("scope missing: %s", u)
	}
	if !strings.Contains(u, "include_granted_scopes=true") {
		t.Fatalf("include_granted_scopes missing: %s", u)
	}
	if !strings.Contains(u, "client_id=CID") {
		t.Fatalf("client_id missing: %s", u)
	}
}

func TestAuthorizeURL(t *testing.T) {
	p := Provider{AuthorizeURL: "https://accounts/auth", DefaultScopes: []string{"a", "b"},
		AuthorizeExtra: map[string]string{"access_type": "offline", "prompt": "consent"}}
	u := p.ConsentURL("cid", "https://cb", "state123", p.DefaultScopes)
	for _, want := range []string{"client_id=cid", "state=state123", "access_type=offline", "prompt=consent", "scope=a+b", "response_type=code"} {
		if !strings.Contains(u, want) {
			t.Fatalf("authorize url missing %q: %s", want, u)
		}
	}
}
