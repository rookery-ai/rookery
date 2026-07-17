package web

import (
	"net/http"
	"net/url"
	"testing"
)

// TestHandleSetupPostBlockedAfterCompletion is the template-dashboard
// counterpart of TestAPISetupPostBlockedAfterCompletion (SP5 final review
// fix): once a workspace's setup is complete (needs_setup=0), POST
// /dashboard/setup must not replay a setup step — it must redirect to
// /dashboard instead of, e.g., silently rotating the master password via the
// step-2 form. GET /dashboard/setup stays reachable (harmless).
func TestHandleSetupPostBlockedAfterCompletion(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doForm(t, s, "/dashboard/setup", url.Values{
		"action":          {"master_password"},
		"master_password": {"sneaky-new-password"},
		"confirm":         {"sneaky-new-password"},
	}, cookies)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected a redirect, got %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard once setup is complete, got %q", loc)
	}
}
