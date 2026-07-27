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

// The mode is opt-in per data file: only the Meta-family providers use it, and every
// other provider must keep the standard refresh_token path.
func TestOnlyMetaFamilyUsesExchange(t *testing.T) {
	metaFamily := map[string]bool{"meta_ads": true, "facebook": true}
	reg, _ := LoadBundled()
	for _, name := range reg.ProviderNames() {
		p, _ := reg.ProviderByName(name)
		if p.UsesTokenExchange() && !metaFamily[name] {
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

// ── Facebook Page ────────────────────────────────────────────────────────────

// Publishing needs the PAGE token, which only the post_connect hook can obtain, and
// every action addresses the Page via {{conn.page_id}} that the same hook stores.
// Without the hook the provider would connect and then 403 on every call.
func TestFacebookDeclaresPageTokenHook(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("facebook")
	if !ok {
		t.Fatal("facebook provider not loaded")
	}
	if p.PostConnect != "meta_page_token" {
		t.Errorf("post_connect = %q, want meta_page_token", p.PostConnect)
	}
	if !p.UsesTokenExchange() {
		t.Error("facebook must use the exchange token mode")
	}
	for _, want := range []string{"pages_show_list", "pages_manage_posts"} {
		var found bool
		for _, s := range p.DefaultScopes {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing scope %q — the page-token hook fails without it", want)
		}
	}
}

func TestFacebookPostIsGatedAndRendersFromConnExtra(t *testing.T) {
	reg, _ := LoadBundled()
	a, ok := reg.Action("facebook", "facebook_create_post")
	if !ok {
		t.Fatal("facebook_create_post not found")
	}
	if !a.PublicWrite || !a.Mutating {
		t.Error("publishing to a Page must be public_write and mutating")
	}

	// page_id comes from the connection's extra, not from an argument: the model must
	// not be able to post to an arbitrary Page id it invented.
	method, u, body, _, err := renderRequest(a, map[string]any{"message": "hello"},
		map[string]string{"page_id": "PG1"})
	if err != nil {
		t.Fatalf("renderRequest: %v", err)
	}
	if method != "POST" || u != "https://graph.facebook.com/v21.0/PG1/feed" {
		t.Errorf("unexpected request: %s %s", method, u)
	}
	if got := string(body); !contains(got, "message=hello") {
		t.Errorf("form body did not render: %s", got)
	}
	// link was not supplied and must be dropped rather than posted empty.
	if contains(string(body), "link=") {
		t.Errorf("unsupplied optional field should be dropped: %s", body)
	}
}
