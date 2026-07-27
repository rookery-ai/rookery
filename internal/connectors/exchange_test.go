package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		// The token-exchange grant is a Meta-family mechanism (Facebook/Instagram use
		// fb_exchange_token, Threads th_exchange_token). Asserting the HOST rather than
		// a name list means a new Meta provider needs no edit here, while a non-Meta
		// provider adopting the mode by mistake still fails.
		metaHost := contains(p.TokenURL, "graph.facebook.com") || contains(p.TokenURL, "graph.threads.net")
		if !metaHost {
			t.Errorf("provider %q uses the token-exchange grant but its token endpoint "+
				"is %q — that grant only exists on Meta-operated hosts", name, p.TokenURL)
		}
		if g := p.ExchangeGrant(); !contains(g, "_exchange_token") {
			t.Errorf("provider %q has exchange grant %q, which is not an exchange grant", name, g)
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

// ── Phase 4 remainder ────────────────────────────────────────────────────────

// Threads uses th_exchange_token, not Meta's fb_exchange_token. Sending the wrong
// grant name fails with an opaque error that never mentions the grant.
func TestThreadsUsesItsOwnExchangeGrant(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("threads")
	if !ok {
		t.Fatal("threads provider not loaded")
	}
	if !p.UsesTokenExchange() {
		t.Error("threads must use the exchange token mode")
	}
	if got := p.ExchangeGrant(); got != "th_exchange_token" {
		t.Errorf("ExchangeGrant() = %q, want th_exchange_token", got)
	}
	// It is NOT a Meta alias: its own OAuth and API hosts.
	if p.AuthParent != "" {
		t.Errorf("threads must not alias another provider, got auth_parent=%q", p.AuthParent)
	}
	if !contains(p.TokenURL, "graph.threads.net") {
		t.Errorf("token endpoint = %q, want graph.threads.net", p.TokenURL)
	}
}

// The default must remain Meta's grant so the existing providers are unaffected.
func TestExchangeGrantDefaultsToMeta(t *testing.T) {
	reg, _ := LoadBundled()
	p, _ := reg.ProviderByName("facebook")
	if got := p.ExchangeGrant(); got != "fb_exchange_token" {
		t.Errorf("facebook ExchangeGrant() = %q, want fb_exchange_token", got)
	}
}

// TikTok calls the client id "client_key" in both the consent URL and the token
// request; the default must stay client_id for everyone else.
func TestTikTokUsesClientKey(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("tiktok")
	if !ok {
		t.Fatal("tiktok provider not loaded")
	}
	if got := p.ClientIDParam(); got != "client_key" {
		t.Errorf("ClientIDParam() = %q, want client_key", got)
	}
	u := p.ConsentURL("CK123", "https://example.com/cb", "st", p.DefaultScopes)
	if !contains(u, "client_key=CK123") {
		t.Errorf("consent URL must carry client_key: %s", u)
	}
	if contains(u, "client_id=") {
		t.Errorf("consent URL must not also send client_id: %s", u)
	}

	other, _ := reg.ProviderByName("github")
	if got := other.ClientIDParam(); got != "client_id" {
		t.Errorf("github ClientIDParam() = %q, want the client_id default", got)
	}
}

// TikTok's draft upload is the audit-free path: it lands in the creator's inbox for
// them to publish by hand. It is mutating but NOT public_write — nothing goes public,
// and gating it would add an approval step in front of an action that is already
// human-reviewed by construction.
func TestTikTokDraftUploadIsNotPublicWrite(t *testing.T) {
	reg, _ := LoadBundled()
	a, ok := reg.Action("tiktok", "tiktok_upload_draft")
	if !ok {
		t.Fatal("tiktok_upload_draft not found")
	}
	if a.PublicWrite {
		t.Error("an inbox draft is not public — gating it would double up on a review " +
			"the creator already performs in the TikTok app")
	}
	if !a.Mutating {
		t.Error("uploading is still a write")
	}
}

// Pinterest's trial tier makes sandbox-only pins, but on Standard access the same call
// is a real public post — so it must be gated.
func TestPinterestCreatePinIsGated(t *testing.T) {
	reg, _ := LoadBundled()
	a, ok := reg.Action("pinterest", "pinterest_create_pin")
	if !ok {
		t.Fatal("pinterest_create_pin not found")
	}
	if !a.PublicWrite || !a.Mutating {
		t.Error("creating a pin is public on Standard access and must be gated")
	}
}

// ── Phase 5: advertising ─────────────────────────────────────────────────────

// A Google Ads developer token cannot be discovered from any API — it is issued out
// of band — which is the entire reason connect_inputs had to work on the OAuth path.
// Everything else (customer id, campaigns) is reachable once you have it.
func TestGoogleAdsCollectsUndiscoverableInputs(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("google_ads")
	if !ok {
		t.Fatal("google_ads provider not loaded")
	}
	if p.AuthParent != "google" {
		t.Errorf("auth_parent = %q, want google", p.AuthParent)
	}
	want := map[string]bool{"developer_token": true, "customer_id": true}
	got := map[string]bool{}
	for _, ci := range p.ConnectInputs {
		got[ci.Key] = ci.Required
	}
	for k := range want {
		req, present := got[k]
		if !present {
			t.Errorf("missing connect_input %q", k)
			continue
		}
		if !req {
			t.Errorf("connect_input %q must be required — the API returns an opaque error without it", k)
		}
	}
	// The manager id is genuinely optional; marking it required would block every
	// non-manager account from connecting at all.
	if got["login_customer_id"] {
		t.Error("login_customer_id must be optional — most accounts have no manager account")
	}
	// The developer token travels as a header on every call, sourced from the connection.
	if p.StaticHeaders["developer-token"] != "{{conn.developer_token}}" {
		t.Errorf("developer-token header = %q, want the conn template",
			p.StaticHeaders["developer-token"])
	}
}

// GAQL reporting is one action, so the row cap must be required there or a report can
// return unbounded rows into the coder's context.
func TestGoogleAdsSearchRequiresPageSize(t *testing.T) {
	reg, _ := LoadBundled()
	a, ok := reg.Action("google_ads", "google_ads_search")
	if !ok {
		t.Fatal("google_ads_search not found")
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(a.Params, &schema); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range schema.Required {
		if r == "page_size" {
			found = true
		}
	}
	if !found {
		t.Errorf("google_ads_search must require page_size, got required=%v", schema.Required)
	}
	// The customer id comes from the connection, not an argument.
	if !contains(a.Request.URL, "{{conn.customer_id}}") {
		t.Errorf("query must target the configured customer: %s", a.Request.URL)
	}
}

// LinkedIn Ads aliases the LinkedIn app but needs its own partner-gated scopes.
func TestLinkedInAdsAliasesLinkedIn(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("linkedin_ads")
	if !ok {
		t.Fatal("linkedin_ads provider not loaded")
	}
	if p.AuthParent != "linkedin" {
		t.Errorf("auth_parent = %q, want linkedin", p.AuthParent)
	}
	oauth, ok := reg.OAuthProvider("linkedin_ads")
	if !ok || oauth.Name != "linkedin" {
		t.Fatalf("OAuthProvider did not resolve to linkedin: %+v", oauth)
	}
	// The aliased parent supplies the version headers the whole LinkedIn API needs.
	if oauth.StaticHeaders["LinkedIn-Version"] == "" {
		t.Error("resolved parent must supply LinkedIn-Version")
	}
}

// Both advertising providers are gated behind an application the user may not have.
// Their setup steps must say so FIRST, or a user connects and then sees only 403s
// with no idea why.
func TestGatedAdProvidersWarnUpFront(t *testing.T) {
	reg, _ := LoadBundled()
	for _, name := range []string{"google_ads", "linkedin_ads", "reddit", "pinterest", "tiktok"} {
		p, ok := reg.ProviderByName(name)
		if !ok {
			t.Fatalf("provider %q not loaded", name)
		}
		if len(p.SetupSteps) == 0 {
			t.Errorf("%s has no setup steps", name)
			continue
		}
		if !contains(p.SetupSteps[0], "IMPORTANT") {
			t.Errorf("%s's first setup step must lead with its access constraint, got %q",
				name, p.SetupSteps[0])
		}
	}
}

// Static header values are TEMPLATED. Asserting the YAML contains the template passes
// whether or not Execute resolves it — the discriminating check is what reaches the
// wire. A literal "{{conn.developer_token}}" header 401s every Google Ads call.
func TestStaticHeadersAreTemplatedAndEmptyOnesDropped(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}

	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	// Point the action at the test server while keeping the provider's real headers.
	a, ok := reg.Action("google_ads", "google_ads_search")
	if !ok {
		t.Fatal("google_ads_search not found")
	}
	a.Request.URL = srv.URL + "/search"
	reg.actions["google_ads"] = []Action{a}

	_, err = Execute(context.Background(), reg, stubStore{}, srv.Client(),
		ConnRef{ID: "c1", Provider: "google_ads", Extra: map[string]string{
			"developer_token": "DT-1234",
			"customer_id":     "9999999999",
			// login_customer_id deliberately absent: most accounts have no manager.
		}},
		"google_ads_search",
		map[string]any{"query": "SELECT campaign.name FROM campaign", "page_size": 25},
		Policy{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := gotHeaders.Get("developer-token"); got != "DT-1234" {
		t.Errorf("developer-token header = %q, want the resolved value DT-1234", got)
	}
	// An optional header with no value must be ABSENT, not empty: Google Ads rejects a
	// blank login-customer-id.
	if _, present := gotHeaders["Login-Customer-Id"]; present {
		t.Errorf("unset optional header was sent anyway: %q", gotHeaders.Get("login-customer-id"))
	}
}

// An aliased child inherits the parent's static headers AND may add its own. Reading
// only the parent's would drop google_ads's developer-token entirely, since its parent
// declares none.
func TestAliasedChildInheritsAndExtendsStaticHeaders(t *testing.T) {
	reg, _ := LoadBundled()

	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"elements":[]}`))
	}))
	defer srv.Close()

	a, ok := reg.Action("linkedin_ads", "linkedin_ads_list_accounts")
	if !ok {
		t.Fatal("linkedin_ads_list_accounts not found")
	}
	a.Request.URL = srv.URL + "/adAccounts"
	reg.actions["linkedin_ads"] = []Action{a}

	if _, err := Execute(context.Background(), reg, stubStore{}, srv.Client(),
		ConnRef{ID: "c1", Provider: "linkedin_ads"},
		"linkedin_ads_list_accounts", map[string]any{}, Policy{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Inherited from the linkedin parent — LinkedIn rejects unversioned calls.
	if gotHeaders.Get("LinkedIn-Version") == "" {
		t.Error("aliased child did not inherit the parent's LinkedIn-Version header")
	}
	if got := gotHeaders.Get("X-Restli-Protocol-Version"); got != "2.0.0" {
		t.Errorf("X-Restli-Protocol-Version = %q, want 2.0.0", got)
	}
}
