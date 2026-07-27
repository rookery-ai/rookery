package connectors

import (
	"encoding/json"
	"testing"
)

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
	// Derived from the token host rather than a hardcoded name list: fb_exchange_token
	// is a Meta grant, so any provider using it must be talking to Meta — and a list
	// would need editing every time a Meta provider is added, which is how it drifts.
	reg, _ := LoadBundled()
	for _, name := range reg.ProviderNames() {
		p, _ := reg.ProviderByName(name)
		if !p.UsesTokenExchange() {
			continue
		}
		if !contains(p.TokenURL, "graph.facebook.com") {
			t.Errorf("provider %q uses the fb_exchange_token grant but its token endpoint "+
				"is %q — that grant only exists on Meta", name, p.TokenURL)
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

// ── Instagram ────────────────────────────────────────────────────────────────

// Instagram publishes in two steps. Modelling them as two ACTIONS (rather than one
// action needing a two-call framework feature) has a useful property: only the second
// is public_write, so staging an image never waits on approval and only the publish
// does.
func TestInstagramTwoStepPublishGatingSplit(t *testing.T) {
	reg, _ := LoadBundled()

	create, ok := reg.Action("instagram", "instagram_create_media")
	if !ok {
		t.Fatal("instagram_create_media not found")
	}
	if create.PublicWrite {
		t.Error("staging a media container publishes nothing and must not be gated")
	}
	if create.Mutating {
		t.Error("staging is not a public mutation; gating/build rules should leave it alone")
	}

	pub, ok := reg.Action("instagram", "instagram_publish_media")
	if !ok {
		t.Fatal("instagram_publish_media not found")
	}
	if !pub.PublicWrite || !pub.Mutating {
		t.Error("the publish step must be public_write and mutating")
	}
}

// Every Instagram action is addressed by the ig_user_id the post_connect hook stored,
// never by an argument — the model must not be able to publish to another account.
func TestInstagramAddressedByConnExtra(t *testing.T) {
	reg, _ := LoadBundled()
	for _, name := range []string{"instagram_account_info", "instagram_list_media",
		"instagram_create_media", "instagram_publish_media"} {
		a, ok := reg.Action("instagram", name)
		if !ok {
			t.Fatalf("%s not found", name)
		}
		if !contains(a.Request.URL, "{{conn.ig_user_id}}") {
			t.Errorf("%s must address the account via conn.ig_user_id, got %s", name, a.Request.URL)
		}
	}
}

func TestInstagramDeclaresIGHookAndScopes(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("instagram")
	if !ok {
		t.Fatal("instagram provider not loaded")
	}
	if p.PostConnect != "meta_ig_user" {
		t.Errorf("post_connect = %q, want meta_ig_user", p.PostConnect)
	}
	for _, want := range []string{"instagram_basic", "instagram_content_publish"} {
		var found bool
		for _, s := range p.DefaultScopes {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing scope %q", want)
		}
	}
}

func TestInstagramPublishRenders(t *testing.T) {
	reg, _ := LoadBundled()
	a, _ := reg.Action("instagram", "instagram_publish_media")
	method, u, body, _, err := renderRequest(a, map[string]any{"creation_id": "C1"},
		map[string]string{"ig_user_id": "IG9"})
	if err != nil {
		t.Fatalf("renderRequest: %v", err)
	}
	if method != "POST" || u != "https://graph.facebook.com/v21.0/IG9/media_publish" {
		t.Errorf("unexpected request: %s %s", method, u)
	}
	if !contains(string(body), "creation_id=C1") {
		t.Errorf("form body did not render: %s", body)
	}
}

// ── Phase 4 social providers ─────────────────────────────────────────────────

// Every publishing action across every provider must be public_write, or the approval
// gate silently does not apply to it. This is the invariant most likely to be broken
// by a future data file, and the failure is invisible until something posts unreviewed.
func TestAllPublishingActionsAreGated(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	// Actions whose names say they publish. Named explicitly rather than pattern-
	// matched so adding a publisher forces a deliberate edit here.
	publishers := []struct{ provider, action string }{
		{"linkedin", "linkedin_create_post"},
		{"youtube", "youtube_post_comment"},
		{"facebook", "facebook_create_post"},
		{"instagram", "instagram_publish_media"},
		{"x", "x_create_post"},
		{"reddit", "reddit_submit_post"},
	}
	for _, p := range publishers {
		a, ok := reg.Action(p.provider, p.action)
		if !ok {
			t.Errorf("%s: action %q not found", p.provider, p.action)
			continue
		}
		if !a.PublicWrite {
			t.Errorf("%s.%s is not public_write — the approval gate does not apply to it",
				p.provider, p.action)
		}
		if !a.Mutating {
			t.Errorf("%s.%s is not mutating — a BUILD could publish with it",
				p.provider, p.action)
		}
	}
}

// Reddit rate-limits or blocks a generic User-Agent outright, and the token endpoint
// needs HTTP Basic client auth. Both are easy to omit and produce failures that do not
// name the cause.
func TestRedditDeclaresUserAgentAndBasicAuth(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("reddit")
	if !ok {
		t.Fatal("reddit provider not loaded")
	}
	if p.StaticHeaders["User-Agent"] == "" {
		t.Error("Reddit blocks or throttles a generic User-Agent — one must be declared")
	}
	if p.TokenAuth != "basic" {
		t.Errorf("token_auth = %q, want basic", p.TokenAuth)
	}
}

// X charges per call, so every read action must force a bounded result set rather than
// letting a model page through a bill.
func TestXReadActionsRequireALimit(t *testing.T) {
	reg, _ := LoadBundled()
	a, ok := reg.Action("x", "x_list_posts")
	if !ok {
		t.Fatal("x_list_posts not found")
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(a.Params, &schema); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range schema.Required {
		if r == "max" {
			found = true
		}
	}
	if !found {
		t.Errorf("x_list_posts must require max — reads are billed per post; required=%v", schema.Required)
	}
}
