package connectors

import (
	"strings"
	"testing"
)

// wave2 is the provider set added in the second everyday-connector wave, with the
// category each must declare. Kept as one table so a provider cannot be added to the
// registry without appearing here.
var wave2 = map[string]string{
	"readwise":    "Productivity",
	"toggl":       "Productivity",
	"ntfy":        "Communication",
	"jellyfin":    "Self-hosted",
	"adguard":     "Self-hosted",
	"miniflux":    "Self-hosted",
	"firefly_iii": "Finance",
	"tmdb":        "Data & Reference",
	"wikipedia":   "Data & Reference",
	"hackernews":  "Data & Reference",
	"frankfurter": "Finance",
	"strava":      "Health & Fitness",
}

func TestWave2ProvidersLoadWithTheRightCategory(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for name, cat := range wave2 {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("wave-2 provider %q not loaded", name)
			continue
		}
		if p.Category != cat {
			t.Errorf("%s category = %q, want %q", name, p.Category, cat)
		}
		if len(r.Actions(name)) == 0 {
			t.Errorf("%s has no actions — did its manifest load?", name)
		}
	}
}

// Strava is the first provider in Health & Fitness, which shipped empty in wave 1.
func TestStravaFillsHealthAndFitness(t *testing.T) {
	r, _ := LoadBundled()
	found := false
	for _, name := range r.ProviderNames() {
		if p, _ := r.ProviderByName(name); p.Category == "Health & Fitness" {
			found = true
		}
	}
	if !found {
		t.Error("Health & Fitness is still empty — Strava should populate it")
	}
}

// Every self-hosted provider must template the per-connection base URL in EVERY action
// and declare the normalizer, or a pasted URL of the wrong shape breaks at call time
// rather than at connect time.
func TestWave2SelfHostedProvidersUseBaseURL(t *testing.T) {
	r, _ := LoadBundled()
	for _, name := range []string{"jellyfin", "adguard", "miniflux", "firefly_iii", "ntfy"} {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("%s not loaded", name)
			continue
		}
		var found bool
		for _, ci := range p.ConnectInputs {
			if ci.Key == "base_url" {
				found = true
				if !ci.Required {
					t.Errorf("%s: base_url must be required", name)
				}
				if ci.Normalize != "base_url" {
					t.Errorf("%s: base_url normalize = %q, want base_url", name, ci.Normalize)
				}
			}
		}
		if !found {
			t.Errorf("%s has no base_url connect input", name)
			continue
		}
		for _, a := range r.Actions(name) {
			if !strings.Contains(a.Request.URL, "{{conn.base_url}}") {
				t.Errorf("%s/%s URL = %q, want it to template {{conn.base_url}}", name, a.Name, a.Request.URL)
			}
		}
	}
}

// Toggl is the reason BasicPassLiteral exists. If the YAML loses it, every call gets
// the credential as the username and an EMPTY password, which Toggl rejects — an auth
// failure that reads as a bad token rather than a bad auth shape.
func TestTogglUsesTheBasicPassLiteral(t *testing.T) {
	r, _ := LoadBundled()
	p, ok := r.ProviderByName("toggl")
	if !ok {
		t.Fatal("toggl not loaded")
	}
	if p.Auth.Placement != "basic" {
		t.Errorf("placement = %q, want basic", p.Auth.Placement)
	}
	if p.Auth.BasicPassLiteral != "api_token" {
		t.Errorf("basic_pass_literal = %q, want api_token", p.Auth.BasicPassLiteral)
	}
}

// AdGuard Home has no separate API key: it reuses the web-UI credentials, so the
// username is a connect input and the password is the pasted credential.
func TestAdGuardUsesTemplatedBasicUser(t *testing.T) {
	r, _ := LoadBundled()
	p, _ := r.ProviderByName("adguard")
	if p.Auth.BasicUserTemplate != "{{conn.username}}" {
		t.Errorf("basic_user_template = %q, want {{conn.username}}", p.Auth.BasicUserTemplate)
	}
}

// Wikimedia BLOCKS the default Go user-agent (403, robot policy). Without a descriptive
// one every Wikipedia action fails — found by live verification, pinned here because
// nothing else in the test suite would catch its removal.
func TestWikipediaSendsAUserAgent(t *testing.T) {
	r, _ := LoadBundled()
	p, _ := r.ProviderByName("wikipedia")
	ua := p.StaticHeaders["User-Agent"]
	if ua == "" {
		t.Fatal("wikipedia must send a User-Agent — Wikimedia 403s the Go default")
	}
	if !strings.Contains(ua, "Rookery") {
		t.Errorf("User-Agent = %q, want it to identify Rookery per Wikimedia's policy", ua)
	}
}

// The keyless wave-2 providers need no credential, so they are live-verifiable and
// carry no unverified marker. Everything else in the wave does.
func TestWave2VerificationStatus(t *testing.T) {
	r, _ := LoadBundled()
	verified := map[string]bool{"wikipedia": true, "hackernews": true, "frankfurter": true}
	for name := range wave2 {
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
}

// Read-only providers must not carry a mutating action — a write marked read-only slips
// past the build-phase guard and fires for real during agent generation.
func TestReadOnlyWave2ProvidersHaveNoWrites(t *testing.T) {
	r, _ := LoadBundled()
	for _, name := range []string{"wikipedia", "hackernews", "frankfurter", "tmdb", "strava"} {
		for _, a := range r.Actions(name) {
			if a.Mutating {
				t.Errorf("%s/%s is mutating, but this provider is read-only", name, a.Name)
			}
		}
	}
}
