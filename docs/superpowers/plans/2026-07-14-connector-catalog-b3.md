# Connector Catalog B3 Implementation Plan (base-URL providers)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Salesforce, Shopify, Mailchimp, and Zendesk — providers whose API base URL is resolved per-connection — via small declarative engine primitives + curated actions.

**Architecture:** Four narrow primitives feed `service_connections.extra` (→ `{{conn.<key>}}` templating): `connect_inputs` (user-entered fields), `token_extra` (fields from the OAuth token response), `key_extra` (parsed from an API key), and a templated Basic-auth username. Plus a `body_arg` primitive (whole request body = one object arg) for Salesforce sObject writes. Then each provider is data (provider YAML + connector YAML).

**Tech Stack:** Go, `gopkg.in/yaml.v3`, Echo v4. Tests: stdlib `testing` + `net/http`.

## Global Constraints

- Package under change: `internal/connectors` + `web/handlers_services.go` + `web/templates/dashboard/services.html`. No DB migrations (reuse `service_connections.extra`).
- Base URLs come only from `{{conn.<key>}}` templating fed by the primitives — no hardcoded instance/subdomain/dc.
- Auth: Salesforce OAuth2; Shopify api-key header `X-Shopify-Access-Token` (`value_prefix: ""`); Mailchimp api-key (dc from key); Zendesk api-key Basic with templated username.
- `applyAuth` signature changes to `applyAuth(req *http.Request, prov Provider, credential string, connExtra map[string]string)` — ALL callers updated; `nil` extra preserves current behavior.
- Every `body:` is a YAML map (scalar `body:` fails LoadBundled). Arbitrary-object bodies use the new `body_arg`.
- Mutating actions (create/update/delete/send/comment) set `mutating: true`.
- **Verification: unit/rendering only** — no live API calls.
- Add providers to `availableServiceProviders` only in the final task.
- Build: `go build ./...`. Test: `go test ./internal/connectors/... ./web/... -count=1`.
- Branch: `main`.

---

## File Structure

- `internal/connectors/registry.go` — Modify: add `Provider.ConnectInputs`, `Provider.TokenExtra`, `Provider.KeyExtra`, `AuthConfig.BasicUserTemplate`, `RequestTemplate.BodyArg`; add `ConnectInput` type.
- `internal/connectors/auth.go` — Modify: `applyAuth` gains `connExtra` + templated Basic username.
- `internal/connectors/execute.go` — Modify: pass `conn.Extra` to `applyAuth`.
- `internal/connectors/auth_test.go`, `b2_test.go` — Modify: update `applyAuth` call sites (+`nil`).
- `internal/connectors/render.go` — Modify: `body_arg` branch in `renderRequest`.
- `internal/connectors/oauth.go` — Modify: `TokenSet.Extra` + capture `token_extra` fields.
- `internal/connectors/keyextra.go` — Create: `DeriveKeyExtra(provider Provider, key string) map[string]string`.
- `web/handlers_services.go` — Modify: `serviceProviderView.ConnectInputs`; `handleConnectAPIKey` stores connect-inputs + key-extra into `extra`; `handleOAuthCallback` merges `token_extra`; `availableServiceProviders`.
- `web/templates/dashboard/services.html` — Modify: render `connect_inputs` fields in the api-key form.
- Providers/connectors YAML: `salesforce`, `shopify`, `mailchimp`, `zendesk` (both dirs).
- Tests: `internal/connectors/b3_test.go` (new).

---

## Task 1: `connect_inputs` primitive + Shopify

**Files:** Modify `registry.go`, `web/handlers_services.go`, `web/templates/dashboard/services.html`; create `providers/shopify.yaml`, `connectors/shopify.yaml`, `internal/connectors/b3_test.go`.

**Interfaces:**
- Produces: `ConnectInput` type; `Provider.ConnectInputs []ConnectInput`; connect-form storage into `extra`; `shopify` provider + actions; `b3Reg`/`renderB3` test helpers.

- [ ] **Step 1: Write the failing test** — create `internal/connectors/b3_test.go`:

```go
package connectors

import (
	"encoding/json"
	"testing"
)

func b3Reg(t *testing.T) *Registry {
	t.Helper()
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// renderB3 renders an action with the given args + connExtra, returning method, url, and parsed body.
func renderB3(t *testing.T, r *Registry, provider, action string, args map[string]any, connExtra map[string]string) (string, map[string]any) {
	t.Helper()
	a, ok := r.Action(provider, action)
	if !ok {
		t.Fatalf("%s.%s missing", provider, action)
	}
	_, u, body, _, err := renderRequest(a, args, connExtra)
	if err != nil {
		t.Fatalf("render %s.%s: %v", provider, action, err)
	}
	var m map[string]any
	if len(body) > 0 {
		json.Unmarshal(body, &m)
	}
	return u, m
}

func TestB3_ShopifyConnectInputsAndURL(t *testing.T) {
	r := b3Reg(t)
	p, ok := r.ProviderByName("shopify")
	if !ok || !p.IsAPIKey() {
		t.Fatal("shopify must load as api_key")
	}
	// connect_inputs declares the shop field
	if len(p.ConnectInputs) == 0 || p.ConnectInputs[0].Key != "shop" {
		t.Fatalf("shopify must declare connect_input shop, got %+v", p.ConnectInputs)
	}
	// {{conn.shop}} resolves in the action URL
	u, _ := renderB3(t, r, "shopify", "shopify_list_products", nil, map[string]string{"shop": "acme.myshopify.com"})
	if u != "https://acme.myshopify.com/admin/api/2024-10/products.json" {
		t.Fatalf("shop not substituted into URL: %s", u)
	}
	// api-key header uses X-Shopify-Access-Token with no prefix
	if p.Auth.HeaderName != "X-Shopify-Access-Token" || p.Auth.ValuePrefix != "" {
		t.Fatalf("bad shopify auth: %+v", p.Auth)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB3_Shopify -count=1` → FAIL (shopify missing, `ConnectInputs` undefined).

- [ ] **Step 3: Add types in `registry.go`**

Add the type + field:

```go
// ConnectInput is a per-connection value collected on the api-key connect form and stored in
// service_connections.extra (exposed to request templates + auth as {{conn.<key>}}).
type ConnectInput struct {
	Key      string `yaml:"key"`
	Label    string `yaml:"label"`
	Hint     string `yaml:"hint"`
	Required bool   `yaml:"required"`
}
```

Add to `Provider`: `ConnectInputs []ConnectInput \`yaml:"connect_inputs"\`` (next to `Auth`).

- [ ] **Step 4: Store connect-inputs into `extra` in `handleConnectAPIKey`**

In `web/handlers_services.go` `handleConnectAPIKey`, after resolving `label` and before building the connection, collect connect-inputs into an `extra` JSON:

```go
	extra := map[string]string{}
	for _, ci := range prov.ConnectInputs {
		v := strings.TrimSpace(c.FormValue(ci.Key))
		if ci.Required && v == "" {
			return s.redirectWithError(c, "/dashboard/connectors/services", ci.Label+" is required.")
		}
		if v != "" {
			extra[ci.Key] = v
		}
	}
	extraJSON := ""
	if len(extra) > 0 {
		if b, _ := json.Marshal(extra); b != nil {
			extraJSON = string(b)
		}
	}
```

Add `Extra: extraJSON,` to the `db.InsertServiceConnection(...)` struct literal in this handler. (Ensure `encoding/json` is imported in the file — it already is, used by the OAuth callback.)

- [ ] **Step 5: Surface connect-inputs to the view + render them**

In `serviceProviderView` add: `ConnectInputs []connectors.ConnectInput`. In `showServices`, set `ConnectInputs: p.ConnectInputs` on the view literal (from the `ProviderByName` lookup `p`).

In `web/templates/dashboard/services.html`, inside the api-key form (the `{{if .IsAPIKey}}` block containing the `api_key` + `account_label` inputs), add BEFORE the submit button:

```html
{{range .ConnectInputs}}
  <input type="text" name="{{.Key}}" placeholder="{{.Label}}{{if .Hint}} ({{.Hint}}){{end}}" class="input input-bordered input-sm w-full"{{if .Required}} required{{end}}>
{{end}}
```

- [ ] **Step 6: Create `internal/connectors/providers/shopify.yaml`**

```yaml
name: shopify
label: Shopify
auth:
  kind: api_key
  placement: header
  header_name: X-Shopify-Access-Token
  value_prefix: ""
  key_label: "Shopify Admin API access token"
  key_hint: "shpat_..."
  setup_url: https://www.shopify.com/admin/settings/apps/development
connect_inputs:
  - {key: shop, label: "Store domain", hint: "mystore.myshopify.com", required: true}
```

- [ ] **Step 7: Create `internal/connectors/connectors/shopify.yaml`**

```yaml
provider: shopify
actions:
  - name: shopify_list_products
    description: "List products in the store. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}}}
    request: {method: GET, url: "https://{{conn.shop}}/admin/api/2024-10/products.json", query: {limit: "{{limit}}"}}
    response_extract: "$.products"
  - name: shopify_get_product
    description: "Get a product by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://{{conn.shop}}/admin/api/2024-10/products/{{id}}.json"}
    response_extract: "$.product"
  - name: shopify_create_product
    description: "Create a product. product is an object (title, body_html, vendor, variants…). Mutating."
    mutating: true
    params: {type: object, properties: {product: {type: object}}, required: [product]}
    request: {method: POST, url: "https://{{conn.shop}}/admin/api/2024-10/products.json", body: {product: "{{product}}"}}
    response_extract: "$.product"
  - name: shopify_update_product
    description: "Update a product by id. product is an object of fields to change. Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, product: {type: object}}, required: [id, product]}
    request: {method: PUT, url: "https://{{conn.shop}}/admin/api/2024-10/products/{{id}}.json", body: {product: "{{product}}"}}
    response_extract: "$.product"
  - name: shopify_list_orders
    description: "List orders. Read-only."
    mutating: false
    params: {type: object, properties: {status: {type: string}, limit: {type: integer}}}
    request: {method: GET, url: "https://{{conn.shop}}/admin/api/2024-10/orders.json", query: {status: "{{status}}", limit: "{{limit}}"}}
    response_extract: "$.orders"
  - name: shopify_get_order
    description: "Get an order by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://{{conn.shop}}/admin/api/2024-10/orders/{{id}}.json"}
    response_extract: "$.order"
  - name: shopify_list_customers
    description: "List customers. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}}}
    request: {method: GET, url: "https://{{conn.shop}}/admin/api/2024-10/customers.json", query: {limit: "{{limit}}"}}
    response_extract: "$.customers"
  - name: shopify_create_draft_order
    description: "Create a draft order. draft_order is an object (line_items[], customer…). Mutating."
    mutating: true
    params: {type: object, properties: {draft_order: {type: object}}, required: [draft_order]}
    request: {method: POST, url: "https://{{conn.shop}}/admin/api/2024-10/draft_orders.json", body: {draft_order: "{{draft_order}}"}}
    response_extract: "$.draft_order"
```

- [ ] **Step 8: Run** `go test ./internal/connectors/ -run TestB3_Shopify -count=1 && go build ./...` → PASS + clean.
- [ ] **Step 9: Commit** `feat(connectors): connect_inputs primitive + Shopify provider (8 actions)`.

---

## Task 2: `token_extra` + `body_arg` primitives + Salesforce

**Files:** Modify `registry.go`, `oauth.go`, `render.go`, `web/handlers_services.go`; create `providers/salesforce.yaml`, `connectors/salesforce.yaml`; modify `b3_test.go`.

**Interfaces:**
- Consumes: `renderRequest`, `OAuthClient`.
- Produces: `TokenSet.Extra`; `Provider.TokenExtra []string`; `RequestTemplate.BodyArg string`; `salesforce` provider + actions.

- [ ] **Step 1: Write the failing tests** — add to `b3_test.go`:

```go
func TestB3_TokenExtraCapture(t *testing.T) {
	// A token response carrying instance_url is captured into TokenSet.Extra for a provider
	// declaring token_extra: [instance_url].
	p := Provider{TokenExtra: []string{"instance_url"}}
	ts, err := parseTokenResponse([]byte(`{"access_token":"AT","expires_in":3600,"instance_url":"https://na1.salesforce.com"}`), p)
	if err != nil {
		t.Fatal(err)
	}
	if ts.AccessToken != "AT" || ts.Extra["instance_url"] != "https://na1.salesforce.com" {
		t.Fatalf("token_extra not captured: %+v", ts)
	}
}

func TestB3_SalesforceBodyArgAndURL(t *testing.T) {
	r := b3Reg(t)
	if _, ok := r.ProviderByName("salesforce"); !ok {
		t.Fatal("salesforce not loaded")
	}
	// body_arg: whole body is the fields object
	_, m := renderB3(t, r, "salesforce", "salesforce_create_sobject",
		map[string]any{"type": "Account", "fields": map[string]any{"Name": "Acme"}}, map[string]string{"instance_url": "https://na1.salesforce.com"})
	if m["Name"] != "Acme" {
		t.Fatalf("body_arg should marshal fields as the whole body, got %v", m)
	}
	// {{conn.instance_url}} resolves in the URL
	u, _ := renderB3(t, r, "salesforce", "salesforce_get_sobject",
		map[string]any{"type": "Account", "id": "001"}, map[string]string{"instance_url": "https://na1.salesforce.com"})
	if u != "https://na1.salesforce.com/services/data/v60.0/sobjects/Account/001" {
		t.Fatalf("instance_url not substituted: %s", u)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run 'TestB3_TokenExtra|TestB3_Salesforce' -count=1` → FAIL.

- [ ] **Step 3: `TokenSet.Extra` + `parseTokenResponse` in `oauth.go`**

Add `Extra map[string]string` to the `TokenSet` struct. Add a helper and use it in `tokenRequest`:

```go
// parseTokenResponse decodes a token endpoint JSON body into a TokenSet, also capturing any
// prov.TokenExtra fields (e.g. Salesforce instance_url) into TokenSet.Extra.
func parseTokenResponse(b []byte, prov Provider) (TokenSet, error) {
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return TokenSet{}, err
	}
	ts := TokenSet{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresIn: out.ExpiresIn}
	if len(prov.TokenExtra) > 0 {
		var raw map[string]any
		if json.Unmarshal(b, &raw) == nil {
			ts.Extra = map[string]string{}
			for _, k := range prov.TokenExtra {
				if v, ok := raw[k].(string); ok {
					ts.Extra[k] = v
				}
			}
		}
	}
	return ts, nil
}
```

In `tokenRequest`, replace the existing inline unmarshal of `access_token/refresh_token/expires_in` (near the end) with `return parseTokenResponse(b, p)`. (The `p Provider` is already a parameter of `tokenRequest`.)

- [ ] **Step 4: `Provider.TokenExtra` + `RequestTemplate.BodyArg` in `registry.go`**

Add `TokenExtra []string \`yaml:"token_extra"\`` to `Provider`. Add `BodyArg string \`yaml:"body_arg"\`` to `RequestTemplate`.

- [ ] **Step 5: `body_arg` branch in `render.go` `renderRequest`**

In the body `switch`, add as the FIRST case (before `BodyBuilder`):

```go
	case a.Request.BodyArg != "":
		body, err = json.Marshal(args[a.Request.BodyArg])
		contentType = "application/json"
```

- [ ] **Step 6: Merge `token_extra` into `extra` in `handleOAuthCallback`**

In `web/handlers_services.go` `handleOAuthCallback`, where `extraJSON` is built from `post_connect`, also merge `ts.Extra`. Replace the extra-building block so both sources combine:

```go
	extraMap := map[string]string{}
	if prov.PostConnect != "" {
		if vals, perr := connectors.RunPostConnect(ctx, prov.PostConnect, nil, ts.AccessToken); perr != nil {
			return s.redirectWithError(c, "/dashboard/connectors/services", "Connected, but setup failed: "+perr.Error())
		} else {
			for k, v := range vals {
				extraMap[k] = v
			}
		}
	}
	for k, v := range ts.Extra { // token_extra fields (e.g. Salesforce instance_url)
		extraMap[k] = v
	}
	extraJSON := ""
	if len(extraMap) > 0 {
		if b, _ := json.Marshal(extraMap); b != nil {
			extraJSON = string(b)
		}
	}
```

- [ ] **Step 7: Create `internal/connectors/providers/salesforce.yaml`**

```yaml
name: salesforce
label: Salesforce
authorize_url: https://login.salesforce.com/services/oauth2/authorize
token_url: https://login.salesforce.com/services/oauth2/token
token_extra: [instance_url]
default_scopes: [api, refresh_token]
setup_url: https://help.salesforce.com/s/articleView?id=sf.connected_app_create.htm
setup_steps:
  - "Salesforce Setup → App Manager → New Connected App → Enable OAuth Settings."
  - "Add the redirect URI shown above (Callback URL); select scopes: Manage user data via APIs (api) + refresh_token."
  - "After saving, copy the Consumer Key (client id) and Consumer Secret (client secret) below."
```

> Salesforce returns `instance_url` in the token response; `token_extra` captures it → `{{conn.instance_url}}`.

- [ ] **Step 8: Create `internal/connectors/connectors/salesforce.yaml`**

```yaml
provider: salesforce
actions:
  - name: salesforce_soql_query
    description: "Run a SOQL query. Read-only. e.g. SELECT Id,Name FROM Account LIMIT 10"
    mutating: false
    params: {type: object, properties: {soql: {type: string}}, required: [soql]}
    request: {method: GET, url: "{{conn.instance_url}}/services/data/v60.0/query", query: {q: "{{soql}}"}}
    response_extract: "$.records"
  - name: salesforce_search
    description: "Run a SOSL search. Read-only. e.g. FIND {Acme} IN ALL FIELDS RETURNING Account(Id,Name)"
    mutating: false
    params: {type: object, properties: {sosl: {type: string}}, required: [sosl]}
    request: {method: GET, url: "{{conn.instance_url}}/services/data/v60.0/search", query: {q: "{{sosl}}"}}
    response_extract: "$"
  - name: salesforce_list_sobjects
    description: "List available sObject types. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "{{conn.instance_url}}/services/data/v60.0/sobjects"}
    response_extract: "$.sobjects"
  - name: salesforce_describe_sobject
    description: "Describe an sObject type's fields. Read-only."
    mutating: false
    params: {type: object, properties: {type: {type: string}}, required: [type]}
    request: {method: GET, url: "{{conn.instance_url}}/services/data/v60.0/sobjects/{{type}}/describe"}
    response_extract: "$"
  - name: salesforce_get_sobject
    description: "Get one record by sObject type + id. Read-only."
    mutating: false
    params: {type: object, properties: {type: {type: string}, id: {type: string}}, required: [type, id]}
    request: {method: GET, url: "{{conn.instance_url}}/services/data/v60.0/sobjects/{{type}}/{{id}}"}
    response_extract: "$"
  - name: salesforce_create_sobject
    description: "Create a record. type is the sObject (Account, Contact, …); fields is an object of field→value. Mutating."
    mutating: true
    params: {type: object, properties: {type: {type: string}, fields: {type: object}}, required: [type, fields]}
    request: {method: POST, url: "{{conn.instance_url}}/services/data/v60.0/sobjects/{{type}}", body_arg: fields}
    response_extract: "$"
  - name: salesforce_update_sobject
    description: "Update a record by type + id. fields is an object of fields to change. Mutating."
    mutating: true
    params: {type: object, properties: {type: {type: string}, id: {type: string}, fields: {type: object}}, required: [type, id, fields]}
    request: {method: PATCH, url: "{{conn.instance_url}}/services/data/v60.0/sobjects/{{type}}/{{id}}", body_arg: fields}
    response_extract: "$"
  - name: salesforce_delete_sobject
    description: "Delete a record by type + id. Mutating and irreversible."
    mutating: true
    params: {type: object, properties: {type: {type: string}, id: {type: string}}, required: [type, id]}
    request: {method: DELETE, url: "{{conn.instance_url}}/services/data/v60.0/sobjects/{{type}}/{{id}}"}
    response_extract: "$"
```

- [ ] **Step 9: Run** `go test ./internal/connectors/ -run 'TestB3_TokenExtra|TestB3_Salesforce|TestExchange|TestAccessToken' -count=1 && go build ./...` → PASS (existing OAuth tests unaffected by the `parseTokenResponse` refactor).
- [ ] **Step 10: Commit** `feat(connectors): token_extra + body_arg primitives + Salesforce provider`.

---

## Task 3: `key_extra` primitive + Mailchimp

**Files:** Create `internal/connectors/keyextra.go`, `providers/mailchimp.yaml`, `connectors/mailchimp.yaml`; modify `registry.go`, `web/handlers_services.go`, `b3_test.go`.

**Interfaces:**
- Produces: `Provider.KeyExtra map[string]string`; `DeriveKeyExtra(prov, key) map[string]string`; `mailchimp` provider + actions.

- [ ] **Step 1: Write the failing test** — add to `b3_test.go`:

```go
func TestB3_MailchimpKeyExtra(t *testing.T) {
	r := b3Reg(t)
	p, ok := r.ProviderByName("mailchimp")
	if !ok || !p.IsAPIKey() {
		t.Fatal("mailchimp must load as api_key")
	}
	got := DeriveKeyExtra(p, "abcdef0123456789-us21")
	if got["dc"] != "us21" {
		t.Fatalf("dc not parsed from key: %v", got)
	}
	// a key with no dash yields no dc (empty), not a panic
	if v := DeriveKeyExtra(p, "nodash"); v["dc"] != "" {
		t.Fatalf("expected empty dc for dashless key, got %v", v)
	}
	u, _ := renderB3(t, r, "mailchimp", "mailchimp_list_audiences", nil, map[string]string{"dc": "us21"})
	if u != "https://us21.api.mailchimp.com/3.0/lists" {
		t.Fatalf("dc not substituted into URL: %s", u)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB3_Mailchimp -count=1` → FAIL.

- [ ] **Step 3: `Provider.KeyExtra` + `DeriveKeyExtra`**

Add to `Provider` in `registry.go`: `KeyExtra map[string]string \`yaml:"key_extra"\`` (maps an extra key → a derive rule; only `"suffix"` supported).

Create `internal/connectors/keyextra.go`:

```go
package connectors

import "strings"

// DeriveKeyExtra derives per-connection extra values from a pasted API key, per the provider's
// key_extra rules. Only the "suffix" rule is supported: the substring after the last '-' in the
// key (Mailchimp keys are "<secret>-<dc>"). Unknown rules and dashless keys yield "".
func DeriveKeyExtra(prov Provider, key string) map[string]string {
	if len(prov.KeyExtra) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, rule := range prov.KeyExtra {
		switch rule {
		case "suffix":
			if i := strings.LastIndex(key, "-"); i >= 0 && i < len(key)-1 {
				out[k] = key[i+1:]
			} else {
				out[k] = ""
			}
		default:
			out[k] = ""
		}
	}
	return out
}
```

- [ ] **Step 4: Wire `DeriveKeyExtra` into `handleConnectAPIKey`**

In `handleConnectAPIKey` (`web/handlers_services.go`), after collecting `connect_inputs` into `extra` (Task 1) and before marshaling `extraJSON`, merge key-derived values:

```go
	for k, v := range connectors.DeriveKeyExtra(prov, apiKey) {
		extra[k] = v
	}
```

(This sits before the `extraJSON` marshal so both connect-inputs and key-extra land in `extra`.)

- [ ] **Step 5: Create `internal/connectors/providers/mailchimp.yaml`**

```yaml
name: mailchimp
label: Mailchimp
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "Mailchimp API key"
  key_hint: "abc123...-us21"
  setup_url: https://us1.admin.mailchimp.com/account/api/
key_extra:
  dc: suffix
```

> Mailchimp accepts the API key as `Authorization: Bearer <key>`; the `-us21` suffix → `{{conn.dc}}`.

- [ ] **Step 6: Create `internal/connectors/connectors/mailchimp.yaml`**

```yaml
provider: mailchimp
actions:
  - name: mailchimp_list_audiences
    description: "List audiences (lists). Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://{{conn.dc}}.api.mailchimp.com/3.0/lists"}
    response_extract: "$.lists"
  - name: mailchimp_get_audience
    description: "Get an audience by list id. Read-only."
    mutating: false
    params: {type: object, properties: {list_id: {type: string}}, required: [list_id]}
    request: {method: GET, url: "https://{{conn.dc}}.api.mailchimp.com/3.0/lists/{{list_id}}"}
    response_extract: "$"
  - name: mailchimp_list_members
    description: "List members of an audience. Read-only."
    mutating: false
    params: {type: object, properties: {list_id: {type: string}, count: {type: integer}}, required: [list_id]}
    request: {method: GET, url: "https://{{conn.dc}}.api.mailchimp.com/3.0/lists/{{list_id}}/members", query: {count: "{{count}}"}}
    response_extract: "$.members"
  - name: mailchimp_get_member
    description: "Get a member by list id + subscriber hash (MD5 of lowercased email). Read-only."
    mutating: false
    params: {type: object, properties: {list_id: {type: string}, subscriber_hash: {type: string}}, required: [list_id, subscriber_hash]}
    request: {method: GET, url: "https://{{conn.dc}}.api.mailchimp.com/3.0/lists/{{list_id}}/members/{{subscriber_hash}}"}
    response_extract: "$"
  - name: mailchimp_add_member
    description: "Add a member to an audience (status e.g. subscribed). Mutating."
    mutating: true
    params: {type: object, properties: {list_id: {type: string}, email: {type: string}, status: {type: string}}, required: [list_id, email, status]}
    request:
      method: POST
      url: "https://{{conn.dc}}.api.mailchimp.com/3.0/lists/{{list_id}}/members"
      body: {email_address: "{{email}}", status: "{{status}}"}
    response_extract: "$"
  - name: mailchimp_list_campaigns
    description: "List campaigns. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://{{conn.dc}}.api.mailchimp.com/3.0/campaigns"}
    response_extract: "$.campaigns"
  - name: mailchimp_create_campaign
    description: "Create a campaign. recipients {list_id} + settings object. Mutating."
    mutating: true
    params: {type: object, properties: {type: {type: string}, list_id: {type: string}, settings: {type: object}}, required: [type, list_id, settings]}
    request:
      method: POST
      url: "https://{{conn.dc}}.api.mailchimp.com/3.0/campaigns"
      body: {type: "{{type}}", recipients: {list_id: "{{list_id}}"}, settings: "{{settings}}"}
    response_extract: "$"
  - name: mailchimp_send_campaign
    description: "Send a campaign by id. Delivers real email. Mutating."
    mutating: true
    params: {type: object, properties: {campaign_id: {type: string}}, required: [campaign_id]}
    request: {method: POST, url: "https://{{conn.dc}}.api.mailchimp.com/3.0/campaigns/{{campaign_id}}/actions/send"}
    response_extract: "$"
```

- [ ] **Step 7: Run** `go test ./internal/connectors/ -run TestB3_Mailchimp -count=1 && go build ./...` → PASS.
- [ ] **Step 8: Commit** `feat(connectors): key_extra primitive + Mailchimp provider (dc from key)`.

---

## Task 4: Templated Basic username (`applyAuth` signature) + Zendesk

**Files:** Modify `registry.go`, `auth.go`, `execute.go`, `auth_test.go`, `b2_test.go`; create `providers/zendesk.yaml`, `connectors/zendesk.yaml`; modify `b3_test.go`.

**Interfaces:**
- Produces: `AuthConfig.BasicUserTemplate`; `applyAuth(req, prov, credential, connExtra)`; `zendesk` provider + actions.

- [ ] **Step 1: Write the failing test** — add to `b3_test.go`:

```go
import "net/http" // add to b3_test.go imports

func TestB3_ZendeskBasicTemplatedUser(t *testing.T) {
	r := b3Reg(t)
	p, ok := r.ProviderByName("zendesk")
	if !ok || !p.IsAPIKey() {
		t.Fatal("zendesk must load as api_key")
	}
	req, _ := http.NewRequest("GET", "https://acme.zendesk.com/api/v2/tickets.json", nil)
	applyAuth(req, p, "APITOKEN", map[string]string{"email": "me@acme.com"})
	u, pw, ok := req.BasicAuth()
	if !ok || u != "me@acme.com/token" || pw != "APITOKEN" {
		t.Fatalf("zendesk basic auth wrong: u=%q pw=%q ok=%v", u, pw, ok)
	}
	// ticket create wraps under {ticket:{}}
	_, m := renderB3(t, r, "zendesk", "zendesk_create_ticket",
		map[string]any{"ticket": map[string]any{"subject": "Help"}}, map[string]string{"subdomain": "acme"})
	tk, _ := m["ticket"].(map[string]any)
	if tk["subject"] != "Help" {
		t.Fatalf("ticket wrapper wrong: %v", m)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB3_Zendesk -count=1` → FAIL (compile: `applyAuth` takes 3 args; `zendesk` missing).

- [ ] **Step 3: `AuthConfig.BasicUserTemplate` in `registry.go`**

Add to `AuthConfig`: `BasicUserTemplate string \`yaml:"basic_user_template"\``.

- [ ] **Step 4: Change `applyAuth` in `auth.go`**

```go
func applyAuth(req *http.Request, prov Provider, credential string, connExtra map[string]string) {
	a := prov.Auth
	if a.Kind != "api_key" {
		req.Header.Set("Authorization", "Bearer "+credential)
		return
	}
	switch a.Placement {
	case "query":
		q := req.URL.Query()
		q.Set(a.ParamName, credential)
		req.URL.RawQuery = q.Encode()
	case "basic":
		if a.BasicUserTemplate != "" {
			user := subst(a.BasicUserTemplate, nil, connExtra)
			req.SetBasicAuth(user, credential)
		} else {
			req.SetBasicAuth(credential, "")
		}
	default: // "header"
		name := a.HeaderName
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, a.ValuePrefix+credential)
	}
}
```

- [ ] **Step 5: Update all `applyAuth` callers**

- `execute.go:94`: `applyAuth(req, prov, token, conn.Extra)` (`conn.Extra` is `map[string]string` on `ConnRef`).
- `auth_test.go`: the 4 calls — append `, nil` to each.
- `b2_test.go:135` (`TestB2_ClickUp`): `applyAuth(req, p, "pk_123", nil)`.

- [ ] **Step 6: Create `internal/connectors/providers/zendesk.yaml`**

```yaml
name: zendesk
label: Zendesk
auth:
  kind: api_key
  placement: basic
  basic_user_template: "{{conn.email}}/token"
  key_label: "Zendesk API token"
  key_hint: "from Admin → Apps and integrations → APIs → Zendesk API"
  setup_url: https://support.zendesk.com/hc/en-us/articles/4408889192858
connect_inputs:
  - {key: subdomain, label: "Zendesk subdomain", hint: "acme (from acme.zendesk.com)", required: true}
  - {key: email, label: "Agent email", hint: "you@acme.com", required: true}
```

> Auth is HTTP Basic with username `{email}/token` and the API token as the password.

- [ ] **Step 7: Create `internal/connectors/connectors/zendesk.yaml`**

```yaml
provider: zendesk
actions:
  - name: zendesk_list_tickets
    description: "List tickets. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/tickets.json"}
    response_extract: "$.tickets"
  - name: zendesk_get_ticket
    description: "Get a ticket by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/tickets/{{id}}.json"}
    response_extract: "$.ticket"
  - name: zendesk_create_ticket
    description: "Create a ticket. ticket is an object (subject, comment{body}, requester…). Mutating."
    mutating: true
    params: {type: object, properties: {ticket: {type: object}}, required: [ticket]}
    request: {method: POST, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/tickets.json", body: {ticket: "{{ticket}}"}}
    response_extract: "$.ticket"
  - name: zendesk_update_ticket
    description: "Update a ticket by id. ticket is an object of fields to change. Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, ticket: {type: object}}, required: [id, ticket]}
    request: {method: PUT, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/tickets/{{id}}.json", body: {ticket: "{{ticket}}"}}
    response_extract: "$.ticket"
  - name: zendesk_add_comment
    description: "Add a comment to a ticket (public by default). Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, body: {type: string}}, required: [id, body]}
    request:
      method: PUT
      url: "https://{{conn.subdomain}}.zendesk.com/api/v2/tickets/{{id}}.json"
      body: {ticket: {comment: {body: "{{body}}"}}}
    response_extract: "$.ticket"
  - name: zendesk_list_users
    description: "List users. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/users.json"}
    response_extract: "$.users"
  - name: zendesk_get_user
    description: "Get a user by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/users/{{id}}.json"}
    response_extract: "$.user"
  - name: zendesk_create_user
    description: "Create a user. user is an object (name, email, role…). Mutating."
    mutating: true
    params: {type: object, properties: {user: {type: object}}, required: [user]}
    request: {method: POST, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/users.json", body: {user: "{{user}}"}}
    response_extract: "$.user"
  - name: zendesk_search
    description: "Search across tickets/users/orgs with a query string. Read-only."
    mutating: false
    params: {type: object, properties: {query: {type: string}}, required: [query]}
    request: {method: GET, url: "https://{{conn.subdomain}}.zendesk.com/api/v2/search.json", query: {query: "{{query}}"}}
    response_extract: "$.results"
```

- [ ] **Step 8: Run** `go test ./internal/connectors/... -count=1 && go build ./...` → PASS (the whole connectors package, since the `applyAuth` signature touched shared tests).
- [ ] **Step 9: Commit** `feat(connectors): templated Basic-username auth + Zendesk provider`.

---

## Task 5: Expose the 4 in the UI

**Files:** Modify `web/handlers_services.go`; modify `b3_test.go`.

- [ ] **Step 1: Write the failing test** — add to `b3_test.go`:

```go
func TestB3_AllProvidersLoad(t *testing.T) {
	r := b3Reg(t)
	for _, name := range []string{"salesforce", "shopify", "mailchimp", "zendesk"} {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Fatalf("%s not loaded", name)
		}
		if len(r.Actions(name)) < 8 {
			t.Fatalf("%s has <8 actions: %d", name, len(r.Actions(name)))
		}
		_ = p
	}
	// salesforce is OAuth; the other three are api_key
	if p, _ := r.ProviderByName("salesforce"); p.IsAPIKey() {
		t.Fatal("salesforce should be OAuth")
	}
	for _, name := range []string{"shopify", "mailchimp", "zendesk"} {
		if p, _ := r.ProviderByName(name); !p.IsAPIKey() {
			t.Fatalf("%s should be api_key", name)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB3_AllProvidersLoad -count=1` → PASS if Tasks 1-4 done.

- [ ] **Step 3: Add the 4 to `availableServiceProviders`** in `web/handlers_services.go` (append to the existing slice, no removals):

```go
	"hubspot", "calendly", "asana", "airtable", "sendgrid", "intercom", "clickup", "monday", "dropbox", "zoom",
	"salesforce", "shopify", "mailchimp", "zendesk",
}
```

- [ ] **Step 4: Build + smoke** — `go build -o bin/simple-agents ./cmd/simple-agents && go test ./internal/connectors/... ./web/... -count=1`. Manual: `make deploy`, open `/dashboard/connectors/services` — Shopify/Mailchimp/Zendesk show the paste-key form (Shopify + Zendesk also show their connect-input fields), Salesforce shows the OAuth form.

- [ ] **Step 5: Commit**

```bash
git add web/handlers_services.go internal/connectors/b3_test.go
git commit -m "feat(web): expose B3 providers (Salesforce, Shopify, Mailchimp, Zendesk)"
```

---

## Self-Review

**Spec coverage:** `connect_inputs` (T1), `token_extra` (T2), `key_extra` (T3), templated-Basic-username + `applyAuth` signature (T4); plus `body_arg` (T2, planning-discovered — Salesforce sObject bodies are a raw fields object with no wrapper key, unrepresentable by a YAML-map `body:`). All 4 providers + auth kinds + ~8-10 actions each; base URLs via `{{conn.*}}`; UI exposure (T5). ✓

**Placeholder scan:** No TBD/TODO. Action selections are complete YAML.

**Type consistency:** `ConnectInput`/`ConnectInputs`, `TokenSet.Extra`/`TokenExtra`/`parseTokenResponse`, `KeyExtra`/`DeriveKeyExtra`, `BasicUserTemplate`, `BodyArg`, and the new `applyAuth(req, prov, credential, connExtra)` signature are used consistently across tasks. `renderRequest`/`subst`/`ConnRef.Extra`/`RunPostConnect` are existing symbols. `availableServiceProviders` extended once (T5). Provider slugs consistent across provider file `name:`, connector file `provider:`, tests, UI list.

**Notes for the executor:**
- **`body_arg` is a planning-discovered 5th primitive** (Salesforce create/update sObject: the whole body is the `fields` object, no wrapper key — a YAML-map `body:` can't express that). It's ~3 lines in `renderRequest` (Task 2 Step 5) and reusable.
- The **`applyAuth` signature change (T4) is cross-cutting**: update `execute.go` (pass `conn.Extra`), `auth_test.go` (4 calls, `+nil`), and `b2_test.go:135` (`+nil`). Run the FULL `go test ./internal/connectors/...` after T4, not a single-test filter.
- Every arbitrary-object body other than Salesforce uses the wrapper-map pattern (`{product: "{{product}}"}`, `{ticket: "{{ticket}}"}`, `{user: "{{user}}"}`) — valid YAML-map bodies; only Salesforce needs `body_arg`.
