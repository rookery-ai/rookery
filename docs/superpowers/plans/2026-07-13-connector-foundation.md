# Connector Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add API-key auth and a generic nested/array request-body engine to `internal/connectors`, proven end-to-end against Slack (OAuth), OpenAI (API-key), and an extended Gmail.

**Architecture:** A declarative `auth:` block on each provider drives both the connect UI and request-time auth injection (`applyAuth`), replacing the single hardcoded Bearer line. A tree-walking `renderBody` turns nested/array YAML `body:` templates into JSON safely (values placed as Go values, then `json.Marshal`ed), with the existing `body_builder` kept as an escape hatch for non-JSON encodings. API-key connections reuse `service_connections` with zero new columns.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, `modernc.org/sqlite`, Echo v4. Tests use stdlib `testing` + `net/http/httptest`.

## Global Constraints

- Package under change: `internal/connectors` (+ `web/handlers_services.go`, `web/server.go`, one template, `cmd/livecheck/main.go`). No new packages.
- **Zero new DB columns.** API-key connections store the key in `service_connections.encrypted_access_token`, `encrypted_refresh_token=''`, `expires_at=''`, `status='ACTIVE'`.
- All credentials encrypted with `secrets.EncryptWithSystemKey` / `DecryptWithSystemKey`.
- OAuth providers with no `auth:` block (or `kind: oauth2`) must behave EXACTLY as today (`Authorization: Bearer <token>`).
- Mutating actions stay blocked at build time via the existing `buildPhase` guard in `Execute` — do not weaken it.
- Never author an action that sends outbound messages on the user's behalf in an auto-run livecheck path; mutating actions are `[skip]`-listed.
- Build: `go build -o bin/simple-agents ./cmd/simple-agents`. Tests: `go test ./internal/connectors/... -count=1`.
- Branch: `feat/connector-foundation` (already created).

---

## File Structure

- `internal/connectors/registry.go` — Modify: add `AuthConfig` type + `Provider.Auth` field + `Provider.IsAPIKey()`; add `RequestTemplate.Body map[string]any`.
- `internal/connectors/providers/openai.yaml` — Create: API-key provider config.
- `internal/connectors/providers/slack.yaml` — Create: OAuth provider config (non-expiring token).
- `internal/connectors/connectors/openai.yaml` — Create: ~10 OpenAI actions.
- `internal/connectors/connectors/slack.yaml` — Create: ~10 Slack actions.
- `internal/connectors/connectors/google.yaml` — Modify: add 6 Gmail actions.
- `internal/connectors/execute.go` — Modify: replace hardcoded Bearer with `applyAuth`.
- `internal/connectors/auth.go` — Create: `applyAuth`.
- `internal/connectors/render.go` — Modify: add `renderBody` + `body:` branch; add `gmail_reply` builder.
- `internal/connectors/schema.go` — Modify: `array` param support.
- `internal/connectors/dbstore.go` — Modify: `api_key` branch in `AccessToken`.
- `web/handlers_services.go` — Modify: auth-kind-aware page view + `handleConnectAPIKey`; add slack/openai to `availableServiceProviders`.
- `web/server.go` — Modify: register the API-key connect route.
- `web/templates/dashboard/services.html` — Modify: API-key connect form branch.
- `cmd/livecheck/main.go` — Modify: `checkSlack`, `checkOpenAI`.

---

## Task 1: Auth config types + OpenAI provider file

**Files:**
- Modify: `internal/connectors/registry.go`
- Create: `internal/connectors/providers/openai.yaml`
- Test: `internal/connectors/registry_test.go`

**Interfaces:**
- Produces: `AuthConfig` struct; `Provider.Auth AuthConfig`; `Provider.IsAPIKey() bool`; `RequestTemplate.Body map[string]any`.

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/registry_test.go`:

```go
func TestLoadParsesAPIKeyAuth(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	p, ok := r.ProviderByName("openai")
	if !ok {
		t.Fatal("openai provider not loaded")
	}
	if !p.IsAPIKey() {
		t.Fatalf("openai should be api_key, got kind=%q", p.Auth.Kind)
	}
	if p.Auth.Placement != "header" || p.Auth.HeaderName != "Authorization" || p.Auth.ValuePrefix != "Bearer " {
		t.Fatalf("bad auth block: %+v", p.Auth)
	}
}

func TestOAuthProviderDefaultsToOAuth(t *testing.T) {
	r, _ := LoadBundled()
	p, _ := r.ProviderByName("google")
	if p.IsAPIKey() {
		t.Fatal("google must not be api_key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestLoadParsesAPIKeyAuth -count=1`
Expected: FAIL — `openai provider not loaded` (and `IsAPIKey` undefined compile error).

- [ ] **Step 3: Add the types in `registry.go`**

Add after the `Provider` struct's fields (before `func (p Provider) NonExpiring()`):

```go
// AuthConfig declares how a provider authenticates. Absent or kind=="oauth2" → the
// legacy OAuth Bearer path. kind=="api_key" → a static user-supplied key injected per
// placement; drives the connect UI (no OAuth app, a paste-key form) too.
type AuthConfig struct {
	Kind        string `yaml:"kind"`         // "oauth2" (default) | "api_key"
	Placement   string `yaml:"placement"`    // "header" | "query" | "basic"
	HeaderName  string `yaml:"header_name"`  // for placement=header
	ValuePrefix string `yaml:"value_prefix"` // e.g. "Bearer "
	ParamName   string `yaml:"param_name"`   // for placement=query
	KeyLabel    string `yaml:"key_label"`    // UI: "OpenAI API key"
	KeyHint     string `yaml:"key_hint"`     // UI placeholder: "sk-..."
	SetupURL    string `yaml:"setup_url"`    // UI: where to get the key
}
```

Add `Auth AuthConfig \`yaml:"auth"\`` as a field on the `Provider` struct (next to `PostConnect`). Add the method:

```go
// IsAPIKey reports whether this provider authenticates with a static API key.
func (p Provider) IsAPIKey() bool { return p.Auth.Kind == "api_key" }
```

Add `Body` to `RequestTemplate` (used by Task 4; declared here so the type is stable):

```go
	// Body is a nested template (maps/arrays) rendered to JSON by renderBody. Preferred over
	// BodyJSON for anything non-flat. BodyBuilder still wins when set (non-JSON encodings).
	Body map[string]any `yaml:"body"`
```

- [ ] **Step 4: Create `internal/connectors/providers/openai.yaml`**

```yaml
name: openai
label: OpenAI
auth:
  kind: api_key
  placement: header
  header_name: Authorization
  value_prefix: "Bearer "
  key_label: "OpenAI API key"
  key_hint: "sk-..."
  setup_url: https://platform.openai.com/api-keys
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestLoadParsesAPIKeyAuth|TestOAuthProviderDefaultsToOAuth' -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/providers/openai.yaml internal/connectors/registry_test.go
git commit -m "feat(connectors): AuthConfig type + OpenAI api_key provider"
```

---

## Task 2: `applyAuth` injection in Execute

**Files:**
- Create: `internal/connectors/auth.go`
- Modify: `internal/connectors/execute.go:94`
- Test: `internal/connectors/auth_test.go`

**Interfaces:**
- Consumes: `Provider.Auth` (Task 1).
- Produces: `func applyAuth(req *http.Request, prov Provider, credential string)`.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/auth_test.go`:

```go
package connectors

import (
	"net/http"
	"testing"
)

func newReq(t *testing.T, u string) *http.Request {
	r, err := http.NewRequest("GET", u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyAuthOAuthBearerDefault(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{}, "TOK") // no auth block → oauth2 Bearer
	if got := req.Header.Get("Authorization"); got != "Bearer TOK" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyAuthHeaderPrefix(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "header", HeaderName: "Authorization", ValuePrefix: "Bearer "}}, "sk-1")
	if got := req.Header.Get("Authorization"); got != "Bearer sk-1" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyAuthQueryParam(t *testing.T) {
	req := newReq(t, "https://api/x?a=1")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "query", ParamName: "api_key"}}, "K")
	if got := req.URL.Query().Get("api_key"); got != "K" {
		t.Fatalf("query api_key=%q, url=%s", got, req.URL.String())
	}
}

func TestApplyAuthBasic(t *testing.T) {
	req := newReq(t, "https://api/x")
	applyAuth(req, Provider{Auth: AuthConfig{Kind: "api_key", Placement: "basic"}}, "sk_live")
	u, p, ok := req.BasicAuth()
	if !ok || u != "sk_live" || p != "" {
		t.Fatalf("basic auth wrong: u=%q p=%q ok=%v", u, p, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestApplyAuth -count=1`
Expected: FAIL — `applyAuth` undefined.

- [ ] **Step 3: Create `internal/connectors/auth.go`**

```go
package connectors

import "net/http"

// applyAuth injects the connection credential into req per the provider's auth block.
// Default (no block / kind=="oauth2") is the legacy Authorization: Bearer <token>.
func applyAuth(req *http.Request, prov Provider, credential string) {
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
		req.SetBasicAuth(credential, "")
	default: // "header"
		name := a.HeaderName
		if name == "" {
			name = "Authorization"
		}
		req.Header.Set(name, a.ValuePrefix+credential)
	}
}
```

- [ ] **Step 4: Wire it into `Execute`**

In `internal/connectors/execute.go`, replace the line at `execute.go:94`:

```go
		req.Header.Set("Authorization", "Bearer "+token)
```

with:

```go
		applyAuth(req, prov, token)
```

(`prov` is already fetched above via `reg.ProviderByName(conn.Provider)`.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestApplyAuth|TestExecute' -count=1`
Expected: PASS (existing `TestExecuteReadRewritesURLAndBearer` still passes — default path unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/auth.go internal/connectors/auth_test.go internal/connectors/execute.go
git commit -m "feat(connectors): applyAuth injects api_key (header/query/basic) or oauth Bearer"
```

---

## Task 3: DBTokenStore api_key branch

**Files:**
- Modify: `internal/connectors/dbstore.go`
- Test: `internal/connectors/dbstore_test.go`

**Interfaces:**
- Consumes: `Provider.IsAPIKey()` (Task 1); the `openai` provider (Task 1).
- Produces: `AccessToken` returns the stored key directly (no refresh) for api_key providers.

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/dbstore_test.go` (follow the existing setup in that file for `openDB`/workspace/system key — reuse its helpers):

```go
func TestAccessTokenAPIKeyReturnsStoredKeyNoRefresh(t *testing.T) {
	d, key := newTestDBAndKey(t) // existing helper in dbstore_test.go
	ws := seedWorkspace(t, d)    // existing helper
	reg, _ := LoadBundled()

	enc, _ := secrets.EncryptWithSystemKey("sk-secret", key)
	// api_key connection: empty refresh + empty expiry (would normally be treated as expired).
	d.InsertServiceConnection(context.Background(), db.ServiceConnection{
		ID: "k1", WorkspaceID: ws, Provider: "openai", AccountLabel: "default",
		EncryptedAccessToken: enc, ExpiresAt: "", Status: "ACTIVE",
	})

	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{}}
	got, err := store.AccessToken(context.Background(), ConnRef{ID: "k1", Provider: "openai"})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "sk-secret" {
		t.Fatalf("got %q, want sk-secret", got)
	}
}
```

> If `newTestDBAndKey`/`seedWorkspace` aren't the exact helper names in `dbstore_test.go`, use whatever that file already defines to get a `*db.DB`, a `[]byte` system key, and a workspace id. Add imports `context`, `github.com/ilijad1/simple-agents/internal/db`, `github.com/ilijad1/simple-agents/internal/secrets` if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestAccessTokenAPIKey -count=1`
Expected: FAIL — falls into the refresh path (empty `expires_at` → `expired()` true → tries to refresh, errors on missing OAuth config).

- [ ] **Step 3: Add the api_key branch in `dbstore.go`**

In `AccessToken`, immediately after the `row.Status != "ACTIVE"` check and BEFORE the `prov.NonExpiring()` logic, insert:

```go
	// API-key connections hold a static credential in encrypted_access_token — never refresh.
	prov, _ := s.Reg.ProviderByName(row.Provider)
	if prov.IsAPIKey() {
		tok, err := secrets.DecryptWithSystemKey(row.EncryptedAccessToken, s.SystemKey)
		if err != nil {
			return "", &ConnectorError{KindOther, "decrypt api key: " + err.Error()}
		}
		return tok, nil
	}
```

Then delete the now-duplicate `prov, _ := s.Reg.ProviderByName(row.Provider)` line that follows (the one just above `if prov.NonExpiring() || !s.expired(...)`), since `prov` is now already in scope.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestAccessToken|TestRefresh' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/dbstore.go internal/connectors/dbstore_test.go
git commit -m "feat(connectors): DBTokenStore returns static key for api_key providers (no refresh)"
```

---

## Task 4: Nested/array body renderer

**Files:**
- Modify: `internal/connectors/render.go`
- Test: `internal/connectors/render_test.go`

**Interfaces:**
- Consumes: `RequestTemplate.Body` (Task 1).
- Produces: `renderBody(node any, args map[string]any, connVars map[string]string) (any, bool)`; a `body:` branch in `renderRequest`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/connectors/render_test.go`:

```go
func TestRenderBodyArrayPassthroughAndOmit(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/modify", Body: map[string]any{
		"addLabelIds":    "{{add}}",
		"removeLabelIds": "{{remove}}",
	}}}
	_, _, body, ct, err := renderRequest(a, map[string]any{"add": []any{"L1", "L2"}}, nil) // remove omitted
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("ct=%s", ct)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	arr, ok := got["addLabelIds"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "L1" {
		t.Fatalf("addLabelIds not passed through as array: %s", body)
	}
	if _, present := got["removeLabelIds"]; present {
		t.Fatalf("absent optional key must be omitted: %s", body)
	}
}

func TestRenderBodyNestedAndEmbedded(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/chat", Body: map[string]any{
		"model": "{{model}}",
		"messages": []any{
			map[string]any{"role": "user", "content": "{{prompt}}"},
		},
	}}}
	_, _, body, _, _ := renderRequest(a, map[string]any{"model": "gpt-4o", "prompt": "hi \"there\""}, nil)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body must be valid json even with quotes in arg: %v (%s)", err, body)
	}
	msgs := got["messages"].([]any)
	m0 := msgs[0].(map[string]any)
	if m0["content"] != `hi "there"` {
		t.Fatalf("nested content wrong: %v", m0["content"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/connectors/ -run TestRenderBody -count=1`
Expected: FAIL — `renderRequest` has no `Body` branch, so `body` is empty.

- [ ] **Step 3: Add `renderBody` + regexp in `render.go`**

Add near the top (after `placeholderRE`):

```go
// lonePlaceholderRE matches a string that is EXACTLY one {{name}} placeholder, so its
// substituted value keeps the arg's real type (array/int/bool) instead of stringifying.
var lonePlaceholderRE = regexp.MustCompile(`^\{\{([\w.]+)\}\}$`)

// renderBody walks a nested body template (maps/arrays/scalars). A leaf that is exactly
// one {{arg}} adopts the arg's real value/type; if that arg is absent/nil the key is
// OMITTED (present=false). A placeholder embedded in a larger string renders to string.
// Returned values are real Go values (marshaled by the caller) so user data can never
// break the JSON.
func renderBody(node any, args map[string]any, connVars map[string]string) (any, bool) {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			if rv, ok := renderBody(v, args, connVars); ok {
				out[k] = rv
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(n))
		for _, e := range n {
			if rv, ok := renderBody(e, args, connVars); ok {
				out = append(out, rv)
			}
		}
		return out, true
	case string:
		if m := lonePlaceholderRE.FindStringSubmatch(n); m != nil {
			name := m[1]
			if strings.HasPrefix(name, "conn.") {
				return connVars[strings.TrimPrefix(name, "conn.")], true
			}
			v, present := args[name]
			if !present || v == nil {
				return nil, false
			}
			return v, true
		}
		return subst(n, args, connVars), true
	default:
		return n, true // scalar literal (int/bool/float from YAML)
	}
}
```

- [ ] **Step 4: Add the `body:` branch in `renderRequest`**

In the `switch` inside `renderRequest`, add a case BEFORE the `BodyJSON` case (and after `BodyBuilder`, so a builder always wins):

```go
	case len(a.Request.Body) > 0:
		rendered, _ := renderBody(a.Request.Body, args, connVars)
		body, err = json.Marshal(rendered)
		contentType = "application/json"
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestRenderBody|TestRenderGmail|TestRenderQuery' -count=1`
Expected: PASS (legacy render tests still pass).

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/render.go internal/connectors/render_test.go
git commit -m "feat(connectors): renderBody nested/array template engine (safe json marshal)"
```

---

## Task 5: Array param schema validation

**Files:**
- Modify: `internal/connectors/schema.go`
- Test: `internal/connectors/schema_test.go`

**Interfaces:**
- Produces: `validateArgs` accepts `type: array` params and validates `items.type` per element.

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/schema_test.go`:

```go
func TestValidateArgsArray(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string"}}},"required":["ids"]}`)
	if err := validateArgs(schema, map[string]any{"ids": []any{"a", "b"}}); err != nil {
		t.Fatalf("valid string array rejected: %v", err)
	}
	if err := validateArgs(schema, map[string]any{"ids": "notarray"}); err == nil {
		t.Fatal("string given for array param must fail")
	}
	if err := validateArgs(schema, map[string]any{"ids": []any{"a", 3}}); err == nil {
		t.Fatal("wrong element type must fail")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestValidateArgsArray -count=1`
Expected: FAIL — `array` falls through `typeOK` default (returns true), so bad input is accepted.

- [ ] **Step 3: Extend `schema.go`**

Add an `Items` field to `propSchema`:

```go
type propSchema struct {
	Type  string      `json:"type"`
	Items *propSchema `json:"items"`
}
```

In `validateArgs`, replace the property-type loop body so arrays validate elements. Change:

```go
	for name, val := range args {
		p, ok := s.Properties[name]
		if !ok || val == nil {
			continue
		}
		if !typeOK(p.Type, val) {
			return fmt.Errorf("argument %q must be %s", name, p.Type)
		}
	}
```

to:

```go
	for name, val := range args {
		p, ok := s.Properties[name]
		if !ok || val == nil {
			continue
		}
		if p.Type == "array" {
			arr, ok := val.([]any)
			if !ok {
				return fmt.Errorf("argument %q must be an array", name)
			}
			if p.Items != nil && p.Items.Type != "" {
				for i, el := range arr {
					if !typeOK(p.Items.Type, el) {
						return fmt.Errorf("argument %q[%d] must be %s", name, i, p.Items.Type)
					}
				}
			}
			continue
		}
		if !typeOK(p.Type, val) {
			return fmt.Errorf("argument %q must be %s", name, p.Type)
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestValidateArgs|TestSchema' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/schema.go internal/connectors/schema_test.go
git commit -m "feat(connectors): validateArgs supports array params with element typing"
```

---

## Task 6: Gmail action expansion (+6 actions)

**Files:**
- Modify: `internal/connectors/connectors/google.yaml`
- Modify: `internal/connectors/render.go` (add `gmail_reply` builder)
- Test: `internal/connectors/render_test.go`

**Interfaces:**
- Consumes: `renderBody` (Task 4), `array` schema (Task 5).
- Produces: actions `gmail_reply_to_thread`, `gmail_modify_labels`, `gmail_list_labels`, `gmail_create_label`, `gmail_move_to_trash`, `gmail_list_threads`; body builder `gmail_reply`.

- [ ] **Step 1: Write the failing test for the reply builder**

Add to `render_test.go`:

```go
func TestRenderGmailReply(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/threads/send", BodyBuilder: "gmail_reply"}}
	_, _, body, ct, err := renderRequest(a, map[string]any{
		"thread_id": "T1", "to": "a@b.com", "subject": "Re: hi", "body": "reply text",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("ct=%s", ct)
	}
	var env struct {
		Raw      string `json:"raw"`
		ThreadID string `json:"threadId"`
	}
	json.Unmarshal(body, &env)
	if env.ThreadID != "T1" {
		t.Fatalf("threadId missing: %s", body)
	}
	dec, _ := base64.URLEncoding.DecodeString(env.Raw)
	if !strings.Contains(string(dec), "reply text") {
		t.Fatalf("body missing: %s", dec)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestRenderGmailReply -count=1`
Expected: FAIL — `unknown body_builder "gmail_reply"`.

- [ ] **Step 3: Add the `gmail_reply` builder in `render.go`**

Register in the `bodyBuilders` map: `"gmail_reply": gmailReply,` and add:

```go
// gmailReply builds a threaded reply: the RFC822 message plus the threadId so Gmail keeps
// it in-thread. Args: thread_id, to, subject, body.
func gmailReply(args map[string]any) ([]byte, string, error) {
	raw := base64.URLEncoding.EncodeToString([]byte(rfc822(args)))
	body, err := json.Marshal(map[string]any{"raw": raw, "threadId": asString(args["thread_id"])})
	return body, "application/json", err
}
```

- [ ] **Step 4: Add the 6 actions to `internal/connectors/connectors/google.yaml`**

Append under `actions:` (exact request templates; `list_labels`/`list_threads` are reads):

```yaml
  - name: gmail_list_labels
    description: "List all Gmail labels (system + user-created) with their ids. Use to resolve a label NAME to its id before gmail_modify_labels. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://gmail.googleapis.com/gmail/v1/users/me/labels"
    response_extract: "$.labels"
  - name: gmail_list_threads
    description: "List Gmail threads matching a query (ids + snippets). Read-only."
    mutating: false
    params:
      type: object
      properties:
        query: {type: string, description: "Gmail search query"}
        max:   {type: integer, description: "max threads (default 10)"}
      required: [query]
    request:
      method: GET
      url: "https://gmail.googleapis.com/gmail/v1/users/me/threads"
      query: {q: "{{query}}", maxResults: "{{max}}"}
    response_extract: "$.threads"
  - name: gmail_create_label
    description: "Create a new Gmail label. A write (creates a label), safe — nothing is sent."
    mutating: false
    params:
      type: object
      properties:
        name: {type: string, description: "label name"}
      required: [name]
    request:
      method: POST
      url: "https://gmail.googleapis.com/gmail/v1/users/me/labels"
      body:
        name: "{{name}}"
        labelListVisibility: "labelShow"
        messageListVisibility: "show"
    response_extract: "$"
  - name: gmail_modify_labels
    description: "Add and/or remove label ids on a Gmail message. Resolve names to ids with gmail_list_labels first. A write to the user's own mailbox; not a send."
    mutating: false
    params:
      type: object
      properties:
        id:     {type: string, description: "message id"}
        add:    {type: array, items: {type: string}, description: "label ids to add"}
        remove: {type: array, items: {type: string}, description: "label ids to remove"}
      required: [id]
    request:
      method: POST
      url: "https://gmail.googleapis.com/gmail/v1/users/me/messages/{{id}}/modify"
      body:
        addLabelIds:    "{{add}}"
        removeLabelIds: "{{remove}}"
    response_extract: "$"
  - name: gmail_move_to_trash
    description: "Move a Gmail message to Trash (recoverable). Use for 'delete/archive this email'. Mutating."
    mutating: true
    params:
      type: object
      properties:
        id: {type: string, description: "message id"}
      required: [id]
    request:
      method: POST
      url: "https://gmail.googleapis.com/gmail/v1/users/me/messages/{{id}}/trash"
    response_extract: "$"
  - name: gmail_reply_to_thread
    description: "Send a reply within an existing Gmail thread (keeps it in-thread). Delivers real mail. Args: thread_id, to, subject, body."
    mutating: true
    params:
      type: object
      properties:
        thread_id: {type: string}
        to:        {type: string}
        subject:   {type: string}
        body:      {type: string}
      required: [thread_id, to, body]
    request:
      method: POST
      url: "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
      body_builder: gmail_reply
    response_extract: "$.id"
```

- [ ] **Step 5: Add the `gmail.modify` scope**

In `internal/connectors/providers/google.yaml`, add to `default_scopes`:

```yaml
  - https://www.googleapis.com/auth/gmail.modify
```

> NOTE for the operator: existing Google connections must be **reconnected** to pick up the new scope (trash/labels return 403 until then).

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestRenderGmail|TestLoad' -count=1 && go build ./...`
Expected: PASS + clean build (validates the YAML parses under `LoadBundled`).

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/connectors/google.yaml internal/connectors/providers/google.yaml internal/connectors/render.go internal/connectors/render_test.go
git commit -m "feat(connectors): +6 Gmail actions (reply, modify_labels array body, labels, trash)"
```

---

## Task 7: Slack provider + 10 actions

**Files:**
- Create: `internal/connectors/providers/slack.yaml`
- Create: `internal/connectors/connectors/slack.yaml`
- Test: `internal/connectors/slack_test.go`

**Interfaces:**
- Consumes: `renderBody` (Task 4).
- Produces: `slack` provider (OAuth, non-expiring); 10 slack actions incl. `slack_send_message` (nested body).

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/slack_test.go`:

```go
package connectors

import (
	"encoding/json"
	"testing"
)

func TestSlackLoadedNonExpiring(t *testing.T) {
	r, _ := LoadBundled()
	p, ok := r.ProviderByName("slack")
	if !ok {
		t.Fatal("slack provider not loaded")
	}
	if !p.NonExpiring() {
		t.Fatal("slack tokens should be non-expiring")
	}
	if len(r.Actions("slack")) < 10 {
		t.Fatalf("expected >=10 slack actions, got %d", len(r.Actions("slack")))
	}
}

func TestSlackSendMessageBodyRenders(t *testing.T) {
	r, _ := LoadBundled()
	a, ok := r.Action("slack", "slack_send_message")
	if !ok {
		t.Fatal("slack_send_message missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"channel": "C1", "text": "hello"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["channel"] != "C1" || got["text"] != "hello" {
		t.Fatalf("bad body: %s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestSlack -count=1`
Expected: FAIL — slack provider not loaded.

- [ ] **Step 3: Create `internal/connectors/providers/slack.yaml`**

```yaml
name: slack
label: Slack
authorize_url: https://slack.com/oauth/v2/authorize
token_url: https://slack.com/api/oauth.v2.access
token_expiry: never
identity_path: ""
default_scopes:
  - chat:write
  - channels:read
  - channels:history
  - groups:read
  - users:read
  - users:read.email
  - reactions:write
  - channels:manage
setup_url: https://api.slack.com/apps
setup_steps:
  - "Go to api.slack.com/apps → Create New App → From scratch."
  - "OAuth & Permissions → add the Bot Token Scopes listed for this connector."
  - "Add the redirect URL shown above under Redirect URLs, then Save."
  - "Install the app to your workspace, then copy the Client ID and Client Secret from Basic Information."
```

> Slack's `oauth.v2.access` returns the bot token under `access_token` for standard installs; `token_expiry: never` matches Slack's default non-rotating tokens. Token-rotation installs are out of scope for the proof.

- [ ] **Step 4: Create `internal/connectors/connectors/slack.yaml`**

All Slack Web API methods below accept `application/json` + Bearer. Reads use GET with query; writes use POST with a JSON `body`.

```yaml
provider: slack
actions:
  - name: slack_send_message
    description: "Post a message to a Slack channel or DM. Use for 'send/post to Slack'. Delivers a real message."
    mutating: true
    params:
      type: object
      properties:
        channel: {type: string, description: "channel id (C...) or user id for a DM"}
        text:    {type: string, description: "message text"}
      required: [channel, text]
    request:
      method: POST
      url: "https://slack.com/api/chat.postMessage"
      body:
        channel: "{{channel}}"
        text: "{{text}}"
    response_extract: "$"
  - name: slack_list_channels
    description: "List channels the token can see (id, name). Read-only."
    mutating: false
    params:
      type: object
      properties:
        limit: {type: integer, description: "max channels (default 100)"}
      required: []
    request:
      method: GET
      url: "https://slack.com/api/conversations.list"
      query: {limit: "{{limit}}", types: "public_channel,private_channel"}
    response_extract: "$.channels"
  - name: slack_fetch_conversation_history
    description: "Fetch recent messages from a channel by id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        channel: {type: string}
        limit:   {type: integer, description: "max messages (default 20)"}
      required: [channel]
    request:
      method: GET
      url: "https://slack.com/api/conversations.history"
      query: {channel: "{{channel}}", limit: "{{limit}}"}
    response_extract: "$.messages"
  - name: slack_fetch_message_thread
    description: "Fetch replies in a thread (channel id + parent message ts). Read-only."
    mutating: false
    params:
      type: object
      properties:
        channel: {type: string}
        ts:      {type: string, description: "parent message timestamp"}
      required: [channel, ts]
    request:
      method: GET
      url: "https://slack.com/api/conversations.replies"
      query: {channel: "{{channel}}", ts: "{{ts}}"}
    response_extract: "$.messages"
  - name: slack_find_channels
    description: "Find channels by name (client-side match on conversations.list). Read-only."
    mutating: false
    params:
      type: object
      properties:
        limit: {type: integer}
      required: []
    request:
      method: GET
      url: "https://slack.com/api/conversations.list"
      query: {limit: "{{limit}}", types: "public_channel,private_channel"}
    response_extract: "$.channels"
  - name: slack_find_user_by_email
    description: "Look up a Slack user by email address. Read-only."
    mutating: false
    params:
      type: object
      properties:
        email: {type: string}
      required: [email]
    request:
      method: GET
      url: "https://slack.com/api/users.lookupByEmail"
      query: {email: "{{email}}"}
    response_extract: "$.user"
  - name: slack_list_users
    description: "List workspace users (id, name, real name). Read-only."
    mutating: false
    params:
      type: object
      properties:
        limit: {type: integer}
      required: []
    request:
      method: GET
      url: "https://slack.com/api/users.list"
      query: {limit: "{{limit}}"}
    response_extract: "$.members"
  - name: slack_add_reaction
    description: "Add an emoji reaction to a message (channel id + ts + emoji name). Mutating."
    mutating: true
    params:
      type: object
      properties:
        channel: {type: string}
        ts:      {type: string, description: "message timestamp"}
        name:    {type: string, description: "emoji name without colons, e.g. thumbsup"}
      required: [channel, ts, name]
    request:
      method: POST
      url: "https://slack.com/api/reactions.add"
      body:
        channel: "{{channel}}"
        timestamp: "{{ts}}"
        name: "{{name}}"
    response_extract: "$"
  - name: slack_create_channel
    description: "Create a new Slack channel. Mutating."
    mutating: true
    params:
      type: object
      properties:
        name:       {type: string, description: "channel name (lowercase, no spaces)"}
        is_private: {type: boolean}
      required: [name]
    request:
      method: POST
      url: "https://slack.com/api/conversations.create"
      body:
        name: "{{name}}"
        is_private: "{{is_private}}"
    response_extract: "$.channel"
  - name: slack_invite_to_channel
    description: "Invite users (comma-separated user ids) to a channel. Mutating."
    mutating: true
    params:
      type: object
      properties:
        channel: {type: string}
        users:   {type: string, description: "comma-separated user ids"}
      required: [channel, users]
    request:
      method: POST
      url: "https://slack.com/api/conversations.invite"
      body:
        channel: "{{channel}}"
        users: "{{users}}"
    response_extract: "$.channel"
```

- [ ] **Step 5: Add slack to the UI provider list**

In `web/handlers_services.go`, extend `availableServiceProviders`:

```go
var availableServiceProviders = []string{"google", "github", "notion", "outlook", "jira", "slack", "openai"}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run TestSlack -count=1 && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 7: Commit**

```bash
git add internal/connectors/providers/slack.yaml internal/connectors/connectors/slack.yaml internal/connectors/slack_test.go web/handlers_services.go
git commit -m "feat(connectors): Slack OAuth provider + 10 actions (nested-body send_message)"
```

---

## Task 8: OpenAI actions (10)

**Files:**
- Create: `internal/connectors/connectors/openai.yaml`
- Test: `internal/connectors/openai_test.go`

**Interfaces:**
- Consumes: `openai` provider (Task 1), `renderBody` (Task 4).
- Produces: 10 OpenAI actions incl. `openai_chat_completion` (nested messages body).

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/openai_test.go`:

```go
package connectors

import (
	"encoding/json"
	"testing"
)

func TestOpenAIChatBody(t *testing.T) {
	r, _ := LoadBundled()
	a, ok := r.Action("openai", "openai_chat_completion")
	if !ok {
		t.Fatal("openai_chat_completion missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{
		"model": "gpt-4o-mini",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["model"] != "gpt-4o-mini" {
		t.Fatalf("bad body: %s", body)
	}
	if _, ok := got["messages"].([]any); !ok {
		t.Fatalf("messages not an array: %s", body)
	}
}

func TestOpenAIActionCount(t *testing.T) {
	r, _ := LoadBundled()
	if n := len(r.Actions("openai")); n < 8 {
		t.Fatalf("expected >=8 openai actions, got %d", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestOpenAI -count=1`
Expected: FAIL — no openai actions loaded.

- [ ] **Step 3: Create `internal/connectors/connectors/openai.yaml`**

```yaml
provider: openai
actions:
  - name: openai_chat_completion
    description: "Call OpenAI chat completions with a messages array. Use for text generation via the user's own OpenAI account."
    mutating: false
    params:
      type: object
      properties:
        model:    {type: string, description: "e.g. gpt-4o-mini"}
        messages: {type: array, items: {type: object}, description: "[{role, content}]"}
      required: [model, messages]
    request:
      method: POST
      url: "https://api.openai.com/v1/chat/completions"
      body:
        model: "{{model}}"
        messages: "{{messages}}"
    response_extract: "$"
  - name: openai_create_embedding
    description: "Create an embedding vector for input text. Read-like (no external side effect)."
    mutating: false
    params:
      type: object
      properties:
        model: {type: string, description: "e.g. text-embedding-3-small"}
        input: {type: string}
      required: [model, input]
    request:
      method: POST
      url: "https://api.openai.com/v1/embeddings"
      body:
        model: "{{model}}"
        input: "{{input}}"
    response_extract: "$"
  - name: openai_create_image
    description: "Generate an image from a text prompt (DALL·E / gpt-image). Creates content in the user's account."
    mutating: false
    params:
      type: object
      properties:
        model:  {type: string, description: "e.g. gpt-image-1 or dall-e-3"}
        prompt: {type: string}
        size:   {type: string, description: "e.g. 1024x1024"}
      required: [prompt]
    request:
      method: POST
      url: "https://api.openai.com/v1/images/generations"
      body:
        model: "{{model}}"
        prompt: "{{prompt}}"
        size: "{{size}}"
    response_extract: "$"
  - name: openai_moderation
    description: "Classify whether text is potentially harmful via the moderations endpoint. Read-only."
    mutating: false
    params:
      type: object
      properties:
        input: {type: string}
      required: [input]
    request:
      method: POST
      url: "https://api.openai.com/v1/moderations"
      body:
        input: "{{input}}"
    response_extract: "$"
  - name: openai_list_models
    description: "List models available to the account. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://api.openai.com/v1/models"
    response_extract: "$.data"
  - name: openai_retrieve_model
    description: "Get details of one model by id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        model: {type: string}
      required: [model]
    request:
      method: GET
      url: "https://api.openai.com/v1/models/{{model}}"
    response_extract: "$"
  - name: openai_list_files
    description: "List files uploaded to the account. Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://api.openai.com/v1/files"
    response_extract: "$.data"
  - name: openai_delete_file
    description: "Delete an uploaded file by id. Mutating."
    mutating: true
    params:
      type: object
      properties:
        file_id: {type: string}
      required: [file_id]
    request:
      method: DELETE
      url: "https://api.openai.com/v1/files/{{file_id}}"
    response_extract: "$"
  - name: openai_create_assistant
    description: "Create an Assistant (Assistants API). Mutating."
    mutating: true
    params:
      type: object
      properties:
        model:        {type: string}
        name:         {type: string}
        instructions: {type: string}
      required: [model]
    request:
      method: POST
      url: "https://api.openai.com/v1/assistants"
      body:
        model: "{{model}}"
        name: "{{name}}"
        instructions: "{{instructions}}"
    response_extract: "$"
```

> **Deferred:** `openai_upload_file` is multipart/form-data, which the JSON `renderBody` engine does not cover; it is intentionally omitted from the proof (would need a dedicated multipart body_builder — schedule in the catalog phase). This leaves 9 actions, satisfying the ≥8 test.

> **Assistants API note:** `openai_create_assistant` requires the header `OpenAI-Beta: assistants=v2`. If the live check returns a 400 about the beta header, add `static_headers: {OpenAI-Beta: "assistants=v2"}` to `providers/openai.yaml`. Verify during Step 5.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run TestOpenAI -count=1 && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/connectors/openai.yaml internal/connectors/openai_test.go
git commit -m "feat(connectors): OpenAI api-key actions (chat, embeddings, images, moderation, files)"
```

---

## Task 9: API-key connect UI

**Files:**
- Modify: `web/handlers_services.go`
- Modify: `web/server.go`
- Modify: `web/templates/dashboard/services.html`

**Interfaces:**
- Consumes: `Provider.IsAPIKey()`, `Provider.Auth` (Task 1); `availableServiceProviders` already includes slack/openai (Task 7).
- Produces: route `POST /dashboard/connectors/services/:provider/apikey` → `handleConnectAPIKey`; `serviceProviderView` carries auth-kind fields.

- [ ] **Step 1: Extend `serviceProviderView` and populate it**

In `web/handlers_services.go`, add fields to `serviceProviderView`:

```go
	IsAPIKey    bool
	KeyLabel    string
	KeyHint     string
	KeySetupURL string
```

In `showServices`, inside the provider loop where `p, ok := s.connectors.ProviderByName(provider)` is available, set them on the view (default empty for OAuth providers):

```go
		isAPIKey, keyLabel, keyHint, keySetupURL := false, "", "", ""
		if p, ok := s.connectors.ProviderByName(provider); ok && p.IsAPIKey() {
			isAPIKey = true
			keyLabel, keyHint, keySetupURL = p.Auth.KeyLabel, p.Auth.KeyHint, p.Auth.SetupURL
		}
```

and add `IsAPIKey: isAPIKey, KeyLabel: keyLabel, KeyHint: keyHint, KeySetupURL: keySetupURL,` to the `serviceProviderView{...}` literal.

- [ ] **Step 2: Add the handler**

Add to `web/handlers_services.go`:

```go
// handleConnectAPIKey stores a static API-key connection for an api_key provider. No OAuth
// app, no redirect: the pasted key is encrypted into service_connections directly.
func (s *Server) handleConnectAPIKey(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	provider := c.Param("provider")
	prov, ok := s.connectors.ProviderByName(provider)
	if !ok || !prov.IsAPIKey() {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Unknown or non-API-key provider.")
	}
	apiKey := strings.TrimSpace(c.FormValue("api_key"))
	if apiKey == "" {
		return s.redirectWithError(c, "/dashboard/connectors/services", "API key is required.")
	}
	label := strings.TrimSpace(c.FormValue("account_label"))
	if label == "" {
		label = "default"
	}
	enc, err := secrets.EncryptWithSystemKey(apiKey, s.systemKey)
	if err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to store the API key.")
	}
	if err := s.db.InsertServiceConnection(c.Request().Context(), db.ServiceConnection{
		ID: uuid.New().String(), WorkspaceID: w.ID, Provider: provider,
		AccountLabel: label, AccountIdentity: label,
		EncryptedAccessToken: enc, Status: "ACTIVE",
	}); err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Failed to save the connection: "+err.Error())
	}
	return c.Redirect(http.StatusSeeOther, "/dashboard/connectors/services")
}
```

- [ ] **Step 3: Register the route**

In `web/server.go`, find where `/dashboard/connectors/services/:provider/connect` is registered and add alongside it:

```go
	// (same group/middleware as the other services routes)
	dash.POST("/connectors/services/:provider/apikey", s.handleConnectAPIKey)
```

> Match the exact router group variable and middleware used by the existing `:provider/connect` POST — copy that line's style.

- [ ] **Step 4: Template branch**

In `web/templates/dashboard/services.html`, find the per-provider card block. Where it currently renders the OAuth creds form + Connect button (guarded by something like `{{if .HasCreds}}`), wrap with an auth-kind branch:

```html
{{if .IsAPIKey}}
  <form method="POST" action="/dashboard/connectors/services/{{.Name}}/apikey">
    <label>{{.KeyLabel}}</label>
    <input type="password" name="api_key" placeholder="{{.KeyHint}}" required>
    <input type="text" name="account_label" placeholder="account label (optional)">
    <button type="submit">Connect</button>
    {{if .KeySetupURL}}<a href="{{.KeySetupURL}}" target="_blank" rel="noopener">Where do I get this key?</a>{{end}}
  </form>
{{else}}
  <!-- existing OAuth creds + Connect markup, unchanged -->
{{end}}
```

> Keep the existing connected-accounts list (it renders the same for both kinds). Only the connect affordance is branched.

- [ ] **Step 5: Build + manual smoke check**

Run: `go build -o bin/simple-agents ./cmd/simple-agents && make deploy`
Then: open `/dashboard/connectors/services`, confirm the **OpenAI** card shows a paste-key form (no client id/secret), and Slack shows the normal OAuth form.
Expected: OpenAI connects with a pasted key; the account appears in the list with status ACTIVE.

- [ ] **Step 6: Commit**

```bash
git add web/handlers_services.go web/server.go web/templates/dashboard/services.html
git commit -m "feat(web): auth-kind-aware connect UI + API-key connect handler"
```

---

## Task 10: Live end-to-end verification (`cmd/livecheck`)

**Files:**
- Modify: `cmd/livecheck/main.go`

**Interfaces:**
- Consumes: everything above; real stored connections for google (reconnected with `gmail.modify`), slack, openai.

- [ ] **Step 1: Add slack + openai cases to the dispatch switch**

In `cmd/livecheck/main.go`, in the `switch c.Provider` block, add:

```go
			case "slack":
				checkSlack(ctx, reg, store, client, ref)
			case "openai":
				checkOpenAI(ctx, reg, store, client, ref)
```

- [ ] **Step 2: Add the check functions**

Append to `cmd/livecheck/main.go`:

```go
func checkSlack(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef) {
	res, ok := run(ctx, reg, store, client, ref, "slack_list_channels", map[string]any{"limit": 5})
	var channel string
	if ok {
		var chans []struct {
			ID string `json:"id"`
		}
		json.Unmarshal(res.Data, &chans)
		if len(chans) > 0 {
			channel = chans[0].ID
		}
	}
	run(ctx, reg, store, client, ref, "slack_list_users", map[string]any{"limit": 5})
	if channel != "" {
		run(ctx, reg, store, client, ref, "slack_fetch_conversation_history", map[string]any{"channel": channel, "limit": 3})
	} else {
		fmt.Printf("  [skip] slack_fetch_conversation_history — no channel id\n")
	}
	// Mutating (send_message, add_reaction, create_channel, invite) are [skip]-listed by the
	// mutating loop; run slack_send_message manually against a throwaway channel on request.
}

func checkOpenAI(ctx context.Context, reg *connectors.Registry, store connectors.TokenStore, client *http.Client, ref connectors.ConnRef) {
	run(ctx, reg, store, client, ref, "openai_list_models", nil)
	run(ctx, reg, store, client, ref, "openai_chat_completion", map[string]any{
		"model":    "gpt-4o-mini",
		"messages": []any{map[string]any{"role": "user", "content": "Reply with the single word: pong"}},
	})
	run(ctx, reg, store, client, ref, "openai_moderation", map[string]any{"input": "hello world"})
}
```

- [ ] **Step 3: Build livecheck**

Run: `go build -o bin/livecheck ./cmd/livecheck`
Expected: clean build.

- [ ] **Step 4: Operator prerequisites (manual, one-time)**

1. Reconnect the **Google** account (Services page) so the new `gmail.modify` scope is granted.
2. Create a **Slack** OAuth app (scopes from `slack.yaml`), connect it, and note a throwaway channel id.
3. Connect an **OpenAI** account via the API-key form.

- [ ] **Step 5: Run the live check per provider**

```bash
SA_SYSTEM_KEY="$(cat ~/.simple-agents-v2/system.key 2>/dev/null || echo "$SA_SYSTEM_KEY")" ./bin/livecheck google
./bin/livecheck slack
./bin/livecheck openai
```

> Use the same env the app uses to load the system key (`secrets.SystemKeyFromEnv()`); if unsure, check how `make deploy` sets it and mirror that.

Expected: each read action prints `[ OK ]`; mutating actions print `[skip]`. Gmail's `gmail_list_labels` returns labels; `openai_chat_completion` returns a completion containing "pong".

- [ ] **Step 6: Run one safe mutating call per provider (manual proof)**

Temporarily invoke (or add a guarded `--write` path) these safe writes and confirm success, then revert any test scaffolding:
- Gmail: `gmail_create_draft` (already in `checkGoogle`) + `gmail_modify_labels` adding a label id to a message.
- Slack: `slack_send_message` to your throwaway channel.
- OpenAI: `openai_chat_completion` (already a completion — no external side effect).

Expected: draft appears in Gmail; label applied; Slack message posted. No outbound email sent.

- [ ] **Step 7: Full test suite + commit**

```bash
go test ./internal/connectors/... -count=1
git add cmd/livecheck/main.go
git commit -m "test(livecheck): slack + openai live-check plans"
```

---

## Self-Review

**Spec coverage:**
- Declarative auth block → Task 1. ✓
- `applyAuth` injection → Task 2. ✓
- Zero-column data model / api_key AccessToken branch → Task 3. ✓
- Nested/array body renderer → Task 4. ✓
- Array schema → Task 5. ✓
- Gmail +6 (reply builder, modify_labels array, list_labels, create_label, move_to_trash, list_threads) + gmail.modify scope → Task 6. ✓
- Slack provider + 10 actions (nested send_message) + non-expiring → Task 7. ✓
- OpenAI ~10 actions (chat nested body) + multipart-upload deferral → Task 8. ✓
- Auth-type-aware connect UI (no creds step for api_key) → Task 9. ✓
- Live E2E real reads + safe mutating, no outbound sends → Task 10. ✓

**Placeholder scan:** No TBD/TODO. The two conditional notes (OpenAI beta header; system-key env) are contingency instructions with concrete actions, not deferred work.

**Type consistency:** `AuthConfig`/`Provider.Auth`/`IsAPIKey()`/`RequestTemplate.Body`/`renderBody`/`applyAuth`/`gmailReply`/`handleConnectAPIKey` are used with identical signatures across the tasks that define and consume them. `availableServiceProviders` gains slack+openai once (Task 7); Task 9 only reads it.

**Known deferrals (in-scope-adjacent, intentional):** `openai_upload_file` (multipart), Slack token rotation, connectors-page redesign, other 27 providers, AWS/PostgreSQL/Stripe — all belong to later sub-projects per the spec.
