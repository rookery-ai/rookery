package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOwnerVerifyCorrectPassword(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/owner-verify",
		map[string]string{"password": "password123"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-verify = %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK            bool   `json:"ok"`
		VerifiedUntil string `json:"verified_until"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.OK {
		t.Error("ok = false")
	}
	until, err := time.Parse(time.RFC3339, body.VerifiedUntil)
	if err != nil {
		t.Fatalf("verified_until %q: %v", body.VerifiedUntil, err)
	}
	if d := time.Until(until); d <= 0 || d > ownerVerifyTTL+time.Minute {
		t.Errorf("verified_until is %v away, want ~%v", d, ownerVerifyTTL)
	}
}

func TestOwnerVerifyWrongPassword(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/owner-verify",
		map[string]string{"password": "definitely-not-it"}, cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("owner-verify = %d, want 401; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_password") {
		t.Errorf("want invalid_password, got %s", rec.Body.String())
	}
}

// The username must come from the session, never the request. There is exactly
// one owner, so accepting a client-supplied username adds an oracle for nothing.
func TestOwnerVerifyIgnoresRequestUsername(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/owner-verify",
		map[string]string{"username": "someone-else", "password": "password123"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-verify = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

func TestSessionReportsOwnerVerified(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	var before struct {
		OwnerVerified bool `json:"owner_verified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if before.OwnerVerified {
		t.Error("owner_verified should be false before verifying")
	}

	cookies = verifyOwnerCookies(t, s, cookies)

	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	var after struct {
		OwnerVerified bool `json:"owner_verified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !after.OwnerVerified {
		t.Error("owner_verified should be true after verifying")
	}
}

// A wrong password is a security event and must be recorded, exactly as
// unlock_failed is.
func TestOwnerVerifyFailureIsAudited(t *testing.T) {
	s, database := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	doJSON(t, s, http.MethodPost, "/api/v1/auth/owner-verify",
		map[string]string{"password": "nope"}, cookies)

	logs, err := database.ListAuditLogs(50)
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	for _, l := range logs {
		if l.Action == "owner_verify_failed" {
			return
		}
	}
	t.Error("no owner_verify_failed audit row written")
}

// Every guarded route 403s without a stamp, and answers with one.
func TestOwnerGateBlocksAndAllows(t *testing.T) {
	guarded := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/overview"},
		{http.MethodGet, "/api/v1/admin/audit"},
		{http.MethodGet, "/api/v1/admin/settings"},
		{http.MethodGet, "/api/v1/admin/public-url"},
		{http.MethodGet, "/api/v1/backup/config"},
		{http.MethodGet, "/api/v1/backup/snapshots"},
	}
	for _, r := range guarded {
		s, _ := newAPITestServer(t)
		cookies := bootstrapAndLogin(t, s)

		rec := doJSON(t, s, r.method, r.path, nil, cookies)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without verification = %d, want 403", r.method, r.path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "owner_verification_required") {
			t.Errorf("%s %s: want owner_verification_required, got %s", r.method, r.path, rec.Body.String())
		}

		cookies = verifyOwnerCookies(t, s, cookies)
		if rec := doJSON(t, s, r.method, r.path, nil, cookies); rec.Code == http.StatusForbidden {
			t.Errorf("%s %s still 403 after verification: %s", r.method, r.path, rec.Body.String())
		}
	}
}

// A stale stamp must be refused, or the TTL is decoration. The stamp is written
// directly into a saved session so the test does not depend on wall-clock time.
func TestOwnerGateExpires(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies = verifyOwnerCookies(t, s, cookies)

	// Sanity: the fresh stamp opens the gate.
	if rec := doJSON(t, s, http.MethodGet, "/api/v1/admin/overview", nil, cookies); rec.Code == http.StatusForbidden {
		t.Fatalf("fresh stamp should pass, got %s", rec.Body.String())
	}

	// Rewrite the stamp to just outside the window and re-save the session.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	sess, err := s.store.Get(req, sessionName)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	sess.Values["owner_verified_at"] = time.Now().Add(-ownerVerifyTTL - time.Minute).Unix()
	if err := sess.Save(req, rec); err != nil {
		t.Fatalf("save session: %v", err)
	}
	stale := rec.Result().Cookies()

	got := doJSON(t, s, http.MethodGet, "/api/v1/admin/overview", nil, stale)
	if got.Code != http.StatusForbidden {
		t.Errorf("stale stamp = %d, want 403", got.Code)
	}
	if !strings.Contains(got.Body.String(), "owner_verification_required") {
		t.Errorf("stale stamp should report the gate, got %s", got.Body.String())
	}
}

// Ordering: an unauthenticated caller must not learn a verification gate exists.
func TestOwnerGateOrderedAfterAuth(t *testing.T) {
	s, _ := newAPITestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/overview", nil) // no cookie
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no session = %d, want 401 from requireOwnerAPI", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "owner_verification_required") {
		t.Errorf("an unauthenticated caller must not see the gate: %s", rec.Body.String())
	}
}

// Workspace entry, listing, and the escape hatches stay reachable, or the gate
// would be inescapable.
func TestOwnerGateLeavesEscapeHatchesOpen(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/session"},
		{http.MethodGet, "/api/v1/workspaces"},
	} {
		if rec := doJSON(t, s, r.method, r.path, nil, cookies); rec.Code == http.StatusForbidden {
			t.Errorf("%s %s must not be gated, got 403: %s", r.method, r.path, rec.Body.String())
		}
	}
}

// THE test that keeps this honest: a route added to the owner group later must
// not silently skip the gate. Enumerated from the router, in the mould of
// TestAPIParityInventory.
func TestEveryInstallLevelRouteIsGated(t *testing.T) {
	s, _ := newAPITestServer(t)
	var checked int
	for _, r := range s.echo.Routes() {
		gated := strings.HasPrefix(r.Path, "/api/v1/admin/") ||
			strings.HasPrefix(r.Path, "/api/v1/backup/") ||
			(r.Path == "/api/v1/workspaces/:id" && r.Method == http.MethodDelete) ||
			(r.Path == "/api/v1/workspaces" && r.Method == http.MethodPost)
		if !gated {
			continue
		}
		checked++

		s2, _ := newAPITestServer(t)
		cookies := bootstrapAndLogin(t, s2)
		rec := doJSON(t, s2, r.Method, concreteRoutePath(r.Path), nil, cookies)
		if rec.Code != http.StatusForbidden ||
			!strings.Contains(rec.Body.String(), "owner_verification_required") {
			t.Errorf("%s %s is not behind requireOwnerVerified (got %d: %s)",
				r.Method, r.Path, rec.Code, rec.Body.String())
		}
	}
	if checked < 12 {
		t.Errorf("only %d install-level routes found; the enumeration is probably wrong", checked)
	}
}

// concreteRoutePath substitutes a dummy value for each :param so the route
// matches. The gate runs before any handler, so the value never has to exist.
func concreteRoutePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, ":") {
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}
