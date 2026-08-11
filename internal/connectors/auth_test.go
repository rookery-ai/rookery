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
	applyAuth(req, Provider{}, "TOK", nil, nil) // no auth block → oauth2 Bearer
	if got := req.Header.Get("Authorization"); got != "Bearer TOK" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyAuthHeaderPrefix(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "header", HeaderName: "Authorization", ValuePrefix: "Bearer "}}, "sk-1", nil, nil)
	if got := req.Header.Get("Authorization"); got != "Bearer sk-1" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyAuthQueryParam(t *testing.T) {
	req := newReq(t, "https://api/x?a=1")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "query", ParamName: "api_key"}}, "K", nil, nil)
	if got := req.URL.Query().Get("api_key"); got != "K" {
		t.Fatalf("query api_key=%q, url=%s", got, req.URL.String())
	}
}

func TestApplyAuthBasic(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "basic"}}, "sk_live", nil, nil)
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

	applyAuth(req, prov, "", nil, nil)

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

	applyAuth(req, prov, "TOKEN123", nil, nil)

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
	applyAuth(req, prov, "SECRET", map[string]string{"email": "a@b.c"}, nil)
	u, p, _ := req.BasicAuth()
	if u != "a@b.c/token" || p != "SECRET" {
		t.Errorf("got %q/%q, want the templated user with the credential as password", u, p)
	}
}

// ── sigv4 ───────────────────────────────────────────────────────────────────

func sigV4Provider() Provider {
	return Provider{Auth: AuthConfig{Kind: "sigv4"}}
}

func TestSigV4SignsWithTheConnectionsRegionAndService(t *testing.T) {
	req := newReq(t, "https://ec2.us-west-2.amazonaws.com/?Action=DescribeInstances")
	extra := map[string]string{
		"access_key_id": "AKIDEXAMPLE",
		"region":        "us-west-2",
		"service":       "ec2",
	}
	if err := applyAuth(req, sigV4Provider(), "SECRETKEY", extra, nil); err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	auth := req.Header.Get("Authorization")
	for _, want := range []string{
		"AWS4-HMAC-SHA256",
		"Credential=AKIDEXAMPLE/",
		"/us-west-2/ec2/aws4_request",
		"Signature=",
	} {
		if !contains(auth, want) {
			t.Errorf("Authorization %q missing %q", auth, want)
		}
	}
}

// The payload is part of the signature, so two different bodies must not sign
// to the same thing — the whole point of threading `body` through applyAuth.
func TestSigV4SignatureCoversTheBody(t *testing.T) {
	extra := map[string]string{"access_key_id": "AK", "region": "us-east-1", "service": "lambda"}

	a := newReq(t, "https://lambda.us-east-1.amazonaws.com/f")
	if err := applyAuth(a, sigV4Provider(), "SK", extra, []byte(`{"x":1}`)); err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	b := newReq(t, "https://lambda.us-east-1.amazonaws.com/f")
	if err := applyAuth(b, sigV4Provider(), "SK", extra, []byte(`{"x":2}`)); err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	if a.Header.Get("Authorization") == b.Header.Get("Authorization") {
		t.Fatal("different bodies signed identically — the payload hash is not reaching the signer")
	}
}

// A connection missing its region or service cannot be signed, and must say so
// rather than sending an unsigned request that fails opaquely at AWS.
func TestSigV4RejectsAnIncompleteConnection(t *testing.T) {
	req := newReq(t, "https://s3.amazonaws.com/bucket")
	err := applyAuth(req, sigV4Provider(), "SK", map[string]string{"access_key_id": "AK"}, nil)
	if err == nil {
		t.Fatal("want an error when region and service are absent")
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("a failed signing must not leave an Authorization header behind")
	}
}

// The arg names are configurable, defaulting to access_key_id/region/service.
func TestSigV4HonoursConfiguredArgNames(t *testing.T) {
	prov := Provider{Auth: AuthConfig{
		Kind:         "sigv4",
		AccessKeyArg: "aws_key",
		RegionArg:    "aws_region",
		ServiceArg:   "aws_service",
	}}
	req := newReq(t, "https://s3.amazonaws.com/bucket")
	extra := map[string]string{"aws_key": "AKCUSTOM", "aws_region": "eu-central-1", "aws_service": "s3"}
	if err := applyAuth(req, prov, "SK", extra, nil); err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	if !contains(req.Header.Get("Authorization"), "Credential=AKCUSTOM/") {
		t.Errorf("configured arg names ignored: %s", req.Header.Get("Authorization"))
	}
}
