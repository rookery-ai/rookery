package connectors

import (
	"net/http"
	"testing"
)

func newReq(t *testing.T, u string) *http.Request {
	r, err := http.NewRequest("GET", u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyAuthOAuthBearerDefault(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{}, "TOK", nil) // no auth block → oauth2 Bearer
	if got := req.Header.Get("Authorization"); got != "Bearer TOK" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyAuthHeaderPrefix(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "header", HeaderName: "Authorization", ValuePrefix: "Bearer "}}, "sk-1", nil)
	if got := req.Header.Get("Authorization"); got != "Bearer sk-1" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyAuthQueryParam(t *testing.T) {
	req := newReq(t, "https://api/x?a=1")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "query", ParamName: "api_key"}}, "K", nil)
	if got := req.URL.Query().Get("api_key"); got != "K" {
		t.Fatalf("query api_key=%q, url=%s", got, req.URL.String())
	}
}

func TestApplyAuthBasic(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "basic"}}, "sk_live", nil)
	u, p, ok := req.BasicAuth()
	if !ok || u != "sk_live" || p != "" {
		t.Fatalf("basic auth wrong: u=%q p=%q ok=%v", u, p, ok)
	}
}
