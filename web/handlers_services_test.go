package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestServicesRedirect_OAuthCallback_ErrorPath_GoesToApp exercises
// handleOAuthCallback's early-exit "authorization was denied" branch, which
// needs no token exchange (no network mocking available in this harness). The
// callback is the one service route still registered standalone after the
// template UI was deleted — its exact path is a frozen external-OAuth redirect
// URI. A real success-path assertion would require stubbing the provider's
// token endpoint, which the test harness doesn't support.
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
	if !strings.HasPrefix(loc, "/connections?error=") {
		t.Fatalf("expected redirect to /connections?error=..., got %q", loc)
	}
}
