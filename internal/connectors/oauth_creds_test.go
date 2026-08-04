package connectors

import (
	"strings"
	"testing"
)

// The labels are DERIVED from each provider's own setup_steps, which name the console
// field verbatim ("Copy the App key (client id) and App secret"). This ties them back to
// that source: rename a label without touching the prose and the card says "App ID" above
// a step telling the user to copy the "Client ID". That divergence is invisible in either
// file alone, which is why it needs a test rather than a review convention.
func TestOAuthCredLabelsMatchSetupSteps(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range r.ProviderNames() {
		p, _ := r.ProviderByName(name)
		steps := strings.ToLower(strings.Join(p.SetupSteps, " "))
		for _, f := range []struct{ what, label string }{
			{"id_label", p.OAuthCreds.IDLabel},
			{"secret_label", p.OAuthCreds.SecretLabel},
		} {
			if f.label == "" {
				continue
			}
			if !strings.Contains(steps, strings.ToLower(f.label)) {
				t.Errorf("%s %s = %q but no setup step mentions it — the connect form and the instructions disagree",
					name, f.what, f.label)
			}
		}
	}
}

// The thirteen providers whose console diverges from "Client ID"/"Client secret", pinned
// by name against their FULL expected value — labels and hints, empty where the default
// is deliberate. TestOAuthCredLabelsMatchSetupSteps proves a declared label agrees with
// its prose; it says nothing when a label is deleted, which would silently revert the
// field to "Client ID" with every other test still green. This is the test that notices.
//
// Deliberately not a "every OAuth provider must declare" rule: the default is genuinely
// correct for github, slack, google, spotify, strava, jira, linkedin and oura, and forcing
// a declaration there would only invite wrong entries.
func TestDivergentOAuthLabelsStayDeclared(t *testing.T) {
	want := map[string]OAuthCreds{
		"dropbox":   {IDLabel: "App key", SecretLabel: "App secret"},
		"facebook":  {IDLabel: "App ID", SecretLabel: "App Secret"},
		"instagram": {IDLabel: "App ID", SecretLabel: "App Secret"},
		"meta_ads":  {IDLabel: "App ID", SecretLabel: "App Secret"},
		"threads": {
			IDLabel:     "Threads App ID",
			IDHint:      "under the Threads API product — DIFFERENT from the Facebook app id",
			SecretLabel: "Threads App Secret",
		},
		"pinterest":  {IDLabel: "App ID", SecretLabel: "App secret key"},
		"salesforce": {IDLabel: "Consumer Key", SecretLabel: "Consumer Secret"},
		"tiktok":     {IDLabel: "Client key", SecretLabel: "Client secret"},
		"mastodon": {
			IDLabel:     "Client key",
			IDHint:      "from Preferences → Development on YOUR OWN instance, not a central provider",
			SecretLabel: "Client secret",
		},
		"outlook": {
			IDLabel:    "Application (client) ID",
			SecretHint: "the secret's Value, not the Secret ID",
		},
		"reddit": {
			IDLabel:     "client ID",
			IDHint:      "the string shown under the app name, not the app name itself",
			SecretLabel: "secret",
		},
		"notion": {IDLabel: "OAuth client ID", SecretLabel: "OAuth client secret"},
		// X's labels are already right; the portal shows TWO credential pairs, so the
		// hints are the entire point of this row.
		"x": {
			IDHint:     "from User authentication settings → OAuth 2.0 — NOT the API Key on the Keys and tokens tab",
			SecretHint: "the OAuth 2.0 Client Secret — NOT the API Key Secret",
		},
	}
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for name, w := range want {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("provider %s is missing from the catalog", name)
			continue
		}
		if p.OAuthCreds != w {
			t.Errorf("%s oauth_creds = %+v, want %+v", name, p.OAuthCreds, w)
		}
	}
}

// Two ways a declaration can be wrong without any other test noticing.
//
// A whitespace-only value is truthy in the SPA's `label || "Client ID"` fallback, so it
// renders as a blank label rather than degrading to the default — worse than declaring
// nothing.
//
// And a provider with auth_parent set has no OAuth app of its own: web/api_services.go
// resolves teams → outlook and google_calendar → google via OAuthProvider() BEFORE reading
// these labels, so a block on the child is never shown. Leaving one in place would assert
// something false about where the credentials go, and would look correct in the YAML.
func TestOAuthCredLabelsAreNonBlank(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range r.ProviderNames() {
		p, _ := r.ProviderByName(name)
		oc := p.OAuthCreds
		if p.AuthParent != "" && oc != (OAuthCreds{}) {
			t.Errorf("%s reuses %s's OAuth app but declares oauth_creds — it would never be read",
				name, p.AuthParent)
		}
		for what, v := range map[string]string{
			"id_label":     oc.IDLabel,
			"id_hint":      oc.IDHint,
			"secret_label": oc.SecretLabel,
			"secret_hint":  oc.SecretHint,
		} {
			if v != "" && strings.TrimSpace(v) == "" {
				t.Errorf("%s %s is whitespace-only — it renders as a blank label instead of falling back", name, what)
			}
		}
	}
}
