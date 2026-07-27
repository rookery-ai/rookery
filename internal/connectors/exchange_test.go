package connectors

import "testing"

// Meta issues no refresh token: renewal re-exchanges the CURRENT access token. The
// mode has to be discoverable from the provider, or DBTokenStore would try a
// refresh_token grant with an empty refresh token and fail on every renewal.
func TestMetaUsesTokenExchange(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := reg.ProviderByName("meta_ads")
	if !ok {
		t.Fatal("meta_ads provider not loaded")
	}
	if !p.UsesTokenExchange() {
		t.Errorf("meta_ads token_expiry = %q, want exchange", p.TokenExpiry)
	}
	// Exchange and never are mutually exclusive: "never" would stop renewal entirely
	// and the 60-day token would silently die.
	if p.NonExpiring() {
		t.Error("an exchange provider must not also be non-expiring")
	}
}

// Every other provider must be unaffected — this mode is opt-in per data file.
func TestOnlyMetaUsesExchange(t *testing.T) {
	reg, _ := LoadBundled()
	for _, name := range reg.ProviderNames() {
		p, _ := reg.ProviderByName(name)
		if p.UsesTokenExchange() && name != "meta_ads" {
			t.Errorf("provider %q unexpectedly uses token exchange", name)
		}
	}
}

// Insights and campaign listing are reads; pausing a campaign is mutating but must
// NOT be public_write — it changes delivery, it does not publish anything, and gating
// it would make an approval-enabled agent wait to pause overspending.
func TestMetaAdsPauseIsNotPublicWrite(t *testing.T) {
	reg, _ := LoadBundled()
	a, ok := reg.Action("meta_ads", "meta_ads_set_campaign_status")
	if !ok {
		t.Fatal("meta_ads_set_campaign_status not found")
	}
	if !a.Mutating {
		t.Error("changing campaign status is a write")
	}
	if a.PublicWrite {
		t.Error("pausing a campaign is private and reversible — gating it would make an " +
			"approval-enabled agent wait before it could stop overspending")
	}
}

func TestMetaAdsInsightsRenders(t *testing.T) {
	_, u, _ := renderFor(t, "meta_ads", "meta_ads_insights", map[string]any{
		"account_id":  "act_123",
		"date_preset": "last_7d",
		"fields":      "spend,impressions",
		"limit":       25,
	})
	for _, want := range []string{
		"https://graph.facebook.com/v21.0/act_123/insights?",
		"date_preset=last_7d", "fields=spend%2Cimpressions", "limit=25",
	} {
		if !contains(u, want) {
			t.Errorf("URL missing %q: %s", want, u)
		}
	}
	// level was not supplied and must be dropped, not sent empty.
	if contains(u, "level=") {
		t.Errorf("unsupplied optional param should be dropped: %s", u)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
