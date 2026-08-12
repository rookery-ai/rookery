package web

import (
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/connectors"
)

// newRegistryServer builds a Server carrying only the bundled connector registry.
// Every assertion below is about provider resolution, which needs no database.
func newRegistryServer(t *testing.T) *Server {
	t.Helper()
	reg, err := connectors.LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	return &Server{connectors: reg}
}

// TestOAuthAppNameResolvesAliasedChildrenToTheirParent pins the fix for the
// defect this file exists for: an aliased provider authenticates through its
// parent's OAuth application, so the redirect URI must be the PARENT's. Building
// it from the child name sent google_calendar a URI that Google Cloud had never
// been told about, and the rejection lands on the consent screen — before a code
// is issued, and therefore before explainOAuthError can translate it.
func TestOAuthAppNameResolvesAliasedChildrenToTheirParent(t *testing.T) {
	s := newRegistryServer(t)

	for _, tc := range []struct{ child, want string }{
		{"google_calendar", "google"},
		{"google_drive", "google"},
		{"google_sheets", "google"},
		{"google_docs", "google"},
		{"google_tasks", "google"},
		{"google_analytics", "google"},
		{"google_searchconsole", "google"},
		{"google_adsense", "google"},
		{"google_ads", "google"},
		{"google_health", "google"},
		{"youtube", "google"},
		{"teams", "outlook"},
		{"linkedin_ads", "linkedin"},
	} {
		if got := s.oauthAppName(tc.child); got != tc.want {
			t.Errorf("oauthAppName(%q) = %q, want %q", tc.child, got, tc.want)
		}
	}
}

// TestOAuthAppNameIsIdentityForUnaliasedProviders guards the other direction: a
// provider owning its own OAuth app must keep its own callback path, which is the
// URI every existing install already has registered.
func TestOAuthAppNameIsIdentityForUnaliasedProviders(t *testing.T) {
	s := newRegistryServer(t)

	for _, name := range []string{"google", "github", "notion", "outlook", "linkedin"} {
		if got := s.oauthAppName(name); got != name {
			t.Errorf("oauthAppName(%q) = %q, want it unchanged", name, got)
		}
	}
}

// TestOAuthAppNameFallsBackToItselfForUnknownProviders documents why the helper
// does not return "": callers build a URL by concatenation, so an empty value
// would produce a silently truncated redirect URI rather than a visible error.
func TestOAuthAppNameFallsBackToItselfForUnknownProviders(t *testing.T) {
	s := newRegistryServer(t)

	if got := s.oauthAppName("definitely-not-a-provider"); got != "definitely-not-a-provider" {
		t.Errorf("oauthAppName(unknown) = %q, want the input back", got)
	}
}

// TestGoogleFamilyShareOneRedirectURI is the property that makes this change
// worth making: one registered URI covers the whole family, so a user who set up
// Gmail can connect Calendar without visiting the Google Cloud console at all.
func TestGoogleFamilyShareOneRedirectURI(t *testing.T) {
	s := newRegistryServer(t)

	const base = "https://rookery.example.com"
	want := base + "/dashboard/connectors/services/callback/google"

	for _, child := range []string{"google", "google_calendar", "google_drive", "youtube"} {
		got := base + "/dashboard/connectors/services/callback/" + s.oauthAppName(child)
		if got != want {
			t.Errorf("redirect URI for %q = %q, want %q", child, got, want)
		}
	}
}

// TestGoogleRequestsTheAccountChooser pins the multi-account fix. "consent" alone
// reuses whichever Google account is already signed in, so a second connect
// silently re-authorized the first and multi-account was unreachable in practice.
// Dropping "consent" is equally wrong — Google stops issuing a refresh token on
// re-consent without it — so both values must survive.
func TestGoogleRequestsTheAccountChooser(t *testing.T) {
	s := newRegistryServer(t)

	p, ok := s.connectors.ProviderByName("google")
	if !ok {
		t.Fatal("google provider missing from the bundled registry")
	}
	prompt := p.AuthorizeExtra["prompt"]
	if !strings.Contains(prompt, "select_account") {
		t.Errorf("google prompt = %q, want it to contain select_account", prompt)
	}
	if !strings.Contains(prompt, "consent") {
		t.Errorf("google prompt = %q, want it to retain consent (refresh-token issuance)", prompt)
	}
}

// TestOutlookRequestsTheAccountChooser covers the second OAuth app that has an
// aliased child (teams), for the same reason.
func TestOutlookRequestsTheAccountChooser(t *testing.T) {
	s := newRegistryServer(t)

	p, ok := s.connectors.ProviderByName("outlook")
	if !ok {
		t.Fatal("outlook provider missing from the bundled registry")
	}
	if got := p.AuthorizeExtra["prompt"]; !strings.Contains(got, "select_account") {
		t.Errorf("outlook prompt = %q, want it to contain select_account", got)
	}
}

// TestSetupModeNamesTheTwoGuidanceShapes keeps the server and the SPA agreeing on
// the two literal values; the wizard switches its entire guidance block on them.
func TestSetupModeNamesTheTwoGuidanceShapes(t *testing.T) {
	if got := setupMode(true); got != "update" {
		t.Errorf("setupMode(true) = %q, want update", got)
	}
	if got := setupMode(false); got != "create" {
		t.Errorf("setupMode(false) = %q, want create", got)
	}
}

// TestDuplicateDecision covers the three cases the upsert used to collapse into
// one. The third row is the data-loss case: connecting a DIFFERENT account under
// an existing label silently overwrote the first one's tokens, and every agent
// bound to it quietly started acting as the new account.
func TestDuplicateDecision(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		existingLabel, existingID string
		incomingLabel, incomingID string
		wantRefused               bool
	}{
		{"reconnect same account", "work", "a@example.com", "work", "a@example.com", false},
		{"second distinct account", "work", "a@example.com", "personal", "b@example.com", false},
		{"same label, different account", "work", "a@example.com", "work", "b@example.com", true},
		{"same account, different label", "work", "a@example.com", "personal", "a@example.com", true},
		{"no identity available, same label", "work", "", "work", "", false},
		{"no incoming identity, same label", "work", "a@example.com", "work", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := duplicateDecision(tc.existingLabel, tc.existingID, tc.incomingLabel, tc.incomingID)
			if (got != "") != tc.wantRefused {
				t.Errorf("duplicateDecision(%q,%q,%q,%q) = %q, wantRefused=%v",
					tc.existingLabel, tc.existingID, tc.incomingLabel, tc.incomingID, got, tc.wantRefused)
			}
		})
	}
}
