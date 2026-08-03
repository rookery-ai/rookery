# Connector Framework for Everyday Connectors — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the framework changes that the nine everyday-connector providers depend on — a keyless auth kind, four new categories, a shared base-URL normalizer, a pinned SSRF stance, an upstream logo source, and a committed live-check harness.

**Architecture:** Every change follows an existing precedent in `internal/connectors`. The keyless auth kind mirrors how `session_exchange` (Bluesky) was added: a new `Auth.Kind` value, a `Provider` predicate, a branch in `applyAuth` and in `DBTokenStore.AccessToken`, and a third `kind` string on the services DTO that the SPA wizard branches on. The base-URL normalizer is a pure function called from the one place connect inputs are validated.

**Tech Stack:** Go 1.x (stdlib only — `net/url`, `net/http`), SQLite via `modernc.org/sqlite`, Echo v4, React + TypeScript + Vitest, Tailwind v4.

## Global Constraints

- **No new dependencies.** The connector layer is stdlib-only; the SigV4 and emoji precedents in this repo both hand-rolled rather than pulling a library.
- **Branch, never commit to `main`.** All work on a feature branch; `main` advances only through squash-merged PRs.
- **Conventional Commits** on every commit: `type(scope): summary`. Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `perf`, `build`, `ci`.
- **`make ci` must pass** before the PR: gofmt, `go vet`, `go test -race`, the six-target cross-compile matrix, and the frontend gate (`tsc -b`, `oxlint`, `vitest`, `vite build`).
- **Go tests run from the repo root**: `go test ./internal/connectors/... -count=1`.
- **Frontend tests run from `web/ui`**: `cd web/ui && npx vitest run <path>`.
- **Do not touch** `GET /dashboard/connectors/services/callback/:provider`. That path is the redirect URI registered in external OAuth apps and is frozen.
- **Provider/manifest YAML is `go:embed`ed** from `internal/connectors/providers/` and `internal/connectors/connectors/`. A file added there is loaded by `LoadBundled()` with no Go change.

---

### Task 1: Keyless auth kind — predicate and request path

**Files:**
- Modify: `internal/connectors/registry.go:145-168` (add `Normalize` field to `ConnectInput`, add `IsKeyless` predicate)
- Modify: `internal/connectors/auth.go:9-33` (early return in `applyAuth`)
- Test: `internal/connectors/auth_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func (p Provider) IsKeyless() bool` — true when `p.Auth.Kind == "none"`. Used by Tasks 2, 3 and 4. Also adds the field `ConnectInput.Normalize string` (yaml `normalize`), consumed by Task 6.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/auth_test.go`:

```go
// A keyless provider (Open-Meteo) has no credential at all. The request must go out
// exactly as rendered: falling through to the default Bearer branch would send
// "Authorization: Bearer " with an empty value, which some servers reject outright.
func TestApplyAuthKeylessLeavesRequestUntouched(t *testing.T) {
	req, err := http.NewRequest("GET", "https://api.open-meteo.com/v1/forecast?latitude=41.99", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	prov := Provider{Name: "open_meteo", Auth: AuthConfig{Kind: "none"}}

	applyAuth(req, prov, "", nil)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header = %q, want empty", got)
	}
	if u, p, ok := req.BasicAuth(); ok {
		t.Errorf("basic auth set to %q/%q, want none", u, p)
	}
	if got := req.URL.RawQuery; got != "latitude=41.99" {
		t.Errorf("query = %q, want it unmodified", got)
	}
}

func TestIsKeylessPredicate(t *testing.T) {
	if !(Provider{Auth: AuthConfig{Kind: "none"}}).IsKeyless() {
		t.Error("kind=none should be keyless")
	}
	for _, k := range []string{"", "oauth2", "api_key", "session_exchange"} {
		if (Provider{Auth: AuthConfig{Kind: k}}).IsKeyless() {
			t.Errorf("kind=%q should not be keyless", k)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run 'TestApplyAuthKeyless|TestIsKeylessPredicate' -count=1`

Expected: FAIL — `p.IsKeyless undefined (type Provider has no field or method IsKeyless)`.

- [ ] **Step 3: Add the predicate and the ConnectInput field**

In `internal/connectors/registry.go`, replace the `ConnectInput` struct (lines 145-150) with:

```go
type ConnectInput struct {
	Key      string `yaml:"key"`
	Label    string `yaml:"label"`
	Hint     string `yaml:"hint"`
	Required bool   `yaml:"required"`
	// Normalize names a canonicalizer applied to the pasted value at connect time.
	// "" (default) stores the value verbatim. "base_url" runs NormalizeBaseURL —
	// see internal/connectors/baseurl.go.
	Normalize string `yaml:"normalize"`
}
```

Immediately after the existing `IsAPIKey` method (line 153), add:

```go
// IsKeyless reports whether this provider needs no credential at all (Open-Meteo).
// Its connections store no secret, are never refreshed, and connect with a bare
// button rather than a paste-a-key form.
func (p Provider) IsKeyless() bool { return p.Auth.Kind == "none" }
```

- [ ] **Step 4: Add the `applyAuth` early return**

In `internal/connectors/auth.go`, replace the first two lines of the function body:

```go
	a := prov.Auth
	if a.Kind != "api_key" {
```

with:

```go
	a := prov.Auth
	// Keyless providers have no credential: leave the request exactly as rendered.
	// The default branch below would set "Authorization: Bearer " with an empty
	// value, which is worse than no header at all.
	if a.Kind == "none" {
		return
	}
	if a.Kind != "api_key" {
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestApplyAuthKeyless|TestIsKeylessPredicate' -count=1`

Expected: PASS.

- [ ] **Step 6: Run the whole package to confirm nothing regressed**

Run: `go test ./internal/connectors/ -count=1`

Expected: PASS (`session_exchange` still reaches the Bearer branch — that is the case most likely to break, and `slack_test.go` / the Bluesky tests cover it).

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/auth.go internal/connectors/auth_test.go
git commit -m "feat(connectors): add keyless auth kind to the request path"
```

---

### Task 2: Keyless token store — return empty without refreshing

**Files:**
- Modify: `internal/connectors/dbstore.go:44-75` (branch in `AccessToken`)
- Test: `internal/connectors/dbstore_test.go`

**Interfaces:**
- Consumes: `Provider.IsKeyless()` from Task 1.
- Produces: `DBTokenStore.AccessToken` returns `("", nil)` for a keyless connection. `Execute` then passes that empty credential to `applyAuth`, which ignores it.

**Why this is needed:** without a branch, a keyless row falls through `IsAPIKey` (false), `UsesSessionExchange` (false) and `NonExpiring` (false, since `token_expiry` is unset), then `s.expired("")` returns true for an empty expiry and the store calls `refresh` — which fails with "missing OAuth app credentials". The connector would appear broken on its very first call.

- [ ] **Step 1: Write the failing test**

Append to `internal/connectors/dbstore_test.go`:

```go
// A keyless connection has no credential, no expiry and no refresh token. AccessToken
// must hand back an empty string cleanly rather than falling through to the refresh
// path, which would fail with "missing OAuth app credentials" on the first call.
func TestAccessTokenKeylessReturnsEmptyWithoutRefreshing(t *testing.T) {
	d, ws := storeTestDB(t)
	ctx := context.Background()

	id := uuid.New().String()
	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: id, WorkspaceID: ws, Provider: "open_meteo",
		AccountLabel: "Open-Meteo", AccountIdentity: "Open-Meteo",
		Status: "ACTIVE",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	reg := &Registry{
		providers: map[string]Provider{
			"open_meteo": {Name: "open_meteo", Auth: AuthConfig{Kind: "none"}},
		},
		actions: map[string][]Action{},
	}
	// No OAuth client and no HTTP client: if the store attempts a refresh, it panics
	// or errors rather than silently succeeding against a stub.
	s := &DBTokenStore{DB: d, SystemKey: mkKey(), Reg: reg}

	tok, err := s.AccessToken(ctx, ConnRef{ID: id, Provider: "open_meteo"})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "" {
		t.Errorf("token = %q, want empty", tok)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestAccessTokenKeyless -count=1`

Expected: FAIL with a `KindNeedsReauth` error mentioning missing OAuth app credentials.

- [ ] **Step 3: Add the keyless branch**

In `internal/connectors/dbstore.go`, immediately after the `prov, _ := s.Reg.ProviderByName(row.Provider)` line and **before** the `if prov.UsesSessionExchange()` block, insert:

```go
	// Keyless providers (Open-Meteo) carry no credential. Return empty rather than
	// falling through: an unset expiry reads as "expired", which would send this row
	// down the refresh path and fail with "missing OAuth app credentials".
	if prov.IsKeyless() {
		return "", nil
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestAccessTokenKeyless -count=1`

Expected: PASS.

- [ ] **Step 5: Add a regression test proving the refresh loop already skips keyless rows**

`ConnectionsNearExpiry` filters on `expires_at <> '' AND encrypted_refresh_token <> ''`, so a keyless row is already excluded and **no production change is needed**. Pin that, because it is load-bearing and invisible.

Append to `internal/connectors/refresh_test.go`:

```go
// A keyless connection stores no expiry and no refresh token, so the background
// refresh loop must never pick it up. This holds today only because
// ConnectionsNearExpiry filters on both columns being non-empty — pin it, since
// relaxing that query would put keyless rows into a refresh path they cannot survive.
func TestRefreshDueSkipsKeylessConnections(t *testing.T) {
	d, ws := storeTestDB(t)
	ctx := context.Background()

	if err := d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: uuid.New().String(), WorkspaceID: ws, Provider: "open_meteo",
		AccountLabel: "Open-Meteo", AccountIdentity: "Open-Meteo", Status: "ACTIVE",
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	s := &DBTokenStore{DB: d, SystemKey: mkKey(), Reg: &Registry{
		providers: map[string]Provider{"open_meteo": {Name: "open_meteo", Auth: AuthConfig{Kind: "none"}}},
		actions:   map[string][]Action{},
	}}

	if n := refreshDue(ctx, s); n != 0 {
		t.Errorf("refreshDue refreshed %d keyless connections, want 0", n)
	}
}
```

- [ ] **Step 6: Run both tests**

Run: `go test ./internal/connectors/ -run 'TestAccessTokenKeyless|TestRefreshDueSkipsKeyless' -count=1`

Expected: PASS (the refresh test passes without any production change — that is the point).

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/dbstore.go internal/connectors/dbstore_test.go internal/connectors/refresh_test.go
git commit -m "feat(connectors): return empty credential for keyless connections"
```

---

### Task 3: Keyless connect endpoint

**Files:**
- Modify: `web/api_services.go:349-385` (`apiConnectAPIKey`)
- Modify: `web/handlers_services.go:190-229` (`connectAPIKeyCore`)
- Test: `web/api_services_keyless_test.go` (create)

**Interfaces:**
- Consumes: `Provider.IsKeyless()` from Task 1.
- Produces: `POST /api/v1/services/:provider/apikey` accepts an **empty** `key` for a keyless provider, and rejects a second connection to the same keyless provider with HTTP 400 and code `already_connected`.

**Design note:** this reuses the existing paste-a-credential route rather than adding a new one. `session_exchange` set that precedent — it is not an API key either, but it connects through the same endpoint because the endpoint's real job is "create a connection row without leaving the browser".

- [ ] **Step 1: Write the failing test**

Create `web/api_services_keyless_test.go`. Model the harness on the existing service-API tests in this package — reuse whatever helper they use to build a `*Server` with a migrated DB and an entered workspace session.

```go
package web

import (
	"net/http"
	"testing"
)

// A keyless provider connects with no credential at all, so the endpoint must not
// reject an empty key the way it does for every other provider.
func TestConnectKeylessAcceptsEmptyKey(t *testing.T) {
	s, ts := newTestServerWithWorkspace(t)
	defer ts.Close()

	res := ts.postJSON(t, "/api/v1/services/open_meteo/apikey", map[string]any{"key": ""})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", res.Code, res.Body.String())
	}

	conns := s.listConnections(t, "open_meteo")
	if len(conns) != 1 {
		t.Fatalf("connections = %d, want 1", len(conns))
	}
	if conns[0].AccountLabel != "Open-Meteo" {
		t.Errorf("label = %q, want the provider label", conns[0].AccountLabel)
	}
}

// Two keyless connections to one provider would produce two identical tool sets that
// ToolDefs slugs by label — harmless but useless, and confusing on the page. Reject
// the duplicate rather than relying on the user not to create it.
func TestConnectKeylessRejectsDuplicate(t *testing.T) {
	_, ts := newTestServerWithWorkspace(t)
	defer ts.Close()

	if res := ts.postJSON(t, "/api/v1/services/open_meteo/apikey", map[string]any{"key": ""}); res.Code != http.StatusOK {
		t.Fatalf("first connect: status %d", res.Code)
	}

	res := ts.postJSON(t, "/api/v1/services/open_meteo/apikey", map[string]any{"key": ""})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("second connect status = %d, want 400", res.Code)
	}
	if body := res.Body.String(); !contains(body, "already_connected") {
		t.Errorf("body = %s, want code already_connected", body)
	}
}
```

If `newTestServerWithWorkspace`, `postJSON`, `listConnections` or `contains` do not already exist in the `web` package's test helpers, write them in this file, following the shape the neighbouring service tests use.

**This test depends on the `open_meteo` provider YAML existing.** It does not yet — it lands in Plan 2. For this task, add a minimal fixture provider so the framework is testable on its own: create `internal/connectors/providers/open_meteo.yaml` with only the auth block (no actions manifest yet):

```yaml
name: open_meteo
label: Open-Meteo
category: Data & Reference
auth:
  kind: none
setup_url: https://open-meteo.com/en/docs
setup_steps:
  - "Open-Meteo needs no account and no API key — press Connect."
  - "The free tier is for non-commercial use, limited to 10,000 calls per day."
  - "Weather data is licensed CC BY 4.0; agents citing a forecast should credit Open-Meteo."
```

Note the category is one of the four added in Task 5. Sequence Task 5 before this task, or the provider fails `category_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./web/ -run 'TestConnectKeyless' -count=1`

Expected: FAIL — the first with 400 "key is required", the second with 200 on the duplicate.

- [ ] **Step 3: Relax the key requirement and gate the provider check**

In `web/api_services.go`, inside `apiConnectAPIKey`, replace:

```go
	prov, ok := s.connectors.ProviderByName(provider)
	if !ok || !prov.PastesCredential() {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown or non-API-key provider: "+provider)
	}
```

with:

```go
	prov, ok := s.connectors.ProviderByName(provider)
	if !ok || !(prov.PastesCredential() || prov.IsKeyless()) {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown or non-API-key provider: "+provider)
	}
```

and replace:

```go
	apiKey := strings.TrimSpace(req.Key)
	if apiKey == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "key is required")
	}
```

with:

```go
	apiKey := strings.TrimSpace(req.Key)
	// A keyless provider has nothing to paste. Every other kind still must.
	if apiKey == "" && !prov.IsKeyless() {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "key is required")
	}
	if prov.IsKeyless() {
		existing, lerr := s.db.ListServiceConnections(c.Request().Context(), w.ID)
		if lerr != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", lerr.Error())
		}
		for _, e := range existing {
			if e.Provider == provider {
				return jsonErr(c, http.StatusBadRequest, "already_connected",
					prov.Label+" needs no credential, so one connection is all there is — it is already connected.")
			}
		}
	}
```

If `ListServiceConnections` has a different name or signature in `internal/db/connectors.go`, use the actual one — `apiListServices` in this same file already lists a workspace's connections and shows the correct call.

- [ ] **Step 4: Give a keyless connection a meaningful label**

In `web/handlers_services.go`, inside `connectAPIKeyCore`, replace:

```go
	if label == "" {
		label = "default"
	}
```

with:

```go
	if label == "" {
		// A keyless connection has no account behind it, so FetchIdentity cannot run
		// and "default" says nothing. Use the provider's own label — it is what the
		// connections page and ToolDefs' multi-account slug both read.
		if prov.IsKeyless() && prov.Label != "" {
			label = prov.Label
		} else {
			label = "default"
		}
	}
```

- [ ] **Step 5: Exempt keyless providers from the redirect-URI setup-steps gate**

Run: `go test ./internal/connectors/ -run TestOAuthSetupStepsNameTheRedirectURI -count=1`

Expected: **FAIL** — `open_meteo: no setup step names {{redirect_uri}}`.

The gate skips providers where `PastesCredential()` is true or `AuthParent` is set. A keyless provider is neither, yet it has no redirect URI to name — the OAuth flow never runs. In `internal/connectors/setup_steps_test.go`, replace:

```go
		if !ok || p.PastesCredential() || len(p.SetupSteps) == 0 {
			continue
		}
```

with:

```go
		// A keyless provider (Open-Meteo) runs no OAuth flow, so it has no redirect
		// URI to name — requiring the placeholder would force a lie into its steps.
		if !ok || p.PastesCredential() || p.IsKeyless() || len(p.SetupSteps) == 0 {
			continue
		}
```

Run it again. Expected: PASS.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./web/ -run 'TestConnectKeyless' -count=1`

Expected: PASS.

- [ ] **Step 7: Run the full web package**

Run: `go test ./web/ -count=1`

Expected: PASS. This package is slow (~343s under `-race`); run without `-race` here and let CI do the race pass.

- [ ] **Step 8: Commit**

```bash
git add web/api_services.go web/handlers_services.go web/api_services_keyless_test.go \
  internal/connectors/providers/open_meteo.yaml internal/connectors/setup_steps_test.go
git commit -m "feat(web/services): connect keyless providers with no credential"
```

---

### Task 4: SPA renders a keyless connector card

**Files:**
- Modify: `web/api_services.go:157-178` (emit `kind: "keyless"`)
- Modify: `web/ui/src/lib/connections.ts:69-84` (document the third `kind` value)
- Modify: `web/ui/src/pages/connections/ServiceWizard.tsx:350` onwards (third branch)
- Test: `web/ui/src/pages/connections/ServiceWizard.test.tsx`

**Interfaces:**
- Consumes: `Provider.IsKeyless()` from Task 1; the connect endpoint from Task 3.
- Produces: `ServiceProvider.kind` gains the value `"keyless"` alongside `"oauth"` and `"api_key"`. The wizard renders a bare Connect button for it — no key field, no client id/secret, no redirect URI, no preflight.

- [ ] **Step 1: Write the failing frontend test**

Append to `web/ui/src/pages/connections/ServiceWizard.test.tsx`:

```tsx
test("a keyless provider renders a bare Connect button and no key field", async () => {
  const provider = {
    name: "open_meteo",
    label: "Open-Meteo",
    category: "Data & Reference",
    kind: "keyless",
    setup_url: "https://open-meteo.com/en/docs",
    setup_steps: ["Open-Meteo needs no account and no API key — press Connect."],
    has_creds: false,
    action_count: 4,
    connect_inputs: [],
    redirect_uri: "",
    preflight: [],
    connections: [],
  };

  renderWizard(provider);

  // No credential input of any kind.
  expect(screen.queryByLabelText(/API key/i)).toBeNull();
  expect(screen.queryByLabelText(/Client ID/i)).toBeNull();
  expect(screen.queryByLabelText(/Client secret/i)).toBeNull();
  // No redirect URI panel — a keyless provider never leaves the browser.
  expect(screen.queryByText(/Redirect URI to register/i)).toBeNull();
  // The Connect button is present and enabled with nothing typed.
  const btn = screen.getByRole("button", { name: /Connect Open-Meteo/i });
  expect(btn).toBeEnabled();
});
```

Use whatever render helper the neighbouring tests in this file already use; if they render `<ServiceWizard provider={...} />` directly inside a `QueryClientProvider`, do the same and name the local helper `renderWizard`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`

Expected: FAIL — the api_key branch renders, so an "Open-Meteo API key" field is found.

- [ ] **Step 3: Emit the new kind from Go**

In `web/api_services.go`, replace:

```go
			if p.PastesCredential() {
				kind = "api_key"
				if p.Auth.SetupURL != "" {
					setupURL = p.Auth.SetupURL
				}
			}
```

with:

```go
			switch {
			case p.IsKeyless():
				// A third kind, not a variant of api_key: the wizard must render no
				// credential field at all, and there is no redirect URI or preflight
				// because nothing ever leaves the browser.
				kind = "keyless"
				if p.Auth.SetupURL != "" {
					setupURL = p.Auth.SetupURL
				}
			case p.PastesCredential():
				kind = "api_key"
				if p.Auth.SetupURL != "" {
					setupURL = p.Auth.SetupURL
				}
			}
```

The existing `if kind == "oauth"` guard below already excludes `"keyless"` from redirect-URI and preflight computation, so no further Go change is needed there.

- [ ] **Step 4: Document the value in the TS DTO**

In `web/ui/src/lib/connections.ts`, replace the `kind: string;` line inside `ServiceProvider` with:

```ts
  // "oauth" | "api_key" | "keyless". Keyless providers (Open-Meteo) need no
  // credential: the wizard shows setup steps and a bare Connect button.
  kind: string;
```

- [ ] **Step 5: Add the wizard branch**

In `web/ui/src/pages/connections/ServiceWizard.tsx`, the connect area currently reads `{provider.kind === "oauth" ? ( … ) : ( …api key form… )}`. Change the outer conditional to handle keyless first. Replace the opening of that ternary:

```tsx
        {provider.kind === "oauth" ? (
```

with:

```tsx
        {provider.kind === "keyless" ? (
          <div className="space-y-3">
            {provider.setup_steps.length > 0 && (
              <ol className="space-y-2">
                {provider.setup_steps.map((s, i) => (
                  <li
                    key={i}
                    className="flex gap-3 rounded-lg border border-border bg-background p-3 text-sm"
                  >
                    <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-muted-surface text-xs font-semibold">
                      {i + 1}
                    </span>
                    <span className="leading-relaxed">{s}</span>
                  </li>
                ))}
              </ol>
            )}
            {connectError && <ErrorNote>{connectError}</ErrorNote>}
            <div className="flex justify-end">
              <Button
                onClick={() => void handleConnectAPIKey()}
                disabled={connectAPIKeyMutation.isPending}
              >
                <Link2 />
                {connectAPIKeyMutation.isPending
                  ? "Connecting…"
                  : `Connect ${provider.label} →`}
              </Button>
            </div>
          </div>
        ) : provider.kind === "oauth" ? (
```

Use the actual handler and mutation names the api_key branch already uses in this file — read them before editing rather than assuming `handleConnectAPIKey` / `connectAPIKeyMutation`. The keyless path posts to the same endpoint with an empty key.

Note the setup steps are rendered as plain `{s}` rather than through `SetupStep`: that component substitutes `{{redirect_uri}}`, and a keyless provider has none.

- [ ] **Step 6: Run the test to verify it passes**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`

Expected: PASS.

- [ ] **Step 7: Run the full frontend gate**

Run: `cd web/ui && npx tsc -b && npx oxlint && npx vitest run`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/api_services.go web/ui/src/lib/connections.ts web/ui/src/pages/connections/ServiceWizard.tsx web/ui/src/pages/connections/ServiceWizard.test.tsx
git commit -m "feat(web/connections): render a bare Connect card for keyless providers"
```

---

### Task 5: Four new categories, pinned on both sides

**Files:**
- Modify: `internal/connectors/category_test.go:6-12` (`validCategories`)
- Modify: `web/ui/src/lib/connections.ts:105-115` (`CATEGORY_ORDER`)
- Test: `internal/connectors/category_test.go` (parity test), `web/ui/src/lib/connections.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: the category strings `"Self-hosted"`, `"Health & Fitness"`, `"Finance"`, `"Data & Reference"` are valid in both the Go validator and the SPA ordering. Every provider YAML in Plan 2 depends on this.

**Why both sides:** a category present only in Go renders under the wrong heading (`groupByCategory` funnels unknown categories into "Other"); one present only in the SPA has no providers. Neither failure is visible in either file alone — the same two-sided trap `TestWorkspaceIconSlugsMatchTheSPA` exists to catch for workspace icons.

- [ ] **Step 1: Write the failing parity test**

Append to `internal/connectors/category_test.go`:

```go
// The SPA's CATEGORY_ORDER and this package's validCategories must agree. A category
// valid only in Go funnels its providers into "Other" on the page (groupByCategory
// treats an unknown category as Other); one present only in the SPA renders an empty
// heading. Neither failure is visible in either file alone.
func TestCategoriesMatchTheSPA(t *testing.T) {
	src, err := os.ReadFile("../../web/ui/src/lib/connections.ts")
	if err != nil {
		t.Fatalf("read connections.ts: %v", err)
	}
	const marker = "export const CATEGORY_ORDER = ["
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatal("CATEGORY_ORDER not found in connections.ts — did it move or get renamed?")
	}
	rest := string(src)[i+len(marker):]
	end := strings.Index(rest, "]")
	if end < 0 {
		t.Fatal("CATEGORY_ORDER array is not terminated")
	}
	spa := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(rest[:end], -1) {
		spa[m[1]] = true
	}
	if len(spa) < 5 {
		t.Fatalf("parsed only %d categories from the SPA — the parse is wrong, not the data", len(spa))
	}
	for c := range validCategories {
		if !spa[c] {
			t.Errorf("category %q is valid in Go but missing from the SPA's CATEGORY_ORDER", c)
		}
	}
	for c := range spa {
		if !validCategories[c] {
			t.Errorf("category %q is in the SPA's CATEGORY_ORDER but not in validCategories", c)
		}
	}
}
```

Add `"os"`, `"regexp"` and `"strings"` to the file's imports.

- [ ] **Step 2: Run the test to verify it passes on the current data**

Run: `go test ./internal/connectors/ -run TestCategoriesMatchTheSPA -count=1`

Expected: PASS. The two lists already agree — this test is written first precisely so the next step's failure proves the test has teeth.

- [ ] **Step 3: Add the four categories to the Go validator only, and watch the test fail**

In `internal/connectors/category_test.go`, replace the `validCategories` map with:

```go
var validCategories = map[string]bool{
	"Google": true, "Publishing & Media": true, "Advertising": true,
	"Productivity": true, "Communication": true, "Commerce": true,
	"Developer": true, "Support": true, "Other": true,
	// Everyday-connector categories. "Self-hosted" groups by auth shape ("runs on
	// my box, needs a base URL") rather than by domain, unlike the other eight —
	// a deliberate inconsistency, because it matches how the page is browsed and
	// the alternative scatters Home Assistant, Immich and Paperless across three
	// unrelated headings.
	"Self-hosted": true, "Health & Fitness": true, "Finance": true,
	"Data & Reference": true,
}
```

Run: `go test ./internal/connectors/ -run TestCategoriesMatchTheSPA -count=1`

Expected: FAIL, naming all four missing categories. This is the proof the parity test works.

- [ ] **Step 4: Add the same four to the SPA**

In `web/ui/src/lib/connections.ts`, replace the `CATEGORY_ORDER` array with:

```ts
export const CATEGORY_ORDER = [
  "Google",
  "Publishing & Media",
  "Advertising",
  "Productivity",
  "Communication",
  "Self-hosted",
  "Health & Fitness",
  "Finance",
  "Data & Reference",
  "Commerce",
  "Developer",
  "Support",
  "Other",
] as const;
```

The four are placed mid-list, after the everyday-facing groups and before the business ones, because the array's whole job is priority order.

- [ ] **Step 5: Run the parity test to verify it passes**

Run: `go test ./internal/connectors/ -run 'TestCategoriesMatchTheSPA|TestEveryProviderHasAValidCategory' -count=1`

Expected: PASS.

- [ ] **Step 6: Confirm an empty category renders no heading**

`groupByCategory` already filters empty buckets, so `Health & Fitness` — which has no providers until a later wave — leaves no blank heading. Add a test pinning it, since a regression here shows as a stray empty section.

Append to `web/ui/src/lib/connections.test.ts`:

```ts
test("a category with no providers renders no heading", () => {
  const groups = groupByCategory([
    { ...baseProvider, name: "todoist", category: "Productivity" },
  ]);
  expect(groups.map(([c]) => c)).toEqual(["Productivity"]);
  expect(groups.map(([c]) => c)).not.toContain("Health & Fitness");
});
```

Use whatever provider fixture the neighbouring tests in that file already define; if none exists, build a minimal `baseProvider` object satisfying `ServiceProvider` in this file.

- [ ] **Step 7: Run the frontend tests**

Run: `cd web/ui && npx vitest run src/lib/connections.test.ts`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/connectors/category_test.go web/ui/src/lib/connections.ts web/ui/src/lib/connections.test.ts
git commit -m "feat(connectors): add self-hosted, health, finance and reference categories"
```

---

### Task 6: Shared base-URL normalizer

**Files:**
- Create: `internal/connectors/baseurl.go`
- Create: `internal/connectors/baseurl_test.go`
- Modify: `web/handlers_services.go:195-204` (apply it in `connectAPIKeyCore`)

**Interfaces:**
- Consumes: `ConnectInput.Normalize` from Task 1.
- Produces: `func NormalizeBaseURL(raw string) (string, error)` — canonicalizes a self-hosted base URL. Plan 2's Home Assistant, Immich and Paperless-ngx providers each declare `normalize: base_url` on their `base_url` connect input.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/baseurl_test.go`:

```go
package connectors

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain host", "https://ha.example.com", "https://ha.example.com"},
		{"trailing slash stripped", "https://ha.example.com/", "https://ha.example.com"},
		{"several trailing slashes stripped", "https://ha.example.com///", "https://ha.example.com"},
		{"surrounding whitespace trimmed", "  https://ha.example.com  ", "https://ha.example.com"},
		{"http allowed for a LAN box", "http://192.168.1.10:8123", "http://192.168.1.10:8123"},
		{"port preserved", "https://ha.example.com:8123", "https://ha.example.com:8123"},
		// A path PREFIX is mainstream, not an error: Nextcloud at /nextcloud and a
		// reverse-proxied Paperless at /paperless are both normal deployments.
		{"path prefix preserved", "https://example.com/nextcloud", "https://example.com/nextcloud"},
		{"path prefix trailing slash stripped", "https://example.com/paperless/", "https://example.com/paperless"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeBaseURL(tc.in)
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q) errored: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeBaseURLRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"whitespace only", "   "},
		// url.Parse reads "homeassistant.local" as a scheme here, so the scheme check
		// is what catches it — not a host check.
		{"no scheme with port", "homeassistant.local:8123"},
		{"no scheme bare host", "ha.example.com"},
		{"unsupported scheme", "ftp://ha.example.com"},
		{"scheme but no host", "https://"},
		{"query string", "https://ha.example.com?token=abc"},
		{"fragment", "https://ha.example.com#section"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := NormalizeBaseURL(tc.in); err == nil {
				t.Errorf("NormalizeBaseURL(%q) = %q, want an error", tc.in, got)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connectors/ -run TestNormalizeBaseURL -count=1`

Expected: FAIL — `undefined: NormalizeBaseURL`.

- [ ] **Step 3: Write the implementation**

Create `internal/connectors/baseurl.go`:

```go
package connectors

import (
	"errors"
	"net/url"
	"strings"
)

// NormalizeBaseURL canonicalizes a user-supplied self-hosted service base URL so
// action templates can concatenate "{{conn.base_url}}/api/..." safely.
//
// A path PREFIX is allowed and preserved: https://host/nextcloud, and a Paperless-ngx
// behind a reverse proxy at /paperless, are mainstream deployments — rejecting them
// would refuse working installs at connect time. A query string or fragment is not
// allowed: neither can survive having a path concatenated onto it.
//
// Validation happens at CONNECT rather than at action time, because a 404 from a
// mistyped host reads as a broken connector rather than as a typo.
func NormalizeBaseURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("base URL is required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("base URL is not a valid URL")
	}
	// A schemeless "host:8123" parses with the host as the SCHEME (scheme grammar
	// allows dots), so this check is what catches a missing scheme — not the host check.
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("base URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", errors.New("base URL must include a host, e.g. https://ha.example.com")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("base URL must not include a query string or #fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestNormalizeBaseURL -count=1`

Expected: PASS.

- [ ] **Step 5: Apply it at connect time**

In `web/handlers_services.go`, inside `connectAPIKeyCore`, replace the connect-input loop:

```go
	extra := map[string]string{}
	for _, ci := range prov.ConnectInputs {
		v := strings.TrimSpace(inputs[ci.Key])
		if ci.Required && v == "" {
			return nil, ci.Label + " is required.", nil
		}
		if v != "" {
			extra[ci.Key] = v
		}
	}
```

with:

```go
	extra := map[string]string{}
	for _, ci := range prov.ConnectInputs {
		v := strings.TrimSpace(inputs[ci.Key])
		if ci.Required && v == "" {
			return nil, ci.Label + " is required.", nil
		}
		if v == "" {
			continue
		}
		// A declared normalizer canonicalizes the pasted value before it is stored,
		// so every action template concatenating onto it sees one shape.
		if ci.Normalize == "base_url" {
			norm, nerr := connectors.NormalizeBaseURL(v)
			if nerr != nil {
				return nil, ci.Label + ": " + nerr.Error(), nil
			}
			v = norm
		}
		extra[ci.Key] = v
	}
```

If this file already imports the connectors package under a different name, or is itself inside it, adjust the call accordingly.

- [ ] **Step 6: Write and run an end-to-end test for the connect path**

Append to `web/api_services_keyless_test.go` (created in Task 3 — it is the natural home for connect-path tests):

```go
// A pasted base URL is normalized before storage, so action templates see one shape.
// The Immich fixture is used because it is the first provider to declare the field.
func TestConnectStoresNormalizedBaseURL(t *testing.T) {
	s, ts := newTestServerWithWorkspace(t)
	defer ts.Close()

	res := ts.postJSON(t, "/api/v1/services/immich/apikey", map[string]any{
		"key":    "test-key",
		"inputs": map[string]string{"base_url": "  https://photos.example.com/  "},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", res.Code, res.Body.String())
	}

	conns := s.listConnections(t, "immich")
	if len(conns) != 1 {
		t.Fatalf("connections = %d, want 1", len(conns))
	}
	if !contains(conns[0].Extra, `"base_url":"https://photos.example.com"`) {
		t.Errorf("stored extra = %s, want a trimmed, slash-stripped base_url", conns[0].Extra)
	}
}

// A malformed base URL is rejected at connect, not discovered as a 404 later.
func TestConnectRejectsSchemelessBaseURL(t *testing.T) {
	_, ts := newTestServerWithWorkspace(t)
	defer ts.Close()

	res := ts.postJSON(t, "/api/v1/services/immich/apikey", map[string]any{
		"key":    "test-key",
		"inputs": map[string]string{"base_url": "photos.example.com"},
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", res.Code, res.Body.String())
	}
}
```

This needs an `immich` provider YAML. Add the auth-only fixture now (its actions manifest lands in Plan 2) — create `internal/connectors/providers/immich.yaml`:

```yaml
name: immich
label: Immich
category: Self-hosted
auth:
  kind: api_key
  placement: header
  header_name: x-api-key
  value_prefix: ""
  key_label: "Immich API key"
  key_hint: "generated in Account Settings → API Keys"
  setup_url: https://immich.app/docs/features/command-line-interface
connect_inputs:
  - key: base_url
    label: "Immich server URL"
    hint: "e.g. https://photos.example.com — a path prefix like /immich is fine"
    required: true
    normalize: base_url
setup_steps:
  - "In Immich open Account Settings → API Keys and press New API Key."
  - "Copy the key — Immich shows it only once."
  - "Enter your server's URL and the key below."
```

Run: `go test ./web/ -run 'TestConnectStoresNormalizedBaseURL|TestConnectRejectsSchemelessBaseURL' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/baseurl.go internal/connectors/baseurl_test.go internal/connectors/providers/immich.yaml web/handlers_services.go web/api_services_keyless_test.go
git commit -m "feat(connectors): normalize self-hosted base URLs at connect time"
```

---

### Task 7: Pin the SSRF stance

**Files:**
- Create: `internal/connectors/netstance_test.go`
- Modify: `CLAUDE.md` (the `internal/connectors` row of the key-packages table, plus a short subsection)

**Interfaces:**
- Consumes: nothing.
- Produces: no production code. A test and a documented rationale.

**Why:** `connectors.Execute` falls back to a plain `&http.Client{Timeout: 30 * time.Second}`, and every call site passes nil or an unguarded client. Reaching a LAN box is the *point* of the self-hosted tier, but nothing records that, so the next person hardening HTTP clients across the codebase would silently break every self-hosted connector. The threat model behind `internal/nethttp` — untrusted content steering a fetch — does not apply to a curated action manifest in a single-owner install.

- [ ] **Step 1: Write the test**

Create `internal/connectors/netstance_test.go`:

```go
package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Execute must be able to reach a loopback/RFC1918 address. This is DELIBERATE: the
// self-hosted connector tier (Home Assistant, Immich, Paperless-ngx) exists to talk to
// a box on the user's own LAN, and internal/nethttp.GuardedClient blocks exactly those
// ranges at dial time.
//
// The guard is right for websearch and the coder's web_fetch, where a URL can be chosen
// by untrusted content. It is wrong here: a connector's host comes either from vendored
// YAML or from a value the single owner typed into their own install.
//
// If this test fails because someone routed connectors through GuardedClient, the fix
// is NOT to weaken this test — it is to decide, deliberately, that self-hosted
// connectors are being dropped.
func TestExecuteReachesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	// httptest binds 127.0.0.1 — a loopback address GuardedClient refuses to dial.
	if !strings.Contains(srv.URL, "127.0.0.1") {
		t.Fatalf("expected a loopback test server, got %s", srv.URL)
	}

	reg := &Registry{
		providers: map[string]Provider{
			"selfhosted_probe": {Name: "selfhosted_probe", Auth: AuthConfig{Kind: "none"}},
		},
		actions: map[string][]Action{
			"selfhosted_probe": {{
				Name:     "probe_status",
				Mutating: false,
				Request:  RequestTemplate{Method: "GET", URL: srv.URL + "/status"},
			}},
		},
	}

	res, err := Execute(context.Background(), reg, keylessStore{}, nil,
		ConnRef{ID: "c1", Provider: "selfhosted_probe"}, "probe_status", nil, Policy{})
	if err != nil {
		t.Fatalf("Execute against a loopback address failed: %v\n\n"+
			"If connectors were switched to nethttp.GuardedClient, every self-hosted "+
			"connector just stopped working.", err)
	}
	if !strings.Contains(string(res.Data), `"ok"`) {
		t.Errorf("payload = %s, want the probe body", res.Data)
	}
}

// keylessStore is a TokenStore for a provider with no credential.
type keylessStore struct{}

func (keylessStore) AccessToken(context.Context, ConnRef) (string, error) { return "", nil }
```

Match `RequestTemplate` and `Action` to the real field names in `internal/connectors/registry.go` — read them before writing, and match `TokenStore`'s real method set for `keylessStore` (add any methods the interface requires beyond `AccessToken`).

- [ ] **Step 2: Run the test to verify it passes**

Run: `go test ./internal/connectors/ -run TestExecuteReachesPrivateAddresses -count=1`

Expected: PASS. This test documents existing behaviour rather than driving new behaviour, so it passes immediately — that is correct for a stance test.

- [ ] **Step 3: Prove the test has teeth**

Temporarily change `internal/connectors/execute.go`'s client fallback from:

```go
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
```

to `client = nethttp.GuardedClient(30 * time.Second)` (adding the import), then run the test again.

Expected: FAIL, with the message about self-hosted connectors.

**Revert this change immediately** — `git checkout internal/connectors/execute.go`. Its only purpose is proving the guard would be caught.

- [ ] **Step 4: Document the stance in CLAUDE.md**

In `CLAUDE.md`, add this subsection immediately after the "Connector service layer" section's bullet list (before the "**UI:**" paragraph):

```markdown
**Connectors deliberately do NOT use the private-address dial guard.**
`connectors.Execute` falls back to a plain `&http.Client{Timeout: 30s}`, and every
call site passes nil or an unguarded client — unlike `internal/websearch`, the coder's
`web_fetch`, and the Discord attachment fetcher, which all use
`nethttp.GuardedClient`. This is the property the **self-hosted tier** (Home Assistant,
Immich, Paperless-ngx) is built on: those services live at RFC1918 or Tailscale
addresses that the guard blocks at dial time. The guard's threat model is untrusted
content steering a fetch; a connector's host comes from vendored YAML or from a value
the single owner typed into their own install, so it does not apply here.
`connectors.TestExecuteReachesPrivateAddresses` pins this, and its failure message
says what breaks. Revisit if Rookery ever becomes multi-tenant — that test is where
the conversation should start.
```

- [ ] **Step 5: Run the package and commit**

Run: `go test ./internal/connectors/ -count=1`

Expected: PASS.

```bash
git add internal/connectors/netstance_test.go CLAUDE.md
git commit -m "test(connectors): pin the deliberate private-address stance"
```

---

### Task 8: Upstream logo source, and resolve the stale coverage comment

**Files:**
- Modify: `scripts/vendor-brand-logos.sh` (add an `UPSTREAM` manifest and its fetch loop)
- Modify: `web/ui/src/components/brand/logos.ts:9-16` (correct the comment)
- Create: `web/ui/src/components/brand/logocoverage.test.ts`
- Create: `web/ui/src/assets/logos/*.svg` (the fetched files, committed)

**Interfaces:**
- Consumes: nothing.
- Produces: vendored SVGs for brands that lobehub, worldvectorlogo and simple-icons all lack. Plan 2's provider slugs resolve to real marks instead of letter tiles.

**Context:** `logos.ts` claims `logocoverage.test.ts` "asserts every slug the app can actually render has a file, so a provider added on the Go side without a logo fails the test run rather than silently degrading to a letter tile." **No such file exists.** `ProviderLogo.test.tsx` checks only that vendored assets are well-formed. A comment describing a guard that is not there is worse than no comment: it stops the next person from adding one.

Checked against simple-icons (3,450 marks): Todoist, Home Assistant, Immich, Paperless-ngx, Jellyfin, AdGuard, Nextcloud, Sonarr, Radarr, ntfy, Firefly III, Actual Budget, Wallabag, FreshRSS, Grafana, Strava, Toggl and Trakt are **present**. YNAB, Raindrop.io, Readwise, Pushover, Open-Meteo, Miniflux, Habitica, Last.fm and TMDB are **absent**.

- [ ] **Step 1: Write the coverage test**

Create `web/ui/src/components/brand/logocoverage.test.ts`:

```ts
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { lookupLogo } from "./logos";

// The repo root, relative to this test file.
const ROOT = join(__dirname, "../../../../..");

/** Every service-connector provider slug, read from the embedded provider YAMLs. */
function providerSlugs(): string[] {
  const dir = join(ROOT, "internal/connectors/providers");
  return readdirSync(dir)
    .filter((f) => f.endsWith(".yaml"))
    .map((f) => {
      const src = readFileSync(join(dir, f), "utf8");
      const m = /^name:\s*(\S+)/m.exec(src);
      return m ? m[1] : f.replace(/\.yaml$/, "");
    });
}

// This test is the guard logos.ts always claimed existed but never had. A provider
// added on the Go side with no vendored mark degrades to a coloured letter tile —
// legible, but it reads as unfinished on the page whose whole job is browsing
// integrations. Fail here instead, where the fix is one line in
// scripts/vendor-brand-logos.sh.
test("every connector provider slug has a vendored logo", () => {
  const slugs = providerSlugs();
  expect(slugs.length).toBeGreaterThan(40);
  const missing = slugs.filter((s) => !lookupLogo(s));
  expect(missing, `no vendored logo for: ${missing.join(", ")}`).toEqual([]);
});
```

- [ ] **Step 2: Run it to see the real gap**

Run: `cd web/ui && npx vitest run src/components/brand/logocoverage.test.ts`

Expected: FAIL, listing the slugs with no mark. Record that list — it is the exact work for the next step. On the current provider set it should be empty or near-empty; the fixtures added in Tasks 3 and 6 (`open_meteo`, `immich`) will appear, and `immich` is available from simple-icons while `open_meteo` is not.

- [ ] **Step 3: Add the upstream source to the vendor script**

In `scripts/vendor-brand-logos.sh`, extend the header comment's source list with a fourth entry:

```bash
#   upstream A pinned URL to the project's own published SVG, for brands none of
#            the three sets above carry (YNAB, Raindrop.io, Readwise, Pushover,
#            Open-Meteo, Miniflux, Habitica, Last.fm, TMDB). Most are open source
#            with a mark in their own repository under a permissive licence; the
#            URL is pinned to a commit, never a branch, so a re-run is reproducible.
```

Then add the manifest and its fetch loop alongside the existing `LOBEHUB` / `WVL` / `SIMPLE` blocks, following their exact style:

```bash
# our-slug|url-to-a-raw-svg  (pinned to a commit or a versioned asset path)
UPSTREAM="
open_meteo|https://raw.githubusercontent.com/open-meteo/open-meteo/<COMMIT>/docs/logo.svg
"

while IFS='|' read -r slug url; do
  [ -z "$slug" ] && continue
  echo "upstream: $slug"
  curl -fsSL -A "$UA" "$url" -o "$TMP/$slug.svg"
  strip_svg "$TMP/$slug.svg" > "$OUT/$slug.svg"
done <<< "$UPSTREAM"
```

Use the script's existing strip helper — read the file and call whatever it actually names (the header says comments, `<script>` and `<style>` are stripped). Resolve each `<COMMIT>` to a real pinned SHA when adding that brand; add only the slugs this plan and Plan 2 need, not the whole list.

- [ ] **Step 4: Run the script and commit the fetched assets**

Run: `./scripts/vendor-brand-logos.sh`

Then verify the new files are well-formed:

Run: `cd web/ui && npx vitest run src/components/brand/ProviderLogo.test.tsx`

Expected: PASS — in particular the "every vendored asset is a well-formed svg with a viewBox" case, which is what catches a 404 page saved as an `.svg`.

- [ ] **Step 5: Correct the stale comment**

In `web/ui/src/components/brand/logos.ts`, replace:

```
// The file name IS the slug, so adding a logo needs no edit here — drop the SVG
// in and it resolves. `logocoverage.test.ts` asserts every slug the app can
// actually render has a file, so a provider added on the Go side without a logo
// fails the test run rather than silently degrading to a letter tile.
```

with:

```
// The file name IS the slug, so adding a logo needs no edit here — drop the SVG
// in and it resolves. `logocoverage.test.ts` asserts every connector provider
// slug has a file, so a provider added on the Go side without a logo fails the
// test run rather than silently degrading to a letter tile. That test was
// missing for a long time while this comment claimed it existed; it is real now.
```

- [ ] **Step 6: Run the coverage test and the full frontend gate**

Run: `cd web/ui && npx vitest run src/components/brand/`

Expected: PASS.

Run: `cd web/ui && npx tsc -b && npx oxlint`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add scripts/vendor-brand-logos.sh web/ui/src/components/brand/logos.ts web/ui/src/components/brand/logocoverage.test.ts web/ui/src/assets/logos/
git commit -m "feat(web/brand): add upstream logo source and the coverage test logos.ts promised"
```

---

### Task 9: Commit the live-check harness

**Files:**
- Create: `cmd/livecheck/main.go` (bring across from the primary working tree)
- Create: `cmd/livecheck/README.md`

**Interfaces:**
- Consumes: `connectors.Execute`, `connectors.DBTokenStore`, `connectors.LoadBundled`.
- Produces: a runnable harness — `go run ./cmd/livecheck <provider> <action> '<json-args>'` — that executes a connector action against real stored tokens. Plan 2's verification step depends on it.

**Why this task exists:** `cmd/livecheck` is **uncommitted**. It lives in the primary working tree at `/home/rookie/rookery/cmd/livecheck/main.go` but is on no branch, so a worktree-isolated implementation does not have it. The spec's verification bar — "live-verify tier A" — is unenforceable without the harness that enforces it.

- [ ] **Step 1: Copy the file across and read it**

```bash
mkdir -p cmd/livecheck
cp /home/rookie/rookery/cmd/livecheck/main.go cmd/livecheck/main.go
```

Then read it in full. It is a dev harness that was never reviewed; check specifically that its `connectors.Execute` call still matches the current signature (`Execute(ctx, reg, store, client, conn, action, args, pol Policy)`) — an older copy may pass a bare `buildPhase bool` where `Policy{}` now goes.

- [ ] **Step 2: Verify it compiles**

Run: `go build ./cmd/livecheck`

Expected: success. If it fails on the `Execute` signature, update the call to pass `connectors.Policy{}` — a live check is neither a build nor an approval-gated run.

- [ ] **Step 3: Confirm it is excluded from the release build**

Run: `grep -n "livecheck" .goreleaser.yaml Makefile`

If `.goreleaser.yaml` builds `./cmd/...` rather than `./cmd/rookery`, add an ignore so the harness never ships as a release artifact. If it already names `./cmd/rookery` explicitly, no change is needed — note that in the commit message.

- [ ] **Step 4: Write the README**

Create `cmd/livecheck/README.md`:

```markdown
# livecheck

A development harness that runs one connector action against a **real** stored
connection, so a provider YAML can be verified against the live API before it ships.

It is not part of the product: it is not built by `make build`, not shipped as a
release artifact, and has no tests. It exists because the connector catalog's stated
verification bar — live-verify tier-A providers — is unenforceable without it.

## Usage

    go run ./cmd/livecheck <provider> <action> '<json-args>'

Example:

    go run ./cmd/livecheck todoist todoist_list_projects '{}'

It reads the same SQLite database the server uses, decrypts the stored connection
with the system key, and calls `connectors.Execute` with an empty `Policy` — no
build-phase guard, no approval gate. **A mutating action will really run**, so prefer
read-only actions when verifying, and use a throwaway account when you cannot.

## Why the whole catalog is not verified

CLAUDE.md records that non-Google connector configs are hand-authored and unverified
against live APIs. Every provider verified with this harness is one fewer in that
category; a provider that cannot be verified ships marked rather than silently joining
it.
```

- [ ] **Step 5: Run gofmt and vet**

Run: `gofmt -l cmd/livecheck && go vet ./cmd/livecheck`

Expected: no output from either.

- [ ] **Step 6: Commit**

```bash
git add cmd/livecheck/
git commit -m "chore(livecheck): commit the connector live-check harness"
```

---

### Task 10: Make response narrowing actually work

**Files:**
- Modify: `internal/connectors/execute.go:237-251` (`extract`)
- Modify: `internal/connectors/registry.go` (add `Action.ResponseFilter`)
- Create: `internal/connectors/extract_test.go`
- Modify: `internal/connectors/connectors/reddit.yaml` (verify the now-working extracts)

**Interfaces:**
- Consumes: nothing.
- Produces: `extract` walks **dotted** paths (`$.data.budgets`), and a new `Action.ResponseFilter` prefix-filters a JSON array after extraction. Plan 2's YNAB, Immich and Home Assistant providers all depend on this.

**This is a live bug, not a new feature.** `extract` today handles exactly three cases: `""`/`"$"` returns the raw body; `"$.key"` looks up **one top-level key**; anything else **silently returns the whole raw body**. So:

- `$.data.children` is already shipped in `reddit.yaml` (2 actions) and silently returns the entire Reddit envelope — `strings.TrimPrefix` yields the literal string `data.children`, which is not a key in the top-level map.
- Wave 1 would have added `$.data.budgets`, `$.data.transactions`, `$.data.month`, `$.data.category_groups` (YNAB) and `$.assets.items` (Immich) to the same silent-failure class.

The silence is the dangerous part: the action looks correct in the YAML, passes every test, and fails only as a truncated blob against the 8 KiB bridge cap on real data.

**`ResponseFilter` exists for Home Assistant.** `GET /api/states` returns every entity in the house and offers **no server-side filter**. Without a client-side one, the flagship self-hosted provider's list action cannot be made usable — an `entity_prefix` parameter that the request ignores would be a lie. Several other list APIs have the same shape, so this is general rather than a Home Assistant special case.

- [ ] **Step 1: Write the failing tests**

Create `internal/connectors/extract_test.go`:

```go
package connectors

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractDottedPath(t *testing.T) {
	raw := []byte(`{"data":{"budgets":[{"id":"b1"},{"id":"b2"}],"other":9},"top":1}`)

	got := extract("$.data.budgets", raw)
	var out []map[string]string
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("extract returned %s, which is not the budgets array: %v", got, err)
	}
	if len(out) != 2 || out[0]["id"] != "b1" {
		t.Errorf("extract = %s, want the two-element budgets array", got)
	}
}

func TestExtractSingleKeyStillWorks(t *testing.T) {
	raw := []byte(`{"items":[1,2,3],"next":"x"}`)
	if got := string(extract("$.items", raw)); got != "[1,2,3]" {
		t.Errorf("extract = %s, want [1,2,3]", got)
	}
}

func TestExtractWholeBody(t *testing.T) {
	raw := []byte(`{"a":1}`)
	for _, p := range []string{"", "$", "  "} {
		if got := string(extract(p, raw)); got != `{"a":1}` {
			t.Errorf("extract(%q) = %s, want the raw body", p, got)
		}
	}
}

// A path that does not resolve must return the raw body rather than nothing — a
// connector returning an empty result reads to the model as "there is no data",
// which is a different and worse claim than "I could not narrow this".
func TestExtractMissingPathFallsBackToRaw(t *testing.T) {
	raw := []byte(`{"a":1}`)
	if got := string(extract("$.nope.deeper", raw)); got != `{"a":1}` {
		t.Errorf("extract = %s, want the raw body on a miss", got)
	}
}

// An array element that is not an object, or an object missing the field, is simply
// not matched — filtering must never panic on real-world payloads.
func TestApplyResponseFilter(t *testing.T) {
	raw := []byte(`[
		{"entity_id":"sensor.kitchen_temp","state":"21"},
		{"entity_id":"light.kitchen","state":"on"},
		{"entity_id":"sensor.hall_temp","state":"19"},
		"not-an-object",
		{"state":"no entity_id here"}
	]`)

	got := applyResponseFilter(raw, ResponseFilter{Field: "entity_id"}, "sensor.")
	var out []map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("filter returned %s: %v", got, err)
	}
	if len(out) != 2 {
		t.Fatalf("filter kept %d elements, want 2: %s", len(out), got)
	}
	for _, e := range out {
		if !strings.HasPrefix(e["entity_id"].(string), "sensor.") {
			t.Errorf("kept %v, which is not a sensor", e)
		}
	}
}

// An empty prefix means "no filter requested" — return everything rather than nothing.
func TestApplyResponseFilterEmptyPrefixIsNoOp(t *testing.T) {
	raw := []byte(`[{"entity_id":"light.kitchen"}]`)
	if got := string(applyResponseFilter(raw, ResponseFilter{Field: "entity_id"}, "")); got != string(raw) {
		t.Errorf("filter = %s, want the input unchanged", got)
	}
}

// A non-array body cannot be filtered; pass it through rather than erroring.
func TestApplyResponseFilterNonArrayPassesThrough(t *testing.T) {
	raw := []byte(`{"entity_id":"light.kitchen"}`)
	if got := string(applyResponseFilter(raw, ResponseFilter{Field: "entity_id"}, "sensor.")); got != string(raw) {
		t.Errorf("filter = %s, want the input unchanged", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/connectors/ -run 'TestExtract|TestApplyResponseFilter' -count=1`

Expected: FAIL — `TestExtractDottedPath` because `extract` returns the whole body, and the filter tests because `applyResponseFilter` and `ResponseFilter` are undefined.

- [ ] **Step 3: Rewrite `extract` to walk dotted paths**

In `internal/connectors/execute.go`, replace the whole `extract` function with:

```go
// extract narrows a response body to the subtree named by a dotted path such as
// "$.data.budgets". It is deliberately not a JSONPath engine: the manifests only ever
// name a nested key, and a real engine would be a dependency plus a surface area.
//
// A path that does not resolve returns the RAW body, not nothing. An empty result
// reads to the model as "there is no data", which is a different and much worse claim
// than "this could not be narrowed".
//
// Before this walked more than one segment, "$.data.children" — shipped in reddit.yaml —
// silently returned the entire envelope, because the whole dotted string was looked up
// as a single literal key. Anything the manifests express here must actually work, or
// the failure only ever shows up as a truncated blob against the bridge's 8 KiB cap.
func extract(path string, raw []byte) json.RawMessage {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return raw
	}
	if !strings.HasPrefix(path, "$.") {
		return raw
	}
	cur := json.RawMessage(raw)
	for _, seg := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		if seg == "" {
			return raw
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(cur, &m); err != nil {
			return raw
		}
		v, ok := m[seg]
		if !ok {
			return raw
		}
		cur = v
	}
	return cur
}
```

- [ ] **Step 4: Add `ResponseFilter` and apply it**

In `internal/connectors/registry.go`, add above the `Action` struct:

```go
// ResponseFilter narrows a JSON ARRAY response client-side, for APIs that offer no
// server-side filter of their own. Home Assistant is the motivating case: GET
// /api/states returns every entity in the house, so an entity_prefix parameter that
// the request ignored would be a lie — the filter is what makes it true.
type ResponseFilter struct {
	// Field is the object key each array element is matched on (e.g. "entity_id").
	Field string `yaml:"field"`
	// PrefixArg names the action argument holding the prefix to match. An absent or
	// empty argument means no filtering, so the action still works unfiltered.
	PrefixArg string `yaml:"prefix_arg"`
}
```

and to the `Action` struct, after `ResponseExtract`:

```go
	// ResponseFilter optionally narrows an array response after ResponseExtract runs.
	ResponseFilter ResponseFilter `yaml:"response_filter"`
```

Then in `internal/connectors/execute.go`, add alongside `extract`:

```go
// applyResponseFilter keeps only the elements of a JSON array whose f.Field value
// starts with prefix. A non-array body, an empty prefix, a non-object element, or an
// element missing the field are all pass-through/no-match rather than errors — this
// runs on real third-party payloads and must never panic or invent an empty result.
func applyResponseFilter(raw json.RawMessage, f ResponseFilter, prefix string) json.RawMessage {
	if f.Field == "" || prefix == "" {
		return raw
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return raw
	}
	kept := make([]json.RawMessage, 0, len(arr))
	for _, el := range arr {
		var obj map[string]any
		if json.Unmarshal(el, &obj) != nil {
			continue
		}
		s, ok := obj[f.Field].(string)
		if !ok || !strings.HasPrefix(s, prefix) {
			continue
		}
		kept = append(kept, el)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return raw
	}
	return out
}
```

Finally, wire it into the return. Replace:

```go
	return Result{Data: extract(a.ResponseExtract, raw)}, nil
```

with:

```go
	data := extract(a.ResponseExtract, raw)
	if a.ResponseFilter.Field != "" {
		data = applyResponseFilter(data, a.ResponseFilter, asString(args[a.ResponseFilter.PrefixArg]))
	}
	return Result{Data: data}, nil
```

`asString` already exists in `render.go` and is what `subst` uses for argument values.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestExtract|TestApplyResponseFilter' -count=1`

Expected: PASS.

- [ ] **Step 6: Check what the fix newly activates**

Dotted extracts that were silently inert now really narrow. Find them:

```bash
grep -rn 'response_extract: "\$\.[^"]*\.' internal/connectors/connectors/
```

Expected: `reddit.yaml`'s two `$.data.children` actions. Confirm that path is right for Reddit's listing envelope (`{"kind":"Listing","data":{"children":[…]}}`) — it is, which is why it was authored that way. Note in the commit message that those two actions start working for the first time.

- [ ] **Step 7: Run the full package and commit**

Run: `go test ./internal/connectors/ -count=1`

Expected: PASS.

```bash
git add internal/connectors/execute.go internal/connectors/registry.go internal/connectors/extract_test.go
git commit -m "fix(connectors): walk dotted response_extract paths and filter array responses"
```

---

### Task 11: Full gate and PR

**Files:** none — verification only.

**Interfaces:**
- Consumes: every prior task.
- Produces: a green `make ci` and a draft PR.

- [ ] **Step 1: Run the full local gate**

Run: `make ci`

Expected: PASS on all of gofmt, `go vet`, `go test -race`, the six-target cross-compile, and the frontend gate. The `web` package alone takes ~343s under `-race`, so allow the full run 15+ minutes.

- [ ] **Step 2: Fix anything the gate catches, then re-run**

Do not proceed with a failing gate. If `go test -race` fails only in `web`, re-run just that package to confirm: `go test ./web/ -race -count=1 -timeout 900s`.

- [ ] **Step 3: Confirm the keyless provider is actually reachable end to end**

Build and start the server, then confirm the connections page lists Open-Meteo under "Data & Reference" with a bare Connect button:

```bash
make deploy
curl -sS http://127.0.0.1:8080/healthz
```

Expected: `/healthz` returns 200 JSON. Then open the SPA's connections page and check the card renders with no key field.

- [ ] **Step 4: Push and open a draft PR**

```bash
git push -u origin HEAD
gh pr create --draft \
  --title "feat(connectors): framework for everyday connectors" \
  --body "Implements Plan 1 of docs/superpowers/specs/2026-08-03-everyday-connectors-design.md: keyless auth kind, four new categories, shared base-URL normalizer, pinned SSRF stance, upstream logo source, and the committed live-check harness. Plan 2 (the nine wave-1 providers) builds on this."
```

The PR title must itself be a valid Conventional Commit — merges are squashes, and the title becomes the commit release-please reads.

---

## Self-Review

**Spec coverage.** Every framework item in the spec's "Framework changes" section maps to a task: `auth.kind: "none"` → Tasks 1–4 (request path, token store, connect endpoint, SPA); `validCategories` +4 → Task 5; shared `base_url` normalization → Task 6; deliberate SSRF stance → Task 7; logo vendoring fourth source → Task 8; `cmd/livecheck` uncommitted → Task 9. The spec's deferred `basic_pass_literal` is correctly absent (Toggl is not in wave 1). The spec's "Implementation sequencing" section names exactly this split.

**Type consistency.** `IsKeyless()` is defined in Task 1 and consumed under that exact name in Tasks 2, 3 and 4. `ConnectInput.Normalize` is added in Task 1 and read in Task 6. `NormalizeBaseURL(raw string) (string, error)` is defined in Task 6 and called there. The DTO value `"keyless"` is emitted in Task 4's Go step and branched on in Task 4's TSX step.

**Sequencing note.** Task 5 must run before Tasks 3 and 6, because the `open_meteo` and `immich` fixture YAMLs declare categories that `category_test.go` rejects until Task 5 lands. This is stated inline in Task 3, Step 1.

**Known soft spots**, flagged rather than papered over: three tasks tell the implementer to read actual helper/field names before writing (the `web` package's test harness in Task 3, the wizard's mutation names in Task 4, the vendor script's strip helper in Task 8) rather than inventing signatures this plan cannot verify. Task 8's `UPSTREAM` manifest needs a real pinned commit SHA resolved at implementation time — a URL cannot be fabricated here.
