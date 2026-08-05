# OAuth Credential Field Labels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the service connect wizard label its two OAuth credential fields the way each provider's own developer console does, instead of hardcoding "Client ID" / "Client secret" for all 21 OAuth providers.

**Architecture:** A new optional `oauth_creds` block on the provider YAML carries four strings (`id_label`, `id_hint`, `secret_label`, `secret_hint`). The web DTO reads it from the provider resolved through `auth_parent` — not from the child's own record — and the SPA renders it with the current strings as fallback. Presentation only: the wire and storage names `client_id` / `client_secret` are unchanged, and no connect or callback code is touched.

**Tech Stack:** Go 1.x (`gopkg.in/yaml.v3`, embedded provider YAML), Echo v4 JSON API, React + TypeScript SPA (Vite, vitest, @testing-library/react).

**Spec:** `docs/superpowers/specs/2026-08-04-oauth-credential-field-labels-design.md`

## Global Constraints

- **Labels are derived from each provider's own `setup_steps`**, which name the console field verbatim. Never invent a label; if the prose and the intended label disagree, the prose is edited in the same commit.
- **`oauth_creds` lives at the provider top level, not inside `auth:`.** Most OAuth providers (`google`, `github`, `notion`) declare no `auth:` block at all; nesting would force one into files that do not need it.
- **A provider with `auth_parent` set must never declare `oauth_creds`.** It has no OAuth app of its own, so the block would never be read.
- **The DTO field is a value struct, never a pointer.** `web/api_services_test.go:35` asserts nothing on this payload serializes as `null`.
- **All four fields default independently.** X declares only hints; Outlook declares an `id_label` and a `secret_hint`.
- **Wire contract frozen:** the POST body to `/api/v1/services/:provider/creds` stays `{client_id, client_secret}`.
- **Eight providers deliberately declare nothing** — `github`, `slack`, `google`, `spotify`, `strava`, `jira`, `linkedin`, `oura` — because their console genuinely says Client ID / Client secret.
- Run the full gate with `make ci` before the final commit.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/connectors/registry.go` | `OAuthCreds` type + the `Provider.OAuthCreds` field | 1 |
| `internal/connectors/providers/*.yaml` (13 files) | The declared labels and hints, plus two prose corrections | 1, 2 |
| `internal/connectors/oauth_creds_test.go` (new) | All three catalog assertions about the block | 1, 2, 3 |
| `internal/connectors/catalog_hygiene_test.go` | One added `session_exchange` case on the existing coherence test | 3 |
| `web/api_services.go` | `apiOAuthCreds` DTO + `auth_parent` resolution | 4 |
| `web/api_services_oauth_labels_test.go` (new) | The child-inherits-parent assertion | 4 |
| `web/ui/src/lib/connections.ts` | The mirrored TS type | 5 |
| `web/ui/src/pages/connections/ServiceWizard.tsx` | Rendering with fallback | 5 |
| `web/ui/src/pages/connections/ServiceWizard.test.tsx` | The declared-override assertion | 5 |

---

### Task 1: `OAuthCreds` type and the Meta family

**Files:**
- Modify: `internal/connectors/registry.go` (add type near `AuthConfig`; add field to `Provider` after `SetupSteps`)
- Modify: `internal/connectors/providers/facebook.yaml`, `instagram.yaml`, `meta_ads.yaml`, `threads.yaml`
- Test: `internal/connectors/oauth_creds_test.go` (create)

**Interfaces:**
- Consumes: `Registry.LoadBundled()`, `Registry.ProviderNames()`, `Registry.ProviderByName(name) (Provider, bool)` — all existing.
- Produces: `connectors.OAuthCreds` struct with exported string fields `IDLabel`, `IDHint`, `SecretLabel`, `SecretHint`; the field `Provider.OAuthCreds OAuthCreds`. Tasks 3 and 4 depend on both names exactly as written.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/oauth_creds_test.go`:

```go
package connectors

import (
	"strings"
	"testing"
)

// The labels are DERIVED from each provider's own setup_steps, which name the console
// field verbatim ("Copy the App key (client id) and App secret"). This ties them back to
// that source: rename a label without touching the prose and the card says "App ID" above
// a step telling the user to copy the "Client ID". That divergence is invisible in either
// file alone, which is why it needs a test rather than a review convention.
func TestOAuthCredLabelsMatchSetupSteps(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range r.ProviderNames() {
		p, _ := r.ProviderByName(name)
		steps := strings.ToLower(strings.Join(p.SetupSteps, " "))
		for _, f := range []struct{ what, label string }{
			{"id_label", p.OAuthCreds.IDLabel},
			{"secret_label", p.OAuthCreds.SecretLabel},
		} {
			if f.label == "" {
				continue
			}
			if !strings.Contains(steps, strings.ToLower(f.label)) {
				t.Errorf("%s %s = %q but no setup step mentions it — the connect form and the instructions disagree",
					name, f.what, f.label)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestOAuthCredLabelsMatchSetupSteps`
Expected: FAIL — a compile error, `p.OAuthCreds undefined (type Provider has no field or method OAuthCreds)`.

- [ ] **Step 3: Add the type and the field**

In `internal/connectors/registry.go`, immediately after the `AuthConfig` struct definition, add:

```go
// OAuthCreds names the two fields a provider's own developer console shows for its OAuth
// app, so the connect form asks for what the user is actually looking at. Meta says
// "App ID"/"App Secret", Salesforce "Consumer Key"/"Consumer Secret", Outlook
// "Application (client) ID" — the form hardcoded "Client ID"/"Client secret" for all of
// them, leaving the user to guess the two were the same thing.
//
// Every field is optional and defaults INDEPENDENTLY. X declares only hints, because its
// portal shows TWO credential pairs (OAuth 1.0a "API Key" and OAuth 2.0 "Client ID") and
// the labels are already right — the disambiguation is the whole value. An empty field
// falls back to "Client ID"/"Client secret", which is correct for the eight providers
// whose console genuinely says that.
//
// This lives at the provider top level rather than inside AuthConfig because most OAuth
// providers (google, github, notion) declare no auth: block at all, and nesting would
// force one into files that do not otherwise need it.
//
// A provider with AuthParent set must leave this empty: OAuthProvider() resolves the child
// to its parent before the labels are read, so a block on the child would never be shown,
// and its presence would state something false about where the credentials go.
// TestOAuthCredLabelsAreNonBlank rejects it.
type OAuthCreds struct {
	IDLabel     string `yaml:"id_label"`
	IDHint      string `yaml:"id_hint"`
	SecretLabel string `yaml:"secret_label"`
	SecretHint  string `yaml:"secret_hint"`
}
```

In the `Provider` struct, immediately after the `SetupSteps []string \`yaml:"setup_steps"\`` line, add:

```go
	// OAuthCreds renames the two credential fields on the connect form to match this
	// provider's console. Empty means "Client ID"/"Client secret".
	OAuthCreds OAuthCreds `yaml:"oauth_creds"`
```

- [ ] **Step 4: Run test to verify it compiles and passes**

Run: `go test ./internal/connectors/ -run TestOAuthCredLabelsMatchSetupSteps -v`
Expected: PASS. Note this pass is **vacuous** — no provider declares the block yet, so the loop body never runs its assertion. Step 6 is where it starts meaning something.

- [ ] **Step 5: Declare the Meta family**

Add this top-level block to `internal/connectors/providers/facebook.yaml`, `instagram.yaml` and `meta_ads.yaml`, immediately after each file's `setup_steps:` list:

```yaml
oauth_creds:
  id_label: "App ID"
  secret_label: "App Secret"
```

Add this to `internal/connectors/providers/threads.yaml`, immediately after its `setup_steps:` list:

```yaml
oauth_creds:
  id_label: "Threads App ID"
  id_hint: "under the Threads API product — DIFFERENT from the Facebook app id"
  secret_label: "Threads App Secret"
```

- [ ] **Step 6: Run the test to verify the labels agree with the prose**

Run: `go test ./internal/connectors/ -run TestOAuthCredLabelsMatchSetupSteps -v`
Expected: PASS, now meaningfully — all six declared labels were found in their own provider's setup steps.

Sanity-check the red by temporarily changing `facebook.yaml`'s `id_label` to `"Application ID"`, re-running, and confirming:
`facebook id_label = "Application ID" but no setup step mentions it`. Restore `"App ID"` before continuing.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/oauth_creds_test.go \
        internal/connectors/providers/facebook.yaml \
        internal/connectors/providers/instagram.yaml \
        internal/connectors/providers/meta_ads.yaml \
        internal/connectors/providers/threads.yaml
git commit -m "feat(connectors): oauth_creds block naming a provider's own credential fields

Meta's console says App ID / App Secret; the connect form said Client ID /
Client secret. The label is pinned to the provider's own setup_steps prose so
the card and the instructions cannot drift apart."
```

---

### Task 2: The remaining nine providers, and the two prose corrections

**Files:**
- Modify: `internal/connectors/providers/dropbox.yaml`, `pinterest.yaml`, `salesforce.yaml`, `tiktok.yaml`, `mastodon.yaml`, `outlook.yaml`, `reddit.yaml`, `notion.yaml`, `x.yaml`
- Test: `internal/connectors/oauth_creds_test.go` (append `TestDivergentOAuthLabelsStayDeclared`)

**Interfaces:**
- Consumes: `connectors.OAuthCreds` and `Provider.OAuthCreds` from Task 1.
- Produces: nothing new in Go; completes the declared set that Task 4's web test asserts over.

- [ ] **Step 1: Declare the nine remaining providers**

Add each block at the top level, immediately after that file's `setup_steps:` list.

`dropbox.yaml`:
```yaml
oauth_creds:
  id_label: "App key"
  secret_label: "App secret"
```

`pinterest.yaml`:
```yaml
oauth_creds:
  id_label: "App ID"
  secret_label: "App secret key"
```

`salesforce.yaml`:
```yaml
oauth_creds:
  id_label: "Consumer Key"
  secret_label: "Consumer Secret"
```

`tiktok.yaml`:
```yaml
oauth_creds:
  id_label: "Client key"
  secret_label: "Client secret"
```

`mastodon.yaml`:
```yaml
oauth_creds:
  id_label: "Client key"
  id_hint: "from Preferences → Development on YOUR OWN instance, not a central provider"
  secret_label: "Client secret"
```

`outlook.yaml`:
```yaml
oauth_creds:
  id_label: "Application (client) ID"
  secret_hint: "the secret's Value, not the Secret ID"
```

`reddit.yaml`:
```yaml
oauth_creds:
  id_label: "client ID"
  id_hint: "the string shown under the app name, not the app name itself"
  secret_label: "secret"
```

`notion.yaml`:
```yaml
oauth_creds:
  id_label: "OAuth client ID"
  secret_label: "OAuth client secret"
```

`x.yaml` — labels are already correct; only the disambiguation is needed, because the portal shows two credential pairs:
```yaml
oauth_creds:
  id_hint: "from User authentication settings → OAuth 2.0 — NOT the API Key on the Keys and tokens tab"
  secret_hint: "the OAuth 2.0 Client Secret — NOT the API Key Secret"
```

- [ ] **Step 2: Run the test to verify it fails on Notion**

Run: `go test ./internal/connectors/ -run TestOAuthCredLabelsMatchSetupSteps -v`
Expected: FAIL with **exactly one** error:
`notion secret_label = "OAuth client secret" but no setup step mentions it`

This failure is predicted by the spec and is the test doing its job. Notion's step reads "Copy the OAuth client ID and client secret and paste them below", so `"OAuth client secret"` is not a contiguous substring. If any other provider also fails, stop — a label was mistyped in Step 1.

- [ ] **Step 3: Correct the Notion prose**

In `internal/connectors/providers/notion.yaml`, change the setup step:

```yaml
  - "Copy the OAuth client ID and OAuth client secret and paste them below."
```

(from `"Copy the OAuth client ID and client secret and paste them below."`)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/connectors/ -run TestOAuthCredLabelsMatchSetupSteps -v`
Expected: PASS.

- [ ] **Step 5: Correct the Dropbox prose**

Dropbox's step reads "Copy the App key (client id) and App secret (client secret) and paste them below." Those parentheses existed only because the form said "Client ID"; now that the field itself says "App key" they read as though there are two separate values to find. In `internal/connectors/providers/dropbox.yaml`, change the step to:

```yaml
  - "Copy the App key and App secret and paste them below."
```

- [ ] **Step 6: Run test to confirm the edit is safe**

Run: `go test ./internal/connectors/ -run TestOAuthCredLabelsMatchSetupSteps -v`
Expected: PASS — `"App key"` and `"App secret"` both survive in the shortened prose.

- [ ] **Step 7: Write the regression pin**

Append to `internal/connectors/oauth_creds_test.go`:

```go
// The thirteen providers whose console diverges from "Client ID"/"Client secret", pinned
// by name against their FULL expected value — labels and hints, empty where the default
// is deliberate. TestOAuthCredLabelsMatchSetupSteps proves a declared label agrees with
// its prose; it says nothing when a label is deleted, which would silently revert the
// field to "Client ID" with every other test still green. This is the test that notices.
//
// Deliberately not a "every OAuth provider must declare" rule: the default is genuinely
// correct for github, slack, google, spotify, strava, jira, linkedin and oura, and forcing
// a declaration there would only invite wrong entries.
func TestDivergentOAuthLabelsStayDeclared(t *testing.T) {
	want := map[string]OAuthCreds{
		"dropbox":    {IDLabel: "App key", SecretLabel: "App secret"},
		"facebook":   {IDLabel: "App ID", SecretLabel: "App Secret"},
		"instagram":  {IDLabel: "App ID", SecretLabel: "App Secret"},
		"meta_ads":   {IDLabel: "App ID", SecretLabel: "App Secret"},
		"threads": {
			IDLabel:     "Threads App ID",
			IDHint:      "under the Threads API product — DIFFERENT from the Facebook app id",
			SecretLabel: "Threads App Secret",
		},
		"pinterest":  {IDLabel: "App ID", SecretLabel: "App secret key"},
		"salesforce": {IDLabel: "Consumer Key", SecretLabel: "Consumer Secret"},
		"tiktok":     {IDLabel: "Client key", SecretLabel: "Client secret"},
		"mastodon": {
			IDLabel:     "Client key",
			IDHint:      "from Preferences → Development on YOUR OWN instance, not a central provider",
			SecretLabel: "Client secret",
		},
		"outlook": {
			IDLabel:    "Application (client) ID",
			SecretHint: "the secret's Value, not the Secret ID",
		},
		"reddit": {
			IDLabel:     "client ID",
			IDHint:      "the string shown under the app name, not the app name itself",
			SecretLabel: "secret",
		},
		"notion": {IDLabel: "OAuth client ID", SecretLabel: "OAuth client secret"},
		// X's labels are already right; the portal shows TWO credential pairs, so the
		// hints are the entire point of this row.
		"x": {
			IDHint:     "from User authentication settings → OAuth 2.0 — NOT the API Key on the Keys and tokens tab",
			SecretHint: "the OAuth 2.0 Client Secret — NOT the API Key Secret",
		},
	}
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for name, w := range want {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Errorf("provider %s is missing from the catalog", name)
			continue
		}
		if p.OAuthCreds != w {
			t.Errorf("%s oauth_creds = %+v, want %+v", name, p.OAuthCreds, w)
		}
	}
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./internal/connectors/ -run TestDivergentOAuthLabelsStayDeclared -v`
Expected: PASS.

A pin test legitimately passes the moment it is written, so demonstrate its red explicitly: delete the whole `oauth_creds` block from `salesforce.yaml`, re-run, and confirm the failure names `salesforce` and reports an empty `oauth_creds` against the wanted `Consumer Key` / `Consumer Secret`. Then restore the block and re-run to green.

- [ ] **Step 9: Run the whole connectors package**

Run: `go test ./internal/connectors/ -count=1`
Expected: PASS — in particular `TestSetupStepsUsePlaceholderNotProse` must still pass after the two prose edits.

- [ ] **Step 10: Commit**

```bash
git add internal/connectors/oauth_creds_test.go internal/connectors/providers/
git commit -m "feat(connectors): credential labels for the remaining divergent providers

Dropbox App key, Salesforce Consumer Key, Outlook Application (client) ID,
TikTok Client key, Notion OAuth client secret, and X's disambiguation between
its OAuth 2.0 and OAuth 1.0a credential pairs. Notion's and Dropbox's setup
step prose move with their labels."
```

---

### Task 3: Declaration guards

**Files:**
- Test: `internal/connectors/oauth_creds_test.go` (append `TestOAuthCredLabelsAreNonBlank`)
- Modify: `internal/connectors/catalog_hygiene_test.go` (add a `session_exchange` case to `TestAuthConfigIsCoherent`)

**Interfaces:**
- Consumes: `Provider.OAuthCreds`, `Provider.AuthParent`, and the existing predicates `Provider.IsKeyless()`, `Provider.IsAPIKey()`, `Provider.UsesSessionExchange()`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the non-blank and no-child-declares test**

Append to `internal/connectors/oauth_creds_test.go`:

```go
// Two ways a declaration can be wrong without any other test noticing.
//
// A whitespace-only value is truthy in the SPA's `label || "Client ID"` fallback, so it
// renders as a blank label rather than degrading to the default — worse than declaring
// nothing.
//
// And a provider with auth_parent set has no OAuth app of its own: web/api_services.go
// resolves teams → outlook and google_calendar → google via OAuthProvider() BEFORE reading
// these labels, so a block on the child is never shown. Leaving one in place would assert
// something false about where the credentials go, and would look correct in the YAML.
func TestOAuthCredLabelsAreNonBlank(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("LoadBundled: %v", err)
	}
	for _, name := range r.ProviderNames() {
		p, _ := r.ProviderByName(name)
		oc := p.OAuthCreds
		if p.AuthParent != "" && oc != (OAuthCreds{}) {
			t.Errorf("%s reuses %s's OAuth app but declares oauth_creds — it would never be read",
				name, p.AuthParent)
		}
		for what, v := range map[string]string{
			"id_label":     oc.IDLabel,
			"id_hint":      oc.IDHint,
			"secret_label": oc.SecretLabel,
			"secret_hint":  oc.SecretHint,
		} {
			if v != "" && strings.TrimSpace(v) == "" {
				t.Errorf("%s %s is whitespace-only — it renders as a blank label instead of falling back", name, what)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it passes, and demonstrate both reds**

Run: `go test ./internal/connectors/ -run TestOAuthCredLabelsAreNonBlank -v`
Expected: PASS.

Demonstrate the child guard: add `oauth_creds:\n  id_label: "Client ID"` to `internal/connectors/providers/teams.yaml`, re-run, confirm
`teams reuses outlook's OAuth app but declares oauth_creds`, then remove it.

Demonstrate the blank guard: set `salesforce.yaml`'s `id_label: " "`, re-run, confirm
`salesforce id_label is whitespace-only`, then restore `"Consumer Key"`.

- [ ] **Step 3: Extend the existing coherence test for `session_exchange`**

In `internal/connectors/catalog_hygiene_test.go`, inside `TestAuthConfigIsCoherent`'s `switch`, add a case immediately after the existing `case p.IsAPIKey():` block:

```go
		case p.UsesSessionExchange():
			// Bluesky pastes an app password into the SAME form an api_key provider uses,
			// and the wizard reads key_label to name it. The switch covered only keyless
			// and api_key, so a blank label here would have shipped an unlabelled field
			// with every test green.
			if p.Auth.KeyLabel == "" {
				t.Errorf("%s uses session_exchange but has no key_label — the form would show a blank field", prov)
			}
```

- [ ] **Step 4: Run test to verify it passes and demonstrate the red**

Run: `go test ./internal/connectors/ -run TestAuthConfigIsCoherent -v`
Expected: PASS (Bluesky declares `key_label: "App password"`).

Demonstrate the red: comment out the `key_label:` line in `internal/connectors/providers/bluesky.yaml`, re-run, confirm
`bluesky uses session_exchange but has no key_label`, then restore it.

- [ ] **Step 5: Run the whole connectors package**

Run: `go test ./internal/connectors/ -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/oauth_creds_test.go internal/connectors/catalog_hygiene_test.go
git commit -m "test(connectors): guard blank credential labels and child declarations

A whitespace label is truthy in the SPA fallback so it renders blank rather
than defaulting, and an oauth_creds block on an auth_parent child is never
read. Also covers session_exchange in the auth-coherence switch, which
reached only keyless and api_key."
```

---

### Task 4: DTO plumbing with `auth_parent` resolution

**Files:**
- Modify: `web/api_services.go` (add `apiOAuthCreds` beside `apiServiceConnectInput`; add the field to `apiServiceProvider`; populate at the existing `credsProvider` resolution, `web/api_services.go:204`)
- Test: `web/api_services_oauth_labels_test.go` (create)

**Interfaces:**
- Consumes: `connectors.Provider.OAuthCreds` (Task 1); the existing `Registry.OAuthProvider(name) (Provider, bool)`, which returns the **parent** `Provider` when `auth_parent` is set; the existing web test helpers `keylessTestServer(t)`, `doJSON(...)` and `contains(...)`.
- Produces: JSON key `oauth_creds` on each element of `GET /api/v1/services` → `providers[]`, an object with string keys `id_label`, `id_hint`, `secret_label`, `secret_hint`. Task 5 mirrors this exactly.

- [ ] **Step 1: Write the failing test**

Create `web/api_services_oauth_labels_test.go`:

```go
package web

import (
	"net/http"
	"strings"
	"testing"
)

// The labels describe an OAuth APP, and twelve providers do not have one of their own:
// the nine google_*, youtube, linkedin_ads and teams all reuse a parent's app through
// auth_parent. Reading the CHILD's record would print "Client ID" over a form that feeds
// a Microsoft registration whose console says "Application (client) ID" — a bug that
// looks correct in api_services.go and in every YAML file. This is the one place a
// plausible implementation is silently wrong, so it gets its own test.
func TestChildProvidersInheritParentCredLabels(t *testing.T) {
	s, cookies, _ := keylessTestServer(t)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/services", nil, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	// outlook declares it; teams must inherit it.
	if n := strings.Count(body, `"id_label":"Application (client) ID"`); n < 2 {
		t.Errorf(`"Application (client) ID" appears %d time(s), want >= 2 (outlook declares it, teams inherits it)`, n)
	}
	// A provider that declares its own labels still carries them.
	if !contains(body, `"id_label":"App ID"`) {
		t.Error("Meta's App ID label never reached the services payload")
	}
	// A value struct, not a pointer: api_services_test.go asserts nothing on this
	// payload serializes as null, and every api_key and keyless provider carries the
	// field too.
	if contains(body, `"oauth_creds":null`) {
		t.Error(`oauth_creds serialized as null — it must be a value struct, not a pointer`)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run TestChildProvidersInheritParentCredLabels -v`
Expected: FAIL — `"Application (client) ID" appears 0 time(s), want >= 2` and `Meta's App ID label never reached the services payload`. The payload has no `oauth_creds` key at all yet.

- [ ] **Step 3: Add the DTO type**

In `web/api_services.go`, immediately after the `apiServiceConnectInput` struct, add:

```go
// apiOAuthCreds names the OAuth app's two credential fields the way the provider's own
// developer console does, so the connect form stops asking for a "Client ID" the user
// cannot find on the page they are looking at.
//
// A VALUE, not a pointer: api_services_test.go asserts nothing on this payload
// serializes as null — the convention connections, connect_inputs and setup_steps
// already follow. Empty fields are the SPA's signal to fall back.
type apiOAuthCreds struct {
	IDLabel     string `json:"id_label"`
	IDHint      string `json:"id_hint"`
	SecretLabel string `json:"secret_label"`
	SecretHint  string `json:"secret_hint"`
}
```

Add the field to `apiServiceProvider`, immediately after `KeyHint`:

```go
	// OAuthCreds is the OAuth-path analogue of KeyLabel/KeyHint. Resolved through
	// auth_parent — see the assignment below.
	OAuthCreds apiOAuthCreds `json:"oauth_creds"`
```

- [ ] **Step 4: Populate it from the resolved parent**

In `web/api_services.go`, replace the existing `credsProvider` block:

```go
		credsProvider := provider
		if op, ok := s.connectors.OAuthProvider(provider); ok && op.Name != provider {
			credsProvider = op.Name
		}
```

with:

```go
		credsProvider := provider
		oauthCreds := apiOAuthCreds{}
		if op, ok := s.connectors.OAuthProvider(provider); ok {
			if op.Name != provider {
				credsProvider = op.Name
			}
			// Read the labels off the RESOLVED provider, never off p: a child
			// (teams → outlook, google_calendar → google) has no OAuth app of its own,
			// and the fields it is being asked for belong to the parent's app.
			oauthCreds = apiOAuthCreds{
				IDLabel:     op.OAuthCreds.IDLabel,
				IDHint:      op.OAuthCreds.IDHint,
				SecretLabel: op.OAuthCreds.SecretLabel,
				SecretHint:  op.OAuthCreds.SecretHint,
			}
		}
```

Then add `OAuthCreds: oauthCreds,` to the `apiServiceProvider{...}` literal, immediately after the `KeyHint:` line.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./web/ -run TestChildProvidersInheritParentCredLabels -v`
Expected: PASS.

- [ ] **Step 6: Run the neighbouring services tests**

Run: `go test ./web/ -run 'TestServices|TestConnect|TestAPIParity' -count=1`
Expected: PASS — the four files that already assert over this payload (`api_services_test.go`, `api_services_keyless_test.go`, `api_services_preflight_test.go`, `api_parity_test.go`) all use presence assertions, so widening it is compatible. The one to watch is `api_services_test.go`'s `:null` check, covered by Step 1's third assertion.

- [ ] **Step 7: Commit**

```bash
git add web/api_services.go web/api_services_oauth_labels_test.go
git commit -m "feat(web): expose oauth_creds on the services payload

Resolved through auth_parent on the same OAuthProvider() call that already
decides has_creds, so teams shows Outlook's 'Application (client) ID' rather
than a Client ID that appears nowhere in its console."
```

---

### Task 5: Wizard rendering

**Files:**
- Modify: `web/ui/src/lib/connections.ts` (add `OAuthCreds` type; add the optional field to `ServiceProvider`)
- Modify: `web/ui/src/pages/connections/ServiceWizard.tsx:416-437` (the two credential-field `<div className="space-y-1">` blocks in the OAuth `view === "creds"` branch)
- Test: `web/ui/src/pages/connections/ServiceWizard.test.tsx`

**Interfaces:**
- Consumes: the JSON key `oauth_creds` produced by Task 4.
- Produces: nothing downstream.

- [ ] **Step 1: Add the mirrored TypeScript type**

In `web/ui/src/lib/connections.ts`, immediately before the `ServiceProvider` type, add:

```ts
// Mirrors apiOAuthCreds. What the provider's own developer console calls its OAuth app
// credentials — Meta says "App ID"/"App Secret", Salesforce "Consumer Key". Each field
// falls back independently to "Client ID"/"Client secret", which is correct for the
// eight providers whose console genuinely says that.
export type OAuthCreds = {
  id_label?: string;
  id_hint?: string;
  secret_label?: string;
  secret_hint?: string;
};
```

Inside `ServiceProvider`, immediately after `key_hint?: string;`, add:

```ts
  // Optional so existing test fixtures that omit it still typecheck.
  oauth_creds?: OAuthCreds;
```

- [ ] **Step 2: Write the failing test**

In `web/ui/src/pages/connections/ServiceWizard.test.tsx`, add this fixture immediately after `API_KEY_PROVIDER`:

```tsx
const OAUTH_RENAMED_FIELDS: ServiceProvider = {
  name: "facebook",
  label: "Facebook",
  category: "Publishing & Media",
  kind: "oauth",
  setup_url: "",
  setup_steps: [],
  has_creds: false,
  action_count: 0,
  connect_inputs: [], redirect_uri: "", preflight: [],
  connections: [],
  oauth_creds: {
    id_label: "App ID",
    secret_label: "App Secret",
    secret_hint: "App settings → Basic",
  },
};
```

and this test at the end of the file:

```tsx
// Meta's console shows "App ID"/"App Secret" while the form said "Client ID"/"Client
// secret", leaving the user to guess the two were the same thing. The rename is
// PRESENTATION ONLY — the POST body must still be {client_id, client_secret}, which is
// the half of this that a careless refactor would break silently.
test("oauth provider renames the credential fields to match its own console", async () => {
  let captured: { client_id: string; client_secret: string } | null = null;
  mockFetch({
    creds: (_provider, body) => {
      captured = body;
      return jsonResponse({ ok: true });
    },
  });
  const user = userEvent.setup();
  wrap(OAUTH_RENAMED_FIELDS);

  await user.click(screen.getByText("open wizard"));

  expect(await screen.findByText("App ID")).toBeInTheDocument();
  expect(screen.queryByText("Client ID")).not.toBeInTheDocument();
  expect(screen.getByText("App settings → Basic")).toBeInTheDocument();

  await user.type(screen.getByLabelText("App ID"), "cid-123");
  await user.type(screen.getByLabelText("App Secret"), "csecret-456");
  await user.click(screen.getByRole("button", { name: /save & continue/i }));

  expect(captured).toEqual({ client_id: "cid-123", client_secret: "csecret-456" });
});
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx -t "renames the credential fields"`
Expected: FAIL — `Unable to find an element with the text: App ID`. The wizard still renders the hardcoded "Client ID".

- [ ] **Step 4: Render the labels with a fallback**

In `web/ui/src/pages/connections/ServiceWizard.tsx`, in the OAuth `view === "creds"` branch, replace the two credential field blocks with:

```tsx
              <div className="space-y-1">
                <Label htmlFor="svc-client-id">
                  {provider.oauth_creds?.id_label || "Client ID"}
                </Label>
                <Input
                  id="svc-client-id"
                  value={clientId}
                  onChange={(e) => setClientId(e.target.value)}
                  autoComplete="off"
                />
                {provider.oauth_creds?.id_hint && (
                  <p className="text-xs text-muted-2">{provider.oauth_creds.id_hint}</p>
                )}
              </div>
              <div className="space-y-1">
                <Label htmlFor="svc-client-secret">
                  {provider.oauth_creds?.secret_label || "Client secret"}
                </Label>
                <Input
                  id="svc-client-secret"
                  type="password"
                  value={clientSecret}
                  onChange={(e) => setClientSecret(e.target.value)}
                  // Not "off": Chrome ignores that on password fields and
                  // pairs them with a nearby text input it fills as the
                  // username. "new-password" opts out of the pairing.
                  autoComplete="new-password"
                />
                {provider.oauth_creds?.secret_hint && (
                  <p className="text-xs text-muted-2">{provider.oauth_creds.secret_hint}</p>
                )}
              </div>
```

The element ids `svc-client-id` / `svc-client-secret` and the `clientId` / `clientSecret` state stay exactly as they were — only the visible text changes.

- [ ] **Step 5: Run test to verify it passes**

Run: `cd web/ui && npx vitest run src/pages/connections/ServiceWizard.test.tsx`
Expected: PASS, all tests in the file. In particular the pre-existing test `oauth provider with no saved creds shows the creds form…` must still pass: its `OAUTH_NO_CREDS` fixture declares no `oauth_creds`, so it exercises the fallback and still finds "Client ID".

- [ ] **Step 6: Typecheck and lint**

Run: `cd web/ui && npx tsc -b && npx oxlint`
Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add web/ui/src/lib/connections.ts \
        web/ui/src/pages/connections/ServiceWizard.tsx \
        web/ui/src/pages/connections/ServiceWizard.test.tsx
git commit -m "feat(web/connections): label OAuth credential fields per provider

The form now says App ID / App Secret for Meta, Consumer Key for Salesforce,
Application (client) ID for Outlook, and disambiguates X's two credential
pairs. Presentation only — the POST body is unchanged."
```

- [ ] **Step 8: Run the full gate**

Run: `make ci`
Expected: PASS — gofmt, `go vet`, `go test -race`, the six-way cross-compile, and the frontend job (`npm ci`, `tsc -b`, `oxlint`, `vitest`, `vite build`).

---

## Verification checklist

- [ ] `go test ./... -count=1` passes.
- [ ] `make ci` passes.
- [ ] Thirteen providers declare `oauth_creds`; the eight listed in Global Constraints declare nothing.
- [ ] No provider with `auth_parent` declares the block.
- [ ] `GET /api/v1/services` contains `"id_label":"Application (client) ID"` at least twice.
- [ ] The creds POST body is still `{client_id, client_secret}`.
