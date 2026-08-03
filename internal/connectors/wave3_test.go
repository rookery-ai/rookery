package connectors

import (
	"strings"
	"testing"
)

// wave3 is the third everyday-connector wave: the self-hosted homelab stack plus a few
// cloud services. Kept as one table so a provider cannot enter the registry without
// declaring its category here.
var wave3 = map[string]string{
	"sonarr":          "Self-hosted",
	"radarr":          "Self-hosted",
	"grafana":         "Self-hosted",
	"n8n":             "Self-hosted",
	"gitea":           "Self-hosted",
	"karakeep":        "Self-hosted",
	"audiobookshelf":  "Self-hosted",
	"changedetection": "Self-hosted",
	"syncthing":       "Self-hosted",
	"steam":           "Data & Reference",
	"lastfm":          "Data & Reference",
	"clockify":        "Productivity",
	"wakatime":        "Developer",
}

func TestWave3ProvidersLoadWithTheRightCategory(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for name, cat := range wave3 {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("wave-3 provider %q not loaded", name)
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

// Every self-hosted wave-3 provider must template the per-connection base URL in EVERY
// action and declare the normalizer.
func TestWave3SelfHostedProvidersUseBaseURL(t *testing.T) {
	r, _ := LoadBundled()
	for name, cat := range wave3 {
		if cat != "Self-hosted" {
			continue
		}
		p, ok := r.ProviderByName(name)
		if !ok {
			continue
		}
		var found bool
		for _, ci := range p.ConnectInputs {
			if ci.Key == "base_url" {
				found = true
				if !ci.Required || ci.Normalize != "base_url" {
					t.Errorf("%s: base_url must be required and normalized, got required=%v normalize=%q",
						name, ci.Required, ci.Normalize)
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

// Gitea sends "token <key>", NOT "Bearer" — the likeliest authoring slip, and one that
// fails as a 401 that reads like a bad key rather than a bad scheme.
func TestGiteaUsesTokenScheme(t *testing.T) {
	r, _ := LoadBundled()
	p, ok := r.ProviderByName("gitea")
	if !ok {
		t.Fatal("gitea not loaded")
	}
	if p.Auth.ValuePrefix != "token " {
		t.Errorf("value_prefix = %q, want \"token \"", p.Auth.ValuePrefix)
	}
}

// Steam and Last.fm put the credential in the QUERY STRING, not a header, and both
// need a per-connection identifier (SteamID64 / username) that the API cannot discover.
func TestQueryKeyProvidersCollectTheirIdentifier(t *testing.T) {
	r, _ := LoadBundled()
	for name, key := range map[string]string{"steam": "steam_id", "lastfm": "username"} {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("%s not loaded", name)
			continue
		}
		if p.Auth.Placement != "query" {
			t.Errorf("%s placement = %q, want query", name, p.Auth.Placement)
		}
		if p.Auth.ParamName == "" {
			t.Errorf("%s has no param_name — the key would never be sent", name)
		}
		var found bool
		for _, ci := range p.ConnectInputs {
			if ci.Key == key {
				found = true
			}
		}
		if !found {
			t.Errorf("%s must collect %q at connect: the API cannot discover it", name, key)
		}
		// Every action must actually use it, or the connection value is dead weight.
		for _, a := range r.Actions(name) {
			blob := a.Request.URL
			for _, v := range a.Request.Query {
				blob += " " + v
			}
			if !strings.Contains(blob, "{{conn."+key+"}}") {
				t.Errorf("%s/%s never references {{conn.%s}}", name, a.Name, key)
			}
		}
	}
}

// Sonarr and Radarr are near-identical APIs and Radarr was derived from Sonarr's
// manifest. Guard the derivation: no radarr action may still point at a series
// endpoint, and no action name may keep the sonarr prefix.
func TestRadarrDerivationLeftNoSonarrTraces(t *testing.T) {
	r, _ := LoadBundled()
	for _, a := range r.Actions("radarr") {
		if strings.HasPrefix(a.Name, "sonarr_") {
			t.Errorf("radarr action %q kept the sonarr prefix", a.Name)
		}
		if strings.Contains(a.Request.URL, "/series") {
			t.Errorf("radarr action %q targets a Sonarr series endpoint: %s", a.Name, a.Request.URL)
		}
		if strings.Contains(strings.ToLower(a.Description), "sonarr") {
			t.Errorf("radarr action %q mentions Sonarr in its description", a.Name)
		}
	}
	// And the reverse, so a future edit cannot cross-contaminate.
	for _, a := range r.Actions("sonarr") {
		if strings.Contains(a.Request.URL, "/movie") {
			t.Errorf("sonarr action %q targets a Radarr movie endpoint: %s", a.Name, a.Request.URL)
		}
	}
}

// Wave 3 ships without live verification — none of these has an obtainable credential
// here. The marker is what keeps that honest rather than implied.
func TestWave3IsMarkedUnverified(t *testing.T) {
	r, _ := LoadBundled()
	for name := range wave3 {
		p, ok := r.ProviderByName(name)
		if !ok {
			continue
		}
		if !p.Unverified {
			t.Errorf("%s was not live-verified, so its YAML must set unverified: true", name)
		}
	}
}

// A read-only provider must carry no mutating action: a write marked read-only slips
// past the build-phase guard and fires for real during agent generation.
func TestReadOnlyWave3ProvidersHaveNoWrites(t *testing.T) {
	r, _ := LoadBundled()
	for _, name := range []string{"steam", "lastfm", "wakatime", "grafana", "syncthing"} {
		for _, a := range r.Actions(name) {
			if a.Mutating {
				t.Errorf("%s/%s is mutating, but this provider is exposed read-only", name, a.Name)
			}
		}
	}
}
