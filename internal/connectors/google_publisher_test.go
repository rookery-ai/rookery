package connectors

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// renderFor loads the bundled registry, finds an action, and renders a request from
// typed args. Shared by the four Google publisher providers.
func renderFor(t *testing.T, provider, action string, args map[string]any) (method, u string, body []byte) {
	t.Helper()
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	a, ok := reg.Action(provider, action)
	if !ok {
		t.Fatalf("action %q not found for provider %q", action, provider)
	}
	method, u, body, _, err = renderRequest(a, args, nil)
	if err != nil {
		t.Fatalf("renderRequest: %v", err)
	}
	return method, u, body
}

// ── AdSense ──────────────────────────────────────────────────────────────────

func TestAdSenseProviderAliasesGoogle(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	p, ok := reg.ProviderByName("google_adsense")
	if !ok {
		t.Fatal("google_adsense provider not loaded")
	}
	if p.AuthParent != "google" {
		t.Errorf("auth_parent = %q, want google", p.AuthParent)
	}
	oauth, ok := reg.OAuthProvider("google_adsense")
	if !ok || oauth.Name != "google" {
		t.Fatalf("OAuthProvider did not resolve to google, got %+v", oauth)
	}
	if oauth.AuthorizeURL == "" || oauth.TokenURL == "" {
		t.Error("resolved parent must supply authorize/token URLs")
	}
}

func TestAdSenseReportRendersPresetRange(t *testing.T) {
	method, u, _ := renderFor(t, "google_adsense", "adsense_report", map[string]any{
		"account":    "accounts/pub-1234567890123456",
		"date_range": "LAST_7_DAYS",
		"metrics":    "ESTIMATED_EARNINGS,CLICKS",
		"limit":      50,
	})
	if method != "GET" {
		t.Errorf("method = %q, want GET", method)
	}
	// The account resource name carries a real path separator; it must NOT be escaped.
	if !strings.HasPrefix(u, "https://adsense.googleapis.com/v2/accounts/pub-1234567890123456/reports:generate?") {
		t.Fatalf("unexpected URL: %s", u)
	}
	for _, want := range []string{"dateRange=LAST_7_DAYS", "metrics=ESTIMATED_EARNINGS%2CCLICKS", "limit=50"} {
		if !strings.Contains(u, want) {
			t.Errorf("URL missing %q: %s", want, u)
		}
	}
	if strings.Contains(u, "dimensions=") {
		t.Errorf("unsupplied optional param should be dropped, not sent empty: %s", u)
	}
}

// ── GA4 ──────────────────────────────────────────────────────────────────────

func TestGA4RunReportBodyShape(t *testing.T) {
	_, u, body := renderFor(t, "google_analytics", "ga4_run_report", map[string]any{
		"property":   "properties/123456789",
		"start_date": "28daysAgo",
		"end_date":   "yesterday",
		"metrics":    "activeUsers,screenPageViews",
		"limit":      50,
	})
	if u != "https://analyticsdata.googleapis.com/v1beta/properties/123456789:runReport" {
		t.Fatalf("unexpected URL: %s", u)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v — %s", err, body)
	}

	// GA4 requires metrics as [{"name": "..."}]. A bare string array renders fine
	// and is rejected by Google at request time, which is the expensive way to
	// discover the shape is wrong.
	metrics, ok := got["metrics"].([]any)
	if !ok || len(metrics) != 2 {
		t.Fatalf("metrics must be a 2-element array, got %v", got["metrics"])
	}
	first, ok := metrics[0].(map[string]any)
	if !ok {
		t.Fatalf("metrics[0] must be an object with a name key, got %T: %v", metrics[0], metrics[0])
	}
	if first["name"] != "activeUsers" {
		t.Errorf(`metrics[0].name = %v, want "activeUsers"`, first["name"])
	}

	ranges, ok := got["dateRanges"].([]any)
	if !ok || len(ranges) != 1 {
		t.Fatalf("dateRanges must be a one-element array, got %v", got["dateRanges"])
	}
	dr := ranges[0].(map[string]any)
	if dr["startDate"] != "28daysAgo" || dr["endDate"] != "yesterday" {
		t.Errorf("date range did not render: %v", dr)
	}

	// dimensions was not supplied, so the key must be absent — GA4 rejects an
	// empty dimensions array on some report types.
	if _, present := got["dimensions"]; present {
		t.Errorf("unsupplied dimensions must be omitted, got %v", got["dimensions"])
	}
}

func TestGA4RealtimeOmitsDateRange(t *testing.T) {
	_, u, body := renderFor(t, "google_analytics", "ga4_run_realtime_report", map[string]any{
		"property":   "properties/123456789",
		"metrics":    "activeUsers",
		"dimensions": "country",
	})
	if !strings.HasSuffix(u, ":runRealtimeReport") {
		t.Errorf("unexpected URL: %s", u)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if _, present := got["dateRanges"]; present {
		t.Error("realtime reports have a fixed 30-minute window and must not send dateRanges")
	}
	dims, ok := got["dimensions"].([]any)
	if !ok || len(dims) != 1 {
		t.Fatalf("dimensions did not render: %v", got["dimensions"])
	}
}

func TestGA4PropertyDiscoveryUsesAdminHost(t *testing.T) {
	_, u, _ := renderFor(t, "google_analytics", "ga4_list_properties", map[string]any{})
	if u != "https://analyticsadmin.googleapis.com/v1beta/accountSummaries" {
		t.Errorf("unexpected discovery URL: %s", u)
	}
}

// ── Search Console ───────────────────────────────────────────────────────────

// A Search Console site URL is itself a URL sitting in a path segment. Interpolated
// raw, "https://example.com/" becomes extra path separators and the request 404s
// against a nonsense path.
//
// The expected encodings come from Google's own searchanalytics.query reference,
// which documents the siteUrl as fully encoded — colon included. url.PathEscape
// alone leaves ':' untouched (RFC 3986 permits it in a path segment), so a test
// asserting "https:%2F%2F…" would pass while the live request went out wrong.
func TestGSCSiteURLIsEscapedInPath(t *testing.T) {
	cases := map[string]string{
		"https://example.com/":    "https%3A%2F%2Fexample.com%2F",
		"sc-domain:example.com":   "sc-domain%3Aexample.com",
		"https://a.example.com/b": "https%3A%2F%2Fa.example.com%2Fb",
	}
	for site, encoded := range cases {
		_, u, _ := renderFor(t, "google_searchconsole", "gsc_search_analytics", map[string]any{
			"site_url":   site,
			"start_date": "2026-07-01",
			"end_date":   "2026-07-27",
			"row_limit":  50,
		})
		want := "https://www.googleapis.com/webmasters/v3/sites/" + encoded + "/searchAnalytics/query"
		if u != want {
			t.Errorf("site %q not escaped correctly.\n got: %s\nwant: %s", site, u, want)
		}
	}
}

// The escape is opt-in, so identifiers whose slashes are REAL path separators
// must survive untouched. This is the regression the escape could most easily
// cause, and it would only surface as a live 404.
func TestUnescapedIdentifiersKeepTheirSlashes(t *testing.T) {
	_, u, _ := renderFor(t, "google_adsense", "adsense_list_adclients", map[string]any{
		"account": "accounts/pub-1234567890123456",
	})
	if !strings.Contains(u, "/accounts/pub-1234567890123456/adclients") {
		t.Errorf("AdSense account separator was escaped: %s", u)
	}
	_, u2, _ := renderFor(t, "google_analytics", "ga4_metadata", map[string]any{
		"property": "properties/123456789",
	})
	if !strings.Contains(u2, "/properties/123456789/metadata") {
		t.Errorf("GA4 property separator was escaped: %s", u2)
	}
}

func TestGSCSearchAnalyticsBody(t *testing.T) {
	_, _, body := renderFor(t, "google_searchconsole", "gsc_search_analytics", map[string]any{
		"site_url":   "https://example.com/",
		"start_date": "2026-07-01",
		"end_date":   "2026-07-27",
		"dimensions": []any{"query"},
		"row_limit":  50,
	})
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	// Unlike GA4, Search Console takes a plain string array here.
	dims, ok := got["dimensions"].([]any)
	if !ok || len(dims) != 1 || dims[0] != "query" {
		t.Errorf("dimensions must be a plain string array, got %v", got["dimensions"])
	}
	if got["startDate"] != "2026-07-01" {
		t.Errorf("startDate did not render: %v", got["startDate"])
	}
}

func TestGSCListSitesNeedsNoArgs(t *testing.T) {
	method, u, _ := renderFor(t, "google_searchconsole", "gsc_list_sites", map[string]any{})
	if method != "GET" || u != "https://www.googleapis.com/webmasters/v3/sites" {
		t.Errorf("unexpected request: %s %s", method, u)
	}
}

// ── YouTube ──────────────────────────────────────────────────────────────────

func TestYouTubeChannelUsesMine(t *testing.T) {
	_, u, _ := renderFor(t, "youtube", "youtube_my_channel", map[string]any{})
	if !strings.Contains(u, "mine=true") {
		t.Errorf("channel lookup must use mine=true, got %s", u)
	}
	// contentDetails carries the uploads playlist id youtube_list_videos needs.
	if !strings.Contains(u, "contentDetails") {
		t.Errorf("part must include contentDetails: %s", u)
	}
}

func TestYouTubeAnalyticsRendersChannelMine(t *testing.T) {
	_, u, _ := renderFor(t, "youtube", "youtube_analytics_report", map[string]any{
		"start_date": "2026-06-01",
		"end_date":   "2026-06-30",
		"metrics":    "views,estimatedMinutesWatched",
	})
	if !strings.HasPrefix(u, "https://youtubeanalytics.googleapis.com/v2/reports?") {
		t.Fatalf("unexpected analytics host: %s", u)
	}
	for _, want := range []string{"ids=channel%3D%3DMINE", "startDate=2026-06-01", "metrics=views%2CestimatedMinutesWatched"} {
		if !strings.Contains(u, want) {
			t.Errorf("URL missing %q: %s", want, u)
		}
	}
}

// ── Cross-provider invariants ────────────────────────────────────────────────

// Every Phase 1 provider is read-only, every action name satisfies the tool-name
// regex the coder backends enforce, and no name collides across providers (tool
// names share one flat namespace when a workspace connects several accounts).
func TestPublisherProvidersAreReadOnlyAndWellNamed(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	seen := map[string]string{}

	// Collisions must be checked against EVERY provider, not just the new four —
	// a new action named "gmail_send_email" would silently shadow Google's.
	for _, p := range reg.ProviderNames() {
		for _, a := range reg.Actions(p) {
			if prev, dup := seen[a.Name]; dup {
				t.Errorf("action name %q is declared by both %s and %s", a.Name, prev, p)
			}
			seen[a.Name] = p
		}
	}

	for _, p := range []string{"google_adsense", "google_analytics", "google_searchconsole", "youtube"} {
		actions := reg.Actions(p)
		if len(actions) == 0 {
			t.Errorf("provider %q has no actions", p)
		}
		for _, a := range actions {
			// The reporting/discovery actions must stay read-only. The only writes
			// these providers may carry are explicitly-marked public_write publishing
			// actions, which the approval gate can hold — an unmarked mutating action
			// would slip past the gate entirely.
			if a.Mutating && !a.PublicWrite {
				t.Errorf("%s: action %q is mutating but not public_write — it would bypass the approval gate", p, a.Name)
			}
			if !valid.MatchString(a.Name) {
				t.Errorf("%s: action name %q fails the tool-name regex", p, a.Name)
			}
			if a.Description == "" {
				t.Errorf("%s: action %q has no description — the model cannot pick it", p, a.Name)
			}
			var schema map[string]any
			if err := json.Unmarshal(a.Params, &schema); err != nil {
				t.Errorf("%s: action %q has an unparseable schema: %v", p, a.Name, err)
			}
		}
	}
}

// Every report action must REQUIRE its row limit. An omitted optional param is
// dropped, not defaulted — so a description saying "default 50" enforces nothing
// and the provider's own default applies instead: 100000 rows for AdSense, 10000
// for GA4, 1000 for Search Console. The 8 KiB bridge cap would then truncate a
// report the agent believed it received whole.
func TestReportActionsRequireARowLimit(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, tc := range []struct{ provider, action, param string }{
		{"google_adsense", "adsense_report", "limit"},
		{"google_analytics", "ga4_run_report", "limit"},
		{"google_searchconsole", "gsc_search_analytics", "row_limit"},
	} {
		a, ok := reg.Action(tc.provider, tc.action)
		if !ok {
			t.Fatalf("action %q not found", tc.action)
		}
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(a.Params, &schema); err != nil {
			t.Fatalf("%s schema: %v", tc.action, err)
		}
		found := false
		for _, r := range schema.Required {
			if r == tc.param {
				found = true
			}
		}
		if !found {
			t.Errorf("%s must require %q, got required=%v", tc.action, tc.param, schema.Required)
		}
	}
}

// The four new providers all alias google, so none of them may declare its own
// OAuth endpoints — those must resolve from the parent or a connect silently
// targets the wrong authorization server.
func TestPublisherProvidersDeclareNoOwnEndpoints(t *testing.T) {
	reg, _ := LoadBundled()
	for _, name := range []string{"google_adsense", "google_analytics", "google_searchconsole", "youtube"} {
		p, ok := reg.ProviderByName(name)
		if !ok {
			t.Fatalf("provider %q not loaded", name)
		}
		if p.AuthorizeURL != "" || p.TokenURL != "" {
			t.Errorf("%s must inherit endpoints from google, not declare its own", name)
		}
		if len(p.DefaultScopes) == 0 {
			t.Errorf("%s declares no scopes, so consent would request none", name)
		}
	}
}

// ── Publishing actions (Phase 2) ─────────────────────────────────────────────

// The whole gate keys off public_write, so the publishing actions must carry it —
// and must be mutating too, or the build guard would let a generation run post.
func TestPublishingActionsAreMarkedPublicWrite(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, tc := range []struct{ provider, action string }{
		{"linkedin", "linkedin_create_post"},
		{"youtube", "youtube_post_comment"},
	} {
		a, ok := reg.Action(tc.provider, tc.action)
		if !ok {
			t.Fatalf("action %q not found", tc.action)
		}
		if !a.PublicWrite {
			t.Errorf("%s must be public_write — the approval gate keys off it", tc.action)
		}
		if !a.Mutating {
			t.Errorf("%s must be mutating — otherwise a build could publish", tc.action)
		}
		// The description is what the model reads before deciding to call it.
		if !strings.Contains(strings.ToUpper(a.Description), "REAL") {
			t.Errorf("%s description should warn it publishes for real: %q", tc.action, a.Description)
		}
	}
}

func TestLinkedInPostBodyShape(t *testing.T) {
	_, u, body := renderFor(t, "linkedin", "linkedin_create_post", map[string]any{
		"person_id": "ABC123",
		"text":      "Shipping today",
	})
	if u != "https://api.linkedin.com/rest/posts" {
		t.Fatalf("unexpected URL: %s", u)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v — %s", err, body)
	}
	if got["author"] != "urn:li:person:ABC123" {
		t.Errorf("author URN did not render: %v", got["author"])
	}
	if got["commentary"] != "Shipping today" {
		t.Errorf("commentary = %v", got["commentary"])
	}
	if got["lifecycleState"] != "PUBLISHED" {
		t.Errorf("lifecycleState = %v, want PUBLISHED", got["lifecycleState"])
	}
	dist, ok := got["distribution"].(map[string]any)
	if !ok || dist["feedDistribution"] != "MAIN_FEED" {
		t.Errorf("distribution did not render: %v", got["distribution"])
	}
}

// LinkedIn rejects requests without its versioning headers, and they come from the
// provider's static_headers rather than the action.
func TestLinkedInDeclaresVersionHeaders(t *testing.T) {
	reg, _ := LoadBundled()
	p, ok := reg.ProviderByName("linkedin")
	if !ok {
		t.Fatal("linkedin provider not loaded")
	}
	// LinkedIn sunsets each monthly version after ~12 months and rejects a sunset
	// value outright, so "non-empty" is not enough — a stale pin fails 100% of calls
	// with an error that does not obviously point at the header.
	v := p.StaticHeaders["LinkedIn-Version"]
	if !regexp.MustCompile(`^\d{6}$`).MatchString(v) {
		t.Fatalf("LinkedIn-Version = %q, want a YYYYMM value", v)
	}
	if v < "202509" {
		t.Errorf("LinkedIn-Version %q is at or past sunset (LinkedIn supports roughly the "+
			"last 12 monthly releases) — bump it to a currently supported version", v)
	}
	if p.StaticHeaders["X-Restli-Protocol-Version"] != "2.0.0" {
		t.Errorf("X-Restli-Protocol-Version = %q, want 2.0.0", p.StaticHeaders["X-Restli-Protocol-Version"])
	}
}

func TestYouTubeCommentBodyShape(t *testing.T) {
	_, u, body := renderFor(t, "youtube", "youtube_post_comment", map[string]any{
		"video_id": "vid123",
		"text":     "nice one",
	})
	if !strings.Contains(u, "part=snippet") {
		t.Errorf("part=snippet is required: %s", u)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v — %s", err, body)
	}
	snip, ok := got["snippet"].(map[string]any)
	if !ok {
		t.Fatalf("snippet missing: %v", got)
	}
	if snip["videoId"] != "vid123" {
		t.Errorf("videoId = %v", snip["videoId"])
	}
	top, ok := snip["topLevelComment"].(map[string]any)
	if !ok {
		t.Fatalf("topLevelComment missing: %v", snip)
	}
	inner, ok := top["snippet"].(map[string]any)
	if !ok || inner["textOriginal"] != "nice one" {
		t.Errorf("comment text did not render into the nested snippet: %v", top)
	}
}

// Commenting needs force-ssl; youtube.readonly alone cannot write. Requesting it up
// front avoids a re-consent the first time an agent tries to comment.
func TestYouTubeRequestsCommentScope(t *testing.T) {
	reg, _ := LoadBundled()
	p, _ := reg.ProviderByName("youtube")
	var found bool
	for _, s := range p.DefaultScopes {
		if strings.Contains(s, "youtube.force-ssl") {
			found = true
		}
	}
	if !found {
		t.Error("youtube must request force-ssl, or youtube_post_comment 403s")
	}
}
