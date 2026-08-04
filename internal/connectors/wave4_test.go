package connectors

import (
	"strings"
	"testing"
)

var wave4 = map[string]string{
	"openlibrary":   "Data & Reference",
	"openstreetmap": "Data & Reference",
	"openfoodfacts": "Data & Reference",
	"nextcloud":     "Self-hosted",
	"mealie":        "Self-hosted",
	"vikunja":       "Self-hosted",
	"gotify":        "Self-hosted",
	"linkwarden":    "Self-hosted",
	"portainer":     "Self-hosted",
	"fitbit":        "Health & Fitness",
	"oura":          "Health & Fitness",
	"spotify":       "Publishing & Media",
	"trakt":         "Publishing & Media",
}

func TestWave4ProvidersLoadWithTheRightCategory(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for name, cat := range wave4 {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("wave-4 provider %q not loaded", name)
			continue
		}
		if p.Category != cat {
			t.Errorf("%s category = %q, want %q", name, p.Category, cat)
		}
		if len(r.Actions(name)) == 0 {
			t.Errorf("%s has no actions", name)
		}
	}
}

// Health & Fitness held only Strava after wave 2. Wave 4 is what makes it a real
// category rather than a placeholder.
func TestHealthAndFitnessHasSeveralProviders(t *testing.T) {
	r, _ := LoadBundled()
	var got []string
	for _, name := range r.ProviderNames() {
		if p, _ := r.ProviderByName(name); p.Category == "Health & Fitness" {
			got = append(got, name)
		}
	}
	if len(got) < 3 {
		t.Errorf("Health & Fitness has %v, want at least three providers", got)
	}
}

// Three separate services block or throttle anonymous clients that do not identify
// themselves: Wikimedia 403s outright, and Nominatim and Open Library both require it
// by usage policy. A missing User-Agent fails at call time with an error that reads
// like a bad request rather than a missing header.
func TestPolicyBoundProvidersSendAUserAgent(t *testing.T) {
	r, _ := LoadBundled()
	for _, name := range []string{"wikipedia", "openstreetmap", "openlibrary", "openfoodfacts"} {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("%s not loaded", name)
			continue
		}
		ua := p.StaticHeaders["User-Agent"]
		if ua == "" {
			t.Errorf("%s must send a User-Agent — its operator blocks or throttles anonymous clients", name)
			continue
		}
		if !strings.Contains(ua, "Rookery") {
			t.Errorf("%s User-Agent = %q, want it to identify Rookery", name, ua)
		}
	}
}

// Nextcloud's OCS API refuses any request without OCS-APIRequest and defaults to XML
// without an explicit Accept. Both are mandatory, not politeness.
func TestNextcloudSendsTheOCSHeaders(t *testing.T) {
	r, _ := LoadBundled()
	p, ok := r.ProviderByName("nextcloud")
	if !ok {
		t.Fatal("nextcloud not loaded")
	}
	if p.StaticHeaders["OCS-APIRequest"] != "true" {
		t.Error("nextcloud must send OCS-APIRequest: true — the OCS API rejects requests without it")
	}
	if p.StaticHeaders["Accept"] != "application/json" {
		t.Error("nextcloud must ask for JSON — the OCS API returns XML by default, which the extractor cannot read")
	}
	// The app password is the Basic PASSWORD and the username comes from a connect input.
	if p.Auth.BasicUserTemplate != "{{conn.username}}" {
		t.Errorf("basic_user_template = %q, want {{conn.username}}", p.Auth.BasicUserTemplate)
	}
}

// Trakt rejects any request that does not declare the API version.
func TestTraktDeclaresItsAPIVersion(t *testing.T) {
	r, _ := LoadBundled()
	p, _ := r.ProviderByName("trakt")
	if p.StaticHeaders["trakt-api-version"] != "2" {
		t.Error("trakt must send trakt-api-version: 2")
	}
	if p.Auth.HeaderName != "trakt-api-key" {
		t.Errorf("auth header = %q, want trakt-api-key", p.Auth.HeaderName)
	}
}

// Fitbit and Spotify both require HTTP Basic client authentication on the token
// endpoint; sending the client id and secret in the body fails with invalid_client.
func TestOAuthProvidersNeedingBasicTokenAuthDeclareIt(t *testing.T) {
	r, _ := LoadBundled()
	for _, name := range []string{"fitbit", "spotify"} {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("%s not loaded", name)
			continue
		}
		if p.TokenAuth != "basic" {
			t.Errorf("%s token_auth = %q, want basic — the token endpoint rejects body credentials", name, p.TokenAuth)
		}
	}
}

func TestWave4SelfHostedProvidersUseBaseURL(t *testing.T) {
	r, _ := LoadBundled()
	for name, cat := range wave4 {
		if cat != "Self-hosted" {
			continue
		}
		p, _ := r.ProviderByName(name)
		var found bool
		for _, ci := range p.ConnectInputs {
			if ci.Key == "base_url" {
				found = true
				if !ci.Required || ci.Normalize != "base_url" {
					t.Errorf("%s: base_url must be required and normalized", name)
				}
			}
		}
		if !found {
			t.Errorf("%s has no base_url connect input", name)
			continue
		}
		for _, a := range r.Actions(name) {
			if !strings.Contains(a.Request.URL, "{{conn.base_url}}") {
				t.Errorf("%s/%s does not template {{conn.base_url}}", name, a.Name)
			}
		}
	}
}

// The keyless wave-4 providers were verified against their live APIs; the rest carry
// the marker. Withings was deliberately DROPPED rather than guessed: its token exchange
// posts action=requesttoken, which this framework cannot express.
func TestWave4VerificationStatus(t *testing.T) {
	r, _ := LoadBundled()
	verified := map[string]bool{"openlibrary": true, "openstreetmap": true, "openfoodfacts": true}
	for name := range wave4 {
		p, ok := r.ProviderByName(name)
		if !ok {
			continue
		}
		if verified[name] && p.Unverified {
			t.Errorf("%s is live-verified but marked unverified", name)
		}
		if !verified[name] && !p.Unverified {
			t.Errorf("%s was not live-verified, so its YAML must set unverified: true", name)
		}
	}
	if _, ok := r.ProviderByName("withings"); ok {
		t.Error("withings was dropped on purpose: its token exchange uses a non-standard " +
			"action=requesttoken body. Re-adding it needs framework support, not a YAML.")
	}
}

func TestReadOnlyWave4ProvidersHaveNoWrites(t *testing.T) {
	r, _ := LoadBundled()
	for _, name := range []string{"openlibrary", "openstreetmap", "openfoodfacts", "fitbit", "oura", "spotify", "trakt"} {
		for _, a := range r.Actions(name) {
			if a.Mutating {
				t.Errorf("%s/%s is mutating, but this provider is exposed read-only", name, a.Name)
			}
		}
	}
}
