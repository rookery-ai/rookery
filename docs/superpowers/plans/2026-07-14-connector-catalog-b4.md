# Connector Catalog B4 Implementation Plan (Stripe, Twilio, Trello — final batch)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Stripe, Twilio, and Trello — completing the connector catalog — via one new form-encoded-body primitive plus reuse of existing/B3 auth primitives.

**Architecture:** A `renderForm` primitive builds `application/x-www-form-urlencoded` bodies from a flat `form:` map (Stripe/Twilio). Auth reuses existing paths: Stripe Basic (secret key as username), Twilio Basic (`account_sid` username via B3 `basic_user_template` + `connect_inputs`), Trello api-key + user token as two query params. Each provider is data.

**Tech Stack:** Go (`net/url`), `gopkg.in/yaml.v3`, Echo v4. Tests: stdlib `testing` + `net/http`.

## Global Constraints

- Package under change: `internal/connectors` + `web/handlers_services.go`. No DB migrations. No new UI code (reuses the B3 api-key + connect_inputs card).
- One new primitive: `RequestTemplate.Form map[string]string` + `renderForm`. Everything else reuses existing primitives.
- Auth: Stripe `placement: basic` (secret key = Basic username, empty password); Twilio `placement: basic` + `basic_user_template: "{{conn.account_sid}}"` (auth token = password) + `connect_inputs: [account_sid]`; Trello `placement: query` (`param_name: token`) + `connect_inputs: [trello_key]` with each action's `query` carrying `key: "{{conn.trello_key}}"`.
- Form values are `{{arg}}` placeholders; an array-valued arg expands to repeated keys; empty values omitted; Content-Type `application/x-www-form-urlencoded`.
- Mutating actions (create/update/send/make_call/refund) set `mutating: true`.
- **Verification: unit/rendering only** — no live API calls.
- Add providers to `availableServiceProviders` only in the final task.
- Build: `go build ./...`. Test: `go test ./internal/connectors/... ./web/... -count=1`.
- Branch: `main`. Sub-project D (AWS/PostgreSQL) is dropped — not in this plan.

---

## File Structure

- `internal/connectors/registry.go` — Modify: add `RequestTemplate.Form map[string]string`.
- `internal/connectors/render.go` — Modify: add `renderForm` + a `Form` case in `renderRequest`.
- Providers/connectors YAML: `stripe`, `twilio`, `trello` (both dirs).
- `web/handlers_services.go` — Modify: `availableServiceProviders`.
- Tests: `internal/connectors/b4_test.go` (new).

---

## Task 1: `Form` primitive (`renderForm`) + Stripe

**Files:** Modify `registry.go`, `render.go`; create `providers/stripe.yaml`, `connectors/stripe.yaml`, `internal/connectors/b4_test.go`.

**Interfaces:**
- Produces: `RequestTemplate.Form map[string]string`; `renderForm(form map[string]string, args map[string]any) ([]byte, string)`; `stripe` provider + actions; `b4Reg`/`renderB4Form` test helpers.

- [ ] **Step 1: Write the failing test** — create `internal/connectors/b4_test.go`:

```go
package connectors

import (
	"net/url"
	"testing"
)

func b4Reg(t *testing.T) *Registry {
	t.Helper()
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// renderB4Form renders an action expected to produce a form body; returns method, url, and the
// parsed form values.
func renderB4Form(t *testing.T, r *Registry, provider, action string, args map[string]any, connExtra map[string]string) (string, url.Values) {
	t.Helper()
	a, ok := r.Action(provider, action)
	if !ok {
		t.Fatalf("%s.%s missing", provider, action)
	}
	_, u, body, ct, err := renderRequest(a, args, connExtra)
	if err != nil {
		t.Fatalf("render %s.%s: %v", provider, action, err)
	}
	if ct != "application/x-www-form-urlencoded" && len(body) > 0 {
		t.Fatalf("%s.%s content-type = %q, want form-urlencoded", provider, action, ct)
	}
	v, _ := url.ParseQuery(string(body))
	return u, v
}

func TestB4_RenderFormBasicsAndBracketAndArray(t *testing.T) {
	// Direct renderForm test: flat key, literal bracket key, array→repeated, empty omitted.
	form := map[string]string{
		"email":            "{{email}}",
		"metadata[source]": "{{source}}",
		"expand":           "{{expand}}",
		"name":             "{{name}}",
	}
	body, ct := renderForm(form, map[string]any{
		"email":  "a@b.com",
		"source": "web",
		"expand": []any{"customer", "charges"},
		// name omitted → empty → dropped
	})
	if ct != "application/x-www-form-urlencoded" {
		t.Fatalf("ct=%s", ct)
	}
	v, _ := url.ParseQuery(string(body))
	if v.Get("email") != "a@b.com" {
		t.Fatalf("email: %v", v)
	}
	if v.Get("metadata[source]") != "web" {
		t.Fatalf("bracket key not preserved: %v", v)
	}
	if got := v["expand"]; len(got) != 2 || got[0] != "customer" {
		t.Fatalf("array not expanded to repeated keys: %v", got)
	}
	if _, present := v["name"]; present {
		t.Fatalf("empty value should be omitted: %v", v)
	}
}

func TestB4_StripeCreateCustomer(t *testing.T) {
	r := b4Reg(t)
	p, ok := r.ProviderByName("stripe")
	if !ok || !p.IsAPIKey() || p.Auth.Placement != "basic" {
		t.Fatalf("stripe must be api_key basic, got %+v", p.Auth)
	}
	if len(r.Actions("stripe")) < 8 {
		t.Fatalf("want >=8 stripe actions, got %d", len(r.Actions("stripe")))
	}
	_, v := renderB4Form(t, r, "stripe", "stripe_create_customer", map[string]any{"email": "x@y.com", "name": "Acme"}, nil)
	if v.Get("email") != "x@y.com" || v.Get("name") != "Acme" {
		t.Fatalf("stripe create_customer form: %v", v)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run 'TestB4_RenderForm|TestB4_Stripe' -count=1` → FAIL (`renderForm` undefined, stripe missing).

- [ ] **Step 3: Add `RequestTemplate.Form` in `registry.go`**

Add to `RequestTemplate`: `Form map[string]string \`yaml:"form"\``.

- [ ] **Step 4: Add `renderForm` + the switch case in `render.go`**

Add the function (uses the existing `lonePlaceholderRE`, `subst`, `asString`):

```go
// renderForm builds an application/x-www-form-urlencoded body from a flat form map. Each value is
// a {{arg}} template; empty results are omitted. A lone-placeholder whose arg is an array expands
// to repeated key=value pairs (form array convention). Keys are used literally (Stripe/Twilio
// bracket notation like "metadata[source]" is preserved).
func renderForm(form map[string]string, args map[string]any) ([]byte, string) {
	v := url.Values{}
	for k, tmpl := range form {
		if m := lonePlaceholderRE.FindStringSubmatch(tmpl); m != nil {
			if arr, ok := args[m[1]].([]any); ok {
				for _, e := range arr {
					v.Add(k, asString(e))
				}
				continue
			}
		}
		val := subst(tmpl, args, nil)
		if val == "" {
			continue
		}
		v.Set(k, val)
	}
	return []byte(v.Encode()), "application/x-www-form-urlencoded"
}
```

Ensure `net/url` is imported in `render.go` (it already is — `subst`/query use `url`). Add the case in `renderRequest`'s body switch, right after the `BodyArg` case:

```go
	case len(a.Request.Form) > 0:
		body, contentType = renderForm(a.Request.Form, args)
```

- [ ] **Step 5: Create `internal/connectors/providers/stripe.yaml`**

```yaml
name: stripe
label: Stripe
auth:
  kind: api_key
  placement: basic
  key_label: "Stripe secret key"
  key_hint: "sk_live_... or sk_test_..."
  setup_url: https://dashboard.stripe.com/apikeys
```

> Stripe Basic auth uses the secret key as the username (empty password) — the existing `placement: basic` path (`SetBasicAuth(credential, "")`) does exactly this.

- [ ] **Step 6: Create `internal/connectors/connectors/stripe.yaml`**

```yaml
provider: stripe
actions:
  - name: stripe_create_customer
    description: "Create a Stripe customer. Mutating."
    mutating: true
    params:
      type: object
      properties:
        email: {type: string}
        name: {type: string}
        description: {type: string}
      required: []
    request:
      method: POST
      url: "https://api.stripe.com/v1/customers"
      form: {email: "{{email}}", name: "{{name}}", description: "{{description}}"}
    response_extract: "$"
  - name: stripe_list_customers
    description: "List Stripe customers. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}, email: {type: string}}}
    request: {method: GET, url: "https://api.stripe.com/v1/customers", query: {limit: "{{limit}}", email: "{{email}}"}}
    response_extract: "$.data"
  - name: stripe_get_customer
    description: "Get a Stripe customer by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.stripe.com/v1/customers/{{id}}"}
    response_extract: "$"
  - name: stripe_create_payment_intent
    description: "Create a PaymentIntent (amount in the smallest currency unit, e.g. cents). Mutating."
    mutating: true
    params:
      type: object
      properties:
        amount: {type: integer}
        currency: {type: string}
        customer: {type: string}
        description: {type: string}
      required: [amount, currency]
    request:
      method: POST
      url: "https://api.stripe.com/v1/payment_intents"
      form: {amount: "{{amount}}", currency: "{{currency}}", customer: "{{customer}}", description: "{{description}}"}
    response_extract: "$"
  - name: stripe_list_payment_intents
    description: "List PaymentIntents. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}}}
    request: {method: GET, url: "https://api.stripe.com/v1/payment_intents", query: {limit: "{{limit}}"}}
    response_extract: "$.data"
  - name: stripe_create_refund
    description: "Refund a charge or payment_intent (full or partial). Mutating."
    mutating: true
    params:
      type: object
      properties:
        charge: {type: string}
        payment_intent: {type: string}
        amount: {type: integer}
      required: []
    request:
      method: POST
      url: "https://api.stripe.com/v1/refunds"
      form: {charge: "{{charge}}", payment_intent: "{{payment_intent}}", amount: "{{amount}}"}
    response_extract: "$"
  - name: stripe_list_charges
    description: "List charges. Read-only."
    mutating: false
    params: {type: object, properties: {limit: {type: integer}, customer: {type: string}}}
    request: {method: GET, url: "https://api.stripe.com/v1/charges", query: {limit: "{{limit}}", customer: "{{customer}}"}}
    response_extract: "$.data"
  - name: stripe_get_charge
    description: "Get a charge by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.stripe.com/v1/charges/{{id}}"}
    response_extract: "$"
  - name: stripe_get_balance
    description: "Get the account balance. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.stripe.com/v1/balance"}
    response_extract: "$"
```

- [ ] **Step 7: Run** `go test ./internal/connectors/ -run 'TestB4_RenderForm|TestB4_Stripe|TestLoad' -count=1 && go build ./...` → PASS + clean.
- [ ] **Step 8: Commit** `feat(connectors): form-encoded body primitive + Stripe provider (9 actions)`.

---

## Task 2: Twilio

**Files:** Create `providers/twilio.yaml`, `connectors/twilio.yaml`; modify `b4_test.go`.

**Interfaces:** Consumes `renderForm` (Task 1), `applyAuth` templated Basic username + `connect_inputs` (B3).

- [ ] **Step 1: Write the failing test** — add to `b4_test.go` (needs `net/http`; add to imports):

```go
func TestB4_TwilioBasicUserAndSMS(t *testing.T) {
	r := b4Reg(t)
	p, ok := r.ProviderByName("twilio")
	if !ok || !p.IsAPIKey() || p.Auth.Placement != "basic" {
		t.Fatalf("twilio must be api_key basic, got %+v", p.Auth)
	}
	// Basic username = account_sid from connExtra; credential = auth token (password)
	req, _ := http.NewRequest("POST", "https://api.twilio.com/x", nil)
	applyAuth(req, p, "AUTHTOKEN", map[string]string{"account_sid": "AC123"})
	u, pw, ok := req.BasicAuth()
	if !ok || u != "AC123" || pw != "AUTHTOKEN" {
		t.Fatalf("twilio basic auth: u=%q pw=%q", u, pw)
	}
	// send_sms form body + templated base URL
	url, v := renderB4Form(t, r, "twilio", "twilio_send_sms",
		map[string]any{"To": "+1555", "From": "+1444", "Body": "hi"}, map[string]string{"account_sid": "AC123"})
	if v.Get("Body") != "hi" || v.Get("To") != "+1555" {
		t.Fatalf("twilio send_sms form: %v", v)
	}
	if url != "https://api.twilio.com/2010-04-01/Accounts/AC123/Messages.json" {
		t.Fatalf("twilio url not templated: %s", url)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB4_Twilio -count=1` → FAIL.

- [ ] **Step 3: Create `internal/connectors/providers/twilio.yaml`**

```yaml
name: twilio
label: Twilio
auth:
  kind: api_key
  placement: basic
  basic_user_template: "{{conn.account_sid}}"
  key_label: "Twilio Auth Token"
  key_hint: "your account's Auth Token (Console → Account Info)"
  setup_url: https://console.twilio.com/
connect_inputs:
  - {key: account_sid, label: "Account SID", hint: "AC...", required: true}
```

> Basic auth: username = Account SID (from connect_inputs, via basic_user_template), password = the pasted Auth Token. The SID is also templated into the base URL.

- [ ] **Step 4: Create `internal/connectors/connectors/twilio.yaml`**

```yaml
provider: twilio
actions:
  - name: twilio_send_sms
    description: "Send an SMS. From must be a Twilio number. Delivers a real message. Mutating."
    mutating: true
    params:
      type: object
      properties:
        To: {type: string}
        From: {type: string}
        Body: {type: string}
      required: [To, From, Body]
    request:
      method: POST
      url: "https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}/Messages.json"
      form: {To: "{{To}}", From: "{{From}}", Body: "{{Body}}"}
    response_extract: "$"
  - name: twilio_list_messages
    description: "List recent SMS/MMS messages. Read-only."
    mutating: false
    params: {type: object, properties: {PageSize: {type: integer}}}
    request: {method: GET, url: "https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}/Messages.json", query: {PageSize: "{{PageSize}}"}}
    response_extract: "$.messages"
  - name: twilio_get_message
    description: "Get a message by SID. Read-only."
    mutating: false
    params: {type: object, properties: {sid: {type: string}}, required: [sid]}
    request: {method: GET, url: "https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}/Messages/{{sid}}.json"}
    response_extract: "$"
  - name: twilio_make_call
    description: "Place an outbound call that fetches TwiML from Url. Mutating."
    mutating: true
    params:
      type: object
      properties:
        To: {type: string}
        From: {type: string}
        Url: {type: string, description: "TwiML URL"}
      required: [To, From, Url]
    request:
      method: POST
      url: "https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}/Calls.json"
      form: {To: "{{To}}", From: "{{From}}", Url: "{{Url}}"}
    response_extract: "$"
  - name: twilio_list_calls
    description: "List recent calls. Read-only."
    mutating: false
    params: {type: object, properties: {PageSize: {type: integer}}}
    request: {method: GET, url: "https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}/Calls.json", query: {PageSize: "{{PageSize}}"}}
    response_extract: "$.calls"
  - name: twilio_list_incoming_phone_numbers
    description: "List the account's Twilio phone numbers. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}/IncomingPhoneNumbers.json"}
    response_extract: "$.incoming_phone_numbers"
  - name: twilio_get_account
    description: "Get the Twilio account details. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.twilio.com/2010-04-01/Accounts/{{conn.account_sid}}.json"}
    response_extract: "$"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB4_Twilio -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Twilio provider (basic SID/token + form bodies, 7 actions)`.

---

## Task 3: Trello

**Files:** Create `providers/trello.yaml`, `connectors/trello.yaml`; modify `b4_test.go`.

**Interfaces:** Consumes `applyAuth` query placement + `connect_inputs` (B3); no new mechanism.

- [ ] **Step 1: Write the failing test** — add to `b4_test.go`:

```go
func TestB4_TrelloKeyAndTokenInQuery(t *testing.T) {
	r := b4Reg(t)
	p, ok := r.ProviderByName("trello")
	if !ok || !p.IsAPIKey() || p.Auth.Placement != "query" || p.Auth.ParamName != "token" {
		t.Fatalf("trello must be api_key query token, got %+v", p.Auth)
	}
	if len(r.Actions("trello")) < 8 {
		t.Fatalf("want >=8 trello actions, got %d", len(r.Actions("trello")))
	}
	// key comes from {{conn.trello_key}} in the action query (rendered by renderRequest)
	a, _ := r.Action("trello", "trello_list_boards")
	_, u, _, _, err := renderRequest(a, nil, map[string]string{"trello_key": "KEY123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "key=KEY123") {
		t.Fatalf("trello key not in query: %s", u)
	}
	// token is added by applyAuth (query placement)
	req, _ := http.NewRequest("GET", u, nil)
	applyAuth(req, p, "TOKEN456", nil)
	if req.URL.Query().Get("token") != "TOKEN456" {
		t.Fatalf("trello token not added by applyAuth: %s", req.URL.String())
	}
}
```

Replace the `contains(u, "key=KEY123")` call above with `strings.Contains(u, "key=KEY123")` and add `"strings"` to `b4_test.go`'s import block (it isn't imported yet).

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB4_Trello -count=1` → FAIL.

- [ ] **Step 3: Create `internal/connectors/providers/trello.yaml`**

```yaml
name: trello
label: Trello
auth:
  kind: api_key
  placement: query
  param_name: token
  key_label: "Trello API token"
  key_hint: "the user token (generated alongside your API key)"
  setup_url: https://trello.com/power-ups/admin
connect_inputs:
  - {key: trello_key, label: "Trello API key", hint: "from trello.com/power-ups/admin", required: true}
```

> Trello auth = two query params: `token` (the pasted credential, added by applyAuth) + `key` (from `connect_inputs[trello_key]`, carried in each action's `query` as `{{conn.trello_key}}`). No OAuth 1.0a.

- [ ] **Step 4: Create `internal/connectors/connectors/trello.yaml`**

```yaml
provider: trello
actions:
  - name: trello_list_boards
    description: "List the authenticated member's boards. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request: {method: GET, url: "https://api.trello.com/1/members/me/boards", query: {key: "{{conn.trello_key}}"}}
    response_extract: "$"
  - name: trello_get_board
    description: "Get a board by id. Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.trello.com/1/boards/{{id}}", query: {key: "{{conn.trello_key}}"}}
    response_extract: "$"
  - name: trello_list_lists
    description: "List the lists on a board. Read-only."
    mutating: false
    params: {type: object, properties: {board_id: {type: string}}, required: [board_id]}
    request: {method: GET, url: "https://api.trello.com/1/boards/{{board_id}}/lists", query: {key: "{{conn.trello_key}}"}}
    response_extract: "$"
  - name: trello_list_cards
    description: "List the cards in a list. Read-only."
    mutating: false
    params: {type: object, properties: {list_id: {type: string}}, required: [list_id]}
    request: {method: GET, url: "https://api.trello.com/1/lists/{{list_id}}/cards", query: {key: "{{conn.trello_key}}"}}
    response_extract: "$"
  - name: trello_create_card
    description: "Create a card in a list. Mutating."
    mutating: true
    params: {type: object, properties: {list_id: {type: string}, name: {type: string}, desc: {type: string}}, required: [list_id, name]}
    request: {method: POST, url: "https://api.trello.com/1/cards", query: {key: "{{conn.trello_key}}", idList: "{{list_id}}", name: "{{name}}", desc: "{{desc}}"}}
    response_extract: "$"
  - name: trello_update_card
    description: "Update a card by id (name/desc/list). Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, name: {type: string}, desc: {type: string}, list_id: {type: string}}, required: [id]}
    request: {method: PUT, url: "https://api.trello.com/1/cards/{{id}}", query: {key: "{{conn.trello_key}}", name: "{{name}}", desc: "{{desc}}", idList: "{{list_id}}"}}
    response_extract: "$"
  - name: trello_create_list
    description: "Create a list on a board. Mutating."
    mutating: true
    params: {type: object, properties: {board_id: {type: string}, name: {type: string}}, required: [board_id, name]}
    request: {method: POST, url: "https://api.trello.com/1/lists", query: {key: "{{conn.trello_key}}", idBoard: "{{board_id}}", name: "{{name}}"}}
    response_extract: "$"
  - name: trello_add_comment
    description: "Add a comment to a card. Mutating."
    mutating: true
    params: {type: object, properties: {id: {type: string}, text: {type: string}}, required: [id, text]}
    request: {method: POST, url: "https://api.trello.com/1/cards/{{id}}/actions/comments", query: {key: "{{conn.trello_key}}", text: "{{text}}"}}
    response_extract: "$"
  - name: trello_get_member
    description: "Get a member by id or username ('me' for self). Read-only."
    mutating: false
    params: {type: object, properties: {id: {type: string}}, required: [id]}
    request: {method: GET, url: "https://api.trello.com/1/members/{{id}}", query: {key: "{{conn.trello_key}}"}}
    response_extract: "$"
```

- [ ] **Step 5: Run** `go test ./internal/connectors/ -run TestB4_Trello -count=1 && go build ./...` → PASS.
- [ ] **Step 6: Commit** `feat(connectors): Trello provider (key+token query params, 9 actions)`.

---

## Task 4: Expose the 3 in the UI (completes the catalog)

**Files:** Modify `web/handlers_services.go`; modify `b4_test.go`.

- [ ] **Step 1: Write the failing test** — add to `b4_test.go`:

```go
func TestB4_AllProvidersLoad(t *testing.T) {
	r := b4Reg(t)
	for _, name := range []string{"stripe", "twilio", "trello"} {
		p, ok := r.ProviderByName(name)
		if !ok {
			t.Fatalf("%s not loaded", name)
		}
		if !p.IsAPIKey() {
			t.Fatalf("%s should be api_key", name)
		}
		if len(r.Actions(name)) < 7 {
			t.Fatalf("%s has <7 actions: %d", name, len(r.Actions(name)))
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/connectors/ -run TestB4_AllProvidersLoad -count=1` → PASS if Tasks 1-3 done.

- [ ] **Step 3: Add the 3 to `availableServiceProviders`** in `web/handlers_services.go` (append to the existing slice — which ends with the B3 providers `..., "shopify", "salesforce", "mailchimp", "zendesk"` — no removals):

```go
	"salesforce", "mailchimp", "zendesk", "stripe", "twilio", "trello",
}
```

(Only append `"stripe", "twilio", "trello"` after the current last entries; do not duplicate any existing slug.)

- [ ] **Step 4: Build + smoke** — `go build -o bin/simple-agents ./cmd/simple-agents && go test ./internal/connectors/... ./web/... -count=1`. Manual: `make deploy`, open `/dashboard/connectors/services` — Stripe shows a paste-key form; Twilio shows paste-key + an Account SID field; Trello shows paste-key + a Trello API key field.

- [ ] **Step 5: Commit**

```bash
git add web/handlers_services.go internal/connectors/b4_test.go
git commit -m "feat(web): expose B4 providers (Stripe, Twilio, Trello) — catalog complete"
```

---

## Self-Review

**Spec coverage:** form primitive (`Form` + `renderForm`, T1); Stripe (T1, basic key-as-username + form); Twilio (T2, basic templated SID username + auth-token password + connect_inputs + form + templated base URL); Trello (T3, query key+token, no new mechanism); UI exposure (T4). ~8-10 actions each; unit-only; D dropped. ✓

**Placeholder scan:** No TBD/TODO. Action YAML is complete. The Task 3 test's `strings.Contains` note is a concrete "call it directly" instruction.

**Type consistency:** `RequestTemplate.Form map[string]string` + `renderForm(form, args) ([]byte, string)` used consistently; `b4Reg`/`renderB4Form` defined in T1, reused in T2-T4; `applyAuth(req, prov, credential, connExtra)` (the B3 4-arg signature) used in T2/T3 tests with the extra map / nil. Provider slugs consistent across provider file `name:`, connector file `provider:`, tests, UI list. `availableServiceProviders` extended once (T4).

**Notes for the executor:**
- `renderForm` returns `([]byte, string)` (no error); in `renderRequest` use `body, contentType = renderForm(...)` (leave `err` as-is).
- The form `case` is mutually exclusive per action with body/body_arg/body_builder; placement in the switch is fine after `BodyArg`.
- Twilio's `account_sid` is used in BOTH the Basic username (`basic_user_template`) and the base URL (`{{conn.account_sid}}`) — both fed by the single `connect_inputs[account_sid]` value in `extra`; the pasted credential is the Auth Token.
- Trello render test asserts `key` (from `{{conn.trello_key}}`, added by `renderRequest`) is in the URL; the `token` is added by `applyAuth` at Execute time, so assert it via a separate `applyAuth` call (as the test does) — not via `renderRequest`.
