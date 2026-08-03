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

// A keyless provider (Open-Meteo) has no credential at all. The request must go out
// exactly as rendered: falling through to the default Bearer branch would send
// "Authorization: Bearer " with an empty value, which some servers reject outright.
func TestApplyAuthKeylessLeavesRequestUntouched(t *testing.T) {
	req := newReq(t, "https://api.open-meteo.com/v1/forecast?latitude=41.99")
	prov := Provider{Name: "open_meteo", Auth: AuthConfig{Kind: "none"}}

	applyAuth(req, prov, "", nil)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header = %q, want empty", got)
	}
	if u, p, ok := req.BasicAuth(); ok {
		t.Errorf("basic auth set to %q/%q, want none", u, p)
	}
	if got := req.URL.RawQuery; got != "latitude=41.99" {
		t.Errorf("query = %q, want it unmodified", got)
	}
}

func TestIsKeylessPredicate(t *testing.T) {
	if !(Provider{Auth: AuthConfig{Kind: "none"}}).IsKeyless() {
		t.Error("kind=none should be keyless")
	}
	for _, k := range []string{"", "oauth2", "api_key", "session_exchange"} {
		if (Provider{Auth: AuthConfig{Kind: k}}).IsKeyless() {
			t.Errorf("kind=%q should not be keyless", k)
		}
	}
}

// Toggl Track is the inverse of every other Basic provider: the CREDENTIAL is the
// username and the password is a literal constant ("api_token"). BasicUserTemplate
// templates the username and uses the credential as the password, so it cannot express
// this — hence BasicPassLiteral.
func TestApplyAuthBasicPassLiteral(t *testing.T) {
	req := newReq(t, "https://api.track.toggl.com/api/v9/me")
	prov := Provider{Name: "toggl", Auth: AuthConfig{
		Kind: "api_key", Placement: "basic", BasicPassLiteral: "api_token",
	}}

	applyAuth(req, prov, "TOKEN123", nil)

	u, p, ok := req.BasicAuth()
	if !ok {
		t.Fatal("no basic auth set")
	}
	if u != "TOKEN123" {
		t.Errorf("username = %q, want the credential", u)
	}
	if p != "api_token" {
		t.Errorf("password = %q, want the literal api_token", p)
	}
}

// BasicUserTemplate must keep winning where it is set — Zendesk and Twilio depend on
// the credential being the PASSWORD, which is the opposite arrangement.
func TestApplyAuthBasicUserTemplateStillWins(t *testing.T) {
	req := newReq(t, "https://x/y")
	prov := Provider{Auth: AuthConfig{
		Kind: "api_key", Placement: "basic", BasicUserTemplate: "{{conn.email}}/token",
	}}
	applyAuth(req, prov, "SECRET", map[string]string{"email": "a@b.c"})
	u, p, _ := req.BasicAuth()
	if u != "a@b.c/token" || p != "SECRET" {
		t.Errorf("got %q/%q, want the templated user with the credential as password", u, p)
	}
}
