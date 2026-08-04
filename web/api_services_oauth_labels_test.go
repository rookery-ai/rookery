package web

import (
	"net/http"
	"strings"
	"testing"
)

// The labels describe an OAuth APP, and twelve providers do not have one of their own:
// the nine google_*, youtube, linkedin_ads and teams all reuse a parent's app through
// auth_parent. Reading the CHILD's record would print "Client ID" over a form that feeds
// a Microsoft registration whose console says "Application (client) ID" — a bug that
// looks correct in api_services.go and in every YAML file. This is the one place a
// plausible implementation is silently wrong, so it gets its own test.
func TestChildProvidersInheritParentCredLabels(t *testing.T) {
	s, cookies, _ := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// outlook declares it; teams must inherit it.
	if n := strings.Count(body, `"id_label":"Application (client) ID"`); n < 2 {
		t.Errorf(`"Application (client) ID" appears %d time(s), want >= 2 (outlook declares it, teams inherits it)`, n)
	}
	// A provider that declares its own labels still carries them.
	if !contains(body, `"id_label":"App ID"`) {
		t.Error("Meta's App ID label never reached the services payload")
	}
	// A value struct, not a pointer: api_services_test.go asserts nothing on this
	// payload serializes as null, and every api_key and keyless provider carries the
	// field too.
	if contains(body, `"oauth_creds":null`) {
		t.Error(`oauth_creds serialized as null — it must be a value struct, not a pointer`)
	}
}
