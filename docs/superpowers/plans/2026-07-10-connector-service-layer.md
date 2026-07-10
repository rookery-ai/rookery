# Connector Service Layer Implementation Plan (Spec 1: Google/Gmail spine)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a workspace connect one or more Gmail accounts via self-managed OAuth and expose their curated actions to agents as native, typed function-calling tools — reliable reads and writes with no discovery or slug/arg guessing.

> **STATUS (2026-07-10): Phases 1–4 implemented, all unit tests green, server boots.**
> Deviations from this plan (reconciled against the codebase, all intentional):
> - `db.DB` embeds `*sql.DB` → repositories use `d.ExecContext` etc. (not `d.conn.*`).
> - Module path is `github.com/ilijad1/simple-agents`; tests are `db_test`/package-internal
>   using `db.Open(..., "../../migrations")` + `CreateWorkspace`/`CreateAgent` (no `newTestDB`).
> - OAuth consent-URL method is `Provider.ConsentURL` (the `AuthorizeURL` field name collided).
> - OAuth callback route is `/dashboard/connectors/services/callback/:provider` (authed group).
> - `# Connections:` parser does NOT split on `/` (it's the `provider/label` separator).
> - Multi-account tool labels are **slugified** into tool names (`gmail_send_email__My_Work`)
>   to satisfy the provider function-name charset; resolver reverses via the same slug.
> - `Flow.WithConnectors` exposes connections at BUILD time too (post-review fix), so the
>   build-time mutation guard is live — matching spec §5.
> **Remaining: the real-Google E2E (Task 13 step 6) needs the user's Google Cloud OAuth app
> + consent and has NOT been run. The "weak model calls it reliably" claim is unproven until then.**

**Architecture:** A new `internal/connectors` package loads embedded per-provider OAuth configs + action manifests (data files), and exposes `Execute(ctx, conn, action, args)` — validate args against a JSON schema, ensure a fresh access token (refresh + re-encrypt), render the provider request from a template, call it, normalize the result. OAuth tokens live in two new `systemKey`-encrypted tables. The API coder's `hostToolSet` gains one `llm.Tool` per action of the connections an agent is bound to (`agent_connections`, mirroring `agent_skills`), with multi-account tools label-suffixed. A background goroutine refreshes tokens before expiry.

**Tech Stack:** Go, SQLite (`modernc.org/sqlite`), AES-256-GCM (existing `internal/secrets`), Echo v4 web, `internal/llm` tool types, `gopkg.in/yaml.v3` (already a dependency — verify in Task 0).

## Global Constraints

- Spec doc: `docs/superpowers/specs/2026-07-10-connector-service-layer-design.md`.
- Provider scope: **Google/Gmail only** in this plan. Other providers, CLI-coder surface, chat access, and Composio removal are OUT OF SCOPE (Spec 2/3).
- All tokens and client secrets encrypted under the existing 32-byte `secrets` `systemKey` (headless decrypt) — never under a per-workspace master password.
- New migration files: `migrations/005_connectors.up.sql` / `005_connectors.down.sql`. Never edit existing migrations.
- Follow existing repository style in `internal/db/repositories.go` (context-first, `*DB` receiver, `sql.ErrNoRows` → typed nil/`false`).
- `mutating: true` actions MUST be refused when `SA_BUILD_PHASE=generation` (reuse the build-phase constant `composioassets.BuildPhaseGeneration`).
- Tests: `go test ./... -count=1 -timeout 120s` must stay green. No live Google calls in tests — use `httptest`.
- Tool names are lowercase snake_case; multi-account suffix is `__<label>` (double underscore).
- Commit after every task with the shown message.

---

## File Structure

**New files:**
- `migrations/005_connectors.up.sql`, `migrations/005_connectors.down.sql` — schema.
- `internal/secrets/systemkey.go` — generic `EncryptWithSystemKey`/`DecryptWithSystemKey`.
- `internal/db/connectors.go` — `ServiceProviderConfig`, `ServiceConnection`, `AgentConnection` models + repositories.
- `internal/db/connectors_test.go` — repository round-trip tests.
- `internal/connectors/registry.go` — embedded file loading, `Registry`, `Provider`, `Action` types.
- `internal/connectors/providers/google.yaml` — Google OAuth config.
- `internal/connectors/connectors/google.yaml` — Gmail action manifest.
- `internal/connectors/schema.go` — minimal JSON-schema arg validation.
- `internal/connectors/render.go` — request-template rendering + `body_builder`s (`gmail_rfc822`).
- `internal/connectors/execute.go` — `Execute`, `TokenStore` interface, `ConnectorError` taxonomy.
- `internal/connectors/oauth.go` — `AuthorizeURL`, `ExchangeCode`, `Refresh`, `FetchIdentity`.
- `internal/connectors/*_test.go` — unit tests per file.
- `internal/connectors/refresh.go` — background refresh loop.
- `web/handlers_services.go` — services UI + OAuth connect/callback handlers.
- `web/templates/dashboard/services.html` — connections management page.

**Modified files:**
- `internal/coder/hosttools.go` — connector tools in `tools()` + dispatch in `execute()`; new `hostToolSet` fields.
- `internal/coder/api_engine.go:393` — populate the new fields in `buildHostTools`.
- `internal/coder/coder.go` — `WithConnectors(...)` modifier + `Coder` fields.
- `internal/coder/forworkspace.go` — thread a `connectors.Registry` + token store.
- `internal/agentrunner/runner.go` — load `agent_connections`, pass to coder.
- `internal/agentdesigner/*` — parse `# Connections:` header, persist to `agent_connections`.
- `internal/prompts/prompts.go` — `<available_connections>` (design) + `connectedToolsBlock` (runtime).
- `web/server.go` — register routes; wire refresh loop in `serve` (`cmd/simple-agents/main.go`).

---

## Phase 1 — Data foundation (checkpoint 1)

### Task 0: Preflight — confirm deps and migration runner

**Files:** none (read-only checks).

- [ ] **Step 1: Confirm yaml + migration conventions**

Run:
```bash
grep -R "yaml.v3\|yaml.v2" go.mod
ls migrations/ | tail -5
grep -n "func.*[Mm]igrate" internal/db/*.go | head
```
Expected: a yaml dependency is present (if absent, note it — Task 3 adds `gopkg.in/yaml.v3` via `go get`), migrations numbered up to `004_inbox`, and a migration-runner function exists that applies files alphabetically.

- [ ] **Step 2: Confirm build is green before starting**

Run: `go build ./... && go test ./internal/db/... -count=1`
Expected: PASS. If not, stop and report — the tree must be green before Phase 1.

---

### Task 1: Generic system-key crypto helpers

**Files:**
- Create: `internal/secrets/systemkey.go`
- Test: `internal/secrets/systemkey_test.go`

**Interfaces:**
- Produces: `func EncryptWithSystemKey(plaintext string, systemKey []byte) (string, error)` and `func DecryptWithSystemKey(encoded string, systemKey []byte) (string, error)` — base64 `nonce||ciphertext`, identical framing to `EncryptMasterPassword`. Used by every connector token/secret field.

- [ ] **Step 1: Write the failing test**

`internal/secrets/systemkey_test.go`:
```go
package secrets

import (
	"crypto/rand"
	"testing"
)

func TestSystemKeyRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	enc, err := EncryptWithSystemKey("hello token", key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == "hello token" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	got, err := DecryptWithSystemKey(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != "hello token" {
		t.Fatalf("got %q, want %q", got, "hello token")
	}
}

func TestSystemKeyWrongKeyFails(t *testing.T) {
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	rand.Read(k1)
	rand.Read(k2)
	enc, _ := EncryptWithSystemKey("secret", k1)
	if _, err := DecryptWithSystemKey(enc, k2); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestSystemKeyBadLength(t *testing.T) {
	if _, err := EncryptWithSystemKey("x", make([]byte, 16)); err == nil {
		t.Fatal("must reject non-32-byte key")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/secrets/ -run TestSystemKey -v`
Expected: FAIL — `EncryptWithSystemKey` undefined.

- [ ] **Step 3: Write minimal implementation**

`internal/secrets/systemkey.go`:
```go
package secrets

import (
	"encoding/base64"
	"fmt"
)

// EncryptWithSystemKey encrypts plaintext under the 32-byte system key and returns
// base64("nonce||ciphertext"). Same framing as EncryptMasterPassword, but general
// purpose — used for connector OAuth tokens and client secrets that a headless
// background/refresh loop must be able to decrypt without a master password.
func EncryptWithSystemKey(plaintext string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes, got %d", len(systemKey))
	}
	ct, nonce, err := aesGCMEncrypt(systemKey, []byte(plaintext))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonce, ct...)), nil
}

// DecryptWithSystemKey reverses EncryptWithSystemKey.
func DecryptWithSystemKey(encoded string, systemKey []byte) (string, error) {
	if len(systemKey) != 32 {
		return "", fmt.Errorf("system key must be 32 bytes, got %d", len(systemKey))
	}
	combined, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if len(combined) < 12 {
		return "", fmt.Errorf("ciphertext too short")
	}
	pt, err := aesGCMDecrypt(systemKey, combined[12:], combined[:12])
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(pt), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/secrets/ -run TestSystemKey -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/systemkey.go internal/secrets/systemkey_test.go
git commit -m "feat(secrets): generic EncryptWithSystemKey/DecryptWithSystemKey helpers"
```

---

### Task 2: Migration + connector models & repositories

**Files:**
- Create: `migrations/005_connectors.up.sql`, `migrations/005_connectors.down.sql`
- Create: `internal/db/connectors.go`
- Test: `internal/db/connectors_test.go`

**Interfaces:**
- Produces models:
  ```go
  type ServiceProviderConfig struct { ID, WorkspaceID, Provider, EncryptedClientID, EncryptedClientSecret, CreatedAt, UpdatedAt string }
  type ServiceConnection struct {
      ID, WorkspaceID, Provider, AccountLabel, AccountIdentity, Scopes string
      EncryptedAccessToken, EncryptedRefreshToken string
      ExpiresAt, Status, CreatedAt, UpdatedAt string
  }
  type AgentConnection struct { AgentID, ConnectionID string }
  ```
- Produces repositories (all `func (d *DB) ...`):
  - `UpsertServiceProviderConfig(ctx, cfg ServiceProviderConfig) error`
  - `GetServiceProviderConfig(ctx, workspaceID, provider string) (*ServiceProviderConfig, error)` (nil if none)
  - `InsertServiceConnection(ctx, c ServiceConnection) error`
  - `GetServiceConnection(ctx, id string) (*ServiceConnection, error)`
  - `ListServiceConnections(ctx, workspaceID string) ([]ServiceConnection, error)`
  - `UpdateConnectionTokens(ctx, id, encAccess, expiresAt, status string) error`
  - `UpdateConnectionStatus(ctx, id, status string) error`
  - `DeleteServiceConnection(ctx, id string) error`
  - `SetAgentConnections(ctx, agentID string, connIDs []string) error` (replace-all)
  - `ListAgentConnections(ctx, agentID string) ([]ServiceConnection, error)` (join)
  - `ConnectionsNearExpiry(ctx, cutoff string) ([]ServiceConnection, error)` (for the refresh loop)

- [ ] **Step 1: Write the migration**

`migrations/005_connectors.up.sql`:
```sql
-- Self-managed OAuth connector layer. Per-workspace OAuth app credentials and one
-- row per connected account (multi-account = multiple rows). All secret columns are
-- AES-256-GCM encrypted under the system key so the headless refresh loop and cron
-- runs decrypt without a master password.
CREATE TABLE IF NOT EXISTS service_provider_configs (
    id                      TEXT PRIMARY KEY,
    workspace_id            TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    provider                TEXT NOT NULL,
    encrypted_client_id     TEXT NOT NULL,
    encrypted_client_secret TEXT NOT NULL,
    created_at              TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at              TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, provider)
);

CREATE TABLE IF NOT EXISTS service_connections (
    id                       TEXT PRIMARY KEY,
    workspace_id             TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    provider                 TEXT NOT NULL,
    account_label            TEXT NOT NULL,
    account_identity         TEXT NOT NULL DEFAULT '',
    scopes                   TEXT NOT NULL DEFAULT '',
    encrypted_access_token   TEXT NOT NULL DEFAULT '',
    encrypted_refresh_token  TEXT NOT NULL DEFAULT '',
    expires_at               TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL DEFAULT 'ACTIVE',
    created_at               TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at               TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(workspace_id, provider, account_label)
);
CREATE INDEX IF NOT EXISTS idx_svc_conn_ws ON service_connections(workspace_id);
CREATE INDEX IF NOT EXISTS idx_svc_conn_expiry ON service_connections(status, expires_at);

CREATE TABLE IF NOT EXISTS agent_connections (
    agent_id      TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    connection_id TEXT NOT NULL REFERENCES service_connections(id) ON DELETE CASCADE,
    PRIMARY KEY (agent_id, connection_id)
);
```

`migrations/005_connectors.down.sql`:
```sql
DROP TABLE IF EXISTS agent_connections;
DROP TABLE IF EXISTS service_connections;
DROP TABLE IF EXISTS service_provider_configs;
```

- [ ] **Step 2: Write the failing repository test**

`internal/db/connectors_test.go` (use the same test-DB helper other `internal/db` tests use — check `inbox_test.go` for the exact helper name, e.g. `newTestDB(t)`):
```go
package db

import (
	"context"
	"testing"
)

func TestServiceConnectionRoundTrip(t *testing.T) {
	d := newTestDB(t) // same helper as inbox_test.go
	ctx := context.Background()
	ws := seedWorkspace(t, d) // same helper inbox_test.go uses to make a workspace row

	conn := ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		AccountIdentity: "ilija@x.com", Scopes: "gmail.send",
		EncryptedAccessToken: "enc-a", EncryptedRefreshToken: "enc-r",
		ExpiresAt: "2999-01-01T00:00:00Z", Status: "ACTIVE",
	}
	if err := d.InsertServiceConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetServiceConnection(ctx, "c1")
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.AccountIdentity != "ilija@x.com" {
		t.Fatalf("identity: %q", got.AccountIdentity)
	}
	list, err := d.ListServiceConnections(ctx, ws)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}

	if err := d.UpdateConnectionTokens(ctx, "c1", "enc-a2", "3000-01-01T00:00:00Z", "ACTIVE"); err != nil {
		t.Fatal(err)
	}
	got, _ = d.GetServiceConnection(ctx, "c1")
	if got.EncryptedAccessToken != "enc-a2" {
		t.Fatalf("token not updated: %q", got.EncryptedAccessToken)
	}
}

func TestAgentConnectionsReplaceAll(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	ws := seedWorkspace(t, d)
	ag := seedAgent(t, d, ws) // helper analogous to inbox_test.go's agent seeding
	for _, id := range []string{"c1", "c2"} {
		d.InsertServiceConnection(ctx, ServiceConnection{ID: id, WorkspaceID: ws, Provider: "google", AccountLabel: id})
	}
	if err := d.SetAgentConnections(ctx, ag, []string{"c1", "c2"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAgentConnections(ctx, ag, []string{"c1"}); err != nil { // replace-all
		t.Fatal(err)
	}
	got, err := d.ListAgentConnections(ctx, ag)
	if err != nil || len(got) != 1 || got[0].ID != "c1" {
		t.Fatalf("expected only c1, got %v (%v)", got, err)
	}
}
```
> Note: if `newTestDB`/`seedWorkspace`/`seedAgent` don't exist under those names, mirror whatever `internal/db/inbox_test.go` uses — read it first and match.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/db/ -run "TestServiceConnection|TestAgentConnections" -v`
Expected: FAIL — models/methods undefined.

- [ ] **Step 4: Implement models + repositories**

`internal/db/connectors.go`:
```go
package db

import (
	"context"
	"database/sql"
	"strings"
)

type ServiceProviderConfig struct {
	ID, WorkspaceID, Provider              string
	EncryptedClientID, EncryptedClientSecret string
	CreatedAt, UpdatedAt                   string
}

type ServiceConnection struct {
	ID, WorkspaceID, Provider              string
	AccountLabel, AccountIdentity, Scopes  string
	EncryptedAccessToken, EncryptedRefreshToken string
	ExpiresAt, Status, CreatedAt, UpdatedAt string
}

func (d *DB) UpsertServiceProviderConfig(ctx context.Context, c ServiceProviderConfig) error {
	_, err := d.conn.ExecContext(ctx, `
INSERT INTO service_provider_configs (id, workspace_id, provider, encrypted_client_id, encrypted_client_secret)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(workspace_id, provider) DO UPDATE SET
  encrypted_client_id=excluded.encrypted_client_id,
  encrypted_client_secret=excluded.encrypted_client_secret,
  updated_at=datetime('now')`,
		c.ID, c.WorkspaceID, c.Provider, c.EncryptedClientID, c.EncryptedClientSecret)
	return err
}

func (d *DB) GetServiceProviderConfig(ctx context.Context, workspaceID, provider string) (*ServiceProviderConfig, error) {
	var c ServiceProviderConfig
	err := d.conn.QueryRowContext(ctx, `
SELECT id, workspace_id, provider, encrypted_client_id, encrypted_client_secret, created_at, updated_at
FROM service_provider_configs WHERE workspace_id=? AND provider=?`, workspaceID, provider).
		Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.EncryptedClientID, &c.EncryptedClientSecret, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

const svcConnCols = `id, workspace_id, provider, account_label, account_identity, scopes,
	encrypted_access_token, encrypted_refresh_token, expires_at, status, created_at, updated_at`

func scanConn(s interface{ Scan(...any) error }) (ServiceConnection, error) {
	var c ServiceConnection
	err := s.Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.AccountLabel, &c.AccountIdentity, &c.Scopes,
		&c.EncryptedAccessToken, &c.EncryptedRefreshToken, &c.ExpiresAt, &c.Status, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (d *DB) InsertServiceConnection(ctx context.Context, c ServiceConnection) error {
	if c.Status == "" {
		c.Status = "ACTIVE"
	}
	_, err := d.conn.ExecContext(ctx, `
INSERT INTO service_connections (`+svcConnCols+`)
VALUES (?,?,?,?,?,?,?,?,?,?,datetime('now'),datetime('now'))`,
		c.ID, c.WorkspaceID, c.Provider, c.AccountLabel, c.AccountIdentity, c.Scopes,
		c.EncryptedAccessToken, c.EncryptedRefreshToken, c.ExpiresAt, c.Status)
	return err
}

func (d *DB) GetServiceConnection(ctx context.Context, id string) (*ServiceConnection, error) {
	c, err := scanConn(d.conn.QueryRowContext(ctx, `SELECT `+svcConnCols+` FROM service_connections WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *DB) ListServiceConnections(ctx context.Context, workspaceID string) ([]ServiceConnection, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT `+svcConnCols+` FROM service_connections WHERE workspace_id=? ORDER BY provider, account_label`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) UpdateConnectionTokens(ctx context.Context, id, encAccess, expiresAt, status string) error {
	_, err := d.conn.ExecContext(ctx, `
UPDATE service_connections SET encrypted_access_token=?, expires_at=?, status=?, updated_at=datetime('now') WHERE id=?`,
		encAccess, expiresAt, status, id)
	return err
}

func (d *DB) UpdateConnectionStatus(ctx context.Context, id, status string) error {
	_, err := d.conn.ExecContext(ctx, `UPDATE service_connections SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
	return err
}

func (d *DB) DeleteServiceConnection(ctx context.Context, id string) error {
	_, err := d.conn.ExecContext(ctx, `DELETE FROM service_connections WHERE id=?`, id)
	return err
}

func (d *DB) ConnectionsNearExpiry(ctx context.Context, cutoff string) ([]ServiceConnection, error) {
	rows, err := d.conn.QueryContext(ctx, `
SELECT `+svcConnCols+` FROM service_connections
WHERE status='ACTIVE' AND expires_at <> '' AND expires_at <= ? AND encrypted_refresh_token <> ''`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) SetAgentConnections(ctx context.Context, agentID string, connIDs []string) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_connections WHERE agent_id=?`, agentID); err != nil {
		return err
	}
	for _, id := range connIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO agent_connections (agent_id, connection_id) VALUES (?, ?)`, agentID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) ListAgentConnections(ctx context.Context, agentID string) ([]ServiceConnection, error) {
	rows, err := d.conn.QueryContext(ctx, `
SELECT `+prefixCols("sc", svcConnCols)+`
FROM agent_connections ac JOIN service_connections sc ON sc.id = ac.connection_id
WHERE ac.agent_id=? ORDER BY sc.provider, sc.account_label`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceConnection
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// prefixCols qualifies a comma-separated column list with a table alias.
func prefixCols(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
```
> The `*DB` field holding `*sql.DB` may be named `conn` or `db` — check `internal/db/db.go` and match. If a `prefixCols`-style helper already exists, reuse it instead of adding a duplicate.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/db/ -run "TestServiceConnection|TestAgentConnections" -v`
Expected: PASS. Then `go build ./...`.

- [ ] **Step 6: Commit**

```bash
git add migrations/005_connectors.up.sql migrations/005_connectors.down.sql internal/db/connectors.go internal/db/connectors_test.go
git commit -m "feat(db): connector tables + models + repositories (provider configs, connections, agent bindings)"
```

**CHECKPOINT 1** — stop for review. Data foundation is in place and independently tested.

---

## Phase 2 — Connectors engine (checkpoint 2)

### Task 3: Registry + embedded Google files

**Files:**
- Create: `internal/connectors/registry.go`
- Create: `internal/connectors/providers/google.yaml`
- Create: `internal/connectors/connectors/google.yaml`
- Test: `internal/connectors/registry_test.go`

**Interfaces:**
- Produces:
  ```go
  type Provider struct {
      Name          string   `yaml:"name"`
      AuthorizeURL  string   `yaml:"authorize_url"`
      TokenURL      string   `yaml:"token_url"`
      DefaultScopes []string `yaml:"default_scopes"`
      UserinfoURL   string   `yaml:"userinfo_url"`
      IdentityPath  string   `yaml:"identity_path"` // JSON field holding the account identity, e.g. "email"
  }
  type Action struct {
      Name        string          `yaml:"name"`
      Description string          `yaml:"description"`
      Mutating    bool            `yaml:"mutating"`
      Params      json.RawMessage `yaml:"-"`      // compiled JSON schema (from ParamsRaw)
      ParamsRaw   map[string]any  `yaml:"params"`
      Request     RequestTemplate `yaml:"request"`
      ResponseExtract string      `yaml:"response_extract"`
  }
  type RequestTemplate struct {
      Method      string            `yaml:"method"`
      URL         string            `yaml:"url"`
      Query       map[string]string `yaml:"query"`
      BodyBuilder string            `yaml:"body_builder"`
      BodyJSON    map[string]string `yaml:"body_json"`
  }
  type Registry struct { /* unexported maps */ }
  func LoadBundled() (*Registry, error)
  func (r *Registry) ProviderByName(name string) (Provider, bool)
  func (r *Registry) Actions(provider string) []Action
  func (r *Registry) Action(provider, name string) (Action, bool)
  ```

- [ ] **Step 1: Author the Google data files**

`internal/connectors/providers/google.yaml`:
```yaml
name: google
authorize_url: https://accounts.google.com/o/oauth2/v2/auth
token_url: https://oauth2.googleapis.com/token
userinfo_url: https://www.googleapis.com/oauth2/v2/userinfo
identity_path: email
default_scopes:
  - https://www.googleapis.com/auth/gmail.readonly
  - https://www.googleapis.com/auth/gmail.compose
  - https://www.googleapis.com/auth/gmail.send
  - https://www.googleapis.com/auth/userinfo.email
```

`internal/connectors/connectors/google.yaml`:
```yaml
provider: google
actions:
  - name: gmail_search
    description: "Search the connected Gmail account and return matching message ids + snippets. Use for 'find/search my email', 'do I have mail about X'. Read-only."
    mutating: false
    params:
      type: object
      properties:
        query: {type: string, description: "Gmail search query, e.g. 'from:boss newer_than:7d'"}
        max:   {type: integer, description: "max messages (default 10)"}
      required: [query]
    request:
      method: GET
      url: "https://gmail.googleapis.com/gmail/v1/users/me/messages"
      query:
        q: "{{query}}"
        maxResults: "{{max}}"
    response_extract: "$.messages"
  - name: gmail_get_message
    description: "Fetch one Gmail message's headers + body by id (from gmail_search). Read-only."
    mutating: false
    params:
      type: object
      properties:
        id: {type: string, description: "message id"}
      required: [id]
    request:
      method: GET
      url: "https://gmail.googleapis.com/gmail/v1/users/me/messages/{{id}}"
    response_extract: "$"
  - name: gmail_create_draft
    description: "Create a Gmail DRAFT (not sent) in the connected account. Use for 'draft/prepare an email for review'. A write, but safe — nothing is delivered."
    mutating: false
    params:
      type: object
      properties:
        to:      {type: string}
        subject: {type: string}
        body:    {type: string}
      required: [to, body]
    request:
      method: POST
      url: "https://gmail.googleapis.com/gmail/v1/users/me/drafts"
      body_builder: gmail_draft
    response_extract: "$.id"
  - name: gmail_send_email
    description: "SEND an email from the connected Gmail account. Use only when the user wants to actually send (not draft). Delivers real mail."
    mutating: true
    params:
      type: object
      properties:
        to:      {type: string}
        subject: {type: string}
        body:    {type: string}
      required: [to, body]
    request:
      method: POST
      url: "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
      body_builder: gmail_rfc822
    response_extract: "$.id"
```

- [ ] **Step 2: Write the failing test**

`internal/connectors/registry_test.go`:
```go
package connectors

import "testing"

func TestLoadBundledGoogle(t *testing.T) {
	r, err := LoadBundled()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p, ok := r.ProviderByName("google")
	if !ok || p.TokenURL == "" || len(p.DefaultScopes) == 0 {
		t.Fatalf("google provider not loaded: %+v", p)
	}
	acts := r.Actions("google")
	if len(acts) != 4 {
		t.Fatalf("want 4 gmail actions, got %d", len(acts))
	}
	send, ok := r.Action("google", "gmail_send_email")
	if !ok || !send.Mutating {
		t.Fatalf("gmail_send_email must be mutating: %+v", send)
	}
	if len(send.Params) == 0 {
		t.Fatal("params schema must be compiled to JSON")
	}
	if draft, _ := r.Action("google", "gmail_create_draft"); draft.Mutating {
		t.Fatal("gmail_create_draft must NOT be mutating")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/connectors/ -run TestLoadBundled -v`
Expected: FAIL — package/`LoadBundled` undefined.

- [ ] **Step 4: Implement the registry**

`internal/connectors/registry.go`:
```go
// Package connectors owns the self-managed-OAuth connector layer: per-provider
// OAuth configs + curated action manifests (embedded data files), and the typed
// Execute path agents call. Adding a service = adding a providers/<p>.yaml and a
// connectors/<p>.yaml; no Go changes.
package connectors

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"

	"gopkg.in/yaml.v3"
)

//go:embed providers/*.yaml connectors/*.yaml
var files embed.FS

type Provider struct {
	Name          string   `yaml:"name"`
	AuthorizeURL  string   `yaml:"authorize_url"`
	TokenURL      string   `yaml:"token_url"`
	UserinfoURL   string   `yaml:"userinfo_url"`
	IdentityPath  string   `yaml:"identity_path"`
	DefaultScopes []string `yaml:"default_scopes"`
}

type RequestTemplate struct {
	Method      string            `yaml:"method"`
	URL         string            `yaml:"url"`
	Query       map[string]string `yaml:"query"`
	BodyBuilder string            `yaml:"body_builder"`
	BodyJSON    map[string]string `yaml:"body_json"`
}

type Action struct {
	Name            string          `yaml:"name"`
	Description     string          `yaml:"description"`
	Mutating        bool            `yaml:"mutating"`
	ParamsRaw       map[string]any  `yaml:"params"`
	Request         RequestTemplate `yaml:"request"`
	ResponseExtract string          `yaml:"response_extract"`
	Params          json.RawMessage `yaml:"-"` // compiled from ParamsRaw at load
}

type manifest struct {
	Provider string   `yaml:"provider"`
	Actions  []Action `yaml:"actions"`
}

type Registry struct {
	providers map[string]Provider
	actions   map[string][]Action // provider -> actions
}

func LoadBundled() (*Registry, error) {
	r := &Registry{providers: map[string]Provider{}, actions: map[string][]Action{}}

	pents, _ := files.ReadDir("providers")
	for _, e := range pents {
		b, err := files.ReadFile(path.Join("providers", e.Name()))
		if err != nil {
			return nil, err
		}
		var p Provider
		if err := yaml.Unmarshal(b, &p); err != nil {
			return nil, fmt.Errorf("provider %s: %w", e.Name(), err)
		}
		r.providers[p.Name] = p
	}

	cents, _ := files.ReadDir("connectors")
	for _, e := range cents {
		b, err := files.ReadFile(path.Join("connectors", e.Name()))
		if err != nil {
			return nil, err
		}
		var m manifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", e.Name(), err)
		}
		for i := range m.Actions {
			raw, err := json.Marshal(m.Actions[i].ParamsRaw)
			if err != nil {
				return nil, fmt.Errorf("%s.%s params: %w", m.Provider, m.Actions[i].Name, err)
			}
			m.Actions[i].Params = raw
		}
		r.actions[m.Provider] = m.Actions
	}
	return r, nil
}

func (r *Registry) ProviderByName(name string) (Provider, bool) { p, ok := r.providers[name]; return p, ok }
func (r *Registry) Actions(provider string) []Action           { return r.actions[provider] }
func (r *Registry) Action(provider, name string) (Action, bool) {
	for _, a := range r.actions[provider] {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}
```
> If `gopkg.in/yaml.v3` is not yet a dependency (Task 0), run `go get gopkg.in/yaml.v3 && go mod tidy` as part of this step.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/connectors/ -run TestLoadBundled -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connectors/registry.go internal/connectors/providers internal/connectors/connectors internal/connectors/registry_test.go go.mod go.sum
git commit -m "feat(connectors): embedded registry + Google/Gmail provider config and action manifest"
```

---

### Task 4: Arg schema validation

**Files:**
- Create: `internal/connectors/schema.go`
- Test: `internal/connectors/schema_test.go`

**Interfaces:**
- Produces: `func validateArgs(schema json.RawMessage, args map[string]any) error` — checks `required` present and top-level property types (`string`/`integer`/`boolean`/`number`). Returns an error naming the offending field. (Deliberately minimal — no external JSON-schema lib; the manifests only use flat object schemas.)

- [ ] **Step 1: Write the failing test**

`internal/connectors/schema_test.go`:
```go
package connectors

import "testing"

func TestValidateArgs(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"to":{"type":"string"},"max":{"type":"integer"}},"required":["to"]}`)
	if err := validateArgs(schema, map[string]any{"to": "x@y.com", "max": 3}); err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if err := validateArgs(schema, map[string]any{"max": 3}); err == nil {
		t.Fatal("missing required 'to' must fail")
	}
	if err := validateArgs(schema, map[string]any{"to": 5}); err == nil {
		t.Fatal("wrong type for 'to' must fail")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/connectors/ -run TestValidateArgs -v`
Expected: FAIL — `validateArgs` undefined.

- [ ] **Step 3: Implement**

`internal/connectors/schema.go`:
```go
package connectors

import (
	"encoding/json"
	"fmt"
)

type propSchema struct {
	Type string `json:"type"`
}
type objSchema struct {
	Properties map[string]propSchema `json:"properties"`
	Required   []string              `json:"required"`
}

func validateArgs(schema json.RawMessage, args map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	var s objSchema
	if err := json.Unmarshal(schema, &s); err != nil {
		return fmt.Errorf("bad action schema: %w", err)
	}
	for _, req := range s.Required {
		if v, ok := args[req]; !ok || v == nil {
			return fmt.Errorf("missing required argument %q", req)
		}
	}
	for name, val := range args {
		p, ok := s.Properties[name]
		if !ok || val == nil {
			continue
		}
		if !typeOK(p.Type, val) {
			return fmt.Errorf("argument %q must be %s", name, p.Type)
		}
	}
	return nil
}

func typeOK(t string, v any) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer", "number":
		switch v.(type) {
		case float64, int, int64, json.Number:
			return true
		}
		return false
	default:
		return true
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/connectors/ -run TestValidateArgs -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/schema.go internal/connectors/schema_test.go
git commit -m "feat(connectors): minimal arg-schema validation for action params"
```

---

### Task 5: Request rendering + body builders

**Files:**
- Create: `internal/connectors/render.go`
- Test: `internal/connectors/render_test.go`

**Interfaces:**
- Produces:
  - `func renderRequest(a Action, args map[string]any) (method, url string, body []byte, contentType string, err error)` — substitutes `{{name}}` placeholders in URL + query (URL-encoding query values), drops query keys whose value resolved empty, and dispatches `body_builder`.
  - Body builders `gmail_draft` and `gmail_rfc822` (RFC-822 message, base64url, wrapped in the Gmail JSON envelope). Registered in a `map[string]func(args map[string]any) (body []byte, contentType string, err error)`.

- [ ] **Step 1: Write the failing test**

`internal/connectors/render_test.go`:
```go
package connectors

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderQuerySubstitution(t *testing.T) {
	a := Action{Request: RequestTemplate{
		Method: "GET",
		URL:    "https://api/messages",
		Query:  map[string]string{"q": "{{query}}", "maxResults": "{{max}}"},
	}}
	_, u, _, _, err := renderRequest(a, map[string]any{"query": "from:boss", "max": float64(5)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "q=from%3Aboss") || !strings.Contains(u, "maxResults=5") {
		t.Fatalf("bad url: %s", u)
	}
}

func TestRenderDropsEmptyQuery(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "GET", URL: "https://api/m", Query: map[string]string{"q": "{{query}}", "maxResults": "{{max}}"}}}
	_, u, _, _, _ := renderRequest(a, map[string]any{"query": "hi"}) // max omitted
	if strings.Contains(u, "maxResults") {
		t.Fatalf("empty query param should be dropped: %s", u)
	}
}

func TestRenderGmailRFC822(t *testing.T) {
	a := Action{Request: RequestTemplate{Method: "POST", URL: "https://api/send", BodyBuilder: "gmail_rfc822"}}
	_, _, body, ct, err := renderRequest(a, map[string]any{"to": "a@b.com", "subject": "Hi", "body": "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type: %s", ct)
	}
	var env struct{ Raw string `json:"raw"` }
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	dec, err := base64.URLEncoding.DecodeString(env.Raw)
	if err != nil {
		t.Fatalf("raw not base64url: %v", err)
	}
	if !strings.Contains(string(dec), "To: a@b.com") || !strings.Contains(string(dec), "Hello") {
		t.Fatalf("rfc822 missing fields: %s", dec)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/connectors/ -run TestRender -v`
Expected: FAIL — `renderRequest` undefined.

- [ ] **Step 3: Implement**

`internal/connectors/render.go`:
```go
package connectors

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var placeholderRE = regexp.MustCompile(`\{\{(\w+)\}\}`)

// asString renders an arg value for substitution (integers without a trailing .0).
func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func subst(tmpl string, args map[string]any) string {
	return placeholderRE.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := placeholderRE.FindStringSubmatch(m)[1]
		return asString(args[name])
	})
}

type bodyBuilder func(args map[string]any) (body []byte, contentType string, err error)

var bodyBuilders = map[string]bodyBuilder{
	"gmail_rfc822": gmailRFC822,
	"gmail_draft":  gmailDraft,
}

func renderRequest(a Action, args map[string]any) (method, u string, body []byte, contentType string, err error) {
	method = a.Request.Method
	if method == "" {
		method = "GET"
	}
	u = subst(a.Request.URL, args)
	if len(a.Request.Query) > 0 {
		q := url.Values{}
		for k, tmpl := range a.Request.Query {
			val := subst(tmpl, args)
			if val == "" {
				continue // drop unresolved/empty params
			}
			q.Set(k, val)
		}
		if enc := q.Encode(); enc != "" {
			u += "?" + enc
		}
	}
	switch {
	case a.Request.BodyBuilder != "":
		bb, ok := bodyBuilders[a.Request.BodyBuilder]
		if !ok {
			return "", "", nil, "", fmt.Errorf("unknown body_builder %q", a.Request.BodyBuilder)
		}
		body, contentType, err = bb(args)
	case len(a.Request.BodyJSON) > 0:
		m := map[string]any{}
		for k, tmpl := range a.Request.BodyJSON {
			m[k] = subst(tmpl, args)
		}
		body, err = json.Marshal(m)
		contentType = "application/json"
	}
	return method, u, body, contentType, err
}

func rfc822(args map[string]any) string {
	var b strings.Builder
	b.WriteString("To: " + asString(args["to"]) + "\r\n")
	if s := asString(args["subject"]); s != "" {
		b.WriteString("Subject: " + s + "\r\n")
	}
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(asString(args["body"]))
	return b.String()
}

func gmailRFC822(args map[string]any) ([]byte, string, error) {
	raw := base64.URLEncoding.EncodeToString([]byte(rfc822(args)))
	body, err := json.Marshal(map[string]string{"raw": raw})
	return body, "application/json", err
}

func gmailDraft(args map[string]any) ([]byte, string, error) {
	raw := base64.URLEncoding.EncodeToString([]byte(rfc822(args)))
	body, err := json.Marshal(map[string]any{"message": map[string]string{"raw": raw}})
	return body, "application/json", err
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/connectors/ -run TestRender -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/render.go internal/connectors/render_test.go
git commit -m "feat(connectors): request-template rendering + Gmail body builders"
```

---

### Task 6: OAuth client (authorize/exchange/refresh/identity)

**Files:**
- Create: `internal/connectors/oauth.go`
- Test: `internal/connectors/oauth_test.go`

**Interfaces:**
- Produces (methods on `Provider`, HTTP client injectable for tests):
  ```go
  type OAuthClient struct { HTTP *http.Client }
  type TokenSet struct { AccessToken, RefreshToken string; ExpiresIn int }
  func (p Provider) AuthorizeURL(clientID, redirectURI, state string) string
  func (c OAuthClient) ExchangeCode(ctx, p Provider, clientID, clientSecret, code, redirectURI string) (TokenSet, error)
  func (c OAuthClient) Refresh(ctx, p Provider, clientID, clientSecret, refreshToken string) (TokenSet, error)
  func (c OAuthClient) FetchIdentity(ctx, p Provider, accessToken string) (string, error)
  ```

- [ ] **Step 1: Write the failing test** (httptest fake for token + userinfo)

`internal/connectors/oauth_test.go`:
```go
package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangeAndIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","expires_in":3600}`))
		case strings.HasSuffix(r.URL.Path, "/userinfo"):
			if r.Header.Get("Authorization") != "Bearer AT" {
				t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
			}
			w.Write([]byte(`{"email":"ilija@x.com"}`))
		}
	}))
	defer srv.Close()

	p := Provider{Name: "google", TokenURL: srv.URL + "/token", UserinfoURL: srv.URL + "/userinfo", IdentityPath: "email"}
	c := OAuthClient{HTTP: srv.Client()}
	ts, err := c.ExchangeCode(context.Background(), p, "cid", "csec", "code123", "https://cb")
	if err != nil || ts.AccessToken != "AT" || ts.RefreshToken != "RT" || ts.ExpiresIn != 3600 {
		t.Fatalf("exchange: %+v %v", ts, err)
	}
	id, err := c.FetchIdentity(context.Background(), p, "AT")
	if err != nil || id != "ilija@x.com" {
		t.Fatalf("identity: %q %v", id, err)
	}
}

func TestAuthorizeURL(t *testing.T) {
	p := Provider{AuthorizeURL: "https://accounts/auth", DefaultScopes: []string{"a", "b"}}
	u := p.AuthorizeURL("cid", "https://cb", "state123")
	for _, want := range []string{"client_id=cid", "state=state123", "access_type=offline", "prompt=consent", "scope=a+b", "response_type=code"} {
		if !strings.Contains(u, want) {
			t.Fatalf("authorize url missing %q: %s", want, u)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/connectors/ -run "TestExchange|TestAuthorize" -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

`internal/connectors/oauth.go`:
```go
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type OAuthClient struct{ HTTP *http.Client }

func (c OAuthClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

type TokenSet struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

func (p Provider) AuthorizeURL(clientID, redirectURI, state string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(p.DefaultScopes, " "))
	q.Set("state", state)
	q.Set("access_type", "offline") // request a refresh token
	q.Set("prompt", "consent")      // force refresh-token issuance on re-consent
	return p.AuthorizeURL_join(q)
}

func (p Provider) AuthorizeURL_join(q url.Values) string {
	sep := "?"
	if strings.Contains(p.AuthorizeURL, "?") {
		sep = "&"
	}
	return p.AuthorizeURL + sep + q.Encode()
}

func (c OAuthClient) tokenRequest(ctx context.Context, p Provider, form url.Values) (TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return TokenSet{}, &ConnectorError{Kind: KindNetwork, Msg: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return TokenSet{}, &ConnectorError{Kind: KindAuth, Msg: fmt.Sprintf("token endpoint %d: %s", resp.StatusCode, string(b))}
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return TokenSet{}, err
	}
	return TokenSet{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ExpiresIn: out.ExpiresIn}, nil
}

func (c OAuthClient) ExchangeCode(ctx context.Context, p Provider, clientID, clientSecret, code, redirectURI string) (TokenSet, error) {
	f := url.Values{}
	f.Set("grant_type", "authorization_code")
	f.Set("code", code)
	f.Set("client_id", clientID)
	f.Set("client_secret", clientSecret)
	f.Set("redirect_uri", redirectURI)
	return c.tokenRequest(ctx, p, f)
}

func (c OAuthClient) Refresh(ctx context.Context, p Provider, clientID, clientSecret, refreshToken string) (TokenSet, error) {
	f := url.Values{}
	f.Set("grant_type", "refresh_token")
	f.Set("refresh_token", refreshToken)
	f.Set("client_id", clientID)
	f.Set("client_secret", clientSecret)
	ts, err := c.tokenRequest(ctx, p, f)
	if err != nil {
		return ts, err
	}
	if ts.RefreshToken == "" { // Google omits it on refresh — keep the existing one
		ts.RefreshToken = refreshToken
	}
	return ts, nil
}

func (c OAuthClient) FetchIdentity(ctx context.Context, p Provider, accessToken string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", p.UserinfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http().Do(req)
	if err != nil {
		return "", &ConnectorError{Kind: KindNetwork, Msg: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", &ConnectorError{Kind: KindAuth, Msg: fmt.Sprintf("userinfo %d: %s", resp.StatusCode, string(b))}
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	if v, ok := m[p.IdentityPath].(string); ok {
		return v, nil
	}
	return "", nil
}
```
> `ConnectorError`/`Kind*` come from Task 7. Implement Task 7's `execute.go` error types first if the compiler complains, or land Tasks 6+7 together — they share the error type. (Recommended: write `ConnectorError` at the top of Task 7's file before running Task 6's test; the tests are independent.)

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/connectors/ -run "TestExchange|TestAuthorize" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/oauth.go internal/connectors/oauth_test.go
git commit -m "feat(connectors): self-managed OAuth client (authorize/exchange/refresh/identity)"
```

---

### Task 7: Execute — the typed choke point

**Files:**
- Create: `internal/connectors/execute.go`
- Test: `internal/connectors/execute_test.go`

**Interfaces:**
- Produces:
  ```go
  type Kind int
  const (KindOther Kind = iota; KindAuth; KindRateLimit; KindServer; KindNetwork; KindBuildBlocked; KindBadArgs)
  type ConnectorError struct { Kind Kind; Msg string }
  func (e *ConnectorError) Error() string

  // TokenStore lets Execute read/refresh/persist a connection's tokens without importing db.
  type TokenStore interface {
      // AccessToken returns a currently-valid bearer token for conn, refreshing +
      // persisting if near expiry. Returns ErrNeedsReauth-flavored ConnectorError on failure.
      AccessToken(ctx context.Context, conn ConnRef) (string, error)
  }
  type ConnRef struct { ID, Provider, AccountIdentity string }

  type Result struct { Data json.RawMessage } // normalized payload (response_extract applied)

  func Execute(ctx context.Context, reg *Registry, store TokenStore, http *http.Client,
      conn ConnRef, actionName string, args map[string]any, buildPhase bool) (Result, error)
  ```
- `Execute`: look up action → `validateArgs` → if `a.Mutating && buildPhase` return `KindBuildBlocked` → `store.AccessToken` → `renderRequest` → HTTP call with `Authorization: Bearer` (retry one transient 429/5xx) → map ≥400 to a typed `ConnectorError` → apply `response_extract` (support `$` and `$.field`) → `Result`.

- [ ] **Step 1: Write the failing test** (fake provider API + fake TokenStore)

`internal/connectors/execute_test.go`:
```go
package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeStore struct{ tok string }

func (f fakeStore) AccessToken(_ context.Context, _ ConnRef) (string, error) { return f.tok, nil }

func testRegistry(t *testing.T) *Registry {
	r, err := LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExecuteReadRewritesURLAndBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Errorf("bearer missing")
		}
		w.Write([]byte(`{"messages":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	reg := testRegistry(t)
	// Point the gmail_search action at the test server by cloning the action with a rewritten URL.
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = srv.URL + "/messages"
	reg.actions["google"] = []Action{a}

	res, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{ID: "c1", Provider: "google"}, "gmail_search", map[string]any{"query": "hi"}, false)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(string(res.Data), "m1") {
		t.Fatalf("extract failed: %s", res.Data)
	}
}

func TestExecuteBuildBlocksMutating(t *testing.T) {
	reg := testRegistry(t)
	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, http.DefaultClient,
		ConnRef{ID: "c1", Provider: "google"}, "gmail_send_email",
		map[string]any{"to": "a@b.com", "body": "hi"}, true) // buildPhase = true
	ce, ok := err.(*ConnectorError)
	if !ok || ce.Kind != KindBuildBlocked {
		t.Fatalf("expected KindBuildBlocked, got %v", err)
	}
}

func TestExecuteBadArgs(t *testing.T) {
	reg := testRegistry(t)
	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, http.DefaultClient,
		ConnRef{Provider: "google"}, "gmail_search", map[string]any{}, false) // missing query
	if ce, ok := err.(*ConnectorError); !ok || ce.Kind != KindBadArgs {
		t.Fatalf("expected KindBadArgs, got %v", err)
	}
}

func TestExecuteMapsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid creds"}`))
	}))
	defer srv.Close()
	reg := testRegistry(t)
	a, _ := reg.Action("google", "gmail_search")
	a.Request.URL = srv.URL + "/m"
	reg.actions["google"] = []Action{a}
	_, err := Execute(context.Background(), reg, fakeStore{tok: "AT"}, srv.Client(),
		ConnRef{Provider: "google"}, "gmail_search", map[string]any{"query": "x"}, false)
	if ce, ok := err.(*ConnectorError); !ok || ce.Kind != KindAuth {
		t.Fatalf("expected KindAuth, got %v", err)
	}
	_ = json.RawMessage{}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/connectors/ -run TestExecute -v`
Expected: FAIL — `Execute`/`ConnectorError` undefined.

- [ ] **Step 3: Implement**

`internal/connectors/execute.go`:
```go
package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Kind int

const (
	KindOther Kind = iota
	KindAuth
	KindRateLimit
	KindServer
	KindNetwork
	KindBuildBlocked
	KindBadArgs
	KindNeedsReauth
)

type ConnectorError struct {
	Kind Kind
	Msg  string
}

func (e *ConnectorError) Error() string { return e.Msg }

type ConnRef struct {
	ID, Provider, AccountIdentity string
}

type TokenStore interface {
	AccessToken(ctx context.Context, conn ConnRef) (string, error)
}

type Result struct {
	Data json.RawMessage
}

func Execute(ctx context.Context, reg *Registry, store TokenStore, client *http.Client,
	conn ConnRef, actionName string, args map[string]any, buildPhase bool) (Result, error) {

	a, ok := reg.Action(conn.Provider, actionName)
	if !ok {
		return Result{}, &ConnectorError{KindOther, fmt.Sprintf("unknown action %q for %s", actionName, conn.Provider)}
	}
	if err := validateArgs(a.Params, args); err != nil {
		return Result{}, &ConnectorError{KindBadArgs, err.Error()}
	}
	if a.Mutating && buildPhase {
		return Result{}, &ConnectorError{KindBuildBlocked,
			fmt.Sprintf("build-time guard: %q sends/modifies for real and is blocked during generation — it will run when the agent executes for real", actionName)}
	}
	token, err := store.AccessToken(ctx, conn)
	if err != nil {
		return Result{}, err // TokenStore returns a typed ConnectorError
	}
	method, u, body, contentType, err := renderRequest(a, args)
	if err != nil {
		return Result{}, &ConnectorError{KindOther, err.Error()}
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	var raw []byte
	var status int
	for attempt := 0; attempt < 2; attempt++ {
		req, e := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
		if e != nil {
			return Result{}, &ConnectorError{KindOther, e.Error()}
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, e := client.Do(req)
		if e != nil {
			if attempt == 0 {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			return Result{}, &ConnectorError{KindNetwork, e.Error()}
		}
		raw, _ = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		status = resp.StatusCode
		if (status == 429 || status >= 500) && attempt == 0 {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		break
	}

	if status >= 400 {
		return Result{}, mapHTTPError(status, raw)
	}
	return Result{Data: extract(a.ResponseExtract, raw)}, nil
}

func mapHTTPError(status int, raw []byte) *ConnectorError {
	msg := fmt.Sprintf("provider returned %d: %s", status, truncate(string(raw), 500))
	switch {
	case status == 401 || status == 403:
		return &ConnectorError{KindAuth, msg}
	case status == 429:
		return &ConnectorError{KindRateLimit, msg}
	case status >= 500:
		return &ConnectorError{KindServer, msg}
	default:
		return &ConnectorError{KindOther, msg}
	}
}

// extract applies a tiny subset of JSONPath: "$" (whole body) or "$.field" (top-level key).
func extract(path string, raw []byte) json.RawMessage {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return raw
	}
	if strings.HasPrefix(path, "$.") {
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) == nil {
			if v, ok := m[strings.TrimPrefix(path, "$.")]; ok {
				return v
			}
		}
	}
	return raw
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/connectors/ -count=1 -v`
Expected: PASS (all connectors tests).

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/execute.go internal/connectors/execute_test.go
git commit -m "feat(connectors): typed Execute choke point (validate, build-guard, call, normalize errors)"
```

**CHECKPOINT 2** — stop for review. The engine is complete and fully unit-tested against httptest; no wiring yet.

---

## Phase 3 — OAuth web subsystem + token store + refresh (checkpoint 3)

### Task 8: DB-backed TokenStore

**Files:**
- Create: `internal/connectors/dbstore.go`
- Test: `internal/connectors/dbstore_test.go`

**Interfaces:**
- Consumes: `db` repositories (Task 2), `secrets.DecryptWithSystemKey/EncryptWithSystemKey` (Task 1), `OAuthClient` (Task 6), `Registry` (Task 3).
- Produces:
  ```go
  type DBTokenStore struct {
      DB        *db.DB
      SystemKey []byte
      Reg       *Registry
      OAuth     OAuthClient
      Now       func() time.Time // injectable; nil → time.Now
  }
  func (s *DBTokenStore) AccessToken(ctx, conn ConnRef) (string, error) // implements TokenStore
  ```
  Reads the connection row; if `expires_at` within a 2-min skew, refreshes via `OAuth.Refresh` using the workspace's decrypted client creds, re-encrypts + persists the new access token + expiry, and on refresh failure sets status `NEEDS_REAUTH` and returns a `KindNeedsReauth` error.

- [ ] **Step 1: Write the failing test** (fake token endpoint; near-expiry row triggers refresh)

`internal/connectors/dbstore_test.go`:
```go
package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"simple-agents/internal/db"      // adjust to the module path (see go.mod)
	"simple-agents/internal/secrets"
)

func mkKey() []byte { k := make([]byte, 32); for i := range k { k[i] = byte(i) }; return k }

func TestAccessTokenRefreshesNearExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"NEW","expires_in":3600}`))
	}))
	defer srv.Close()

	d := db.NewTestDB(t)                 // use the db package's exported test helper (add if missing)
	ws := db.SeedWorkspace(t, d)
	key := mkKey()
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(context.Background(), db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: encID, EncryptedClientSecret: encSec})
	encRefresh, _ := secrets.EncryptWithSystemKey("RT", key)
	encOld, _ := secrets.EncryptWithSystemKey("OLD", key)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	d.InsertServiceConnection(context.Background(), db.ServiceConnection{
		ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "work",
		EncryptedAccessToken: encOld, EncryptedRefreshToken: encRefresh, ExpiresAt: past, Status: "ACTIVE"})

	reg := testRegistry(t)
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}

	tok, err := store.AccessToken(context.Background(), ConnRef{ID: "c1", Provider: "google"})
	if err != nil || tok != "NEW" {
		t.Fatalf("want refreshed NEW, got %q %v", tok, err)
	}
	got, _ := d.GetServiceConnection(context.Background(), "c1")
	if dec, _ := secrets.DecryptWithSystemKey(got.EncryptedAccessToken, key); dec != "NEW" {
		t.Fatalf("new token not persisted: %q", dec)
	}
}
```
> Adjust the import path (`simple-agents/...`) to the real module path from `go.mod`. If `db.NewTestDB`/`db.SeedWorkspace` are currently unexported, export thin wrappers (or add exported test helpers) so this cross-package test can build.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/connectors/ -run TestAccessToken -v`
Expected: FAIL — `DBTokenStore` undefined.

- [ ] **Step 3: Implement**

`internal/connectors/dbstore.go`:
```go
package connectors

import (
	"context"
	"fmt"
	"time"

	"simple-agents/internal/db"      // adjust to module path
	"simple-agents/internal/secrets"
)

const expirySkew = 2 * time.Minute

type DBTokenStore struct {
	DB        *db.DB
	SystemKey []byte
	Reg       *Registry
	OAuth     OAuthClient
	Now       func() time.Time
}

func (s *DBTokenStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *DBTokenStore) AccessToken(ctx context.Context, conn ConnRef) (string, error) {
	row, err := s.DB.GetServiceConnection(ctx, conn.ID)
	if err != nil || row == nil {
		return "", &ConnectorError{KindOther, fmt.Sprintf("connection %s not found", conn.ID)}
	}
	if row.Status != "ACTIVE" {
		return "", &ConnectorError{KindNeedsReauth, fmt.Sprintf("connection %s is %s — reconnect it in Settings → Connectors", row.AccountLabel, row.Status)}
	}
	if !s.expired(row.ExpiresAt) {
		return secrets.DecryptWithSystemKey(row.EncryptedAccessToken, s.SystemKey)
	}
	return s.refresh(ctx, row)
}

func (s *DBTokenStore) expired(expiresAt string) bool {
	if expiresAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return s.now().Add(expirySkew).After(t)
}

func (s *DBTokenStore) refresh(ctx context.Context, row *db.ServiceConnection) (string, error) {
	prov, ok := s.Reg.ProviderByName(row.Provider)
	if !ok {
		return "", &ConnectorError{KindOther, "unknown provider " + row.Provider}
	}
	cfg, err := s.DB.GetServiceProviderConfig(ctx, row.WorkspaceID, row.Provider)
	if err != nil || cfg == nil {
		return "", &ConnectorError{KindNeedsReauth, "missing OAuth app credentials for " + row.Provider}
	}
	cid, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientID, s.SystemKey)
	csec, _ := secrets.DecryptWithSystemKey(cfg.EncryptedClientSecret, s.SystemKey)
	refreshTok, err := secrets.DecryptWithSystemKey(row.EncryptedRefreshToken, s.SystemKey)
	if err != nil || refreshTok == "" {
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		return "", &ConnectorError{KindNeedsReauth, "no refresh token — reconnect " + row.AccountLabel}
	}
	ts, err := s.OAuth.Refresh(ctx, prov, cid, csec, refreshTok)
	if err != nil {
		s.DB.UpdateConnectionStatus(ctx, row.ID, "NEEDS_REAUTH")
		return "", &ConnectorError{KindNeedsReauth, "token refresh failed for " + row.AccountLabel + "; reconnect it (" + err.Error() + ")"}
	}
	encNew, _ := secrets.EncryptWithSystemKey(ts.AccessToken, s.SystemKey)
	exp := s.now().Add(time.Duration(ts.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	if err := s.DB.UpdateConnectionTokens(ctx, row.ID, encNew, exp, "ACTIVE"); err != nil {
		return "", &ConnectorError{KindOther, err.Error()}
	}
	return ts.AccessToken, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/connectors/ -run TestAccessToken -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/connectors/dbstore.go internal/connectors/dbstore_test.go
git commit -m "feat(connectors): DB-backed TokenStore with headless refresh + NEEDS_REAUTH handling"
```

---

### Task 9: OAuth web handlers (creds, connect, callback, delete)

**Files:**
- Create: `web/handlers_services.go`
- Create: `web/templates/dashboard/services.html`
- Modify: `web/server.go` (register routes)
- Test: `web/handlers_services_test.go`

**Interfaces:**
- Consumes: `Server` fields — the pattern used by existing handlers (`c.Get("workspace").(*db.Workspace)`, `s.db`, the system key accessor used by `secretsLookup`, the registry loaded once at startup and stored on `Server`, e.g. `s.connectors *connectors.Registry`).
- Produces routes:
  ```
  GET  /dashboard/connectors/services
  POST /dashboard/connectors/services/:provider/creds
  POST /dashboard/connectors/services/:provider/connect
  GET  /oauth/callback/:provider
  POST /dashboard/connectors/services/:id/delete
  ```
- Produces `func signState(secret []byte, payload string) string` / `verifyState` (HMAC-SHA256, base64, with an embedded unix-timestamp checked against a 10-min TTL).

- [ ] **Step 1: Write the failing test for state signing**

`web/handlers_services_test.go`:
```go
package web

import (
	"testing"
	"time"
)

func TestStateSignVerify(t *testing.T) {
	secret := []byte("system-key-or-any-secret-32bytes")
	payload := "ws1|google|work|nonce"
	tok := signState(secret, payload, time.Now())
	got, ok := verifyState(secret, tok, time.Now())
	if !ok || got != payload {
		t.Fatalf("verify: %q %v", got, ok)
	}
	if _, ok := verifyState(secret, tok, time.Now().Add(11*time.Minute)); ok {
		t.Fatal("expired state must fail")
	}
	if _, ok := verifyState(secret, tok+"x", time.Now()); ok {
		t.Fatal("tampered state must fail")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./web/ -run TestStateSign -v`
Expected: FAIL — `signState` undefined.

- [ ] **Step 3: Implement handlers + state helpers**

`web/handlers_services.go` — implement:
- `signState(secret []byte, payload string, now time.Time) string`: `base64( ts "|" payload "|" hex(hmac(ts|payload)) )`.
- `verifyState(secret []byte, tok string, now time.Time) (payload string, ok bool)`: constant-time HMAC compare + TTL check (10 min).
- `showServices(c)`: load `s.db.ListServiceConnections(ctx, ws.ID)` + which providers have creds; render `services.html`.
- `handleSaveProviderCreds(c)`: read `client_id`/`client_secret` form fields, `secrets.EncryptWithSystemKey(...)`, `UpsertServiceProviderConfig`. Redirect back.
- `handleConnect(c)`: require creds exist; build `redirectURI = publicBaseURL(c) + "/oauth/callback/" + provider`; `state = signState(systemKey, ws.ID+"|"+provider+"|"+label+"|"+nonce, now)`; `http.Redirect` to `provider.AuthorizeURL(clientID, redirectURI, state)`. `label` from the form (default the value the callback later overwrites via identity).
- `handleOAuthCallback(c)`: `verifyState` (splits payload → ws, provider, label); decrypt client creds; `OAuthClient{}.ExchangeCode(...)`; `FetchIdentity(...)`; encrypt tokens; compute expiry; `InsertServiceConnection` (status ACTIVE; label; identity; scopes = provider.DefaultScopes joined). On any error render an error page. Redirect to `/dashboard/connectors/services`.
- `handleDeleteConnection(c)`: `DeleteServiceConnection`.
- `publicBaseURL(c echo.Context) string`: `os.Getenv("SA_PUBLIC_URL")` or `c.Scheme() + "://" + c.Request().Host`.

Full code for the state helpers (the tested unit):
```go
package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const stateTTL = 10 * time.Minute

func stateMAC(secret []byte, msg string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func signState(secret []byte, payload string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	msg := ts + "|" + payload
	tok := msg + "|" + stateMAC(secret, msg)
	return base64.RawURLEncoding.EncodeToString([]byte(tok))
}

func verifyState(secret []byte, tok string, now time.Time) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(b), "|", 3)
	if len(parts) != 3 {
		return "", false
	}
	ts, payload, mac := parts[0], parts[1], parts[2]
	if !hmac.Equal([]byte(mac), []byte(stateMAC(secret, ts+"|"+payload))) {
		return "", false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(sec, 0)) > stateTTL {
		return "", false
	}
	return payload, true
}

var _ = fmt.Sprintf
```
> Implement the HTTP handlers alongside these helpers in the same file, following the exact `Server`-method + `c.Render` pattern in `web/handlers_connectors.go`. Register the routes in `web/server.go` next to the existing `/connectors` block, and register `GET /oauth/callback/:provider` at the top level (it runs post-login but is not under `/dashboard` middleware that requires an active workspace only if the session still has it — keep it under the same auth group as `/dashboard`).

- [ ] **Step 4: Run to verify it passes + build**

Run: `go test ./web/ -run TestStateSign -v && go build ./...`
Expected: PASS + clean build. (Handler behavior is exercised manually in Task 12; the unit test covers the security-critical state signing.)

- [ ] **Step 5: Commit**

```bash
git add web/handlers_services.go web/templates/dashboard/services.html web/server.go web/handlers_services_test.go
git commit -m "feat(web): self-managed OAuth connect/callback + signed state + connections UI"
```

---

### Task 10: Background refresh loop + startup wiring

**Files:**
- Create: `internal/connectors/refresh.go`
- Test: `internal/connectors/refresh_test.go`
- Modify: `cmd/simple-agents/main.go` (start the loop in `serve`; store `*Registry` on the web `Server`)

**Interfaces:**
- Produces:
  ```go
  func RunRefreshLoop(ctx context.Context, store *DBTokenStore, interval time.Duration)
  func refreshDue(ctx context.Context, store *DBTokenStore) int // one pass; returns count refreshed (for tests)
  ```
  `refreshDue` calls `store.DB.ConnectionsNearExpiry(cutoff = now+10min)` and, for each, invokes `store.AccessToken` (which refreshes + persists as a side effect).

- [ ] **Step 1: Write the failing test**

`internal/connectors/refresh_test.go`:
```go
package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"simple-agents/internal/db"
	"simple-agents/internal/secrets"
)

func TestRefreshDueRefreshesSoonExpiring(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"REFRESHED","expires_in":3600}`))
	}))
	defer srv.Close()
	d := db.NewTestDB(t)
	ws := db.SeedWorkspace(t, d)
	key := mkKey()
	encID, _ := secrets.EncryptWithSystemKey("cid", key)
	encSec, _ := secrets.EncryptWithSystemKey("csec", key)
	d.UpsertServiceProviderConfig(context.Background(), db.ServiceProviderConfig{ID: "pc1", WorkspaceID: ws, Provider: "google", EncryptedClientID: encID, EncryptedClientSecret: encSec})
	encR, _ := secrets.EncryptWithSystemKey("RT", key)
	soon := time.Now().Add(3 * time.Minute).UTC().Format(time.RFC3339) // within the 10-min cutoff
	d.InsertServiceConnection(context.Background(), db.ServiceConnection{ID: "c1", WorkspaceID: ws, Provider: "google", AccountLabel: "w", EncryptedRefreshToken: encR, ExpiresAt: soon, Status: "ACTIVE"})

	reg := testRegistry(t)
	reg.providers["google"] = Provider{Name: "google", TokenURL: srv.URL + "/token"}
	store := &DBTokenStore{DB: d, SystemKey: key, Reg: reg, OAuth: OAuthClient{HTTP: srv.Client()}}
	if n := refreshDue(context.Background(), store); n != 1 {
		t.Fatalf("want 1 refreshed, got %d", n)
	}
	got, _ := d.GetServiceConnection(context.Background(), "c1")
	if dec, _ := secrets.DecryptWithSystemKey(got.EncryptedAccessToken, key); dec != "REFRESHED" {
		t.Fatalf("not refreshed: %q", dec)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/connectors/ -run TestRefreshDue -v`
Expected: FAIL — `refreshDue` undefined.

- [ ] **Step 3: Implement**

`internal/connectors/refresh.go`:
```go
package connectors

import (
	"context"
	"log/slog"
	"time"
)

const refreshCutoff = 10 * time.Minute

func refreshDue(ctx context.Context, store *DBTokenStore) int {
	cutoff := store.now().Add(refreshCutoff).UTC().Format(time.RFC3339)
	rows, err := store.DB.ConnectionsNearExpiry(ctx, cutoff)
	if err != nil {
		slog.Warn("connectors refresh: query failed", "err", err)
		return 0
	}
	n := 0
	for _, r := range rows {
		if _, err := store.AccessToken(ctx, ConnRef{ID: r.ID, Provider: r.Provider}); err != nil {
			slog.Warn("connectors refresh: failed", "conn", r.ID, "err", err)
			continue
		}
		n++
	}
	return n
}

func RunRefreshLoop(ctx context.Context, store *DBTokenStore, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refreshDue(ctx, store)
		}
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/connectors/ -run TestRefreshDue -v`
Expected: PASS.

- [ ] **Step 5: Wire startup**

In `cmd/simple-agents/main.go` `serve` (near where the scheduler/reminder goroutines start), after the DB + system key are available:
```go
connReg, err := connectors.LoadBundled()
if err != nil {
	return fmt.Errorf("load connectors: %w", err)
}
connStore := &connectors.DBTokenStore{DB: database, SystemKey: systemKey, Reg: connReg, OAuth: connectors.OAuthClient{}}
go connectors.RunRefreshLoop(ctx, connStore, 5*time.Minute)
```
Pass `connReg` (and `connStore`) into the web `Server` constructor and store them on the struct (Task 9 reads `s.connectors`). Build.

- [ ] **Step 6: Run full build + tests, commit**

Run: `go build ./... && go test ./internal/connectors/... ./web/... -count=1`
Expected: PASS.
```bash
git add internal/connectors/refresh.go internal/connectors/refresh_test.go cmd/simple-agents/main.go web/server.go
git commit -m "feat(connectors): background token-refresh loop + serve wiring"
```

**CHECKPOINT 3** — stop for review. A workspace can connect a Gmail account end-to-end and tokens self-refresh; agents don't see the tools yet.

---

## Phase 4 — Agent binding + typed-tool exposure (checkpoint 4)

### Task 11: `# Connections:` parsing + persistence + designer prompt

**Files:**
- Create: `internal/agentdesigner/parse_connections.go`
- Test: `internal/agentdesigner/parse_connections_test.go`
- Modify: `internal/agentdesigner/*` save paths (`saveAndFinish`/`updateAndFinish`) to call `db.SetAgentConnections`
- Modify: `internal/prompts/prompts.go` — add `<available_connections>` to the design prompt

**Interfaces:**
- Produces: `func parseConnectionsLine(agentMD string, available []db.ServiceConnection) []string` — returns the connection IDs the agent declared, matched case-insensitively by `provider` and/or `account_label`/`account_identity`. Mirrors `parseSkillsLine`'s tolerance. Returns `nil` when no `# Connections:` header exists; a present-but-`none` header returns an empty non-nil slice.

- [ ] **Step 1: Write the failing test**

`internal/agentdesigner/parse_connections_test.go`:
```go
package agentdesigner

import (
	"testing"

	"simple-agents/internal/db"
)

func avail() []db.ServiceConnection {
	return []db.ServiceConnection{
		{ID: "c1", Provider: "google", AccountLabel: "work", AccountIdentity: "work@x.com"},
		{ID: "c2", Provider: "google", AccountLabel: "personal", AccountIdentity: "me@x.com"},
	}
}

func TestParseConnectionsByLabel(t *testing.T) {
	md := "# Connections: google/work\n\nBody"
	got := parseConnectionsLine(md, avail())
	if len(got) != 1 || got[0] != "c1" {
		t.Fatalf("got %v", got)
	}
}

func TestParseConnectionsNoneHeader(t *testing.T) {
	got := parseConnectionsLine("# Connections: none\n", avail())
	if got == nil || len(got) != 0 {
		t.Fatalf("none header must be non-nil empty, got %v", got)
	}
}

func TestParseConnectionsMissingHeader(t *testing.T) {
	if got := parseConnectionsLine("no header here", avail()); got != nil {
		t.Fatalf("missing header must be nil, got %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/agentdesigner/ -run TestParseConnections -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement** (model on the existing `parseSkillsLine` in the same package — read it and mirror its heading tolerance)

`internal/agentdesigner/parse_connections.go`:
```go
package agentdesigner

import (
	"regexp"
	"strings"

	"simple-agents/internal/db"
)

var connHeaderRE = regexp.MustCompile(`(?im)^#{1,6}\s*connections\s*[:\-=]\s*(.+)$`)

func parseConnectionsLine(agentMD string, available []db.ServiceConnection) []string {
	m := connHeaderRE.FindStringSubmatch(agentMD)
	if m == nil {
		return nil // no header at all → "declared none/unknown"
	}
	rest := strings.TrimSpace(m[1])
	if rest == "" || strings.EqualFold(rest, "none") {
		return []string{} // present-but-empty → explicitly none
	}
	tokens := regexp.MustCompile(`[,;|+&/\n]`).Split(rest, -1)
	var out []string
	seen := map[string]bool{}
	for _, tok := range tokens {
		t := strings.Trim(strings.TrimSpace(tok), "`'\"")
		if t == "" {
			continue
		}
		for _, conn := range available {
			if matchesConn(t, conn) && !seen[conn.ID] {
				out = append(out, conn.ID)
				seen[conn.ID] = true
			}
		}
	}
	return out
}

func matchesConn(tok string, c db.ServiceConnection) bool {
	tok = strings.ToLower(tok)
	// Accept "provider", "provider/label", "label", or the account identity.
	cands := []string{
		strings.ToLower(c.Provider + "/" + c.AccountLabel),
		strings.ToLower(c.AccountLabel),
		strings.ToLower(c.AccountIdentity),
	}
	for _, cnd := range cands {
		if cnd != "" && cnd == tok {
			return true
		}
	}
	// bare provider name matches all of that provider's connections
	return tok == strings.ToLower(c.Provider)
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/agentdesigner/ -run TestParseConnections -v`
Expected: PASS.

- [ ] **Step 5: Wire persistence + prompt**

- In `saveAndFinish`/`updateAndFinish` (wherever `parseSkillsLine` result is persisted via `SetAgentSkills`-equivalent), add: load `available := db.ListServiceConnections(ctx, workspaceID)`, `ids := parseConnectionsLine(agentMD, available)`, and if `ids != nil` call `db.SetAgentConnections(ctx, agentID, ids)`.
- In `internal/prompts`: add a `ConnectionRef{Provider, Label, Identity string}` type and an `<available_connections>` block builder listing each with a one-line "declare as `provider/label`" instruction and the `# Connections:` header contract (mirror the `<available_skills>` / `# Skills:` wording). Thread the workspace's connections into the design params where `Skills` is threaded (via a `WithConnLister` closure analogous to `WithKBLister`).

- [ ] **Step 6: Build + test + commit**

Run: `go build ./... && go test ./internal/agentdesigner/... -count=1`
Expected: PASS.
```bash
git add internal/agentdesigner/parse_connections.go internal/agentdesigner/parse_connections_test.go internal/agentdesigner/*.go internal/prompts/prompts.go
git commit -m "feat(designer): parse # Connections: header, persist agent_connections, prompt block"
```

---

### Task 12: Expose connector tools in the API engine

**Files:**
- Modify: `internal/coder/coder.go` (fields + `WithConnectors`)
- Modify: `internal/coder/forworkspace.go` (accept registry + store)
- Modify: `internal/coder/api_engine.go:393` (`buildHostTools` populates fields)
- Modify: `internal/coder/hosttools.go` (`tools()` append connector tools; `execute()` dispatch)
- Modify: `internal/agentrunner/runner.go` (load bound connections, `WithConnectors`)
- Test: `internal/coder/connectortools_test.go`

**Interfaces:**
- Consumes: `connectors.Registry`, `connectors.TokenStore`, `connectors.Execute`, `db.ServiceConnection`.
- Produces on `Coder`:
  ```go
  func (c *Coder) WithConnectors(reg *connectors.Registry, store connectors.TokenStore, bound []connectors.BoundConn) *Coder
  // in package connectors, a UI/runner-facing view:
  type BoundConn struct { ID, Provider, AccountLabel, AccountIdentity string }
  ```
- New `hostToolSet` fields: `connReg *connectors.Registry`, `connStore connectors.TokenStore`, `boundConns []connectors.BoundConn`.
- New: `func (h *hostToolSet) connectorTools() []llm.Tool` and dispatch in `execute()`.
- **Tool naming:** for a provider with exactly one bound connection, the tool name is the bare action name (`gmail_send_email`). With ≥2 bound connections of the same provider, each action's tool is suffixed `__<label>` (`gmail_send_email__work`). The dispatcher parses the suffix back to the target connection.

- [ ] **Step 1: Write the failing test**

`internal/coder/connectortools_test.go`:
```go
package coder

import (
	"strings"
	"testing"

	"simple-agents/internal/connectors"
)

func loadReg(t *testing.T) *connectors.Registry {
	r, err := connectors.LoadBundled()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConnectorToolsSingleAccountBareNames(t *testing.T) {
	h := &hostToolSet{
		connReg:    loadReg(t),
		boundConns: []connectors.BoundConn{{ID: "c1", Provider: "google", AccountLabel: "work"}},
	}
	names := map[string]bool{}
	for _, tl := range h.connectorTools() {
		names[tl.Name] = true
	}
	if !names["gmail_send_email"] || !names["gmail_search"] {
		t.Fatalf("expected bare gmail tools, got %v", names)
	}
	for n := range names {
		if strings.Contains(n, "__") {
			t.Fatalf("single account must not suffix: %s", n)
		}
	}
}

func TestConnectorToolsMultiAccountSuffixed(t *testing.T) {
	h := &hostToolSet{
		connReg: loadReg(t),
		boundConns: []connectors.BoundConn{
			{ID: "c1", Provider: "google", AccountLabel: "work"},
			{ID: "c2", Provider: "google", AccountLabel: "personal"},
		},
	}
	names := map[string]bool{}
	for _, tl := range h.connectorTools() {
		names[tl.Name] = true
	}
	if !names["gmail_send_email__work"] || !names["gmail_send_email__personal"] {
		t.Fatalf("expected suffixed tools, got %v", names)
	}
	if names["gmail_send_email"] {
		t.Fatalf("bare name must not appear when multi-account")
	}
}

func TestResolveConnectorCall(t *testing.T) {
	h := &hostToolSet{
		connReg: loadReg(t),
		boundConns: []connectors.BoundConn{
			{ID: "c1", Provider: "google", AccountLabel: "work"},
			{ID: "c2", Provider: "google", AccountLabel: "personal"},
		},
	}
	conn, action, ok := h.resolveConnectorTool("gmail_send_email__personal")
	if !ok || conn.ID != "c2" || action != "gmail_send_email" {
		t.Fatalf("resolve: %+v %q %v", conn, action, ok)
	}
	if _, _, ok := h.resolveConnectorTool("read_file"); ok {
		t.Fatal("non-connector tool must not resolve")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/coder/ -run "TestConnectorTools|TestResolveConnector" -v`
Expected: FAIL — fields/methods undefined.

- [ ] **Step 3: Implement**

Add to `connectors` package (`registry.go` or a small `view.go`):
```go
type BoundConn struct {
	ID, Provider, AccountLabel, AccountIdentity string
}
```

Add to `hostToolSet` struct (`hosttools.go`):
```go
connReg    *connectors.Registry
connStore  connectors.TokenStore
boundConns []connectors.BoundConn
```

`internal/coder/connectortools.go` (new):
```go
package coder

import (
	"context"
	"encoding/json"
	"strings"

	"simple-agents/internal/connectors"
	"simple-agents/internal/llm"
)

// providerCounts returns how many bound connections each provider has (for suffixing).
func (h *hostToolSet) providerCounts() map[string]int {
	m := map[string]int{}
	for _, b := range h.boundConns {
		m[b.Provider]++
	}
	return m
}

func (h *hostToolSet) toolName(action string, b connectors.BoundConn, counts map[string]int) string {
	if counts[b.Provider] > 1 {
		return action + "__" + b.AccountLabel
	}
	return action
}

func (h *hostToolSet) connectorTools() []llm.Tool {
	if h.connReg == nil || len(h.boundConns) == 0 {
		return nil
	}
	counts := h.providerCounts()
	var out []llm.Tool
	for _, b := range h.boundConns {
		for _, a := range h.connReg.Actions(b.Provider) {
			desc := a.Description
			if counts[b.Provider] > 1 {
				desc = "[" + b.AccountLabel + " / " + b.AccountIdentity + "] " + desc
			}
			out = append(out, llm.Tool{
				Name:        h.toolName(a.Name, b, counts),
				Description: desc,
				Parameters:  a.Params,
			})
		}
	}
	return out
}

// resolveConnectorTool maps a tool name back to (connection, base action).
func (h *hostToolSet) resolveConnectorTool(name string) (connectors.BoundConn, string, bool) {
	if h.connReg == nil {
		return connectors.BoundConn{}, "", false
	}
	counts := h.providerCounts()
	base, label := name, ""
	if i := strings.LastIndex(name, "__"); i >= 0 {
		base, label = name[:i], name[i+2:]
	}
	for _, b := range h.boundConns {
		if _, ok := h.connReg.Action(b.Provider, base); !ok {
			continue
		}
		if counts[b.Provider] > 1 {
			if b.AccountLabel == label {
				return b, base, true
			}
			continue
		}
		if label == "" {
			return b, base, true
		}
	}
	return connectors.BoundConn{}, "", false
}

func (h *hostToolSet) executeConnectorTool(ctx context.Context, name string, args map[string]any) string {
	b, action, ok := h.resolveConnectorTool(name)
	if !ok {
		return "error: unknown connector tool " + name
	}
	res, err := connectors.Execute(ctx, h.connReg, h.connStore, h.httpClient,
		connectors.ConnRef{ID: b.ID, Provider: b.Provider, AccountIdentity: b.AccountIdentity},
		action, args, h.verifyBuild)
	if err != nil {
		return "error: " + err.Error() // ConnectorError messages are already actionable
	}
	if len(res.Data) == 0 {
		return "(action succeeded; no data returned)"
	}
	return string(res.Data)
}

var _ = json.Marshal
```

Wire into existing methods:
- `hosttools.go` `tools()`: at the end, `tools = append(tools, h.connectorTools()...)`.
- `hosttools.go` `execute()`: before the `default:` case, add
  ```go
  default:
      if _, _, ok := h.resolveConnectorTool(call.Name); ok {
          var args map[string]any
          _ = json.Unmarshal(call.Args, &args)
          return h.executeConnectorTool(ctx, call.Name, args)
      }
      // ...existing unknown-tool handling
  ```
  (Match the real switch structure — connector tools are recognized by `resolveConnectorTool`.)
- `coder.go`: add fields `connReg *connectors.Registry`, `connStore connectors.TokenStore`, `boundConns []connectors.BoundConn`, and:
  ```go
  func (c *Coder) WithConnectors(reg *connectors.Registry, store connectors.TokenStore, bound []connectors.BoundConn) *Coder {
      c2 := *c
      c2.connReg, c2.connStore, c2.boundConns = reg, store, bound
      return &c2
  }
  ```
- `api_engine.go` `buildHostTools`: set `connReg: c.connReg, connStore: c.connStore, boundConns: c.boundConns` on the returned `hostToolSet`.
- `agentrunner/runner.go` `runCoderAgent`: load `conns, _ := db.ListAgentConnections(ctx, agentID)`, map to `[]connectors.BoundConn`, and add `.WithConnectors(reg, store, bound)` to the coder builder chain (thread `reg`/`store` into the runner the same way the coder factory is injected — `Runner.WithConnectors(reg, store)` set in `main.go`).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/coder/ -run "TestConnectorTools|TestResolveConnector" -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Full build + suite**

Run: `go build ./... && go test ./... -count=1 -timeout 120s`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/coder/ internal/agentrunner/runner.go cmd/simple-agents/main.go
git commit -m "feat(coder): expose bound connections as native typed tools in the API engine"
```

---

### Task 13: Runtime prompt block + manual E2E

**Files:**
- Modify: `internal/prompts/prompts.go` — `connectedToolsBlock(bound []ConnectionRef)` naming the available typed tools per account (no discovery spec). Injected into `BuildCoderPrompt` when the agent has bound connections.
- Modify: `internal/agentrunner/runner.go` — pass bound connections into the prompt builder.

- [ ] **Step 1: Write the failing test**

`internal/prompts/connected_tools_test.go`:
```go
package prompts

import (
	"strings"
	"testing"
)

func TestConnectedToolsBlock(t *testing.T) {
	block := connectedToolsBlock([]ConnectionRef{{Provider: "google", Label: "work", Identity: "w@x.com"}})
	if !strings.Contains(block, "work") || !strings.Contains(block, "google") {
		t.Fatalf("block missing account: %s", block)
	}
	if strings.Contains(strings.ToLower(block), "discover") {
		t.Fatalf("runtime block must NOT tell the agent to discover — tools are the interface")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/prompts/ -run TestConnectedTools -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement** `ConnectionRef` + `connectedToolsBlock` (a short block: "You have native tools for these connected accounts. Call them directly with typed arguments — do not write scripts or discover slugs. With multiple accounts of one service, the tool name ends in `__<label>`.") and inject it in `BuildCoderPrompt` where `composioRuntimeNote()` is added, gated on `len(bound) > 0`.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/prompts/ -run TestConnectedTools -v`
Expected: PASS. Then `go build ./... && go test ./... -count=1`.

- [ ] **Step 5: Commit**

```bash
git add internal/prompts/prompts.go internal/prompts/connected_tools_test.go internal/agentrunner/runner.go
git commit -m "feat(prompts): runtime connected-tools block (typed tools, no discovery)"
```

- [ ] **Step 6: Manual end-to-end verification** (documented; not automated)

1. `make deploy`; open `/dashboard/connectors/services`.
2. In a Google Cloud project, create an OAuth client (Web) with redirect
   `https://<host>/oauth/callback/google`; enable Gmail API; add yourself as a test user.
3. Save the client_id/secret in the services page; click Connect → complete consent.
   Verify a `service_connections` row appears with your email as `account_identity`,
   status ACTIVE.
4. Create an agent (API coder on a weak model, e.g. the mistral profile) whose task is
   "search my gmail for the latest invoice and draft a reply for review". Confirm the
   generated AGENT.md has `# Connections: google/<label>`.
5. Run the agent; confirm it calls `gmail_search` then `gmail_create_draft` (check the
   run log tool milestones), a real draft appears in Gmail, and a `[CHAT]` summary is
   delivered — with NO Composio discovery in the log.
6. Confirm build-time: during generation the send-shaped path is build-blocked (no real
   send), but create-draft runs.
7. Record results in the run log / a note; if all pass, this completes Spec 1.

**CHECKPOINT 4** — Spec 1 complete: connect → refresh → typed tools → agent read+write, proven on Google/Gmail.

---

## Self-Review

**Spec coverage:**
- Native typed tools → Task 12 (exposure), Task 13 (runtime prompt). ✓
- Self-managed OAuth (flow/tokens/refresh) → Tasks 6, 8, 9, 10. ✓
- Extensible by data file → Task 3 (registry + embedded yaml); adding a provider needs only new yaml + creds. ✓
- Multi-account first-class → Task 2 (schema/UNIQUE), Task 12 (suffixed tools). ✓
- Read + write with build guard → Task 3 (manifest `mutating`), Task 7 (`KindBuildBlocked`). ✓
- Agent binding mirrors skills → Task 2 (`agent_connections`), Task 11 (`# Connections:`). ✓
- `systemKey` encryption / headless refresh → Task 1, Task 8, Task 10. ✓
- Error taxonomy surfaced to model → Task 7 (`ConnectorError`), Task 8 (`NEEDS_REAUTH`). ✓
- Pilot = Google/Gmail only; no other providers/CLI/chat/Composio-removal → scope held. ✓

**Placeholder scan:** No "TBD"/"handle errors"-style gaps; every code step carries real code. The handler *behavior* in Task 9 is described step-by-step with full code for the security-critical unit (state signing) and explicit wiring instructions pointing at the concrete existing pattern (`handlers_connectors.go`) — acceptable because it's boilerplate-shaped and manually verified in Task 13.

**Type consistency:** `ConnectorError`/`Kind*` defined once (Task 7), consumed by Tasks 6/8/12. `BoundConn` defined Task 12, `ConnRef`/`TokenStore`/`Result`/`Execute` Task 7. `ServiceConnection`/repositories Task 2, consumed by Tasks 8/10/11/12. `signState`/`verifyState` Task 9. Names consistent across tasks.

**Known cross-package test-helper dependency:** Tasks 2/8/10 rely on `internal/db` test helpers (`NewTestDB`/`SeedWorkspace`/`SeedAgent`). Task 2 Step 2 says to read `internal/db/inbox_test.go` and match the real names, exporting thin wrappers if a cross-package test (Tasks 8/10) needs them. This is the one place the executing engineer must reconcile with reality before the test compiles — flagged, not hidden.
