# UI Redesign Sub-plan 1: API Layer + Parity Inventory — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the complete `/api/v1` JSON API over the existing services so the new SPA (sub-plans 2-6) has a full backend, with a route-parity test as the merge gate.

**Architecture:** New `web/api_*.go` files add thin JSON handlers as `*Server` methods next to the existing template handlers (which keep working until sub-plan 6 deletes them). Shared business logic is extracted into helpers — never duplicated. Auth = same cookie sessions, new JSON-returning middleware variants. Spec: `docs/superpowers/specs/2026-07-16-ui-redesign-design.md` §3, §12.

**Tech Stack:** Go, Echo v4, gorilla/sessions, SQLite (`internal/db`), httptest for tests.

## Global Constraints

- All work happens on branch `ui-redesign` (created in Task 1). Never commit to `main`.
- Every new endpoint lives under `/api/v1`, is a method on `*Server`, in a `web/api_*.go` file.
- Error responses use the envelope `{"error":{"code":"<snake_case>","message":"<human text>"}}` via the `jsonErr` helper. Success responses are bare DTOs (no wrapper).
- **Documented exception:** endpoints that are ALREADY JSON in the template UI (design chat family, chat messages, smoke coder, polls, skill-design family) are re-registered on the API group **unchanged** — their errors are `{"error":"string"}`. Normalizing them would break the live template JS; they get the envelope in sub-plan 6 when templates die. The SPA must handle both shapes on exactly these endpoints.
- Ownership checks are mandatory: any `:id` resource must verify `resource.WorkspaceID == workspace.ID` and return 404 (`not_found`) otherwise — same as the template handlers.
- Audit logging (`s.audit.Log`) is preserved on every mutating endpoint, same action names as the template handlers.
- Existing template routes must keep passing `go test ./... -count=1 -timeout 120s` at every commit.
- Time fields marshal as RFC3339 (Go `time.Time` default). DTO JSON keys are `snake_case`.
- Run tests from the repo root: `go test ./web/... -run <Name> -count=1`.

---

### Task 1: Branch, API scaffolding, JSON middleware, test helpers

**Files:**
- Create: `web/api.go`
- Create: `web/api_test_helpers_test.go`
- Create: `web/api_middleware_test.go`
- Modify: `web/server.go` (one line in `setupRoutes`)

**Interfaces:**
- Produces: `jsonErr(c echo.Context, status int, code, msg string) error`, `bindJSON[T any]`-style helper `bindAPI(c, &req) error`, middleware `requireOwnerAPI`, `requireActiveWorkspaceAPI`, `requireSetupCompleteAPI`, route groups built in `setupAPIRoutes()` — `s.apiPublic` (none), `s.apiOwner` (owner-gated), `s.apiDash` (owner+workspace+setup-gated). Test helpers `newAPITestServer(t)`, `bootstrapAndLogin(t, s)`, `createAndEnterWorkspace(t, s, cookies)`, `doJSON(t, s, method, path, body, cookies)`.
- Consumes: existing `Server` fields, `currentOwner`, `activeWorkspace`.

- [ ] **Step 1: Create the branch**

```bash
cd /home/rookie/simple-agents-v2 && git checkout -b ui-redesign
```

- [ ] **Step 2: Write failing middleware tests** — `web/api_middleware_test.go`:

```go
package web

import (
	"net/http"
	"testing"
)

func TestAPIMiddlewareUnauthenticated(t *testing.T) {
	s, _ := newAPITestServer(t)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, nil)
	// /auth/session is public: 200 {"authenticated":false}
	if rec.Code != 200 {
		t.Fatalf("session: got %d", rec.Code)
	}
	if got := rec.Body.String(); !contains(got, `"authenticated":false`) {
		t.Fatalf("session body: %s", got)
	}

	// A dash-group route without a session → 401 envelope.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("agents unauthenticated: got %d want 401", rec.Code)
	}
	if got := rec.Body.String(); !contains(got, `"code":"not_authenticated"`) {
		t.Fatalf("envelope: %s", got)
	}
}

func TestAPIMiddlewareNoWorkspace(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d want 403", rec.Code)
	}
	if got := rec.Body.String(); !contains(got, `"code":"no_workspace"`) {
		t.Fatalf("envelope: %s", got)
	}
}
```

(`TestAPIMiddlewareUnauthenticated` also needs `/api/v1/agents` + `/api/v1/auth/session` to exist — Task 1 registers a stub `GET /api/v1/agents` returning `501 not_implemented` inside the dash group so the middleware is testable before Task 4; Task 4 replaces the stub.)

- [ ] **Step 3: Write the test helpers** — `web/api_test_helpers_test.go`:

```go
package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/ilijad1/simple-agents/internal/db"
)

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// newAPITestServer builds a Server with a temp DB and no gateway/runner/flows.
func newAPITestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	t.Setenv("SA_SYSTEM_KEY", strings.Repeat("ab", 32)) // 64 hex chars
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "test.db"), "../migrations")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	cfg := &config.Config{}
	cfg.Data.Dir = dir
	cfg.Server.TemplatesDir = "templates"
	s, err := NewServer(cfg, database, nil, nil, nil, filepath.Join(dir, "homes"), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s, database
}

// doJSON performs a request against the echo instance and returns the recorder.
func doJSON(t *testing.T, s *Server, method, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	return rec
}

// bootstrapAndLogin creates the owner (admin/password123) and logs in via the API.
func bootstrapAndLogin(t *testing.T, s *Server) []*http.Cookie {
	t.Helper()
	if _, err := auth.BootstrapOwner(s.db, "admin", "password123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "password123"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	res := rec.Result()
	return res.Cookies()
}

// createAndEnterWorkspace makes a workspace, marks setup complete with a known
// master password ("master-pw-1"), and enters it. Returns updated cookies + ws id.
func createAndEnterWorkspace(t *testing.T, s *Server, cookies []*http.Cookie) ([]*http.Cookie, string) {
	t.Helper()
	w, err := auth.CreateWorkspace(s.db, "ws1", "test workspace")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	// Complete "setup" directly in the DB: store encrypted master pw + salt,
	// clear needs_setup — mirrors what the setup wizard does.
	encPw, err := secrets.EncryptMasterPassword("master-pw-1", s.systemKey)
	if err != nil {
		t.Fatalf("encrypt master pw: %v", err)
	}
	salt := secrets.NewGenerateSalt()
	if err := s.db.UpdateWorkspaceSetup(w.ID, encPw, salt); err != nil {
		t.Fatalf("workspace setup: %v", err)
	}
	if err := s.db.SetWorkspaceNeedsSetup(w.ID, false); err != nil {
		t.Fatalf("clear needs_setup: %v", err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+w.ID+"/enter",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("enter workspace: %d %s", rec.Code, rec.Body.String())
	}
	// Session cookie is rewritten on enter — merge the fresh cookie.
	return rec.Result().Cookies(), w.ID
}
```

> NOTE for the implementer: check the exact names of `secrets.NewGenerateSalt` (it exists at `internal/secrets/service.go:253`, verify its return signature) and whether `db` has `SetWorkspaceNeedsSetup`; if setup-completion is done differently (grep `NeedsSetup` in `internal/db`), use the same call the setup wizard (`web/handlers_setup.go:100-135`) uses. Adjust the helper only — tests stay the same. Add missing imports (`echo`, `secrets`).
> `createAndEnterWorkspace` depends on Task 3's `/enter` endpoint; until Task 3 lands it is only referenced by later tasks' tests, so it compiles but isn't called. If the compiler complains about unused imports before Task 3, keep the helper in a file-level `var _ = createAndEnterWorkspace` guard or add it in Task 3 instead.

- [ ] **Step 4: Run tests to verify they fail**

```bash
go test ./web/... -run TestAPIMiddleware -count=1
```
Expected: FAIL (`/api/v1/...` routes don't exist → 404s, helpers reference missing `setupAPIRoutes`).

- [ ] **Step 5: Implement `web/api.go`**

```go
package web

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// apiErrBody is the uniform error envelope: {"error":{"code","message"}}.
type apiErrBody struct {
	Error apiErrDetail `json:"error"`
}
type apiErrDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func jsonErr(c echo.Context, status int, code, msg string) error {
	return c.JSON(status, apiErrBody{Error: apiErrDetail{Code: code, Message: msg}})
}

// bindAPI binds a JSON request body, translating bind failures to the envelope.
func bindAPI(c echo.Context, v any) error {
	if err := c.Bind(v); err != nil {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "malformed JSON body")
	}
	return nil
}

// requireOwnerAPI is requireOwner with JSON responses instead of redirects.
func (s *Server) requireOwnerAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		o, ok := s.currentOwner(c)
		if !ok {
			return jsonErr(c, http.StatusUnauthorized, "not_authenticated", "log in first")
		}
		if o.MustChangePassword && c.Path() != "/api/v1/auth/change-password" {
			return jsonErr(c, http.StatusForbidden, "must_change_password", "password change required")
		}
		c.Set("owner", o)
		return next(c)
	}
}

// requireActiveWorkspaceAPI is requireActiveWorkspace with a JSON 403.
func (s *Server) requireActiveWorkspaceAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		w, ok := s.activeWorkspace(c)
		if !ok {
			return jsonErr(c, http.StatusForbidden, "no_workspace", "enter a workspace first")
		}
		c.Set("workspace", w)
		return next(c)
	}
}

// requireSetupCompleteAPI is requireSetupComplete with a JSON 403.
func (s *Server) requireSetupCompleteAPI(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		w := c.Get("workspace").(*db.Workspace)
		if w.NeedsSetup && !strings.HasPrefix(c.Path(), "/api/v1/setup") {
			return jsonErr(c, http.StatusForbidden, "needs_setup", "complete workspace setup first")
		}
		return next(c)
	}
}

// setupAPIRoutes registers the /api/v1 groups. Endpoint registrations are added
// group-by-group in api_*.go files' registration funcs, called from here.
func (s *Server) setupAPIRoutes() {
	api := s.echo.Group("/api/v1")

	// Public (no auth): session bootstrap + login.
	s.registerAuthAPI(api)

	// Owner-gated (no workspace needed): workspaces, admin, audit.
	owner := api.Group("", s.requireOwnerAPI)
	_ = owner // used from Task 3 on

	// Workspace-gated: everything tenant-scoped.
	dash := api.Group("", s.requireOwnerAPI, s.requireActiveWorkspaceAPI, s.requireSetupCompleteAPI)
	dash.GET("/agents", func(c echo.Context) error { // stub — replaced in Task 4
		return jsonErr(c, http.StatusNotImplemented, "not_implemented", "coming in Task 4")
	})
}
```

Add missing imports (`db`, `strings`). In `web/server.go`, at the END of `setupRoutes()` add:

```go
	s.setupAPIRoutes()
```

Create an empty `registerAuthAPI` for now (Task 2 fills it) in `web/api_auth.go`:

```go
package web

import "github.com/labstack/echo/v4"

func (s *Server) registerAuthAPI(g *echo.Group) {
	// Filled in Task 2. Session endpoint is needed by Task 1's middleware test:
	g.GET("/auth/session", s.apiAuthSession)
}
```

and a minimal `apiAuthSession` (full version in Task 2):

```go
func (s *Server) apiAuthSession(c echo.Context) error {
	if _, ok := s.currentOwner(c); !ok {
		return c.JSON(http.StatusOK, map[string]any{"authenticated": false})
	}
	return c.JSON(http.StatusOK, map[string]any{"authenticated": true})
}
```

- [ ] **Step 6: Run the middleware tests**

```bash
go test ./web/... -run TestAPIMiddleware -count=1
```
Expected: `TestAPIMiddlewareUnauthenticated` PASS. `TestAPIMiddlewareNoWorkspace` PASS (login works only after Task 2 — if it fails on the missing login endpoint, mark it with `t.Skip("needs Task 2")` and remove the skip in Task 2).

- [ ] **Step 7: Full suite + commit**

```bash
go test ./web/... -count=1 && git add -A && git commit -m "feat(api): /api/v1 scaffolding — error envelope, JSON auth middleware, test harness"
```

---

### Task 2: Auth & session endpoints

**Files:**
- Modify: `web/api_auth.go`
- Create: `web/api_auth_test.go`

**Interfaces:**
- Produces: `POST /api/v1/auth/login {username,password}` → 200 `{"ok":true,"must_change_password":bool}` / 401 `invalid_credentials`; `POST /api/v1/auth/logout` → 200 `{"ok":true}`; `GET /api/v1/auth/session` → `{"authenticated":bool, "owner":{"id","username","must_change_password"}, "workspace":<apiWorkspace|null>, "workspaces":[apiWorkspace]}`; `POST /api/v1/auth/change-password {password,confirm}` (owner-gated) → 200/400. DTO `apiWorkspace{id,name,about,needs_setup,created_at}` + `toAPIWorkspace(*db.Workspace)` — reused by Task 3.
- Consumes: `auth.Authenticate`, `auth.ChangePassword`, `s.setOwnerSession`, `s.clearSession`, `s.audit`.

- [ ] **Step 1: Write failing tests** — `web/api_auth_test.go`:

```go
package web

import (
	"net/http"
	"testing"
)

func TestAPILoginLogoutSession(t *testing.T) {
	s, _ := newAPITestServer(t)
	// Bad creds → 401 envelope.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "nobody", "password": "wrong"}, nil)
	if rec.Code != http.StatusUnauthorized || !contains(rec.Body.String(), `"code":"invalid_credentials"`) {
		t.Fatalf("bad creds: %d %s", rec.Code, rec.Body.String())
	}
	// Good creds → cookie; session reflects owner.
	cookies := bootstrapAndLogin(t, s)
	rec = doJSON(t, s, http.MethodGet, "/api/v1/auth/session", nil, cookies)
	body := rec.Body.String()
	if rec.Code != 200 || !contains(body, `"authenticated":true`) || !contains(body, `"username":"admin"`) {
		t.Fatalf("session: %d %s", rec.Code, body)
	}
	// Logout kills the session.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/auth/logout", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("logout: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./web/... -run TestAPILoginLogoutSession -count=1` → FAIL (login route missing).

- [ ] **Step 3: Implement** — replace `web/api_auth.go` with:

```go
package web

import (
	"errors"
	"net/http"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type apiWorkspace struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	About      string    `json:"about"`
	NeedsSetup bool      `json:"needs_setup"`
	CreatedAt  time.Time `json:"created_at"`
}

func toAPIWorkspace(w *db.Workspace) apiWorkspace {
	return apiWorkspace{ID: w.ID, Name: w.Name, About: w.About, NeedsSetup: w.NeedsSetup, CreatedAt: w.CreatedAt}
}

func (s *Server) registerAuthAPI(g *echo.Group) {
	g.GET("/auth/session", s.apiAuthSession)
	g.POST("/auth/login", s.apiLogin)
	g.POST("/auth/logout", s.apiLogout)
	g.POST("/auth/change-password", s.apiChangePassword, s.requireOwnerAPI)
}

func (s *Server) apiAuthSession(c echo.Context) error {
	o, ok := s.currentOwner(c)
	if !ok {
		return c.JSON(http.StatusOK, map[string]any{"authenticated": false})
	}
	out := map[string]any{
		"authenticated": true,
		"owner": map[string]any{
			"id": o.ID, "username": o.Username, "must_change_password": o.MustChangePassword,
		},
		"workspace": nil,
	}
	wss, _ := s.db.ListWorkspaces()
	list := make([]apiWorkspace, 0, len(wss))
	for _, w := range wss {
		list = append(list, toAPIWorkspace(w))
	}
	out["workspaces"] = list
	if w, ok := s.activeWorkspace(c); ok {
		out["workspace"] = toAPIWorkspace(w)
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) apiLogin(c echo.Context) error {
	var req struct{ Username, Password string }
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	o, err := auth.Authenticate(s.db, req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCreds) {
			return jsonErr(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		}
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	if err := s.setOwnerSession(c, o.ID); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log("", "login", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "must_change_password": o.MustChangePassword})
}

func (s *Server) apiLogout(c echo.Context) error {
	if o, ok := s.currentOwner(c); ok {
		s.audit.Log("", "logout", "owner:"+o.ID, "", c.RealIP())
	}
	_ = s.clearSession(c)
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) apiChangePassword(c echo.Context) error {
	o := c.Get("owner").(*db.Owner)
	var req struct{ Password, Confirm string }
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if len(req.Password) < 8 {
		return jsonErr(c, http.StatusBadRequest, "password_too_short", "password must be at least 8 characters")
	}
	if req.Password != req.Confirm {
		return jsonErr(c, http.StatusBadRequest, "password_mismatch", "passwords do not match")
	}
	if err := auth.ChangePassword(s.db, o.ID, req.Password); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log("", "change_password", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}
```

Add the `time` import. JSON binding note: lowercase JSON keys bind to these fields because Echo's default binder matches case-insensitively; keep the anonymous structs.

- [ ] **Step 4: Run tests** — `go test ./web/... -run 'TestAPILoginLogoutSession|TestAPIMiddleware' -count=1` → PASS (remove any Task 1 skip).

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat(api): auth endpoints — login/logout/session/change-password"`

---

### Task 3: Workspaces + owner endpoints

**Files:**
- Create: `web/api_workspaces.go`, `web/api_workspaces_test.go`

**Interfaces:**
- Produces (owner group): `GET /api/v1/workspaces` → `{"workspaces":[apiWorkspace]}`; `POST /api/v1/workspaces {name,about}` → 201 apiWorkspace (also sets it active — mirrors `handleAdminCreateWorkspace` behavior); `POST /api/v1/workspaces/:id/enter {master_password}` → 200 `{"ok":true,"needs_setup":bool}` / 401 `wrong_master_password`; `POST /api/v1/workspaces/leave` → 200; `DELETE /api/v1/workspaces/:id` → 200; `GET /api/v1/workspaces/:id/permissions` → `{"permissions":[{"name","granted"}]}`; `PUT /api/v1/workspaces/:id/permissions {grant:[],revoke:[]}` → 200; `GET /api/v1/admin/overview` → `{"workspace_count","agent_count","recent_audit":[...]}`; `GET /api/v1/admin/audit?limit=100` → `{"logs":[...]}`; `GET /api/v1/admin/settings` / `PUT /api/v1/admin/settings` (port of `loadAdminSettings`/`handleAdminSaveSettings`, `web/handlers_admin.go:254-301`).
- Consumes: `auth.CreateWorkspace`, `s.verifyWorkspaceMasterPassword` (`web/handlers_admin.go:188`), `s.db.{ListWorkspaces,GetWorkspaceByID,DeleteWorkspace,ListPermissions,GrantPermission,RevokePermission,CountAgents,ListAuditLogs,GetSystemSetting,SetSystemSetting}`, `allPermissions` (`web/handlers_admin.go:101`).

- [ ] **Step 1: Failing test** — `web/api_workspaces_test.go`:

```go
package web

import (
	"net/http"
	"testing"
)

func TestAPIWorkspaceLifecycle(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)

	// Create → 201, becomes active (needs_setup=true).
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces",
		map[string]string{"name": "ws-a", "about": "first"}, cookies)
	if rec.Code != http.StatusCreated || !contains(rec.Body.String(), `"name":"ws-a"`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	cookies = append(cookies, rec.Result().Cookies()...)

	// Duplicate name → 409.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces",
		map[string]string{"name": "ws-a"}, cookies)
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup: %d", rec.Code)
	}

	// Leave → session no longer has an active workspace.
	rec = doJSON(t, s, http.MethodPost, "/api/v1/workspaces/leave", nil, cookies)
	if rec.Code != 200 {
		t.Fatalf("leave: %d", rec.Code)
	}
}

func TestAPIEnterWorkspaceWrongPassword(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	// Re-enter with a wrong password → 401 wrong_master_password.
	rec := doJSON(t, s, http.MethodPost, "/api/v1/workspaces/"+wsID+"/enter",
		map[string]string{"master_password": "nope"}, cookies)
	if rec.Code != http.StatusUnauthorized || !contains(rec.Body.String(), "wrong_master_password") {
		t.Fatalf("enter wrong pw: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Verify failure** — `go test ./web/... -run TestAPIWorkspace -count=1` → FAIL.

- [ ] **Step 3: Implement `web/api_workspaces.go`.** Register in `setupAPIRoutes` on the owner group (replace the `_ = owner` line with `s.registerWorkspacesAPI(owner)`). Handlers are direct ports of `web/handlers_admin.go:53-240` with JSON in/out; the enter handler mirrors lines 136-163 exactly (needs-setup → enter without password, else `verifyWorkspaceMasterPassword`), returning `{"ok":true,"needs_setup":w.NeedsSetup}` instead of redirecting; create returns 409 `workspace_exists` on `auth.ErrWorkspaceExists`; delete clears the active workspace if it was the deleted one (port lines 171-182). Permissions PUT validates names against `allPermissions` and applies grants then revokes. Admin overview/audit/settings port `showAdminDashboard`/`showAuditLog`/`loadAdminSettings`+`handleAdminSaveSettings` field-for-field (audit DTO: `{workspace_id,action,target,detail,ip,created_at}` — check the exact `db.AuditLog` field names in `internal/db` and mirror them in snake_case).

- [ ] **Step 4: Run** — `go test ./web/... -run TestAPIWorkspace -count=1` → PASS. Full suite green.

- [ ] **Step 5: Commit** — `git commit -am "feat(api): workspace lifecycle + owner/admin endpoints"`

---

### Task 4: Agents + design endpoints

**Files:**
- Create: `web/api_agents.go`, `web/api_agents_test.go`
- Modify: `web/handlers_agents.go` (extract `loadAgentDetail`)

**Interfaces:**
- Produces (dash group): `GET /api/v1/agents` → `{"agents":[apiAgent],"draft":{...}|null}` with `apiAgent{id,name,description,active,created_at,running}`; `GET /api/v1/agents/:id` → apiAgentDetail (all fields of `agentDetailData` minus pageData: agent, schedule `{cron_expr,next_run_at,enabled}|null`, runs `[{id,status,started_at,finished_at,...}]`, agent_md, state, logs, last_log, attached_skills, core_skills `[{name,description}]`, all_skills, workspace_connections, attached_connection_ids, missing_secrets, running, live_run); `DELETE /api/v1/agents/:id`; `POST /api/v1/agents/:id/run` → 202 `{"status":"started"}`; `PUT /api/v1/agents/:id/schedule {cron_expr}` → 200/400 `invalid_cron`; `DELETE /api/v1/agents/:id/schedule`; `PUT /api/v1/agents/:id/agent-md {content}` (ethics check → 400 `ethics_blocked`); `PUT /api/v1/agents/:id/skills {skill_names:[]}`; `PUT /api/v1/agents/:id/connections {connection_ids:[]}`. Design family re-registered as-is: `POST /agents/design` → `s.handleDesignChat`, plus `/design/cancel|resume|dismiss`, `GET /design/progress` (SSE), `GET /design/state`, `POST /agents/:id/edit/start`, `GET /agents/:id/run/progress` (SSE → `s.handleRunProgress`).
- Produces for reuse: `s.loadAgentDetail(ctx, agent, workspaceID) *agentDetailData` — extracted from `renderAgentDetail` (`web/handlers_agents.go:463-557`): everything except the final `c.Render` call moves into it; `renderAgentDetail` becomes `data := s.loadAgentDetail(...); data.pageData = p; return c.Render(...)`.
- Consumes: everything `renderAgentDetail` consumes today; `s.startManualRun`, `secrets.DecryptMasterPassword` (run endpoint ports `handleRunAgent` lines 582-625, returning 202 instead of a redirect); cron parsing exactly as `handleSaveSchedule` lines 636-668.

- [ ] **Step 1: Failing test** — `web/api_agents_test.go`:

```go
package web

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

func seedAgent(t *testing.T, s *Server, wsID string) *db.Agent {
	t.Helper()
	a := &db.Agent{ID: uuid.New().String(), WorkspaceID: wsID, Name: "Digest",
		Description: "daily digest", Active: true, CreatedAt: time.Now()}
	if err := s.db.CreateAgent(a); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return a
}

func TestAPIAgentsListDetailSchedule(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)
	a := seedAgent(t, s, wsID)

	rec := doJSON(t, s, http.MethodGet, "/api/v1/agents", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"name":"Digest"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+a.ID, nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"agent_md"`) {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}

	// Schedule: bad cron → 400; good cron → 200.
	rec = doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "not-a-cron"}, cookies)
	if rec.Code != http.StatusBadRequest || !contains(rec.Body.String(), "invalid_cron") {
		t.Fatalf("bad cron: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/agents/"+a.ID+"/schedule",
		map[string]string{"cron_expr": "*/10 * * * *"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("good cron: %d %s", rec.Code, rec.Body.String())
	}

	// Foreign agent → 404.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/agents/"+uuid.New().String(), nil, cookies)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign: %d", rec.Code)
	}
}
```

(Check `db.CreateAgent`'s exact name/signature via `grep -n "func (d \*DB) .*Agent" internal/db/*.go` and adjust the seed helper only.)

- [ ] **Step 2: Verify failure**, then **Step 3: Implement**:
  1. Extract `loadAgentDetail` from `renderAgentDetail` (mechanical move; template path stays green).
  2. Write DTO mappers (`toAPIAgentDetail(d *agentDetailData) map[string]any` — explicit fields, snake_case keys) and the handlers; ownership check = `GetAgent` + `WorkspaceID` compare → `jsonErr 404 not_found`.
  3. Register: JSON CRUD on dash group; design/SSE handlers re-registered UNCHANGED (`dash.POST("/agents/design", s.handleDesignChat)` etc. — per Global Constraints these keep their legacy shapes).

- [ ] **Step 4: Run + full suite** — `go test ./web/... -count=1` → PASS.
- [ ] **Step 5: Commit** — `git commit -am "feat(api): agents CRUD/run/schedule + design endpoints on /api/v1"`

---

### Task 5: Skills + skill-designer endpoints

**Files:** Create `web/api_skills.go`, `web/api_skills_test.go`.

**Interfaces:**
- `GET /api/v1/skills` → `{"skills":[{id,name,description,created_at}],"core_skills":[{slug,name,description}],"draft":{...}|null}` (port `showSkills`, `web/handlers_skills.go:44-60`); `GET /api/v1/skills/core/:slug` → `{slug,content}` (port `showCoreSkill`, guard with `skilllibrary.IsCoreSkill`); `GET /api/v1/skills/:id` → `{id,name,description,content}` (port `showSkillDetail`); `PUT /api/v1/skills/:id {content}` (port `handleSaveSkill`); `DELETE /api/v1/skills/:id` (port `handleDeleteSkill` — includes `db.DeleteAgentSkillsByName`); `POST /api/v1/skills {content|zip?}` — port `handleCreateSkill` (`web/handlers_skills.go:76-168`; it handles pasted SKILL.md + ZIP upload — keep multipart support: accept `multipart/form-data` on this one endpoint, JSON alternative `{content}`). Skill-designer family re-registered unchanged: `POST /skills/design`, `/skills/design/cancel|resume|dismiss`, `GET /skills/design/progress` (SSE).
- Consumes: `s.skills` (skillstore), `s.db.{ListSkills,GetSkill,DeleteSkill,DeleteAgentSkillsByName}`, `skilllibrary.{LoadBundled,CoreSkillContent,IsCoreSkill}`, `s.skillFlow`. **NOTE:** `newAPITestServer` passes `skillStore=nil` and `skillFlow=nil` — handlers must nil-check `s.skills`/`s.skillFlow` and return 503 `not_configured` (the design-family handlers already do); the list endpoint works DB-only. For the store-backed test, construct a real `skillstore.Store` in the test if its constructor is cheap (`grep -n "func New" internal/skillstore/*.go`) — otherwise test the DB-only paths.

- [ ] **Step 1:** Failing test (list + core slug + 404 unknown core slug + foreign-skill 404 — same doJSON pattern as Task 4).
- [ ] **Step 2:** Verify fail. **Step 3:** Implement + register. **Step 4:** `go test ./web/... -count=1` PASS. **Step 5:** `git commit -am "feat(api): skills + skill-designer endpoints"`

---

### Task 6: Secrets endpoints

**Files:** Create `web/api_secrets.go`, `web/api_secrets_test.go`.

**Interfaces:**
- `GET /api/v1/secrets` → `{"secrets":[{"name"}]}`; `POST /api/v1/secrets {name,value}` → 201 (write-only; port `handleCreateSecret` `web/handlers_secrets.go:29-75`: decrypts stored master pw via `secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey)`, then `secrets.New(...).Set`; errors: 400 `missing_field`, 400 `setup_incomplete`, 500 `internal`); `DELETE /api/v1/secrets/:name {master_password}` (port `handleDeleteSecret` lines 77-124: verify by attempted decrypt, `secrets.ErrWrongPassword` → 401 `wrong_master_password`; idempotent delete). There is deliberately NO read-value endpoint — spec §9 write-only rule.

- [ ] **Step 1: Failing test:**

```go
func TestAPISecretsWriteOnlyAndDelete(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/secrets",
		map[string]string{"name": "API_KEY", "value": "sekrit"}, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/secrets", nil, cookies)
	if !contains(rec.Body.String(), `"API_KEY"`) || contains(rec.Body.String(), "sekrit") {
		t.Fatalf("list must have name, never value: %s", rec.Body.String())
	}
	// Delete with wrong master password → 401.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/secrets/API_KEY",
		map[string]string{"master_password": "wrong"}, cookies)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong pw: %d %s", rec.Code, rec.Body.String())
	}
	// Correct master password ("master-pw-1" from the helper) → 200.
	rec = doJSON(t, s, http.MethodDelete, "/api/v1/secrets/API_KEY",
		map[string]string{"master_password": "master-pw-1"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Steps 2-5:** verify fail → implement (straight port, envelope errors) → full suite PASS → `git commit -am "feat(api): secrets endpoints (write-only, master-password-gated delete)"`

---

### Task 7: Chat-app connector endpoints

**Files:** Create `web/api_connectors.go`, `web/api_connectors_test.go`.

**Interfaces:**
- `GET /api/v1/connectors` → CredSpec-driven: `{"platforms":[{"platform","label","blurb","setup_steps":[...],"fields":[{name,label,secret}],"connected":bool,"identity":string}]}` — port the data assembly in `showConnectors`/`renderConnectors` (`web/handlers_connectors.go:32-52,245+`; it iterates registered CredSpecs + `db.ListUserPlatformConnections`). `POST /api/v1/connectors {platform, values:{...}}` → 200 `{"ok":true,"identity":...}` / 400 with the validation error — reuses `s.saveConnector(workspaceID, platform, values)` (`web/handlers_connectors.go:54-102`) which already returns `(identity, botStartErr, err)`; `DELETE /api/v1/connectors/:platform` (port `handleDeleteConnector`); `POST /api/v1/connectors/:platform/test` → `{"ok":bool,"identity"|"error"}` (wrap `testConnectorIdentity`, lines 174-203).
- Consumes: `gateway.CredSpecFor`/registry iteration (see how `renderConnectors` enumerates), `s.saveConnector`, `s.testConnectorIdentity`, `s.db.ListUserPlatformConnections`.

- [ ] **Steps:** failing test (GET lists telegram platform card unauthenticated→401, authed→200 containing `"telegram"`; POST with garbage token → 400) → implement → suite PASS → `git commit -am "feat(api): chat-app connector endpoints (CredSpec-driven)"`

---

### Task 8: Service-connection (OAuth/API-key) endpoints

**Files:** Create `web/api_services.go`, `web/api_services_test.go`.

**Interfaces:**
- `GET /api/v1/services` → port `showServices` (`web/handlers_services.go:97-158`) data: `{"providers":[{name,label,kind(oauth|api_key),setup_url,setup_steps,has_creds,connect_inputs:[...],connections:[{id,label,identity,status}]}]}`; `POST /api/v1/services/:provider/creds {client_id,client_secret}` (port `handleSaveProviderCreds`); `POST /api/v1/services/:provider/connect {label?}` → 200 `{"redirect_url":"<provider consent URL>"}` — port `handleConnectService` (lines 199-229) but RETURN the consent URL instead of 302-redirecting (the SPA does `window.location = redirect_url`); `POST /api/v1/services/:provider/apikey {key, inputs:{...}}` (port `handleConnectAPIKey`); `DELETE /api/v1/services/:id` (port `handleDeleteServiceConnection`). **The OAuth callback stays where it is** (`/dashboard/connectors/services/callback/:provider`) — browser-redirect route, not an API; do not move it in this sub-plan.
- Consumes: `s.connectors` registry, `s.connStore`, state signing helpers in `handlers_services.go`, `s.publicBaseURL`/`s.callbackURL`.

- [ ] **Steps:** failing test (GET services lists `"google"` provider; connect without saved creds → 400 envelope; delete unknown id → 404) → implement → suite PASS → `git commit -am "feat(api): service-connection endpoints; connect returns consent URL as JSON"`

---

### Task 9: Chats endpoints

**Files:** Create `web/api_chats.go`, `web/api_chats_test.go`.

**Interfaces:**
- `GET /api/v1/chats` → `{"chats":[{id,name,platform,active,created_at,updated_at}]}`; `POST /api/v1/chats {name?}` → 201 chat DTO (port `handleCreateChat` `web/handlers_misc.go:42-61` incl. default name from workspace-local time); `GET /api/v1/chats/:id` → `{chat, messages:[{role,content,created_at}]}`; `POST /api/v1/chats/:id/messages` → re-register `s.handleChatMessage` UNCHANGED (already JSON; legacy `{response}`/`{error}` shape per Global Constraints); `POST /api/v1/chats/:id/resume`, `POST /api/v1/chats/:id/stop`, `DELETE /api/v1/chats/:id` — ports returning `{"ok":true}`.
- Consumes: `s.db.{ListChats,GetChat,CreateChat,ListChatMessages,ResumeChat,StopChat,DeleteChat}`, `profile.LoadLocation`.

- [ ] **Steps:** failing test (create → list shows it → detail has empty messages → stop → delete → detail 404; check exact `db.ChatMessage` field names before writing the DTO) → implement → suite PASS → `git commit -am "feat(api): chats endpoints"`

---

### Task 10: Reminders + inbox endpoints

**Files:** Create `web/api_home.go`, `web/api_home_test.go`.

**Interfaces:**
- `GET /api/v1/reminders` → `{"reminders":[{id,message,remind_at,sent}]}` (check `db.Reminder` fields); `POST /api/v1/reminders {message,when}` → 201 `{id,message,remind_at}` / 400 `unparseable_time` — port `handleCreateReminder` (`web/handlers_misc.go:248-288`) including `buildLLMTimeParser(s.coderForWorkspace(u.ID))` + `reminder.ParseNaturalTimeFull`; with a nil/unconfigured coder the LLM fallback just won't fire (deterministic parser still works — test with `"in 10 minutes"`); `DELETE /api/v1/reminders/:id`; `GET /api/v1/reminders/poll` → re-register `s.handlePollReminders` unchanged.
- `GET /api/v1/inbox?limit=100&offset=0` → `{"messages":[{id,source,agent_name,trigger,status,body,read,created_at}],"unread":n}` (port `showInbox` + full body, not the 160-char preview); `GET /api/v1/inbox/poll` → re-register `s.handleInboxPoll` unchanged; `POST /api/v1/inbox/:id/read`, `POST /api/v1/inbox/read-all`, `DELETE /api/v1/inbox/:id` → ports returning `{"ok":true}` (`web/handlers_inbox.go:70-90`).

- [ ] **Steps:** failing test (create reminder "in 10 minutes" → 201; list contains it; delete; inbox: seed a row via `s.db` insert helper — find it with `grep -n "InboxMessage" internal/db/*.go` — then list/read/read-all/delete assertions) → implement → suite PASS → `git commit -am "feat(api): reminders + inbox endpoints"`

---

### Task 11: Knowledge-base endpoints

**Files:** Create `web/api_kb.go`, `web/api_kb_test.go`.

**Interfaces:**
- `GET /api/v1/kb/tree?path=` → `{"path","nodes":[{name,display_name,path,is_dir,system:bool}]}` — port `showKB` + `enrichKBDisplayNames` (`web/handlers_kb.go:59-125`); mark system dirs (`chats`, `.kb` already hidden by `vault.List`, agent log dirs) the same way the template does.
- `GET /api/v1/kb/note?path=` → `{"path","content","html","backlinks":[...]}` — port `viewKBNote`+`editKBNote` (127-183): raw markdown + `s.renderMarkdown(workspaceID, content)` output + backlinks if the view handler loads them.
- `PUT /api/v1/kb/note {path,content}` (port `handleSaveKBNote`); `POST /api/v1/kb/new {path,is_dir}` (port `handleNewKBNote`); `DELETE /api/v1/kb/note?path=` (port `handleDeleteKBNote`); `POST /api/v1/kb/rename {from,to}` (port `handleRenameKBNote`); `GET /api/v1/kb/search?q=` → `{"hits":[{path,line,snippet}]}` (port `searchKB` → `s.vault.NewSearcher().Search`); `GET /api/v1/kb/raw?path=` → raw file download (re-register `s.rawKBNote` as-is).
- All path handling goes through `vault.Resolve` inside the vault methods — never bypass it. Escape attempts → 400 `invalid_path` (map the error the vault returns).

- [ ] **Step 1: Failing test:**

```go
func TestAPIKBRoundTrip(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, _ = createAndEnterWorkspace(t, s, cookies)

	rec := doJSON(t, s, http.MethodPost, "/api/v1/kb/new",
		map[string]any{"path": "notes/hello.md", "is_dir": false}, cookies)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("new: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPut, "/api/v1/kb/note",
		map[string]string{"path": "notes/hello.md", "content": "# Hello\n\nworld [[other]]"}, cookies)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=notes/hello.md", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "world") {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	// Path escape → 400.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/note?path=../../etc/passwd", nil, cookies)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("escape must be rejected: %d", rec.Code)
	}
	// Tree shows the note.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/kb/tree?path=notes", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "hello") {
		t.Fatalf("tree: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Steps 2-5:** verify fail → implement → suite PASS → `git commit -am "feat(api): knowledge-base endpoints (tree/note/save/new/delete/rename/search/raw)"`

---

### Task 12: Settings + setup + coder endpoints

**Files:** Create `web/api_settings.go`, `web/api_settings_test.go`; Modify `web/handlers_misc.go` (extract two helpers).

**Interfaces:**
- Extract from `handleSaveWorkspaceCoder` (`web/handlers_misc.go:487-566`) a shared core: `saveWorkspaceCoderCore(w *db.Workspace, f coderForm) (userErrMsg string, err error)` with `coderForm{Kind,Bin,TimeoutS,Provider,Model,BaseURL,APIKey string}` — everything between form parsing and the final render, including `coder.PlanKeySecret` + secret write. Template handler and API handler both call it (template path re-tested by existing suite).
- Extract from `handleChangeMasterPassword` (lines 582-644): `changeMasterPasswordCore(u *db.Workspace, oldPw, newPw string) (userErrMsg string, err error)` — verification, re-encryption loop, `UpdateWorkspaceSetup`.
- Endpoints: `GET /api/v1/settings` → `{profile:{display_name,email,location,timezone,tone,language,notes}, workspace:{name,about}, coder:{kind,bin,timeout_s,provider,model,base_url,api_key_secret}, detected_coders:[...], api_providers:[...], coder_catalog:[...], secret_names:[...]}` (port `buildSettingsData` + `coderCatalogJSON` — emit the catalog as a plain JSON array, not `template.JS`); `PUT /api/v1/settings/profile` (port `handleSaveSettings`); `PUT /api/v1/settings/workspace {name,about}`; `PUT /api/v1/settings/coder` (JSON bind coderForm → core; userErr → 400 envelope); `POST /api/v1/settings/coder/test` → re-register `s.handleSmokeCoder`; `PUT /api/v1/settings/master-password {current,new_password,confirm}` → core; wrong old pw → 401 `wrong_master_password`.
- Setup: `GET /api/v1/setup` → `{"step":n,...}` and `POST /api/v1/setup {step,...fields}` — port `showSetup`/`handleSetup` step dispatch (`web/handlers_setup.go:32-232`): JSON responses `{"ok":true,"next_step":n}` / envelope errors; reuse the per-step handlers' logic (extract cores only where the step handler renders mid-logic; most are short enough to port directly).

- [ ] **Steps:** failing test (GET settings has `"detected_coders"` and never leaks secret VALUES; PUT profile round-trips display_name; PUT master-password with wrong current → 401) → extract cores (run full suite — template paths must stay green) → implement API handlers → suite PASS → `git commit -am "feat(api): settings/setup/coder endpoints with shared cores"`

---

### Task 13: Global search endpoint

**Files:** Create `web/api_search.go`, `web/api_search_test.go`.

**Interfaces:**
- `GET /api/v1/search?q=<query>` (dash group) → grouped results, max 5 per group:

```json
{"query":"ohrid","groups":[
  {"kind":"notes","items":[{"title":"notes/ohrid-trip.md","path":"notes/ohrid-trip.md","line":3,"snippet":"...","url":"/kb?path=notes/ohrid-trip.md"}]},
  {"kind":"agents","items":[{"title":"Digest","id":"...","url":"/agents/..."}]},
  {"kind":"chats","items":[...]},{"kind":"skills","items":[...]},
  {"kind":"connections","items":[...]},{"kind":"secrets","items":[{"title":"API_KEY"}]},
  {"kind":"reminders","items":[...]}
]}
```

- [ ] **Step 1: Failing test:**

```go
func TestAPIGlobalSearch(t *testing.T) {
	s, _ := newAPITestServer(t)
	cookies := bootstrapAndLogin(t, s)
	cookies, wsID := createAndEnterWorkspace(t, s, cookies)

	// Seed: one note, one agent, one chat.
	if err := s.vault.WriteNote(wsID, "notes/ohrid-trip.md", []byte("lake apartments in Ohrid")); err != nil {
		t.Fatalf("write note: %v", err)
	}
	seedAgent(t, s, wsID) // name "Digest"
	rec := doJSON(t, s, http.MethodGet, "/api/v1/search?q=ohrid", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), "ohrid-trip") {
		t.Fatalf("notes hit: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodGet, "/api/v1/search?q=digest", nil, cookies)
	if rec.Code != 200 || !contains(rec.Body.String(), `"kind":"agents"`) {
		t.Fatalf("agents hit: %d %s", rec.Code, rec.Body.String())
	}
	// Empty query → 400.
	rec = doJSON(t, s, http.MethodGet, "/api/v1/search?q=", nil, cookies)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty q: %d", rec.Code)
	}
}
```

(Verify `vault.WriteNote`'s signature — `grep -n "func (v \*Vault) WriteNote" internal/vault/vault.go` — and adjust the seed line.)

- [ ] **Step 3: Implement `web/api_search.go`:**

```go
package web

import (
	"net/http"
	"strings"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type searchItem struct {
	Title   string `json:"title"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	URL     string `json:"url,omitempty"`
}
type searchGroup struct {
	Kind  string       `json:"kind"`
	Items []searchItem `json:"items"`
}

const searchGroupLimit = 5

func (s *Server) apiGlobalSearch(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return jsonErr(c, http.StatusBadRequest, "empty_query", "q is required")
	}
	lq := strings.ToLower(q)
	match := func(fields ...string) bool {
		for _, f := range fields {
			if strings.Contains(strings.ToLower(f), lq) {
				return true
			}
		}
		return false
	}
	var groups []searchGroup
	add := func(kind string, items []searchItem) {
		if len(items) > searchGroupLimit {
			items = items[:searchGroupLimit]
		}
		if len(items) > 0 {
			groups = append(groups, searchGroup{Kind: kind, Items: items})
		}
	}

	// Notes: full-text via the vault searcher (ripgrep or Go fallback).
	if hits, err := s.vault.NewSearcher().Search(c.Request().Context(), u.ID, q); err == nil {
		var items []searchItem
		for _, h := range hits {
			items = append(items, searchItem{Title: h.Path, Path: h.Path, Line: h.Line,
				Snippet: h.Snippet, URL: "/kb?path=" + h.Path})
		}
		add("notes", items)
	}

	agents, _ := s.db.ListAgents(u.ID)
	var items []searchItem
	for _, a := range agents {
		if match(a.Name, a.Description) {
			items = append(items, searchItem{Title: a.Name, ID: a.ID, URL: "/agents/" + a.ID})
		}
	}
	add("agents", items)

	chats, _ := s.db.ListChats(u.ID)
	items = nil
	for _, ch := range chats {
		if match(ch.Name) {
			items = append(items, searchItem{Title: ch.Name, ID: ch.ID, URL: "/chats/" + ch.ID})
		}
	}
	add("chats", items)

	skills, _ := s.db.ListSkills(u.ID)
	items = nil
	for _, sk := range skills {
		if match(sk.Name, sk.Description) {
			items = append(items, searchItem{Title: sk.Name, ID: sk.ID, URL: "/skills/" + sk.ID})
		}
	}
	add("skills", items)

	conns, _ := s.db.ListServiceConnections(c.Request().Context(), u.ID)
	items = nil
	for _, cn := range conns {
		if match(cn.Provider, cn.AccountLabel, cn.AccountIdentity) {
			items = append(items, searchItem{Title: cn.Provider + " · " + cn.AccountLabel, ID: cn.ID, URL: "/connections"})
		}
	}
	add("connections", items)

	names, _ := s.db.ListSecretNames(u.ID)
	items = nil
	for _, n := range names {
		if match(n) {
			items = append(items, searchItem{Title: n, URL: "/secrets"})
		}
	}
	add("secrets", items)

	rems, _ := s.db.ListReminders(u.ID)
	items = nil
	for _, r := range rems {
		if match(r.Message) {
			items = append(items, searchItem{Title: r.Message, ID: r.ID, URL: "/"})
		}
	}
	add("reminders", items)

	if groups == nil {
		groups = []searchGroup{}
	}
	return c.JSON(http.StatusOK, map[string]any{"query": q, "groups": groups})
}
```

Register on the dash group. (Verify `db.ServiceConnection` field names — `AccountLabel`/`AccountIdentity` — against `internal/db`; adjust if they differ.)

- [ ] **Steps 4-5:** tests PASS → `git commit -am "feat(api): global search endpoint (notes/agents/chats/skills/connections/secrets/reminders)"`

---

### Task 14: Parity inventory test + docs

**Files:** Create `web/api_parity_test.go`; Modify `/home/rookie/simple-agents-v2/CLAUDE.md` (routes section).

**Interfaces:** none new — this is the merge gate from spec §12.

- [ ] **Step 1: Write the parity test** (this is the LAST task so every row must pass):

```go
package web

import (
	"strings"
	"testing"
)

// TestAPIParityInventory is the sub-plan-1 merge gate: every planned /api/v1
// route from the spec's §12 inventory must be registered. Adding a UI surface
// in later sub-plans without its API row failing here first is a process bug.
func TestAPIParityInventory(t *testing.T) {
	s, _ := newAPITestServer(t)
	want := []string{
		"GET /api/v1/auth/session", "POST /api/v1/auth/login", "POST /api/v1/auth/logout",
		"POST /api/v1/auth/change-password",
		"GET /api/v1/workspaces", "POST /api/v1/workspaces", "POST /api/v1/workspaces/:id/enter",
		"POST /api/v1/workspaces/leave", "DELETE /api/v1/workspaces/:id",
		"GET /api/v1/workspaces/:id/permissions", "PUT /api/v1/workspaces/:id/permissions",
		"GET /api/v1/admin/overview", "GET /api/v1/admin/audit",
		"GET /api/v1/admin/settings", "PUT /api/v1/admin/settings",
		"GET /api/v1/agents", "GET /api/v1/agents/:id", "DELETE /api/v1/agents/:id",
		"POST /api/v1/agents/:id/run", "GET /api/v1/agents/:id/run/progress",
		"PUT /api/v1/agents/:id/schedule", "DELETE /api/v1/agents/:id/schedule",
		"PUT /api/v1/agents/:id/agent-md", "PUT /api/v1/agents/:id/skills",
		"PUT /api/v1/agents/:id/connections",
		"POST /api/v1/agents/design", "POST /api/v1/agents/design/cancel",
		"POST /api/v1/agents/design/resume", "POST /api/v1/agents/design/dismiss",
		"GET /api/v1/agents/design/progress", "GET /api/v1/agents/design/state",
		"POST /api/v1/agents/:id/edit/start",
		"GET /api/v1/skills", "POST /api/v1/skills", "GET /api/v1/skills/core/:slug",
		"GET /api/v1/skills/:id", "PUT /api/v1/skills/:id", "DELETE /api/v1/skills/:id",
		"POST /api/v1/skills/design", "POST /api/v1/skills/design/cancel",
		"POST /api/v1/skills/design/resume", "POST /api/v1/skills/design/dismiss",
		"GET /api/v1/skills/design/progress",
		"GET /api/v1/secrets", "POST /api/v1/secrets", "DELETE /api/v1/secrets/:name",
		"GET /api/v1/connectors", "POST /api/v1/connectors",
		"DELETE /api/v1/connectors/:platform", "POST /api/v1/connectors/:platform/test",
		"GET /api/v1/services", "POST /api/v1/services/:provider/creds",
		"POST /api/v1/services/:provider/connect", "POST /api/v1/services/:provider/apikey",
		"DELETE /api/v1/services/:id",
		"GET /api/v1/chats", "POST /api/v1/chats", "GET /api/v1/chats/:id",
		"POST /api/v1/chats/:id/messages", "POST /api/v1/chats/:id/resume",
		"POST /api/v1/chats/:id/stop", "DELETE /api/v1/chats/:id",
		"GET /api/v1/reminders", "POST /api/v1/reminders", "DELETE /api/v1/reminders/:id",
		"GET /api/v1/reminders/poll",
		"GET /api/v1/inbox", "GET /api/v1/inbox/poll", "POST /api/v1/inbox/:id/read",
		"POST /api/v1/inbox/read-all", "DELETE /api/v1/inbox/:id",
		"GET /api/v1/kb/tree", "GET /api/v1/kb/note", "PUT /api/v1/kb/note",
		"POST /api/v1/kb/new", "DELETE /api/v1/kb/note", "POST /api/v1/kb/rename",
		"GET /api/v1/kb/search", "GET /api/v1/kb/raw",
		"GET /api/v1/settings", "PUT /api/v1/settings/profile", "PUT /api/v1/settings/workspace",
		"PUT /api/v1/settings/coder", "POST /api/v1/settings/coder/test",
		"PUT /api/v1/settings/master-password",
		"GET /api/v1/setup", "POST /api/v1/setup",
		"GET /api/v1/search",
	}
	have := make(map[string]bool)
	for _, r := range s.echo.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing %d /api/v1 routes:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
```

If any earlier task registered a route under a slightly different path, fix the ROUTE to match this table (the table is the contract from the spec), not the test — unless the spec itself was wrong, in which case update both and note it in the commit message.

- [ ] **Step 2:** `go test ./web/... -run TestAPIParityInventory -count=1` → PASS (fix gaps if not).
- [ ] **Step 3:** Update the repo `CLAUDE.md` "Web UI routes" section: add a `/api/v1` subsection listing the groups (one line per group, pointing at `web/api_parity_test.go` as the authoritative inventory).
- [ ] **Step 4:** Full suite: `go test ./... -count=1 -timeout 120s` → PASS.
- [ ] **Step 5:** `git commit -am "test(api): parity inventory merge gate + docs"`

---

## Self-review notes (already applied)

- **Spec coverage:** §3 (envelope, middleware codes, SSE under /api/v1, search endpoint) → Tasks 1, 4, 13; §12 inventory → Task 14 table mirrors it 1:1 for the API tier (OAuth callback deliberately stays a browser route; agent import has NO current backend route — it ships with sub-plan 4's designer surface, noted in the spec table).
- **Legacy-JSON exception** is a deliberate, documented deviation from spec §3, resolved in sub-plan 6.
- **Type consistency:** helpers `jsonErr`/`bindAPI`/`doJSON`/`bootstrapAndLogin`/`createAndEnterWorkspace` and DTO `apiWorkspace`/`toAPIWorkspace` are defined once (Tasks 1-2) and only referenced afterward. Where a `db.*` model's exact field names were not verified during planning, the task says so explicitly and names the grep to run — adjust the seed/DTO line, never the test's assertions.
