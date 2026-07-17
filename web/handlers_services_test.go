package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestStateSignVerify(t *testing.T) {
	secret := []byte("system-key-or-any-secret-32bytes")
	payload := "ws1~google~work~nonce"
	tok := signState(secret, payload, time.Now())
	got, ok := verifyState(secret, tok, time.Now())
	if !ok || got != payload {
		t.Fatalf("verify: %q %v", got, ok)
	}
	if _, ok := verifyState(secret, tok, time.Now().Add(11*time.Minute)); ok {
		t.Fatal("expired state must fail")
	}
	if _, ok := verifyState(secret, tok+"x", time.Now()); ok {
		t.Fatal("tampered state must fail")
	}
}

// doForm posts a application/x-www-form-urlencoded request (the template
// dashboard handlers read via c.FormValue, unlike the JSON API in
// api_services.go) and returns the recorder — used to inspect the redirect
// Location header these handlers issue.
func doForm(t *testing.T, s *Server, path string, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

// The services template dashboard handlers (web/handlers_services.go) used to
// redirect back to the server-rendered "/dashboard/connectors/services" page
// on every outcome. The SPA replaces that page, so every redirect — success
// and error alike — now lands on "/app/connections" (the old template page
// itself stays reachable directly via GET, only the POST-handler redirect
// targets changed). See task-5 brief.

func TestServicesRedirect_SaveCreds_MissingFields_GoesToApp(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doForm(t, s, "/dashboard/connectors/services/google/creds", url.Values{
		"client_id": {"only-id"},
	}, cookies)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/app/connections?error=") {
		t.Fatalf("expected redirect to /app/connections?error=..., got %q", loc)
	}
}

func TestServicesRedirect_SaveCreds_Success_GoesToApp(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doForm(t, s, "/dashboard/connectors/services/google/creds", url.Values{
		"client_id":     {"test-client-id"},
		"client_secret": {"test-client-secret"},
	}, cookies)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc != "/app/connections" {
		t.Fatalf("expected redirect to /app/connections, got %q", loc)
	}
}

func TestServicesRedirect_ConnectAPIKey_UnknownProvider_GoesToApp(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doForm(t, s, "/dashboard/connectors/services/not-a-real-provider/apikey", url.Values{
		"api_key": {"sk-test"},
	}, cookies)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/app/connections?error=") {
		t.Fatalf("expected redirect to /app/connections?error=..., got %q", loc)
	}
}

func TestServicesRedirect_ConnectAPIKey_Success_GoesToApp(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doForm(t, s, "/dashboard/connectors/services/openai/apikey", url.Values{
		"api_key": {"sk-test-key"},
	}, cookies)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc != "/app/connections" {
		t.Fatalf("expected redirect to /app/connections, got %q", loc)
	}
}

func TestServicesRedirect_DeleteConnection_NotFound_GoesToApp(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doForm(t, s, "/dashboard/connectors/services/not-a-real-id/delete", url.Values{}, cookies)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/app/connections?error=") {
		t.Fatalf("expected redirect to /app/connections?error=..., got %q", loc)
	}
}

// TestServicesRedirect_OAuthCallback_ErrorPath_GoesToApp exercises
// handleOAuthCallback's early-exit "authorization was denied" branch, which
// needs no token exchange (no network mocking available in this harness).
// A real success-path assertion would require stubbing the provider's token
// endpoint, which the test harness doesn't support — see task-5 report.
func TestServicesRedirect_OAuthCallback_ErrorPath_GoesToApp(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/connectors/services/callback/google?error=access_denied", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec2 := httptest.NewRecorder()
	s.echo.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", rec2.Code, rec2.Body.String())
	}
	loc := rec2.Header().Get("Location")
	if !strings.HasPrefix(loc, "/app/connections?error=") {
		t.Fatalf("expected redirect to /app/connections?error=..., got %q", loc)
	}
}
