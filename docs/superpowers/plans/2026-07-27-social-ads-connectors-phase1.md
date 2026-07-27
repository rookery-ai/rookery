# Social & Advertising Connectors — Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four read-only Google publisher-side connector providers (AdSense, GA4,
Search Console, YouTube) and the two framework fixes they expose.

**Architecture:** Each provider is two embedded YAML data files — `providers/<p>.yaml`
(auth) and `connectors/<p>.yaml` (typed actions) — reusing the existing Google OAuth app
via `auth_parent: google`, exactly as `google_drive` / `google_sheets` already do. No new
Go auth code. Two framework changes land alongside: the UI provider list becomes
registry-derived instead of a hardcoded slice, and the CLI-coder bridge gains the response
byte cap the API engine already has.

**Tech Stack:** Go 1.x (`modernc.org/sqlite`, `gopkg.in/yaml.v3`, Echo v4), React + Vite
SPA, Vitest.

## Global Constraints

- **Read-only.** No action in Phase 1 sets `mutating: true`. Publishing arrives in Phase 2
  with the approval gate.
- **All four providers use `auth_parent: google`.** They declare only their own scopes,
  label, and setup steps; `authorize_url`, `token_url`, and app credentials resolve from
  the `google` parent.
- **Identifiers are discovered, never configured.** Every provider ships a list action
  (`accounts`, `properties`, `sites`, `mine=true`) using the same scope as its reporting
  action. No `connect_inputs` in this phase.
- **Query values are single-valued.** `render.go:238` uses `q.Set(k, val)` — repeated query
  params are impossible. Google's REST mapping accepts comma-separated values for repeated
  params, so multi-value args are typed `string` (e.g. `"CLICKS,ESTIMATED_EARNINGS"`), never
  `array`.
- **Every new provider needs a vendored logo** in `web/ui/src/assets/logos/<slug>.svg` or
  `web/logo_coverage_test.go` fails. Assets must start with `<svg`, contain a `viewBox`, and
  contain no `<script>` or `<title>`.
- **Conventional Commits**, and never commit to `main` — this work is on a feature branch.
- Run `go test ./... -count=1 -timeout 120s` before each commit.

---

### Task 1: Derive the UI provider list from the registry

`availableServiceProviders` is a hardcoded slice, which already contradicts the package
doc's "adding a service = two YAML files, no Go changes". Fixing it first means every
later task's provider appears in the UI automatically.

**Files:**
- Modify: `web/handlers_services.go:67-69`
- Modify: `web/server.go` (wherever `Server` is constructed — the registry is already a
  field, `s.connectors`)
- Test: `web/services_provider_list_test.go` (create)

**Interfaces:**
- Consumes: `connectors.Registry` (already on `Server` as `s.connectors`).
- Produces: `(*Server).serviceProviders() []string` — registry-ordered provider slugs.
  Replaces the package-level `availableServiceProviders` var for handler use.
  `web/logo_coverage_test.go` and `web/api_services.go` both consume it.

- [ ] **Step 1: Add `ProviderNames()` to the registry**

`internal/connectors/registry.go` — the registry already holds `providers map[string]Provider`.
Map iteration order is random, so sort for a stable UI order:

```go
// ProviderNames returns every loaded provider slug, sorted. The connections page
// renders this set, so it must be deterministic — map order is not.
func (r *Registry) ProviderNames() []string {
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
```

Add `"sort"` to the imports.

- [ ] **Step 2: Write the failing test**

Create `web/services_provider_list_test.go`:

```go
package web

import (
	"testing"

	"github.com/ilijad1/simple-agents/internal/connectors"
)

// The connections page must show every provider the registry can actually
// execute. A hardcoded list silently omits newly added data files.
func TestServiceProvidersCoversRegistry(t *testing.T) {
	reg, err := connectors.LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	s := &Server{connectors: reg}

	got := s.serviceProviders()
	if len(got) != len(reg.ProviderNames()) {
		t.Fatalf("serviceProviders() returned %d providers, registry has %d",
			len(got), len(reg.ProviderNames()))
	}

	seen := map[string]bool{}
	for _, p := range got {
		seen[p] = true
	}
	for _, want := range []string{"google", "github", "notion", "stripe"} {
		if !seen[want] {
			t.Errorf("provider %q missing from serviceProviders()", want)
		}
	}
}
```

- [ ] **Step 3: Run it and confirm it fails**

Run: `go test ./web/ -run TestServiceProvidersCoversRegistry -count=1`
Expected: FAIL — `s.serviceProviders undefined`.

- [ ] **Step 4: Replace the slice with the method**

In `web/handlers_services.go`, delete the `availableServiceProviders` var and its comment,
and add:

```go
// serviceProviders is the set of providers exposed in the UI: every provider the
// registry loaded. Derived rather than hardcoded so adding a service really is
// "two YAML files, no Go changes" — a maintained slice silently omitted them.
func (s *Server) serviceProviders() []string {
	return s.connectors.ProviderNames()
}
```

- [ ] **Step 5: Update the two consumers**

`web/api_services.go:88-89` — replace both references:

```go
providers := s.serviceProviders()
out := make([]apiServiceProvider, 0, len(providers))
for _, provider := range providers {
```

`web/logo_coverage_test.go:38` — the test builds a `Server` differently from handlers, so
load the registry directly rather than reaching for a server instance:

```go
	// Service connectors — the exact list the connections page renders.
	reg, err := connectors.LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	slugs = append(slugs, reg.ProviderNames()...)
```

Add `"github.com/ilijad1/simple-agents/internal/connectors"` to that file's imports.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS. `TestBrandLogoCoverage` still passes — the registry set and the old
hardcoded set are identical today, which is exactly why this refactor is safe to do first.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/registry.go web/handlers_services.go web/api_services.go \
        web/logo_coverage_test.go web/services_provider_list_test.go
git commit -m "refactor(connectors): derive the UI provider list from the registry"
```

---

### Task 2: Cap the connector bridge response

`bridge.go` returns `res.Data` uncapped to CLI coders while the API engine truncates at
`maxToolResult` (8 KiB). A GA4 `runReport` is exactly the payload that exploits this.

**Files:**
- Modify: `internal/connectors/bridge.go:115-122`
- Test: `internal/connectors/bridge_cap_test.go` (create)

**Interfaces:**
- Produces: bridge `/exec` responses stay `{"data": <json>}` when under the cap. Over the
  cap they become `{"data": "<prefix>…", "truncated": true, "note": "<guidance>"}` — `data`
  becomes a JSON *string* instead of the original value. The shape change is deliberate and
  self-describing: a silently-cut JSON value would be indistinguishable from real data.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/bridge_cap_test.go`:

```go
package connectors

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapBridgeDataUnderCapIsUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"rows":[1,2,3]}`)
	out := capBridgeData(raw)

	got, ok := out["data"]
	if !ok {
		t.Fatal(`under-cap response must keep the "data" key`)
	}
	if string(got.(json.RawMessage)) != `{"rows":[1,2,3]}` {
		t.Errorf("under-cap data was modified: %s", got)
	}
	if _, truncated := out["truncated"]; truncated {
		t.Error("under-cap response must not be marked truncated")
	}
}

func TestCapBridgeDataOverCapTruncatesWithNotice(t *testing.T) {
	raw := json.RawMessage(`{"rows":"` + strings.Repeat("x", maxBridgeResult+100) + `"}`)
	out := capBridgeData(raw)

	if out["truncated"] != true {
		t.Fatal("over-cap response must be marked truncated")
	}
	s, ok := out["data"].(string)
	if !ok {
		t.Fatalf("over-cap data must be a string, got %T", out["data"])
	}
	if len(s) > maxBridgeResult+8 { // +8 allows the ellipsis rune
		t.Errorf("truncated data is %d bytes, cap is %d", len(s), maxBridgeResult)
	}
	note, _ := out["note"].(string)
	if !strings.Contains(note, "narrow") {
		t.Errorf("note must tell the model how to recover, got %q", note)
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/connectors/ -run TestCapBridgeData -count=1`
Expected: FAIL — `undefined: capBridgeData`, `undefined: maxBridgeResult`.

- [ ] **Step 3: Implement the cap**

In `internal/connectors/bridge.go`, above the handler:

```go
// maxBridgeResult mirrors coder.maxToolResult: the API engine truncates a connector
// result before it reaches the model, and a CLI coder reading this bridge must not be
// handed an unbounded one. Analytics and ad-insights responses are the payloads that
// make the difference — a 30-day report can run to megabytes.
const maxBridgeResult = 8 * 1024

// capBridgeData bounds a connector result for the wire. Under the cap the response is
// unchanged: {"data": <original json>}. Over it, data becomes a truncated STRING plus an
// explicit truncated/note pair — cutting a JSON value in place would produce something
// that reads as real, complete data.
func capBridgeData(data json.RawMessage) map[string]any {
	if len(data) <= maxBridgeResult {
		return map[string]any{"data": data}
	}
	return map[string]any{
		"data":      string(data[:maxBridgeResult]) + "…",
		"truncated": true,
		"note": "response exceeded " + strconv.Itoa(maxBridgeResult) +
			" bytes and was cut. Re-run with a narrower query — a shorter date range, " +
			"a smaller limit, or fewer dimensions.",
	}
}
```

Add `"strconv"` to the imports.

- [ ] **Step 4: Use it in the handler**

Replace `bridge.go:122`:

```go
		writeJSON(w, http.StatusOK, capBridgeData(res.Data))
```

The handler's declared response type changes from `map[string]json.RawMessage` to the
`map[string]any` that `capBridgeData` returns; `writeJSON` takes `any`, so nothing else
changes.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/connectors/ -count=1`
Expected: PASS, including the existing `bridge_test.go` and `bridge_cli_test.go` — under-cap
responses are byte-identical, which is what keeps them green.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/bridge.go internal/connectors/bridge_cap_test.go
git commit -m "fix(connectors): cap bridge responses like the API engine does"
```

---

### Task 3: AdSense provider

**Files:**
- Create: `internal/connectors/providers/google_adsense.yaml`
- Create: `internal/connectors/connectors/google_adsense.yaml`
- Create: `web/ui/src/assets/logos/google_adsense.svg`
- Test: `internal/connectors/google_publisher_test.go` (create — Tasks 3–6 all add to it)

**Interfaces:**
- Produces: provider slug `google_adsense`; actions `adsense_list_accounts`,
  `adsense_list_adclients`, `adsense_report`. The `account` argument is the full resource
  name (`accounts/pub-XXXXXXXXXXXXXXXX`) as returned by `adsense_list_accounts`, so URL
  templates interpolate it directly with no `accounts/` prefix of their own.

- [ ] **Step 1: Write the provider file**

Create `internal/connectors/providers/google_adsense.yaml`:

```yaml
name: google_adsense
label: Google AdSense
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/adsense.readonly
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "AdSense reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, also enable the AdSense Management API."
  - "Then click Connect here to authorize AdSense access on the same Google account."
```

- [ ] **Step 2: Write the action manifest**

Create `internal/connectors/connectors/google_adsense.yaml`:

```yaml
provider: google_adsense
actions:
  - name: adsense_list_accounts
    description: "List your AdSense accounts. Read-only. Call this FIRST — every other AdSense action needs the account resource name it returns, which looks like accounts/pub-1234567890123456."
    mutating: false
    params:
      type: object
      properties: {}
    request:
      method: GET
      url: "https://adsense.googleapis.com/v2/accounts"
    response_extract: "$.accounts"
  - name: adsense_list_adclients
    description: "List the ad clients (sites/apps) under one AdSense account. Read-only. Use it to find which properties earn."
    mutating: false
    params:
      type: object
      properties:
        account: {type: string, description: "account resource name from adsense_list_accounts, e.g. accounts/pub-1234567890123456"}
      required: [account]
    request:
      method: GET
      url: "https://adsense.googleapis.com/v2/{{account}}/adclients"
    response_extract: "$.adClients"
  - name: adsense_report
    description: "Earnings report for an AdSense account over a preset date range. Read-only. Use for 'how much did I earn last week', 'which page earned most'."
    mutating: false
    params:
      type: object
      properties:
        account:     {type: string, description: "account resource name, e.g. accounts/pub-1234567890123456"}
        date_range:  {type: string, description: "one of TODAY, YESTERDAY, MONTH_TO_DATE, YEAR_TO_DATE, LAST_7_DAYS, LAST_30_DAYS"}
        metrics:     {type: string, description: "comma-separated, e.g. ESTIMATED_EARNINGS,CLICKS,IMPRESSIONS,PAGE_VIEWS"}
        dimensions:  {type: string, description: "comma-separated, e.g. DATE or PAGE_URL or COUNTRY_CODE. Optional."}
        limit:       {type: integer, description: "max rows (default 50 — keep this small, reports are large)"}
      required: [account, date_range, metrics]
    request:
      method: GET
      url: "https://adsense.googleapis.com/v2/{{account}}/reports:generate"
      query:
        dateRange:  "{{date_range}}"
        metrics:    "{{metrics}}"
        dimensions: "{{dimensions}}"
        limit:      "{{limit}}"
    response_extract: "$"
```

Note: `startDate`/`endDate` (for `dateRange: CUSTOM`) are Date *objects*, which flatten to
`startDate.year` / `.month` / `.day` query params. Preset ranges cover the real use cases,
so CUSTOM is deliberately omitted rather than modelled awkwardly.

- [ ] **Step 3: Vendor the logo**

```bash
./scripts/vendor-brand-logos.sh
```

If the script has no AdSense entry, add one following its existing pattern, then verify:

```bash
head -c 40 web/ui/src/assets/logos/google_adsense.svg
```

Expected: output begins with `<svg`. Confirm it contains `viewBox` and no `<title>` or
`<script>` — `TestBrandLogoAssetsAreWellFormed` enforces all three.

- [ ] **Step 4: Write the failing test**

Create `internal/connectors/google_publisher_test.go`:

```go
package connectors

import (
	"encoding/json"
	"strings"
	"testing"
)

// renderFor is a helper for the four Google publisher providers: it loads the
// bundled registry, finds the action, and renders a request from typed args.
func renderFor(t *testing.T, provider, action string, args map[string]any) (method, url string, body []byte) {
	t.Helper()
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	a, ok := reg.Action(provider, action)
	if !ok {
		t.Fatalf("action %q not found for provider %q", action, provider)
	}
	method, url, body, _, err = renderRequest(a, args, nil)
	if err != nil {
		t.Fatalf("renderRequest: %v", err)
	}
	return method, url, body
}

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
	method, url, _ := renderFor(t, "google_adsense", "adsense_report", map[string]any{
		"account":    "accounts/pub-1234567890123456",
		"date_range": "LAST_7_DAYS",
		"metrics":    "ESTIMATED_EARNINGS,CLICKS",
		"limit":      50,
	})
	if method != "GET" {
		t.Errorf("method = %q, want GET", method)
	}
	if !strings.HasPrefix(url, "https://adsense.googleapis.com/v2/accounts/pub-1234567890123456/reports:generate?") {
		t.Fatalf("unexpected URL: %s", url)
	}
	for _, want := range []string{"dateRange=LAST_7_DAYS", "metrics=ESTIMATED_EARNINGS%2CCLICKS", "limit=50"} {
		if !strings.Contains(url, want) {
			t.Errorf("URL missing %q: %s", want, url)
		}
	}
	// dimensions was not supplied, so it must be dropped, not sent empty.
	if strings.Contains(url, "dimensions=") {
		t.Errorf("empty optional param should be dropped: %s", url)
	}
}

func TestAdSenseActionsAreReadOnly(t *testing.T) {
	reg, _ := LoadBundled()
	for _, a := range reg.Actions("google_adsense") {
		if a.Mutating {
			t.Errorf("action %q is mutating — Phase 1 is read-only", a.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(a.Params, &schema); err != nil {
			t.Errorf("action %q has an unparseable schema: %v", a.Name, err)
		}
	}
}
```

`reg.Actions(provider) []Action` is defined at `registry.go:220`, and
`renderRequest(a, args, connVars)` at `render.go:225` returns
`(method, url string, body []byte, contentType string, err error)` — the helper above
matches both.

- [ ] **Step 5: Run it and confirm it fails, then passes**

Run: `go test ./internal/connectors/ -run 'TestAdSense' -count=1`

Before the YAML files exist this fails with "provider not loaded". After Steps 1–2 it
passes. If it fails on the URL assertion, print the actual URL and reconcile — do not edit
the assertion to match a wrong URL.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS. `TestBrandLogoCoverage` now demands `google_adsense.svg`, which Step 3
supplied — if it fails here, the logo is missing or malformed.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/google_adsense.yaml \
        internal/connectors/connectors/google_adsense.yaml \
        internal/connectors/google_publisher_test.go \
        web/ui/src/assets/logos/google_adsense.svg
git commit -m "feat(connectors): add Google AdSense provider"
```

---

### Task 4: GA4 provider

**Files:**
- Create: `internal/connectors/providers/google_analytics.yaml`
- Create: `internal/connectors/connectors/google_analytics.yaml`
- Create: `web/ui/src/assets/logos/google_analytics.svg`
- Modify: `internal/connectors/google_publisher_test.go`

**Interfaces:**
- Produces: provider slug `google_analytics`; actions `ga4_list_properties`,
  `ga4_run_report`, `ga4_run_realtime_report`, `ga4_metadata`. The `property` argument is
  the full resource name (`properties/123456789`) as returned by `ga4_list_properties`.
- Note: this provider spans two API hosts — `analyticsadmin.googleapis.com` for property
  discovery and `analyticsdata.googleapis.com` for reporting. Both are covered by the single
  `analytics.readonly` scope, which is why discovery needs no extra consent.

- [ ] **Step 1: Write the provider file**

```yaml
name: google_analytics
label: Google Analytics (GA4)
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/analytics.readonly
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "Google Analytics reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, enable BOTH the Google Analytics Data API and the Google Analytics Admin API."
  - "Then click Connect here to authorize Analytics access on the same Google account."
```

- [ ] **Step 2: Write the action manifest**

```yaml
provider: google_analytics
actions:
  - name: ga4_list_properties
    description: "List the GA4 properties you can access, with their display names. Read-only. Call this FIRST — reporting actions need the property resource name it returns, e.g. properties/123456789."
    mutating: false
    params:
      type: object
      properties: {}
    request:
      method: GET
      url: "https://analyticsadmin.googleapis.com/v1beta/accountSummaries"
    response_extract: "$.accountSummaries"
  - name: ga4_run_report
    description: "Run a GA4 report over a date range. Read-only. Use for 'how many users last week', 'top pages this month', 'traffic by country'."
    mutating: false
    params:
      type: object
      properties:
        property:   {type: string, description: "property resource name from ga4_list_properties, e.g. properties/123456789"}
        start_date: {type: string, description: "YYYY-MM-DD, or a relative form like 28daysAgo / yesterday"}
        end_date:   {type: string, description: "YYYY-MM-DD, or today / yesterday"}
        metrics:    {type: array, items: {type: string}, description: "GA4 metric names, e.g. [activeUsers, screenPageViews]"}
        dimensions: {type: array, items: {type: string}, description: "GA4 dimension names, e.g. [date] or [pagePath]. Optional."}
        limit:      {type: integer, description: "max rows (default 50 — keep this small)"}
      required: [property, start_date, end_date, metrics]
    request:
      method: POST
      url: "https://analyticsdata.googleapis.com/v1beta/{{property}}:runReport"
      body:
        dateRanges:
          - startDate: "{{start_date}}"
            endDate: "{{end_date}}"
        metrics: "{{metrics}}"
        dimensions: "{{dimensions}}"
        limit: "{{limit}}"
    response_extract: "$.rows"
  - name: ga4_run_realtime_report
    description: "Active users and events in the last 30 minutes. Read-only. Use for 'who is on the site right now'."
    mutating: false
    params:
      type: object
      properties:
        property:   {type: string, description: "property resource name, e.g. properties/123456789"}
        metrics:    {type: array, items: {type: string}, description: "e.g. [activeUsers]"}
        dimensions: {type: array, items: {type: string}, description: "e.g. [country]. Optional."}
      required: [property, metrics]
    request:
      method: POST
      url: "https://analyticsdata.googleapis.com/v1beta/{{property}}:runRealtimeReport"
      body:
        metrics: "{{metrics}}"
        dimensions: "{{dimensions}}"
    response_extract: "$.rows"
  - name: ga4_metadata
    description: "List the metric and dimension names available for a property. Read-only. Call this when a report fails with an unknown metric or dimension."
    mutating: false
    params:
      type: object
      properties:
        property: {type: string, description: "property resource name, e.g. properties/123456789"}
      required: [property]
    request:
      method: GET
      url: "https://analyticsdata.googleapis.com/v1beta/{{property}}/metadata"
    response_extract: "$"
```

The GA4 body needs `metrics` as `[{"name": "activeUsers"}]`, not a bare string array. Verify
in Step 4 whether `renderBody` produces the nested object form from a string array; if it
does not, model the arrays as arrays-of-objects in the template:

```yaml
        metrics:
          - name: "{{metrics}}"
```

Whichever form the test proves correct is the one that ships — do not guess.

- [ ] **Step 3: Vendor the logo**

```bash
./scripts/vendor-brand-logos.sh
head -c 40 web/ui/src/assets/logos/google_analytics.svg
```

- [ ] **Step 4: Write the failing test**

Append to `internal/connectors/google_publisher_test.go`:

```go
func TestGA4RunReportBodyShape(t *testing.T) {
	_, url, body := renderFor(t, "google_analytics", "ga4_run_report", map[string]any{
		"property":   "properties/123456789",
		"start_date": "28daysAgo",
		"end_date":   "yesterday",
		"metrics":    []any{"activeUsers"},
		"limit":      50,
	})
	if url != "https://analyticsdata.googleapis.com/v1beta/properties/123456789:runReport" {
		t.Fatalf("unexpected URL: %s", url)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v — %s", err, body)
	}

	// GA4 requires metrics as [{"name": "..."}]. This assertion is the whole
	// point of the test: a bare string array is accepted by the renderer and
	// rejected by Google at request time, which is the expensive way to find out.
	metrics, ok := got["metrics"].([]any)
	if !ok || len(metrics) == 0 {
		t.Fatalf("metrics missing or not an array: %v", got["metrics"])
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
}

func TestGA4PropertyDiscoveryUsesAdminHost(t *testing.T) {
	_, url, _ := renderFor(t, "google_analytics", "ga4_list_properties", map[string]any{})
	if url != "https://analyticsadmin.googleapis.com/v1beta/accountSummaries" {
		t.Errorf("unexpected discovery URL: %s", url)
	}
}
```

- [ ] **Step 5: Run it, and let it drive the body shape**

Run: `go test ./internal/connectors/ -run 'TestGA4' -count=1 -v`

If `TestGA4RunReportBodyShape` fails at the `metrics[0]` assertion, the template's flat form
is wrong — switch to the arrays-of-objects form from Step 2 and re-run. This is the test
doing its job; do not relax the assertion.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/google_analytics.yaml \
        internal/connectors/connectors/google_analytics.yaml \
        internal/connectors/google_publisher_test.go \
        web/ui/src/assets/logos/google_analytics.svg
git commit -m "feat(connectors): add Google Analytics GA4 provider"
```

---

### Task 5: Search Console provider

**Files:**
- Create: `internal/connectors/providers/google_searchconsole.yaml`
- Create: `internal/connectors/connectors/google_searchconsole.yaml`
- Create: `web/ui/src/assets/logos/google_searchconsole.svg`
- Modify: `internal/connectors/google_publisher_test.go`

**Interfaces:**
- Produces: provider slug `google_searchconsole`; actions `gsc_list_sites`,
  `gsc_search_analytics`, `gsc_list_sitemaps`. The `site_url` argument is a full property
  URL (`https://example.com/` or `sc-domain:example.com`) and **must be URL-escaped in the
  path** — verify the renderer does this in Step 4.

- [ ] **Step 1: Write the provider file**

```yaml
name: google_searchconsole
label: Google Search Console
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/webmasters.readonly
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "Search Console reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, also enable the Google Search Console API."
  - "Then click Connect here to authorize Search Console access on the same Google account."
```

- [ ] **Step 2: Write the action manifest**

```yaml
provider: google_searchconsole
actions:
  - name: gsc_list_sites
    description: "List the Search Console properties you can access. Read-only. Call this FIRST — other actions need the site URL it returns, e.g. https://example.com/ or sc-domain:example.com."
    mutating: false
    params:
      type: object
      properties: {}
    request:
      method: GET
      url: "https://www.googleapis.com/webmasters/v3/sites"
    response_extract: "$.siteEntry"
  - name: gsc_search_analytics
    description: "Search performance for a site: clicks, impressions, CTR, position. Read-only. Use for 'which queries bring traffic', 'how did search traffic change'."
    mutating: false
    params:
      type: object
      properties:
        site_url:   {type: string, description: "site URL from gsc_list_sites, e.g. https://example.com/"}
        start_date: {type: string, description: "YYYY-MM-DD"}
        end_date:   {type: string, description: "YYYY-MM-DD"}
        dimensions: {type: array, items: {type: string}, description: "any of query, page, country, device, date. Optional."}
        row_limit:  {type: integer, description: "max rows (default 50 — keep this small)"}
      required: [site_url, start_date, end_date]
    request:
      method: POST
      url: "https://www.googleapis.com/webmasters/v3/sites/{{site_url}}/searchAnalytics/query"
      body:
        startDate:  "{{start_date}}"
        endDate:    "{{end_date}}"
        dimensions: "{{dimensions}}"
        rowLimit:   "{{row_limit}}"
    response_extract: "$.rows"
  - name: gsc_list_sitemaps
    description: "List submitted sitemaps for a site and their processing status. Read-only."
    mutating: false
    params:
      type: object
      properties:
        site_url: {type: string, description: "site URL from gsc_list_sites"}
      required: [site_url]
    request:
      method: GET
      url: "https://www.googleapis.com/webmasters/v3/sites/{{site_url}}/sitemaps"
    response_extract: "$.sitemap"
```

- [ ] **Step 3: Vendor the logo**

```bash
./scripts/vendor-brand-logos.sh
head -c 40 web/ui/src/assets/logos/google_searchconsole.svg
```

- [ ] **Step 4: Write the failing test — path escaping is the risk here**

Append to `internal/connectors/google_publisher_test.go`:

```go
// A Search Console site URL is itself a URL sitting in a path segment. If the
// renderer interpolates it raw, "https://example.com/" becomes extra path
// separators and the request 404s against a nonsense path.
func TestGSCSiteURLIsEscapedInPath(t *testing.T) {
	_, url, _ := renderFor(t, "google_searchconsole", "gsc_search_analytics", map[string]any{
		"site_url":   "https://example.com/",
		"start_date": "2026-07-01",
		"end_date":   "2026-07-27",
	})
	const want = "https://www.googleapis.com/webmasters/v3/sites/https%3A%2F%2Fexample.com%2F/searchAnalytics/query"
	if url != want {
		t.Errorf("site URL not escaped into the path.\n got: %s\nwant: %s", url, want)
	}
}

func TestGSCListSitesNeedsNoArgs(t *testing.T) {
	method, url, _ := renderFor(t, "google_searchconsole", "gsc_list_sites", map[string]any{})
	if method != "GET" || url != "https://www.googleapis.com/webmasters/v3/sites" {
		t.Errorf("unexpected request: %s %s", method, url)
	}
}
```

- [ ] **Step 5: Run it — it WILL fail, and the obvious fix is wrong**

Run: `go test ./internal/connectors/ -run 'TestGSC' -count=1 -v`
Expected: FAIL. `subst` (`render.go:36`) interpolates raw via `asString`, so
`https://example.com/` lands in the path as literal separators.

**Do not fix this by escaping every path substitution.** AdSense passes
`accounts/pub-1234567890123456` and GA4 passes `properties/123456789` — both contain a `/`
that is a real path separator and must survive. A blanket `url.PathEscape` breaks Tasks 3
and 4, which is exactly the kind of regression a shared renderer invites.

Escaping is **per placeholder**. Extend `subst` to honour an `|escape` suffix:

```go
// placeholderRE must capture an optional |escape suffix, e.g. {{site_url|escape}}.
// Update the pattern to: \{\{([a-zA-Z0-9_.]+)(\|escape)?\}\}

func subst(tmpl string, args map[string]any, connVars map[string]string) string {
	return placeholderRE.ReplaceAllStringFunc(tmpl, func(m string) string {
		g := placeholderRE.FindStringSubmatch(m)
		name, escape := g[1], g[2] == "|escape"
		var val string
		if strings.HasPrefix(name, "conn.") {
			val = connVars[strings.TrimPrefix(name, "conn.")]
		} else {
			val = asString(args[name])
		}
		// Opt-in only: an identifier like "accounts/pub-123" carries a REAL path
		// separator, so escaping every substitution would corrupt it. Only a value
		// that is itself a URL sitting in a path segment asks for this.
		//
		// PathEscape leaves ':' alone (RFC 3986 permits it in a path segment), but
		// Google documents siteUrl fully encoded — "https%3A%2F%2Fwww.example.com%2F"
		// and "sc-domain%3Aexample.com". Without the ReplaceAll the unit test passes
		// with "https:%2F%2F…" and the live request goes out wrong.
		if escape {
			val = strings.ReplaceAll(url.PathEscape(val), ":", "%3A")
		}
		return val
	})
}
```

Add `"net/url"` to `render.go`'s imports, then mark the two Search Console templates:

```yaml
      url: "https://www.googleapis.com/webmasters/v3/sites/{{site_url|escape}}/searchAnalytics/query"
```

```yaml
      url: "https://www.googleapis.com/webmasters/v3/sites/{{site_url|escape}}/sitemaps"
```

- [ ] **Step 5b: Prove the fix is additive**

Run: `go test ./internal/connectors/ -count=1`
Expected: PASS — including `TestAdSenseReportRendersPresetRange` and
`TestGA4RunReportBodyShape`, whose unescaped `/` must still render literally. If either
broke, the escape is being applied unconditionally.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/google_searchconsole.yaml \
        internal/connectors/connectors/google_searchconsole.yaml \
        internal/connectors/google_publisher_test.go \
        internal/connectors/render.go \
        web/ui/src/assets/logos/google_searchconsole.svg
git commit -m "feat(connectors): add Google Search Console provider"
```

(Drop `render.go` from the `git add` if Step 5 required no renderer change.)

---

### Task 6: YouTube provider

**Files:**
- Create: `internal/connectors/providers/youtube.yaml`
- Create: `internal/connectors/connectors/youtube.yaml`
- Create: `web/ui/src/assets/logos/youtube.svg`
- Modify: `internal/connectors/google_publisher_test.go`

**Interfaces:**
- Produces: provider slug `youtube`; actions `youtube_my_channel`, `youtube_list_videos`,
  `youtube_video_stats`, `youtube_list_comments`, `youtube_analytics_report`. No identifier
  argument is needed for the channel itself — `mine=true` and `ids=channel==MINE` resolve it
  from the token.
- Note: spans `youtube.googleapis.com` (Data API v3) and `youtubeanalytics.googleapis.com`
  (Analytics v2). `reports.query` requires `youtube.readonly`; `yt-analytics.readonly` is
  declared as well so revenue-free analytics work without a re-consent later.

- [ ] **Step 1: Write the provider file**

```yaml
name: youtube
label: YouTube
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/youtube.readonly
  - https://www.googleapis.com/auth/yt-analytics.readonly
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "YouTube reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, enable BOTH the YouTube Data API v3 and the YouTube Analytics API."
  - "Then click Connect here to authorize YouTube access on the same Google account."
```

- [ ] **Step 2: Write the action manifest**

```yaml
provider: youtube
actions:
  - name: youtube_my_channel
    description: "Your own channel: title, description, subscriber/view/video counts. Read-only. Call this FIRST — it returns the uploads playlist id other actions need."
    mutating: false
    params:
      type: object
      properties: {}
    request:
      method: GET
      url: "https://youtube.googleapis.com/youtube/v3/channels"
      query:
        part: "snippet,statistics,contentDetails"
        mine: "true"
    response_extract: "$.items"
  - name: youtube_list_videos
    description: "List videos in a playlist — pass the uploads playlist id from youtube_my_channel to list your own uploads, newest first. Read-only."
    mutating: false
    params:
      type: object
      properties:
        playlist_id: {type: string, description: "playlist id; the uploads playlist is at contentDetails.relatedPlaylists.uploads in youtube_my_channel"}
        max:         {type: integer, description: "max results, 1-50 (default 25)"}
      required: [playlist_id]
    request:
      method: GET
      url: "https://youtube.googleapis.com/youtube/v3/playlistItems"
      query:
        part:       "snippet,contentDetails"
        playlistId: "{{playlist_id}}"
        maxResults: "{{max}}"
    response_extract: "$.items"
  - name: youtube_video_stats
    description: "View, like, and comment counts for one or more videos. Read-only."
    mutating: false
    params:
      type: object
      properties:
        video_ids: {type: string, description: "comma-separated video ids, e.g. abc123,def456"}
      required: [video_ids]
    request:
      method: GET
      url: "https://youtube.googleapis.com/youtube/v3/videos"
      query:
        part: "snippet,statistics"
        id:   "{{video_ids}}"
    response_extract: "$.items"
  - name: youtube_list_comments
    description: "Top-level comments on a video, newest first. Read-only. Use for 'what are people saying about my latest video'."
    mutating: false
    params:
      type: object
      properties:
        video_id: {type: string, description: "video id"}
        max:      {type: integer, description: "max results, 1-100 (default 25)"}
      required: [video_id]
    request:
      method: GET
      url: "https://youtube.googleapis.com/youtube/v3/commentThreads"
      query:
        part:       "snippet"
        videoId:    "{{video_id}}"
        order:      "time"
        maxResults: "{{max}}"
    response_extract: "$.items"
  - name: youtube_analytics_report
    description: "Channel analytics over a date range: views, watch time, subscribers gained. Read-only. Use for 'how did the channel do last month'."
    mutating: false
    params:
      type: object
      properties:
        start_date: {type: string, description: "YYYY-MM-DD"}
        end_date:   {type: string, description: "YYYY-MM-DD"}
        metrics:    {type: string, description: "comma-separated, e.g. views,estimatedMinutesWatched,subscribersGained"}
        dimensions: {type: string, description: "comma-separated, e.g. day or country. Optional."}
      required: [start_date, end_date, metrics]
    request:
      method: GET
      url: "https://youtubeanalytics.googleapis.com/v2/reports"
      query:
        ids:        "channel==MINE"
        startDate:  "{{start_date}}"
        endDate:    "{{end_date}}"
        metrics:    "{{metrics}}"
        dimensions: "{{dimensions}}"
    response_extract: "$"
```

- [ ] **Step 3: Vendor the logo**

```bash
./scripts/vendor-brand-logos.sh
head -c 40 web/ui/src/assets/logos/youtube.svg
```

- [ ] **Step 4: Write the failing test**

Append to `internal/connectors/google_publisher_test.go`:

```go
func TestYouTubeChannelUsesMine(t *testing.T) {
	_, url, _ := renderFor(t, "youtube", "youtube_my_channel", map[string]any{})
	if !strings.Contains(url, "mine=true") {
		t.Errorf("channel lookup must use mine=true, got %s", url)
	}
	// contentDetails carries the uploads playlist id the listing action needs.
	if !strings.Contains(url, "contentDetails") {
		t.Errorf("part must include contentDetails: %s", url)
	}
}

func TestYouTubeAnalyticsRendersChannelMine(t *testing.T) {
	_, url, _ := renderFor(t, "youtube", "youtube_analytics_report", map[string]any{
		"start_date": "2026-06-01",
		"end_date":   "2026-06-30",
		"metrics":    "views,estimatedMinutesWatched",
	})
	if !strings.HasPrefix(url, "https://youtubeanalytics.googleapis.com/v2/reports?") {
		t.Fatalf("unexpected analytics host: %s", url)
	}
	for _, want := range []string{"ids=channel%3D%3DMINE", "startDate=2026-06-01", "metrics=views%2CestimatedMinutesWatched"} {
		if !strings.Contains(url, want) {
			t.Errorf("URL missing %q: %s", want, url)
		}
	}
}

// Every Phase 1 provider is read-only and every action name must satisfy the
// tool-name regex the coder backends enforce.
func TestPublisherProvidersAreReadOnlyAndWellNamed(t *testing.T) {
	reg, _ := LoadBundled()
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	seen := map[string]string{}
	for _, p := range []string{"google_adsense", "google_analytics", "google_searchconsole", "youtube"} {
		actions := reg.Actions(p)
		if len(actions) == 0 {
			t.Errorf("provider %q has no actions", p)
		}
		for _, a := range actions {
			if a.Mutating {
				t.Errorf("%s: action %q is mutating — Phase 1 is read-only", p, a.Name)
			}
			if !valid.MatchString(a.Name) {
				t.Errorf("%s: action name %q fails the tool-name regex", p, a.Name)
			}
			if prev, dup := seen[a.Name]; dup {
				t.Errorf("action name %q is used by both %s and %s", a.Name, prev, p)
			}
			seen[a.Name] = p
			if a.Description == "" {
				t.Errorf("%s: action %q has no description — the model cannot pick it", p, a.Name)
			}
		}
	}
}
```

Add `"regexp"` to the test file's imports.

- [ ] **Step 5: Run the provider tests**

Run: `go test ./internal/connectors/ -run 'TestYouTube|TestPublisher' -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Run the full suite**

Run: `go test ./... -count=1 -timeout 120s`
Expected: PASS, with `TestBrandLogoCoverage` now covering all four new slugs.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/youtube.yaml \
        internal/connectors/connectors/youtube.yaml \
        internal/connectors/google_publisher_test.go \
        web/ui/src/assets/logos/youtube.svg
git commit -m "feat(connectors): add YouTube provider"
```

---

### Task 7: Group providers by category on the connections page

32 providers in one flat list, heading for 44. Categories come from the provider YAML so a
new data file lands in the right group without a UI edit.

**Files:**
- Modify: `internal/connectors/registry.go` (add `Category` to `Provider`)
- Modify: all 32 `internal/connectors/providers/*.yaml` (add one `category:` line each)
- Modify: `web/api_services.go` (expose `category` in `apiServiceProvider`)
- Modify: `web/ui/src/pages/connections/ConnectionsPage.tsx`
- Test: `internal/connectors/category_test.go` (create),
  `web/ui/src/pages/connections/connections.test.tsx` (modify)

**Interfaces:**
- Produces: `Provider.Category string` (yaml `category`), surfaced as `category` on the
  services API response. Categories: `Google`, `Publishing & Media`, `Advertising`,
  `Productivity`, `Developer`, `Commerce`, `Support`, `Communication`, `Other`. A provider
  with no category falls into `Other` rather than disappearing.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/category_test.go`:

```go
package connectors

import "testing"

// Every bundled provider must declare a category, or it silently lands in
// "Other" on a page whose whole purpose is grouping.
func TestEveryProviderHasACategory(t *testing.T) {
	reg, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	valid := map[string]bool{
		"Google": true, "Publishing & Media": true, "Advertising": true,
		"Productivity": true, "Developer": true, "Commerce": true,
		"Support": true, "Communication": true, "Other": true,
	}
	for _, name := range reg.ProviderNames() {
		p, _ := reg.ProviderByName(name)
		if p.Category == "" {
			t.Errorf("provider %q has no category", name)
			continue
		}
		if !valid[p.Category] {
			t.Errorf("provider %q has unknown category %q", name, p.Category)
		}
	}
}
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `go test ./internal/connectors/ -run TestEveryProviderHasACategory -count=1`
Expected: FAIL — `p.Category undefined`.

- [ ] **Step 3: Add the field**

In `internal/connectors/registry.go`, in the `Provider` struct's UI block:

```go
	// Category groups this provider on the connections page. One of: Google,
	// Publishing & Media, Advertising, Productivity, Developer, Commerce,
	// Support, Communication, Other. Empty is treated as Other.
	Category string `yaml:"category"`
```

- [ ] **Step 4: Add `category:` to every provider YAML**

Suggested assignment — one line per file, placed under `label:`:

- **Google**: `google`, `google_drive`, `google_sheets`, `google_docs`, `google_adsense`,
  `google_analytics`, `google_searchconsole`
- **Publishing & Media**: `youtube`
- **Productivity**: `notion`, `outlook`, `teams`, `calendly`, `asana`, `airtable`,
  `clickup`, `monday`, `dropbox`, `zoom`, `trello`, `jira`
- **Developer**: `github`, `openai`
- **Commerce**: `stripe`, `shopify`, `salesforce`, `hubspot`
- **Support**: `zendesk`, `intercom`
- **Communication**: `slack`, `sendgrid`, `twilio`, `mailchimp`

Verify none was missed:

```bash
grep -L "^category:" internal/connectors/providers/*.yaml
```

Expected: no output.

- [ ] **Step 5: Run the test**

Run: `go test ./internal/connectors/ -run TestEveryProviderHasACategory -count=1`
Expected: PASS.

- [ ] **Step 6: Expose it on the API**

In `web/api_services.go`, add `Category string \`json:"category"\`` to `apiServiceProvider`
and populate it from the provider in the same loop that fills label and setup fields. Follow
the struct's existing field style.

- [ ] **Step 7: Group in the SPA**

In `ConnectionsPage.tsx`, group the provider cards by `category` before rendering, with a
section heading per group. Use `ContextSection` from `components/shell/ContextPaneParts.tsx`
if the layout suits it; otherwise match the page's existing heading style. Order groups by a
fixed array so the page does not reshuffle as providers are added:

```ts
const CATEGORY_ORDER = [
  "Google",
  "Publishing & Media",
  "Advertising",
  "Productivity",
  "Communication",
  "Commerce",
  "Developer",
  "Support",
  "Other",
] as const;
```

Providers whose category is missing or unrecognised render under `Other`.

- [ ] **Step 8: Update the SPA test**

In `web/ui/src/pages/connections/connections.test.tsx`, add a case asserting that a provider
fixture with `category: "Google"` renders under a "Google" heading, and that a fixture with
an empty category still renders (under "Other") rather than vanishing. Follow the file's
existing fixture and query style.

- [ ] **Step 9: Run both suites**

```bash
go test ./... -count=1 -timeout 120s
cd web/ui && npm test -- --run
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/providers/ \
        internal/connectors/category_test.go web/api_services.go \
        web/ui/src/pages/connections/ConnectionsPage.tsx \
        web/ui/src/pages/connections/connections.test.tsx
git commit -m "feat(web/connections): group providers by category"
```

---

## Manual verification

Automated tests cover rendering and wiring; they cannot prove Google accepts these requests.
After Task 6, verify live against the operator's own Google account:

1. `make build && make deploy`
2. Connections page → connect each of the four new providers (the Google OAuth app already
   exists; each child asks for its own scopes on the same account).
3. For each, run its list action first, then its report action with the identifier the list
   returned:
   - `adsense_list_accounts` → `adsense_report` with `date_range: LAST_7_DAYS`,
     `metrics: ESTIMATED_EARNINGS,CLICKS`
   - `ga4_list_properties` → `ga4_run_report` with `metrics: [activeUsers]`,
     `start_date: 28daysAgo`, `end_date: yesterday`
   - `gsc_list_sites` → `gsc_search_analytics` with a 7-day range and `dimensions: [query]`
   - `youtube_my_channel` → `youtube_analytics_report` with `metrics: views`
4. Record which four actually returned data. A provider whose API was not enabled in Cloud
   Console returns a 403 naming the API — enable it and retry.

**Do this against a temporary data dir, not the live install**, per the project's
live-instance safety rule. Never mutate the operator's production credentials.

## Phases 2–5

Each remaining phase gets its own plan, authored when it starts, because each produces
working software on its own and each depends on framework changes the previous phase proves:

- **Phase 2** — approval gate (`Policy`, `public_write`, `approval_mode`, `pending_actions`,
  worker, `/approve`, inbox) + YouTube upload, LinkedIn personal, Bluesky.
- **Phase 3** — Meta family: encrypted `extra`, page-token `post_connect`, `exchange` token
  mode; Facebook Pages, Instagram, Threads, Meta Ads.
- **Phase 4** — X, TikTok (draft mode), Pinterest (trial), Reddit.
- **Phase 5** — OAuth-path `connect_inputs`, then Google Ads and LinkedIn Ads.

Writing all five now would mean specifying Phase 5's UI against a `connect_inputs` design
that Phase 2's `Policy` refactor may reshape. The spec fixes the architecture; the plans
follow it one phase at a time.
