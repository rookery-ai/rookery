# Connector Catalog B1 Implementation Plan (Google-family + Teams)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Google Drive, Sheets, Docs, and Teams as separate connector providers that reuse the existing Google / MS-Graph OAuth login via a new `auth_parent` alias.

**Architecture:** A child provider declares `auth_parent: <parent>` and carries only its own scopes/actions/label; a `Registry.OAuthProvider` resolver returns the parent for OAuth mechanics (endpoints, app-credentials key, refresh) while `ProviderByName` keeps the child's identity. Each service keeps its own `service_connections` row + token, so per-service agent binding is unchanged. Actions are data files rendered by the existing engine.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, `modernc.org/sqlite`, Echo v4. Tests use stdlib `testing` + `net/http/httptest`.

## Global Constraints

- Package under change: `internal/connectors` (+ `web/handlers_services.go`, `web/server.go`, `web/templates/dashboard/services.html`). No new packages, no DB migrations.
- **Verification is unit/rendering only for B1** — no live API calls; live E2E + `cmd/livecheck` plans are deferred.
- A child provider has **no** `authorize_url`/`token_url`/token settings of its own; those resolve from `auth_parent`. Its `default_scopes` are only the extra scopes it needs.
- Each service gets its **own** `service_connections` row (`provider: google_drive` etc.) with its own token. `agent_connections` binding stays connection-keyed and unchanged.
- OAuth-app credentials (`service_provider_configs`) are stored under the **parent** provider only; children resolve them via `auth_parent`.
- Non-alias providers (google, github, slack, …) must behave EXACTLY as before.
- Mutating actions keep the build-time guard (`mutating: true`).
- Deferred (do NOT build): Drive multipart upload + binary download; live checks; batches B2/B3/B4.
- Build: `go build ./...`. Tests: `go test ./internal/connectors/... ./web/... -count=1`.
- Branch: `main` (feature builds directly on main per operator; the executor may branch if preferred).

---

## File Structure

- `internal/connectors/registry.go` — Modify: add `Provider.AuthParent`; add `Registry.OAuthProvider`.
- `internal/connectors/oauth.go` — Modify: `ConsentURL` takes an explicit `scopes []string`.
- `internal/connectors/dbstore.go` — Modify: `refresh` resolves parent for endpoints + config.
- `internal/connectors/execute.go` — Modify: static-headers lookup via `OAuthProvider`.
- `internal/connectors/providers/google_drive.yaml`, `google_sheets.yaml`, `google_docs.yaml`, `teams.yaml` — Create.
- `internal/connectors/providers/google.yaml`, `outlook.yaml` — Modify: add `include_granted_scopes` to `authorize_extra`.
- `internal/connectors/connectors/google_drive.yaml`, `google_sheets.yaml`, `google_docs.yaml`, `teams.yaml` — Create.
- `internal/connectors/registry_test.go`, `oauth_test.go`, `dbstore_test.go`, plus new `alias_test.go` — Tests.
- `web/handlers_services.go` — Modify: resolve parent for creds/consent; child-card view fields.
- `web/server.go` — no new route (reuses `/connect`).
- `web/templates/dashboard/services.html` — Modify: child-card branch.

---

## Task 1: `auth_parent` field + `OAuthProvider` resolver

**Files:**
- Modify: `internal/connectors/registry.go`
- Create: `internal/connectors/providers/google_drive.yaml` (fixture for the resolver test)
- Test: `internal/connectors/alias_test.go`

**Interfaces:**
- Produces: `Provider.AuthParent string`; `func (r *Registry) OAuthProvider(name string) (Provider, bool)`.

- [ ] **Step 1: Write the failing test**

Create `internal/connectors/alias_test.go`:

```go
package connectors

import "testing"

func TestOAuthProviderResolvesParent(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	// child identity is preserved
	child, ok := r.ProviderByName("google_drive")
	if !ok {
		t.Fatal("google_drive provider not loaded")
	}
	if child.AuthParent != "google" {
		t.Fatalf("auth_parent = %q, want google", child.AuthParent)
	}
	// OAuth mechanics resolve to the parent
	oauth, ok := r.OAuthProvider("google_drive")
	if !ok || oauth.Name != "google" {
		t.Fatalf("OAuthProvider(google_drive) = %q, want google", oauth.Name)
	}
	if oauth.AuthorizeURL == "" || oauth.TokenURL == "" {
		t.Fatal("resolved parent missing OAuth endpoints")
	}
	// a normal provider resolves to itself
	self, ok := r.OAuthProvider("google")
	if !ok || self.Name != "google" {
		t.Fatalf("OAuthProvider(google) = %q, want google", self.Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestOAuthProviderResolvesParent -count=1`
Expected: FAIL — `google_drive provider not loaded` + `OAuthProvider` undefined.

- [ ] **Step 3: Add the field + resolver in `registry.go`**

Add to the `Provider` struct (next to `PostConnect`):

```go
	// AuthParent names another provider whose OAuth app + endpoints this provider reuses.
	// A child (e.g. google_drive → google) declares only its own scopes/actions/label; its
	// authorize_url/token_url/token settings/app-credentials all resolve from the parent.
	AuthParent string `yaml:"auth_parent"`
```

Add the resolver:

```go
// OAuthProvider returns the provider whose OAuth config governs authentication for name:
// the auth_parent when set (one level), else the provider itself. Used for endpoints, token
// settings, static_headers, authorize_extra, and the app-credentials lookup key. ProviderByName
// still returns the child itself (its scopes, label, actions, post_connect).
func (r *Registry) OAuthProvider(name string) (Provider, bool) {
	p, ok := r.providers[name]
	if !ok {
		return Provider{}, false
	}
	if p.AuthParent != "" {
		if parent, ok := r.providers[p.AuthParent]; ok {
			return parent, true
		}
		return Provider{}, false
	}
	return p, true
}
```

- [ ] **Step 4: Create `internal/connectors/providers/google_drive.yaml`**

```yaml
name: google_drive
label: Google Drive
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/drive
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "Google Drive reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console → APIs & Services → Library, also enable the Google Drive API."
  - "Then click Connect here to authorize Drive access on the same Google account."
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/connectors/ -run TestOAuthProviderResolvesParent -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/providers/google_drive.yaml internal/connectors/alias_test.go
git commit -m "feat(connectors): auth_parent alias + OAuthProvider resolver"
```

---

## Task 2: `ConsentURL` scopes param + parent-resolved consent

**Files:**
- Modify: `internal/connectors/oauth.go`
- Modify: `internal/connectors/providers/google.yaml`, `internal/connectors/providers/outlook.yaml`
- Modify: `web/handlers_services.go` (`handleConnectService`)
- Test: `internal/connectors/oauth_test.go`

**Interfaces:**
- Consumes: `OAuthProvider` (Task 1).
- Produces: `func (p Provider) ConsentURL(clientID, redirectURI, state string, scopes []string) string` (scopes now explicit).

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/oauth_test.go`:

```go
func TestConsentURLUsesExplicitScopes(t *testing.T) {
	p := Provider{
		AuthorizeURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		AuthorizeExtra: map[string]string{"include_granted_scopes": "true"},
	}
	u := p.ConsentURL("CID", "https://cb", "STATE", []string{"https://www.googleapis.com/auth/drive"})
	if !strings.Contains(u, "scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdrive") {
		t.Fatalf("scope missing: %s", u)
	}
	if !strings.Contains(u, "include_granted_scopes=true") {
		t.Fatalf("include_granted_scopes missing: %s", u)
	}
	if !strings.Contains(u, "client_id=CID") {
		t.Fatalf("client_id missing: %s", u)
	}
}
```

(Ensure `oauth_test.go` imports `strings`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestConsentURLUsesExplicitScopes -count=1`
Expected: FAIL — compile error (ConsentURL takes 3 args).

- [ ] **Step 3: Change `ConsentURL` in `oauth.go`**

Replace the signature + the scope block:

```go
func (p Provider) ConsentURL(clientID, redirectURI, state string, scopes []string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	if len(scopes) > 0 { // Notion sends no scope param
		q.Set("scope", strings.Join(scopes, " "))
	}
	q.Set("state", state)
	for k, v := range p.AuthorizeExtra {
		q.Set(k, v)
	}
	sep := "?"
	if strings.Contains(p.AuthorizeURL, "?") {
		sep = "&"
	}
	return p.AuthorizeURL + sep + q.Encode()
}
```

- [ ] **Step 4: Add `include_granted_scopes` to the parent providers (data)**

In `internal/connectors/providers/google.yaml`, under `authorize_extra:` add:

```yaml
  include_granted_scopes: "true"
```

In `internal/connectors/providers/outlook.yaml`: if an `authorize_extra:` block exists, add the same line; if not, add:

```yaml
authorize_extra:
  include_granted_scopes: "true"
```

- [ ] **Step 5: Update the web caller in `handleConnectService`**

In `web/handlers_services.go`, `handleConnectService` currently does
`prov, ok := s.connectors.ProviderByName(provider)` then fetches creds and calls
`prov.ConsentURL(clientID, s.callbackURL(c, provider), state)`.

Replace the provider/creds/consent portion with parent resolution:

```go
	child, ok := s.connectors.ProviderByName(provider)
	if !ok {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Unknown provider.")
	}
	oauth, ok := s.connectors.OAuthProvider(provider) // parent when aliased, else self
	if !ok {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Unknown provider.")
	}
	cfg, _ := s.db.GetServiceProviderConfig(c.Request().Context(), w.ID, oauth.Name)
	if cfg == nil {
		return s.redirectWithError(c, "/dashboard/connectors/services",
			"Save your "+oauth.Label+" OAuth app credentials first.")
	}
	clientID, err := secrets.DecryptWithSystemKey(cfg.EncryptedClientID, s.systemKey)
	if err != nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Stored credentials are unreadable; re-enter them.")
	}
	nonce := uuid.New().String()
	payload := strings.Join([]string{w.ID, provider, label, nonce}, "~")
	state := signState(s.systemKey, payload, time.Now())
	return c.Redirect(http.StatusSeeOther, oauth.ConsentURL(clientID, s.callbackURL(c, provider), state, child.DefaultScopes))
```

(The callback still uses `provider` — the child — so the created connection row is stored under the child provider. The OAuth `code`→token exchange in `handleOAuthCallback` must ALSO resolve the parent for client creds + token endpoint; do that in Step 6.)

- [ ] **Step 6: Resolve parent in `handleOAuthCallback`**

In `web/handlers_services.go`, `handleOAuthCallback` fetches the provider config and calls
`ExchangeCode`. Change it to resolve the OAuth parent for BOTH the config lookup and the provider
passed to `ExchangeCode`/`FetchIdentity`, while still storing the connection under the child
`provider` from the state. Concretely, where it currently resolves `prov` and `cfg`:

```go
	oauth, ok := s.connectors.OAuthProvider(provider)
	if !ok {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Unknown provider.")
	}
	cfg, _ := s.db.GetServiceProviderConfig(ctx, w.ID, oauth.Name)
	if cfg == nil {
		return s.redirectWithError(c, "/dashboard/connectors/services", "Missing OAuth app credentials.")
	}
	// use `oauth` (parent) for ExchangeCode/FetchIdentity; store the connection under `provider` (child).
```

Update the `ExchangeCode(ctx, oauth, clientID, clientSecret, code, redirectURI)` and
`FetchIdentity(ctx, oauth, tokenSet.AccessToken)` calls to pass `oauth`. Keep `InsertServiceConnection`
writing `Provider: provider` (the child).

- [ ] **Step 7: Run tests + build**

Run: `go test ./internal/connectors/ -run 'TestConsentURL|TestExchange|TestOAuth' -count=1 && go build ./...`
Expected: PASS + clean build (the connectors ConsentURL tests + the web package compile with the new signature).

- [ ] **Step 8: Commit**

```bash
git add internal/connectors/oauth.go internal/connectors/providers/google.yaml internal/connectors/providers/outlook.yaml internal/connectors/oauth_test.go web/handlers_services.go
git commit -m "feat(connectors): parent-resolved consent + explicit scopes + include_granted_scopes"
```

---

## Task 3: Parent-resolved token refresh

**Files:**
- Modify: `internal/connectors/dbstore.go`
- Test: `internal/connectors/dbstore_test.go`

**Interfaces:**
- Consumes: `OAuthProvider` (Task 1); the `google_drive` provider (Task 1).

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/dbstore_test.go` (mirror `TestAccessTokenRefreshesNearExpiry`, but the connection is a `google_drive` child whose OAuth config lives under `google`):

```go
func TestAccessTokenRefreshResolvesParentConfig(t *testing.T) {
	d, ws := storeTestDB(t)
	key := mkKey()
	reg := testRegistry(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"NEW","expires_in":3600}`))
	}))
	defer srv.Close()

	// OAuth app creds stored under the PARENT provider "google" only.
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(ctx, db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: encID, EncryptedClientSecret: encSec})

	// A child google_drive connection with a stale token + a refresh token.
	encRefresh, _ := secrets.EncryptWithSystemKey("RT", key)
	encOld, _ := secrets.EncryptWithSystemKey("OLD", key)
	d.InsertServiceConnection(ctx, db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google_drive", AccountLabel: "work",
		EncryptedAccessToken: encOld, EncryptedRefreshToken: encRefresh,
		ExpiresAt: "2000-01-01T00:00:00Z", Status: "ACTIVE",
	})

	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}
	// point the parent google provider's token endpoint at the test server
	// (OAuthProvider must resolve google_drive → google, whose TokenURL we override below).
	store.tokenURLOverride = srv.URL // see Step 3

	tok, err := store.AccessToken(ctx, ConnRef{ID: "c1", Provider: "google_drive"})
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if tok != "NEW" {
		t.Fatalf("got %q, want NEW (refreshed via parent config)", tok)
	}
}
```

> The existing refresh tests point the provider's `TokenURL` at an httptest server. If the current
> tests do that by mutating the registry's provider (not via a `tokenURLOverride` field), follow the
> SAME mechanism they use instead of inventing `tokenURLOverride` — inspect
> `TestAccessTokenRefreshesNearExpiry` and copy its exact approach for overriding the token
> endpoint. Delete the `tokenURLOverride` line above and use the established pattern.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestAccessTokenRefreshResolvesParentConfig -count=1`
Expected: FAIL — refresh looks up `google_drive` config (none exists) → `missing OAuth app credentials`.

- [ ] **Step 3: Resolve parent in `refresh`**

In `dbstore.go` `refresh`, change the first two lookups from the row's own provider to the resolved
OAuth parent:

```go
func (s *DBTokenStore) refresh(ctx context.Context, row *db.ServiceConnection) (string, error) {
	prov, ok := s.Reg.OAuthProvider(row.Provider) // parent when aliased, else self
	if !ok {
		return "", &ConnectorError{KindOther, "unknown provider " + row.Provider}
	}
	cfg, err := s.DB.GetServiceProviderConfig(ctx, row.WorkspaceID, prov.Name)
	if err != nil || cfg == nil {
		return "", &ConnectorError{KindNeedsReauth, "missing OAuth app credentials for " + prov.Name}
	}
	// ... rest unchanged (decrypt creds, s.OAuth.Refresh(ctx, prov, ...), persist) ...
```

The rest of `refresh` already uses the local `prov`, so `s.OAuth.Refresh(ctx, prov, cid, csec, refreshTok)` now uses the parent's endpoints. Leave `AccessToken`'s api_key/NonExpiring branches unchanged (a child inherits the parent's expiring behavior — google tokens expire, so refresh runs).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/connectors/ -run 'TestAccessToken|TestRefresh' -count=1`
Expected: PASS (existing refresh tests unaffected; new parent-resolution test passes).

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/dbstore.go internal/connectors/dbstore_test.go
git commit -m "feat(connectors): token refresh resolves auth_parent for config + endpoints"
```

---

## Task 4: Google Drive actions

**Files:**
- Modify: `internal/connectors/providers/google_drive.yaml` (already created in Task 1 — no change needed unless adding actions here)
- Create: `internal/connectors/connectors/google_drive.yaml`
- Test: `internal/connectors/alias_test.go`

**Interfaces:**
- Consumes: `renderBody` + array schema (foundation).
- Produces: 9 Drive actions under provider `google_drive`.

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/alias_test.go`:

```go
func TestGoogleDriveActions(t *testing.T) {
	r, _ := LoadBundled()
	if n := len(r.Actions("google_drive")); n < 8 {
		t.Fatalf("expected >=8 drive actions, got %d", n)
	}
	a, ok := r.Action("google_drive", "drive_share_file")
	if !ok {
		t.Fatal("drive_share_file missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"file_id": "F1", "role": "reader", "type": "user", "email": "x@y.com"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	if got["role"] != "reader" || got["type"] != "user" {
		t.Fatalf("bad share body: %s", body)
	}
}
```

(Ensure `alias_test.go` imports `encoding/json`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestGoogleDriveActions -count=1`
Expected: FAIL — no drive actions loaded.

- [ ] **Step 3: Create `internal/connectors/connectors/google_drive.yaml`**

```yaml
provider: google_drive
actions:
  - name: drive_list_files
    description: "List files in the connected Google Drive (id, name, mimeType). Read-only."
    mutating: false
    params:
      type: object
      properties:
        max: {type: integer, description: "max files (default 20)"}
      required: []
    request:
      method: GET
      url: "https://www.googleapis.com/drive/v3/files"
      query: {pageSize: "{{max}}", fields: "files(id,name,mimeType,modifiedTime,webViewLink)"}
    response_extract: "$.files"
  - name: drive_search_files
    description: "Search Drive files with a Drive query string (e.g. name contains 'report'). Read-only."
    mutating: false
    params:
      type: object
      properties:
        query: {type: string, description: "Drive q query, e.g. \"name contains 'notes'\""}
        max:   {type: integer}
      required: [query]
    request:
      method: GET
      url: "https://www.googleapis.com/drive/v3/files"
      query: {q: "{{query}}", pageSize: "{{max}}", fields: "files(id,name,mimeType,webViewLink)"}
    response_extract: "$.files"
  - name: drive_get_file
    description: "Get a Drive file's metadata (name, mimeType, links) by id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        file_id: {type: string}
      required: [file_id]
    request:
      method: GET
      url: "https://www.googleapis.com/drive/v3/files/{{file_id}}"
      query: {fields: "id,name,mimeType,modifiedTime,webViewLink,parents"}
    response_extract: "$"
  - name: drive_create_folder
    description: "Create a folder in Drive. A write, safe (creates a folder)."
    mutating: false
    params:
      type: object
      properties:
        name:      {type: string}
        parent_id: {type: string, description: "optional parent folder id"}
      required: [name]
    request:
      method: POST
      url: "https://www.googleapis.com/drive/v3/files"
      body:
        name: "{{name}}"
        mimeType: "application/vnd.google-apps.folder"
        parents: "{{parent_id}}"
    response_extract: "$"
  - name: drive_copy_file
    description: "Copy an existing Drive file. Safe write."
    mutating: false
    params:
      type: object
      properties:
        file_id: {type: string}
        name:    {type: string, description: "name for the copy"}
      required: [file_id]
    request:
      method: POST
      url: "https://www.googleapis.com/drive/v3/files/{{file_id}}/copy"
      body:
        name: "{{name}}"
    response_extract: "$"
  - name: drive_rename_file
    description: "Rename a Drive file. Safe write."
    mutating: false
    params:
      type: object
      properties:
        file_id: {type: string}
        name:    {type: string}
      required: [file_id, name]
    request:
      method: PATCH
      url: "https://www.googleapis.com/drive/v3/files/{{file_id}}"
      body:
        name: "{{name}}"
    response_extract: "$"
  - name: drive_move_file
    description: "Move a Drive file to a new parent folder. Safe write."
    mutating: false
    params:
      type: object
      properties:
        file_id:        {type: string}
        add_parent:     {type: string, description: "destination folder id"}
        remove_parent:  {type: string, description: "current parent folder id to remove"}
      required: [file_id, add_parent]
    request:
      method: PATCH
      url: "https://www.googleapis.com/drive/v3/files/{{file_id}}"
      query: {addParents: "{{add_parent}}", removeParents: "{{remove_parent}}"}
    response_extract: "$"
  - name: drive_share_file
    description: "Grant a person access to a Drive file (creates a permission). Mutating (shares data)."
    mutating: true
    params:
      type: object
      properties:
        file_id: {type: string}
        role:    {type: string, description: "reader | writer | commenter"}
        type:    {type: string, description: "user | group | domain | anyone"}
        email:   {type: string, description: "email for user/group"}
      required: [file_id, role, type]
    request:
      method: POST
      url: "https://www.googleapis.com/drive/v3/files/{{file_id}}/permissions"
      body:
        role: "{{role}}"
        type: "{{type}}"
        emailAddress: "{{email}}"
    response_extract: "$"
  - name: drive_delete_file
    description: "Permanently delete a Drive file by id. Mutating and irreversible."
    mutating: true
    params:
      type: object
      properties:
        file_id: {type: string}
      required: [file_id]
    request:
      method: DELETE
      url: "https://www.googleapis.com/drive/v3/files/{{file_id}}"
    response_extract: "$"
```

> Note: `drive_move_file`/`drive_create_folder` accept a single parent via query/`parents`. The
> `parents` body field takes a string id here; Drive also accepts an array, but a single-string
> parent is valid for create. Multipart file **upload** and binary **download/export** are DEFERRED
> (out of scope) — do not add them.

- [ ] **Step 4: Run tests + build**

Run: `go test ./internal/connectors/ -run 'TestGoogleDrive|TestOAuthProvider|TestLoad' -count=1 && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/connectors/google_drive.yaml internal/connectors/alias_test.go
git commit -m "feat(connectors): Google Drive actions (list/search/get/folder/copy/rename/move/share/delete)"
```

---

## Task 5: Google Sheets provider + actions

**Files:**
- Create: `internal/connectors/providers/google_sheets.yaml`
- Create: `internal/connectors/connectors/google_sheets.yaml`
- Test: `internal/connectors/alias_test.go`

**Interfaces:**
- Produces: `google_sheets` provider (`auth_parent: google`) + 8 actions, incl. array/nested bodies.

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/alias_test.go`:

```go
func TestGoogleSheetsAppendArrayBody(t *testing.T) {
	r, _ := LoadBundled()
	if _, ok := r.OAuthProvider("google_sheets"); !ok {
		t.Fatal("google_sheets not loaded / parent unresolved")
	}
	a, ok := r.Action("google_sheets", "sheets_append_values")
	if !ok {
		t.Fatal("sheets_append_values missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{
		"spreadsheet_id": "S1", "range": "Sheet1!A1",
		"values": []any{[]any{"a", "b"}, []any{"c", "d"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	rows, ok := got["values"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("values not a 2-row array: %s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestGoogleSheets -count=1`
Expected: FAIL — provider/action not loaded.

- [ ] **Step 3: Create `internal/connectors/providers/google_sheets.yaml`**

```yaml
name: google_sheets
label: Google Sheets
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/spreadsheets
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "Google Sheets reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, also enable the Google Sheets API."
  - "Then click Connect here to authorize Sheets access on the same Google account."
```

- [ ] **Step 4: Create `internal/connectors/connectors/google_sheets.yaml`**

```yaml
provider: google_sheets
actions:
  - name: sheets_get_values
    description: "Read a cell range from a spreadsheet. Read-only."
    mutating: false
    params:
      type: object
      properties:
        spreadsheet_id: {type: string}
        range:          {type: string, description: "A1 notation, e.g. Sheet1!A1:C10"}
      required: [spreadsheet_id, range]
    request:
      method: GET
      url: "https://sheets.googleapis.com/v4/spreadsheets/{{spreadsheet_id}}/values/{{range}}"
    response_extract: "$"
  - name: sheets_append_values
    description: "Append rows to a range. values is an array of row arrays. Safe write."
    mutating: false
    params:
      type: object
      properties:
        spreadsheet_id: {type: string}
        range:          {type: string}
        values:         {type: array, items: {type: array}, description: "rows: [[a,b],[c,d]]"}
      required: [spreadsheet_id, range, values]
    request:
      method: POST
      url: "https://sheets.googleapis.com/v4/spreadsheets/{{spreadsheet_id}}/values/{{range}}:append"
      query: {valueInputOption: "USER_ENTERED"}
      body:
        values: "{{values}}"
    response_extract: "$"
  - name: sheets_update_values
    description: "Overwrite a range with new values (array of row arrays). Mutating (replaces cells)."
    mutating: true
    params:
      type: object
      properties:
        spreadsheet_id: {type: string}
        range:          {type: string}
        values:         {type: array, items: {type: array}}
      required: [spreadsheet_id, range, values]
    request:
      method: PUT
      url: "https://sheets.googleapis.com/v4/spreadsheets/{{spreadsheet_id}}/values/{{range}}"
      query: {valueInputOption: "USER_ENTERED"}
      body:
        values: "{{values}}"
    response_extract: "$"
  - name: sheets_clear_values
    description: "Clear a cell range. Mutating."
    mutating: true
    params:
      type: object
      properties:
        spreadsheet_id: {type: string}
        range:          {type: string}
      required: [spreadsheet_id, range]
    request:
      method: POST
      url: "https://sheets.googleapis.com/v4/spreadsheets/{{spreadsheet_id}}/values/{{range}}:clear"
    response_extract: "$"
  - name: sheets_create_spreadsheet
    description: "Create a new spreadsheet with a title. Safe write."
    mutating: false
    params:
      type: object
      properties:
        title: {type: string}
      required: [title]
    request:
      method: POST
      url: "https://sheets.googleapis.com/v4/spreadsheets"
      body:
        properties:
          title: "{{title}}"
    response_extract: "$"
  - name: sheets_add_sheet
    description: "Add a tab (sheet) to an existing spreadsheet. Safe write."
    mutating: false
    params:
      type: object
      properties:
        spreadsheet_id: {type: string}
        title:          {type: string, description: "new tab name"}
      required: [spreadsheet_id, title]
    request:
      method: POST
      url: "https://sheets.googleapis.com/v4/spreadsheets/{{spreadsheet_id}}:batchUpdate"
      body:
        requests:
          - addSheet:
              properties:
                title: "{{title}}"
    response_extract: "$"
  - name: sheets_get_metadata
    description: "Get spreadsheet metadata (title, sheet/tab list). Read-only."
    mutating: false
    params:
      type: object
      properties:
        spreadsheet_id: {type: string}
      required: [spreadsheet_id]
    request:
      method: GET
      url: "https://sheets.googleapis.com/v4/spreadsheets/{{spreadsheet_id}}"
      query: {fields: "spreadsheetId,properties.title,sheets.properties"}
    response_extract: "$"
  - name: sheets_batch_update
    description: "Apply a raw batchUpdate requests array (advanced formatting/structure changes). Mutating."
    mutating: true
    params:
      type: object
      properties:
        spreadsheet_id: {type: string}
        requests:       {type: array, description: "array of batchUpdate request objects"}
      required: [spreadsheet_id, requests]
    request:
      method: POST
      url: "https://sheets.googleapis.com/v4/spreadsheets/{{spreadsheet_id}}:batchUpdate"
      body:
        requests: "{{requests}}"
    response_extract: "$"
```

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/connectors/ -run 'TestGoogleSheets|TestOAuthProvider|TestLoad' -count=1 && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/providers/google_sheets.yaml internal/connectors/connectors/google_sheets.yaml internal/connectors/alias_test.go
git commit -m "feat(connectors): Google Sheets provider + 8 actions (array/batchUpdate bodies)"
```

---

## Task 6: Google Docs provider + actions

**Files:**
- Create: `internal/connectors/providers/google_docs.yaml`
- Create: `internal/connectors/connectors/google_docs.yaml`
- Test: `internal/connectors/alias_test.go`

**Interfaces:**
- Produces: `google_docs` provider (`auth_parent: google`) + 6 actions (batchUpdate `requests[]`).

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/alias_test.go`:

```go
func TestGoogleDocsInsertText(t *testing.T) {
	r, _ := LoadBundled()
	if _, ok := r.OAuthProvider("google_docs"); !ok {
		t.Fatal("google_docs not loaded")
	}
	a, ok := r.Action("google_docs", "docs_insert_text")
	if !ok {
		t.Fatal("docs_insert_text missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"document_id": "D1", "text": "hello", "index": float64(1)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	reqs, ok := got["requests"].([]any)
	if !ok || len(reqs) != 1 {
		t.Fatalf("requests[] not built: %s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestGoogleDocs -count=1`
Expected: FAIL — provider/action not loaded.

- [ ] **Step 3: Create `internal/connectors/providers/google_docs.yaml`**

```yaml
name: google_docs
label: Google Docs
auth_parent: google
default_scopes:
  - https://www.googleapis.com/auth/documents
setup_url: https://console.cloud.google.com/apis/credentials
setup_steps:
  - "Google Docs reuses your Google (Gmail) OAuth app. Set up Google first on its card above."
  - "In Google Cloud Console, also enable the Google Docs API."
  - "Then click Connect here to authorize Docs access on the same Google account."
```

- [ ] **Step 4: Create `internal/connectors/connectors/google_docs.yaml`**

```yaml
provider: google_docs
actions:
  - name: docs_create_document
    description: "Create a new Google Doc with a title. Safe write."
    mutating: false
    params:
      type: object
      properties:
        title: {type: string}
      required: [title]
    request:
      method: POST
      url: "https://docs.googleapis.com/v1/documents"
      body:
        title: "{{title}}"
    response_extract: "$"
  - name: docs_get_document
    description: "Fetch a Google Doc's content + structure by id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        document_id: {type: string}
      required: [document_id]
    request:
      method: GET
      url: "https://docs.googleapis.com/v1/documents/{{document_id}}"
    response_extract: "$"
  - name: docs_insert_text
    description: "Insert text at a character index in a doc. Mutating (edits the document)."
    mutating: true
    params:
      type: object
      properties:
        document_id: {type: string}
        text:        {type: string}
        index:       {type: integer, description: "1-based insertion index (default 1)"}
      required: [document_id, text]
    request:
      method: POST
      url: "https://docs.googleapis.com/v1/documents/{{document_id}}:batchUpdate"
      body:
        requests:
          - insertText:
              location:
                index: "{{index}}"
              text: "{{text}}"
    response_extract: "$"
  - name: docs_replace_text
    description: "Replace all occurrences of a string in a doc. Mutating."
    mutating: true
    params:
      type: object
      properties:
        document_id: {type: string}
        find:        {type: string}
        replace:     {type: string}
      required: [document_id, find, replace]
    request:
      method: POST
      url: "https://docs.googleapis.com/v1/documents/{{document_id}}:batchUpdate"
      body:
        requests:
          - replaceAllText:
              containsText:
                text: "{{find}}"
                matchCase: true
              replaceText: "{{replace}}"
    response_extract: "$"
  - name: docs_append_text
    description: "Append text to the end of a doc (index 1 fallback if body index unknown; prefer docs_insert_text with a known index). Mutating."
    mutating: true
    params:
      type: object
      properties:
        document_id: {type: string}
        text:        {type: string}
        end_index:   {type: integer, description: "the document body's end index (from docs_get_document)"}
      required: [document_id, text, end_index]
    request:
      method: POST
      url: "https://docs.googleapis.com/v1/documents/{{document_id}}:batchUpdate"
      body:
        requests:
          - insertText:
              location:
                index: "{{end_index}}"
              text: "{{text}}"
    response_extract: "$"
  - name: docs_batch_update
    description: "Apply a raw batchUpdate requests array (advanced doc edits). Mutating."
    mutating: true
    params:
      type: object
      properties:
        document_id: {type: string}
        requests:    {type: array}
      required: [document_id, requests]
    request:
      method: POST
      url: "https://docs.googleapis.com/v1/documents/{{document_id}}:batchUpdate"
      body:
        requests: "{{requests}}"
    response_extract: "$"
```

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/connectors/ -run 'TestGoogleDocs|TestLoad' -count=1 && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/providers/google_docs.yaml internal/connectors/connectors/google_docs.yaml internal/connectors/alias_test.go
git commit -m "feat(connectors): Google Docs provider + 6 actions (batchUpdate requests[])"
```

---

## Task 7: Teams provider + actions

**Files:**
- Create: `internal/connectors/providers/teams.yaml`
- Create: `internal/connectors/connectors/teams.yaml`
- Test: `internal/connectors/alias_test.go`

**Interfaces:**
- Produces: `teams` provider (`auth_parent: outlook`) + 7 actions (nested message body).

- [ ] **Step 1: Write the failing test**

Add to `internal/connectors/alias_test.go`:

```go
func TestTeamsSendMessageBody(t *testing.T) {
	r, _ := LoadBundled()
	oauth, ok := r.OAuthProvider("teams")
	if !ok || oauth.Name != "outlook" {
		t.Fatalf("teams parent = %q, want outlook", oauth.Name)
	}
	a, ok := r.Action("teams", "teams_send_channel_message")
	if !ok {
		t.Fatal("teams_send_channel_message missing")
	}
	_, _, body, _, err := renderRequest(a, map[string]any{"team_id": "T", "channel_id": "C", "content": "hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(body, &got)
	b, ok := got["body"].(map[string]any)
	if !ok || b["content"] != "hi" {
		t.Fatalf("nested body.content missing: %s", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestTeams -count=1`
Expected: FAIL — provider/action not loaded.

- [ ] **Step 3: Create `internal/connectors/providers/teams.yaml`**

```yaml
name: teams
label: Microsoft Teams
auth_parent: outlook
default_scopes:
  - Team.ReadBasic.All
  - Channel.ReadBasic.All
  - ChannelMessage.Read.All
  - ChannelMessage.Send
setup_url: https://portal.azure.com
setup_steps:
  - "Teams reuses your Microsoft (Outlook) OAuth app. Set up Outlook first on its card above."
  - "In the Azure app registration, add the Microsoft Graph delegated permissions listed for this connector."
  - "Then click Connect here to authorize Teams access on the same Microsoft account."
```

- [ ] **Step 4: Create `internal/connectors/connectors/teams.yaml`**

```yaml
provider: teams
actions:
  - name: teams_list_joined_teams
    description: "List the Teams the connected user is a member of (id, displayName). Read-only."
    mutating: false
    params: {type: object, properties: {}}
    request:
      method: GET
      url: "https://graph.microsoft.com/v1.0/me/joinedTeams"
    response_extract: "$.value"
  - name: teams_list_channels
    description: "List channels in a team by team id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        team_id: {type: string}
      required: [team_id]
    request:
      method: GET
      url: "https://graph.microsoft.com/v1.0/teams/{{team_id}}/channels"
    response_extract: "$.value"
  - name: teams_get_channel
    description: "Get one channel's metadata by team id + channel id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        team_id:    {type: string}
        channel_id: {type: string}
      required: [team_id, channel_id]
    request:
      method: GET
      url: "https://graph.microsoft.com/v1.0/teams/{{team_id}}/channels/{{channel_id}}"
    response_extract: "$"
  - name: teams_send_channel_message
    description: "Post a message to a Teams channel. Delivers a real message. Mutating."
    mutating: true
    params:
      type: object
      properties:
        team_id:    {type: string}
        channel_id: {type: string}
        content:    {type: string}
      required: [team_id, channel_id, content]
    request:
      method: POST
      url: "https://graph.microsoft.com/v1.0/teams/{{team_id}}/channels/{{channel_id}}/messages"
      body:
        body:
          content: "{{content}}"
    response_extract: "$"
  - name: teams_list_channel_messages
    description: "List recent messages in a Teams channel. Read-only."
    mutating: false
    params:
      type: object
      properties:
        team_id:    {type: string}
        channel_id: {type: string}
        max:        {type: integer, description: "max messages (default 20)"}
      required: [team_id, channel_id]
    request:
      method: GET
      url: "https://graph.microsoft.com/v1.0/teams/{{team_id}}/channels/{{channel_id}}/messages"
      query: {$top: "{{max}}"}
    response_extract: "$.value"
  - name: teams_reply_to_message
    description: "Reply to a message in a Teams channel. Delivers a real reply. Mutating."
    mutating: true
    params:
      type: object
      properties:
        team_id:    {type: string}
        channel_id: {type: string}
        message_id: {type: string}
        content:    {type: string}
      required: [team_id, channel_id, message_id, content]
    request:
      method: POST
      url: "https://graph.microsoft.com/v1.0/teams/{{team_id}}/channels/{{channel_id}}/messages/{{message_id}}/replies"
      body:
        body:
          content: "{{content}}"
    response_extract: "$"
  - name: teams_list_members
    description: "List members of a team by team id. Read-only."
    mutating: false
    params:
      type: object
      properties:
        team_id: {type: string}
      required: [team_id]
    request:
      method: GET
      url: "https://graph.microsoft.com/v1.0/teams/{{team_id}}/members"
    response_extract: "$.value"
```

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/connectors/ -run 'TestTeams|TestLoad' -count=1 && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/providers/teams.yaml internal/connectors/connectors/teams.yaml internal/connectors/alias_test.go
git commit -m "feat(connectors): Teams provider + 7 actions (nested Graph message body)"
```

---

## Task 8: Child-card UI + static-headers alias tweak

**Files:**
- Modify: `web/handlers_services.go`
- Modify: `web/templates/dashboard/services.html`
- Modify: `internal/connectors/execute.go`
- Test: `internal/connectors/alias_test.go` (Execute static-headers resolution)

**Interfaces:**
- Consumes: `OAuthProvider` (Task 1); the four child providers (Tasks 4-7).
- Produces: child cards on the Services page; `Execute` resolves static headers via `OAuthProvider`.

- [ ] **Step 1: Write the failing test (Execute static-headers via parent)**

Add to `internal/connectors/alias_test.go` (proves a child inherits the parent's static headers — future-proofing; here we assert the resolution path uses OAuthProvider by pointing a child at a parent that has a static header via a hand-built registry-independent check on Execute using the bundled google_drive whose parent google has none, so instead assert no regression + that a Teams call carries no Authorization collision). Simplest robust test: assert Execute still succeeds for a child action against a stub server and sends the Bearer token:

```go
func TestExecuteChildProviderSendsBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Errorf("bearer missing on child call")
		}
		w.Write([]byte(`{"files":[{"id":"f1"}]}`))
	}))
	defer srv.Close()
	reg := testRegistry(t)
	a, _ := reg.Action("google_drive", "drive_list_files")
	a.Request.URL = srv.URL + "/files"
	reg.actions["google_drive"] = []Action{a}
	res, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c1", Provider: "google_drive"}, "drive_list_files", map[string]any{}, false)
	if err != nil {
		t.Fatalf("execute child: %v", err)
	}
	if !strings.Contains(string(res.Data), "f1") {
		t.Fatalf("extract failed: %s", res.Data)
	}
}
```

(Ensure imports `context`, `net/http`, `net/http/httptest`, `strings` are present in the test file — add a second test file `alias_execute_test.go` if `alias_test.go`'s imports get crowded.)

- [ ] **Step 2: Run test to verify it passes or fails**

Run: `go test ./internal/connectors/ -run TestExecuteChildProviderSendsBearer -count=1`
Expected: PASS already (child has no auth block → Bearer path). If it passes, the static-headers tweak below is a no-op safety change; still apply it.

- [ ] **Step 3: Static-headers via `OAuthProvider` in `execute.go`**

Change `execute.go:82` from:

```go
	prov, _ := reg.ProviderByName(conn.Provider) // for static headers (Notion-Version, GitHub Accept)
```

to:

```go
	prov, _ := reg.OAuthProvider(conn.Provider) // static headers + auth config; resolves auth_parent for aliased providers
```

(`applyAuth(req, prov, token)` then uses the parent's auth config — still oauth2 Bearer for google/outlook children, so behavior is unchanged; a child now correctly inherits any parent `static_headers`.)

- [ ] **Step 4: Add child-card view fields + parent-creds resolution in `showServices`**

In `web/handlers_services.go`, add to `serviceProviderView`:

```go
	IsChild       bool
	ParentName    string
	ParentLabel   string
```

In `showServices`, inside the provider loop, after resolving `p` via `ProviderByName`, resolve the OAuth parent and base `HasCreds` on the parent's config:

```go
		isChild, parentName, parentLabel := false, "", ""
		credsProvider := provider
		if op, ok := s.connectors.OAuthProvider(provider); ok && op.Name != provider {
			isChild, parentName, parentLabel = true, op.Name, op.Label
			credsProvider = op.Name
		}
		cfgForCreds, _ := s.db.GetServiceProviderConfig(ctx, w.ID, credsProvider)
```

Set `HasCreds: cfgForCreds != nil` (replacing the current `cfg != nil`), and add
`IsChild: isChild, ParentName: parentName, ParentLabel: parentLabel` to the `serviceProviderView{...}`
literal. (Keep the existing `cfg` fetch for non-child providers, or reuse `cfgForCreds` throughout.)

- [ ] **Step 5: Add the four providers to `availableServiceProviders`**

```go
var availableServiceProviders = []string{"google", "github", "notion", "outlook", "jira", "slack", "openai", "google_drive", "google_sheets", "google_docs", "teams"}
```

- [ ] **Step 6: Child-card template branch**

In `web/templates/dashboard/services.html`, inside the provider-card OAuth region (the `{{else}}`
non-API-key branch from the foundation), wrap with a child check so child providers show a Connect
button that reuses the parent creds instead of the client-id/secret form:

```html
{{if .IsChild}}
  {{if .HasCreds}}
    <form method="POST" action="/dashboard/connectors/services/{{.Name}}/connect" class="flex gap-2 items-center">
      <input type="text" name="account_label" placeholder="Label (e.g. work)" class="input input-bordered input-sm flex-1" required>
      <button type="submit" class="btn btn-sm btn-primary">Connect</button>
    </form>
    <p class="text-sm opacity-70 mt-1">Uses your {{.ParentLabel}} app credentials.</p>
  {{else}}
    <p class="text-sm opacity-70">Set up {{.ParentLabel}} first (on its card above), then Connect here.</p>
  {{end}}
{{else}}
  <!-- existing OAuth creds + connect markup, unchanged -->
{{end}}
```

Keep the connected-accounts list (shared) unchanged.

- [ ] **Step 7: Build + manual smoke check**

Run: `go build -o bin/simple-agents ./cmd/simple-agents && go test ./internal/connectors/... ./web/... -count=1`
Then (manual): `make deploy`, open `/dashboard/connectors/services` — confirm Drive/Sheets/Docs/Teams cards appear; each shows "Uses your Google/Microsoft app credentials" + a Connect button when the parent has creds, or a "set up parent first" note otherwise.
Expected: build clean, tests pass, cards render.

- [ ] **Step 8: Commit**

```bash
git add internal/connectors/execute.go web/handlers_services.go web/templates/dashboard/services.html internal/connectors/alias_test.go
git commit -m "feat(web): child-provider cards reuse parent OAuth creds; Execute static headers via OAuthProvider"
```

---

## Self-Review

**Spec coverage:**
- `auth_parent` field + `OAuthProvider` resolver → Task 1. ✓
- Consent-URL resolution (parent endpoints, child scopes, `include_granted_scopes`) → Task 2. ✓
- Creds lookup under parent (connect + callback) → Task 2. ✓
- Token refresh resolves parent → Task 3. ✓
- Own connection row per service / binding unchanged → inherent (callback stores child `provider`); asserted via Execute child test in Task 8. ✓
- Drive/Sheets/Docs/Teams actions (~8-10 each) → Tasks 4-7. ✓
- Nested/array bodies (Sheets append/batchUpdate, Docs requests[], Teams body.content) → Tasks 5/6/7 tests. ✓
- Multipart upload / binary download deferred → noted in Task 4, not built. ✓
- UI child cards + parent-creds resolution → Task 8. ✓
- Execute static-headers via OAuthProvider → Task 8. ✓
- Unit/rendering only, live deferred → no live steps anywhere. ✓

**Placeholder scan:** No TBD/TODO. The Task 3 note about matching the existing token-endpoint-override mechanism is a concrete instruction (inspect `TestAccessTokenRefreshesNearExpiry`, copy its approach), not deferred work.

**Type consistency:** `AuthParent`, `OAuthProvider`, `ConsentURL(…, scopes []string)`, `serviceProviderView.{IsChild,ParentName,ParentLabel,HasCreds}` used consistently across tasks. `availableServiceProviders` extended once (Task 8). Provider names (`google_drive`/`google_sheets`/`google_docs`/`teams`) and action names consistent between provider files, connector files, and tests.

**Known adaptation point:** Task 3's token-endpoint override in the test must follow whatever mechanism `TestAccessTokenRefreshesNearExpiry` already uses (the plan flags this explicitly); Task 2's `handleOAuthCallback` edit must match the handler's actual variable names (the executor reads the function first).
