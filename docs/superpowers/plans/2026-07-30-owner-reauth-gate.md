# Owner Re-authentication Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Require the owner password again — server-side, with a 15-minute TTL — before any install-level action: `/api/v1/admin/*`, `/api/v1/backup/*`, and `DELETE /api/v1/workspaces/:id`.

**Architecture:** Mirrors the existing screen lock exactly: a value on the server session, a middleware that refuses guarded routes, and an endpoint that sets it. The owner group in `setupAPIRoutes` splits into an ungated group (list/create/enter/leave workspaces) and a verified group (admin, backup, workspace delete). A route-coverage test enumerates `s.echo.Routes()` so a future route added to the owner group cannot silently skip the gate.

**Tech Stack:** Go 1.x + Echo v4 + gorilla sessions (all already in use — no new dependencies), React 19 + TypeScript + TanStack Query, vitest.

**Spec:** `docs/superpowers/specs/2026-07-30-owner-reauth-gate-design.md`

## Global Constraints

- **No new dependencies.** The session store, `auth.Authenticate`, and `s.audit.Log` all exist.
- **403, never 401**, for a missing or stale verification. The caller *is* authenticated; a 401 would send the SPA's generic handler to the login screen and drop the session.
- **Error code is exactly `owner_verification_required`.** The SPA branches on this string; any other 403 must not render the password prompt.
- **Middleware order is `requireOwnerAPI` → `apiLockGate` → `requireOwnerVerified`.** An unauthenticated caller must get its 401 without learning that a verification gate exists.
- **TTL is a package constant**, `ownerVerifyTTL = 15 * time.Minute`.
- **Never gate the escape hatches**: `/auth/session`, `/auth/login`, `/auth/logout`, `/auth/lock`, `/auth/unlock`, and `/auth/owner-verify` itself.
- **Conventional Commits.** `go test ./... -count=1` before Go commits; `cd web/ui && npx tsc -b && npx oxlint && npx vitest run` before SPA commits.

---

### Task 1: Session helpers and the `owner-verify` endpoint

**Files:**
- Modify: `web/server.go` (after `setLocked`, around line 268)
- Modify: `web/api_auth.go` (route registration ~line 37, handler after `apiUnlock` ~line 129, session payload ~line 68)
- Create: `web/api_owner_verify_test.go`

**Interfaces:**
- Consumes: `s.store` / `sessionName` / `s.currentOwner` (existing, `web/server.go:214-268`), `auth.Authenticate(db, username, password) (*db.Owner, error)` and `auth.ErrInvalidCreds` (existing, used at `web/api_auth.go:136`), `s.audit.Log(workspaceID, action, target, detail, ip)` (existing).
- Produces:
  - `const ownerVerifyTTL = 15 * time.Minute`
  - `func (s *Server) ownerVerifiedAt(c echo.Context) (time.Time, bool)`
  - `func (s *Server) isOwnerVerified(c echo.Context) bool`
  - `func (s *Server) setOwnerVerified(c echo.Context) error`
  - `POST /api/v1/auth/owner-verify` → `{"ok":true,"verified_until":"<RFC3339>"}` / 401 `invalid_password`
  - `owner_verified` bool on `GET /api/v1/auth/session`

- [ ] **Step 1: Write the failing test**

Create `web/api_owner_verify_test.go`:

```go
package web

import (
	"net/http"
	"testing"
	"time"
)

func TestOwnerVerifyCorrectPassword(t *testing.T) {
	_, env := newAPITestServer(t)

	rec := env.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{
		"password": testOwnerPassword,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-verify = %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK            bool   `json:"ok"`
		VerifiedUntil string `json:"verified_until"`
	}
	decodeJSON(t, rec, &body)
	if !body.OK {
		t.Error("ok = false")
	}
	until, err := time.Parse(time.RFC3339, body.VerifiedUntil)
	if err != nil {
		t.Fatalf("verified_until %q: %v", body.VerifiedUntil, err)
	}
	if d := time.Until(until); d <= 0 || d > ownerVerifyTTL+time.Minute {
		t.Errorf("verified_until is %v away, want ~%v", d, ownerVerifyTTL)
	}
}

func TestOwnerVerifyWrongPassword(t *testing.T) {
	_, env := newAPITestServer(t)
	rec := env.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{
		"password": "definitely-not-it",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("owner-verify = %d, want 401; body %s", rec.Code, rec.Body.String())
	}
	if !containsJSONError(rec, "invalid_password") {
		t.Errorf("want invalid_password, got %s", rec.Body.String())
	}
}

// The username must come from the session, never the request. There is exactly
// one owner, so accepting a client-supplied username adds an oracle for nothing.
func TestOwnerVerifyIgnoresRequestUsername(t *testing.T) {
	_, env := newAPITestServer(t)
	rec := env.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{
		"username": "someone-else",
		"password": testOwnerPassword,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner-verify = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
}

func TestSessionReportsOwnerVerified(t *testing.T) {
	_, env := newAPITestServer(t)

	rec := env.get(t, "/api/v1/auth/session")
	var before struct {
		OwnerVerified bool `json:"owner_verified"`
	}
	decodeJSON(t, rec, &before)
	if before.OwnerVerified {
		t.Error("owner_verified should be false before verifying")
	}

	if rec := env.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{
		"password": testOwnerPassword,
	}); rec.Code != http.StatusOK {
		t.Fatalf("verify failed: %s", rec.Body.String())
	}

	rec = env.get(t, "/api/v1/auth/session")
	var after struct {
		OwnerVerified bool `json:"owner_verified"`
	}
	decodeJSON(t, rec, &after)
	if !after.OwnerVerified {
		t.Error("owner_verified should be true after verifying")
	}
}

// A wrong password is a security event and must be recorded, exactly as
// unlock_failed is.
func TestOwnerVerifyFailureIsAudited(t *testing.T) {
	s, env := newAPITestServer(t)
	env.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{"password": "nope"})

	logs, err := s.db.ListAuditLogs(50)
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	for _, l := range logs {
		if l.Action == "owner_verify_failed" {
			return
		}
	}
	t.Error("no owner_verify_failed audit row written")
}
```

**Before writing this**, read `web/api_auth_test.go` and `web/api_lock_test.go`
for the real harness names: the test-server constructor, the session-carrying
request helpers, the owner password the harness bootstraps with, the JSON
decode helper, and the error-code assertion helper. Substitute those exact
names for `newAPITestServer` / `env.postJSON` / `env.get` / `testOwnerPassword`
/ `decodeJSON` / `containsJSONError`, and check `db.ListAuditLogs`'s real
signature (`grep -n "func (d \*DB) ListAuditLogs" internal/db/repositories.go`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./web/ -run 'TestOwnerVerify|TestSessionReportsOwnerVerified' -v`
Expected: FAIL — 404 on the route, `undefined: ownerVerifyTTL`.

- [ ] **Step 3: Add the session helpers**

Append to `web/server.go` after `setLocked`:

```go
// ownerVerifyTTL is how long one owner-password confirmation lasts.
//
// Chosen from the shape of real owner work: saving a backup config, running it,
// and listing the resulting snapshots is three or four requests over a couple of
// minutes, which should cost ONE password entry, not four. Short enough that a
// browser walked away from re-locks within a coffee break.
const ownerVerifyTTL = 15 * time.Minute

// ownerVerifiedAt returns when the owner last confirmed their password on this
// session. Stored as Unix seconds because gorilla's default session encoder
// handles primitives without registering a gob type for time.Time.
func (s *Server) ownerVerifiedAt(c echo.Context) (time.Time, bool) {
	sess, err := s.store.Get(c.Request(), sessionName)
	if err != nil {
		return time.Time{}, false
	}
	secs, ok := sess.Values["owner_verified_at"].(int64)
	if !ok || secs == 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// isOwnerVerified reports whether the owner has confirmed their password within
// the TTL. Absent means NOT verified, so an older session cookie can never be
// read as pre-verified.
func (s *Server) isOwnerVerified(c echo.Context) bool {
	at, ok := s.ownerVerifiedAt(c)
	return ok && time.Since(at) < ownerVerifyTTL
}

// setOwnerVerified stamps a fresh confirmation. Like setLocked, it leaves
// owner_id and active_workspace_id alone.
func (s *Server) setOwnerVerified(c echo.Context) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	sess.Values["owner_verified_at"] = time.Now().Unix()
	return sess.Save(c.Request(), c.Response())
}
```

Add `"time"` to `web/server.go`'s imports if absent.

- [ ] **Step 4: Add the endpoint and the session field**

In `web/api_auth.go`, register the route beside lock/unlock:

```go
	g.POST("/auth/owner-verify", s.apiOwnerVerify, s.requireOwnerAPI)
```

Add the handler after `apiUnlock`:

```go
// ── Owner re-authentication ─────────────────────────────────────────────────
//
// Install-level settings — whole-install restore, snapshot deletion, workspace
// deletion, the public URL — were reachable by anyone holding a logged-in owner
// session, however old. This asks for the owner password again.
//
// It is NOT protection against someone who knows that password; nothing at this
// layer can be. It raises the bar against an unattended-but-unlocked session and
// against a leaked cookie being used for install-destroying actions.
//
// The username comes from the session's owner record, never from the request:
// the single-owner model means there is exactly one valid username, so accepting
// one from the client would add an oracle and buy nothing.
func (s *Server) apiOwnerVerify(c echo.Context) error {
	o := c.Get("owner").(*db.Owner)
	var req struct {
		Password string `json:"password"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if _, err := auth.Authenticate(s.db, o.Username, req.Password); err != nil {
		s.audit.Log("", "owner_verify_failed", "owner:"+o.ID, "", c.RealIP())
		if errors.Is(err, auth.ErrInvalidCreds) {
			return jsonErr(c, http.StatusUnauthorized, "invalid_password", "wrong owner password")
		}
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	if err := s.setOwnerVerified(c); err != nil {
		// Fail CLOSED: a failed stamp just means the owner tries again, unlike
		// a parked agent action where failing closed would silently halt work.
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log("", "owner_verified", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{
		"ok":             true,
		"verified_until": time.Now().Add(ownerVerifyTTL).UTC().Format(time.RFC3339),
	})
}
```

In `apiAuthSession`, beside `out["locked"]`:

```go
	// Reported so a reload lands in the right state without a probe request —
	// the same reasoning that put "locked" and "timezone" here.
	out["owner_verified"] = s.isOwnerVerified(c)
```

Add `"time"` to `web/api_auth.go`'s imports if absent.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./web/ -run 'TestOwnerVerify|TestSessionReportsOwnerVerified' -v`
Expected: PASS (five tests).

- [ ] **Step 6: Commit**

```bash
git add web/server.go web/api_auth.go web/api_owner_verify_test.go
git commit -m "feat(web): add owner password re-verification endpoint"
```

---

### Task 2: `requireOwnerVerified` and the route split

**Files:**
- Modify: `web/api.go:105-130` (`setupAPIRoutes`) and the middleware section (~line 84-100)
- Modify: `web/api_workspaces.go:17-30` (`registerWorkspacesAPI` takes two groups)
- Modify: `web/api_backup.go` (`registerBackupAPI` moves to the verified group — signature unchanged)
- Modify: `web/api_owner_verify_test.go`

**Interfaces:**
- Consumes: `s.isOwnerVerified` (Task 1), `jsonErr` (existing).
- Produces:
  - `func (s *Server) requireOwnerVerified(next echo.HandlerFunc) echo.HandlerFunc`
  - `func (s *Server) registerWorkspacesAPI(g, verified *echo.Group)` — **signature change**, both call sites are in `setupAPIRoutes`

- [ ] **Step 1: Write the failing test**

Append to `web/api_owner_verify_test.go`:

```go
// Every guarded route 403s without a stamp, and answers with one. Table-driven
// over one route per guarded family.
func TestOwnerGateBlocksAndAllows(t *testing.T) {
	guarded := []struct{ method, path string }{
		{"GET", "/api/v1/admin/overview"},
		{"GET", "/api/v1/admin/audit"},
		{"GET", "/api/v1/admin/settings"},
		{"GET", "/api/v1/admin/public-url"},
		{"GET", "/api/v1/backup/config"},
		{"GET", "/api/v1/backup/snapshots"},
	}
	for _, r := range guarded {
		_, env := newAPITestServer(t)
		rec := env.do(t, r.method, r.path, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without verification = %d, want 403", r.method, r.path, rec.Code)
			continue
		}
		if !containsJSONError(rec, "owner_verification_required") {
			t.Errorf("%s %s: want owner_verification_required, got %s", r.method, r.path, rec.Body.String())
		}
		if rec := env.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{
			"password": testOwnerPassword,
		}); rec.Code != http.StatusOK {
			t.Fatalf("verify failed: %s", rec.Body.String())
		}
		if rec := env.do(t, r.method, r.path, nil); rec.Code == http.StatusForbidden {
			t.Errorf("%s %s still 403 after verification: %s", r.method, r.path, rec.Body.String())
		}
	}
}

// A stale stamp must be refused, or the TTL is decoration.
func TestOwnerGateExpires(t *testing.T) {
	_, env := newAPITestServer(t)
	if rec := env.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{
		"password": testOwnerPassword,
	}); rec.Code != http.StatusOK {
		t.Fatalf("verify failed: %s", rec.Body.String())
	}
	env.setSessionValue(t, "owner_verified_at", time.Now().Add(-ownerVerifyTTL-time.Minute).Unix())

	rec := env.do(t, "GET", "/api/v1/admin/overview", nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("stale stamp = %d, want 403", rec.Code)
	}
}

// Ordering: an unauthenticated caller must not learn a verification gate exists.
func TestOwnerGateOrderedAfterAuth(t *testing.T) {
	s, _ := newAPITestServer(t)
	req := httptest.NewRequest("GET", "/api/v1/admin/overview", nil) // no cookie
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no session = %d, want 401 from requireOwnerAPI", rec.Code)
	}
}

// Workspace entry, listing, and the escape hatches stay reachable.
func TestOwnerGateLeavesEscapeHatchesOpen(t *testing.T) {
	_, env := newAPITestServer(t)
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/v1/auth/session"},
		{"GET", "/api/v1/workspaces"},
	} {
		if rec := env.do(t, r.method, r.path, nil); rec.Code == http.StatusForbidden {
			t.Errorf("%s %s must not be gated, got 403: %s", r.method, r.path, rec.Body.String())
		}
	}
}

// THE test that keeps this honest: a route added to the owner group later must
// not silently skip the gate. Enumerated from the router, in the mould of
// TestAPIParityInventory.
func TestEveryInstallLevelRouteIsGated(t *testing.T) {
	s, _ := newAPITestServer(t)
	for _, r := range s.echo.Routes() {
		gatedFamily := strings.HasPrefix(r.Path, "/api/v1/admin/") ||
			strings.HasPrefix(r.Path, "/api/v1/backup/") ||
			(r.Path == "/api/v1/workspaces/:id" && r.Method == "DELETE")
		if !gatedFamily {
			continue
		}
		_, env := newAPITestServer(t)
		rec := env.do(t, r.Method, concreteRoutePath(r.Path), nil)
		if rec.Code != http.StatusForbidden || !containsJSONError(rec, "owner_verification_required") {
			t.Errorf("%s %s is not behind requireOwnerVerified (got %d: %s)",
				r.Method, r.Path, rec.Code, rec.Body.String())
		}
	}
}

// concreteRoutePath substitutes a dummy value for each :param so the route
// matches. The gate runs before any handler, so the value never has to exist.
func concreteRoutePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, ":") {
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}
```

`env.do(method, path, body)` and `env.setSessionValue` may not exist in the
harness. If not, add them next to the existing helpers: `do` issues a request
carrying the session cookie and returns the recorder; `setSessionValue` decodes
the session cookie, sets a key, re-encodes it, and replaces the stored cookie.
If mutating the session from a test is impractical with the current harness,
replace `TestOwnerGateExpires` with a direct unit test of `isOwnerVerified`
against a hand-built `echo.Context` — the TTL must be covered one way or the
other.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./web/ -run TestOwnerGate -v`
Expected: FAIL — every guarded route currently returns 200.

- [ ] **Step 3: Add the middleware**

In `web/api.go`, after `apiLockGate`:

```go
// requireOwnerVerified refuses install-level routes until the owner has
// re-entered their password within ownerVerifyTTL.
//
// Ordered AFTER requireOwnerAPI so an unauthenticated caller still gets its 401
// and never learns this gate exists.
//
// 403, not 401: the caller IS authenticated. A 401 would send the SPA's generic
// handler to the login screen and drop the session — wrong, and hostile.
//
// This is enforced on the server rather than in the SPA because the Owner tab
// fronts POST /api/v1/backup/restore, which swaps the whole install on the next
// boot. A UI-only gate is bypassed by curling that endpoint with the same
// cookie, which would make the gate deter shoulder-surfing and nothing else.
func (s *Server) requireOwnerVerified(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !s.isOwnerVerified(c) {
			return jsonErr(c, http.StatusForbidden, "owner_verification_required",
				"confirm your owner password to continue")
		}
		return next(c)
	}
}
```

- [ ] **Step 4: Split the owner group**

In `setupAPIRoutes`:

```go
	// Owner-gated (no workspace needed).
	owner := api.Group("", s.requireOwnerAPI, s.apiLockGate)

	// Install-level: everything that can destroy or replace the whole install.
	// Backup lives here rather than on `owner` because one snapshot spans every
	// workspace and POST /backup/restore swaps the lot on the next boot.
	ownerVerified := api.Group("", s.requireOwnerAPI, s.apiLockGate, s.requireOwnerVerified)

	s.registerWorkspacesAPI(owner, ownerVerified)
	s.registerBackupAPI(ownerVerified)
```

In `web/api_workspaces.go`:

```go
// registerWorkspacesAPI splits its routes across two groups.
//
// g is owner-gated only: listing is already in the session payload, creating a
// workspace is additive and reversible, and entering already demands that
// workspace's own master password — re-asking for the owner password on every
// switch would be punitive.
//
// verified additionally requires a fresh owner-password confirmation: deleting a
// workspace destroys a tenant, and the admin routes are the install's settings.
func (s *Server) registerWorkspacesAPI(g, verified *echo.Group) {
	g.GET("/workspaces", s.apiListWorkspaces)
	g.POST("/workspaces", s.apiCreateWorkspace)
	g.POST("/workspaces/leave", s.apiLeaveWorkspace)
	g.POST("/workspaces/:id/enter", s.apiEnterWorkspace)

	verified.DELETE("/workspaces/:id", s.apiDeleteWorkspace)

	verified.GET("/admin/overview", s.apiAdminOverview)
	verified.GET("/admin/audit", s.apiAdminAudit)
	verified.GET("/admin/settings", s.apiAdminGetSettings)
	verified.GET("/admin/public-url", s.apiPublicURLState)
	verified.PUT("/admin/public-url", s.apiSavePublicURL)
	verified.POST("/admin/public-url/test", s.apiTestPublicURL)
}
```

- [ ] **Step 5: Run the web suite**

Run: `go test ./web/ -count=1 -timeout 300s`
Expected: PASS. Existing admin/backup tests will fail with 403 — each needs a
verify call in its setup. Add a harness helper rather than repeating it:

```go
// verifyOwner stamps the session so install-level routes are reachable. Every
// admin/backup test needs it now that those routes are gated.
func (e *apiTestEnv) verifyOwner(t *testing.T) {
	t.Helper()
	if rec := e.postJSON(t, "/api/v1/auth/owner-verify", map[string]any{
		"password": testOwnerPassword,
	}); rec.Code != http.StatusOK {
		t.Fatalf("verifyOwner: %d %s", rec.Code, rec.Body.String())
	}
}
```

`TestAPIParityInventory` should still pass unchanged — the routes are the same,
only their group differs. If it fails, the split dropped a route; compare
against its `want` list.

- [ ] **Step 6: Commit**

```bash
git add web/
git commit -m "feat(web): gate admin, backup and workspace delete behind owner re-auth"
```

---

### Task 3: SPA — the Owner tab asks for the password

**Files:**
- Create: `web/ui/src/pages/settings/OwnerGate.tsx`
- Modify: `web/ui/src/pages/settings/SettingsPage.tsx:462` (the `owner` section render)
- Modify: `web/ui/src/lib/session.ts` (session type gains `owner_verified`)
- Modify: `web/ui/src/pages/settings/OwnerSections.test.tsx`

**Interfaces:**
- Consumes: `POST /api/v1/auth/owner-verify` and the 403 `owner_verification_required` from Task 2; `ApiError` (which must expose the server's error code — check `web/ui/src/lib/api.ts` and add a `code` field if it only carries the message).
- Produces: `export function OwnerGate({ children }: { children: React.ReactNode })`

- [ ] **Step 1: Write the failing test**

Add to `web/ui/src/pages/settings/OwnerSections.test.tsx`:

```tsx
it("renders the password prompt when a query 403s with owner_verification_required", async () => {
  server.use(
    http.get("/api/v1/admin/overview", () =>
      HttpResponse.json({ error: "owner_verification_required" }, { status: 403 }),
    ),
  );
  renderOwnerTab();
  expect(await screen.findByLabelText(/owner password/i)).toBeInTheDocument();
  expect(screen.queryByText(/audit/i)).not.toBeInTheDocument();
});

it("renders the body after a successful verify", async () => {
  let verified = false;
  server.use(
    http.post("/api/v1/auth/owner-verify", async () => {
      verified = true;
      return HttpResponse.json({ ok: true, verified_until: "2099-01-01T00:00:00Z" });
    }),
    http.get("/api/v1/admin/overview", () =>
      verified
        ? HttpResponse.json({ workspaces: 1, agents: 0 })
        : HttpResponse.json({ error: "owner_verification_required" }, { status: 403 }),
    ),
  );
  renderOwnerTab();
  await userEvent.type(await screen.findByLabelText(/owner password/i), "hunter2");
  await userEvent.click(screen.getByRole("button", { name: /unlock/i }));
  await waitFor(() => expect(screen.queryByLabelText(/owner password/i)).not.toBeInTheDocument());
});

it("shows an error and keeps the prompt on a wrong password", async () => {
  server.use(
    http.get("/api/v1/admin/overview", () =>
      HttpResponse.json({ error: "owner_verification_required" }, { status: 403 }),
    ),
    http.post("/api/v1/auth/owner-verify", () =>
      HttpResponse.json({ error: "invalid_password", message: "wrong owner password" }, { status: 401 }),
    ),
  );
  renderOwnerTab();
  await userEvent.type(await screen.findByLabelText(/owner password/i), "nope");
  await userEvent.click(screen.getByRole("button", { name: /unlock/i }));
  expect(await screen.findByText(/wrong owner password/i)).toBeInTheDocument();
  expect(screen.getByLabelText(/owner password/i)).toBeInTheDocument();
});

// A different 403 is a real permission error, not a verification gate.
it("does not render the prompt for an unrelated 403", async () => {
  server.use(
    http.get("/api/v1/admin/overview", () =>
      HttpResponse.json({ error: "forbidden" }, { status: 403 }),
    ),
  );
  renderOwnerTab();
  await waitFor(() =>
    expect(screen.queryByLabelText(/owner password/i)).not.toBeInTheDocument(),
  );
});
```

Match the file's existing MSW/harness setup — read `OwnerSections.test.tsx` and
`settings.test.tsx` first and reuse their `server` handle and render helper,
adding a `renderOwnerTab` wrapper if none exists.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web/ui && npx vitest run src/pages/settings/OwnerSections.test.tsx`
Expected: FAIL — no prompt is rendered.

- [ ] **Step 3: Write `OwnerGate`**

Create `web/ui/src/pages/settings/OwnerGate.tsx`:

```tsx
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api, ApiError } from "@/lib/api";

const GATE_CODE = "owner_verification_required";

/**
 * Wraps the Owner settings tab. Install-level endpoints 403 with
 * `owner_verification_required` until the owner re-enters their password, so
 * this probes one of them and renders a prompt in place of the body.
 *
 * There is deliberately no client-side TTL timer: the server owns expiry, and a
 * timer here could only disagree with it. When the stamp lapses the next request
 * 403s and the prompt comes back on its own.
 */
export function OwnerGate({ children }: { children: React.ReactNode }) {
  const qc = useQueryClient();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // The cheapest install-level endpoint; its 403 is the gate signal.
  const probe = useQuery({
    queryKey: ["admin", "overview"],
    queryFn: () => api.get<unknown>("/api/v1/admin/overview"),
    retry: false,
  });

  const gated =
    probe.error instanceof ApiError &&
    probe.error.status === 403 &&
    probe.error.code === GATE_CODE;

  async function verify(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await api.post("/api/v1/auth/owner-verify", { password });
      setPassword("");
      await qc.invalidateQueries();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Something went wrong");
    } finally {
      setBusy(false);
    }
  }

  if (probe.isLoading) return <div className="text-muted-2">Loading…</div>;
  if (!gated) return <>{children}</>;

  return (
    <section className="max-w-sm">
      <div className="flex items-center gap-2">
        <ShieldCheck className="size-5" />
        <h2 className="text-lg font-bold">Owner settings</h2>
      </div>
      <p className="mt-1 text-sm text-muted-2">
        These settings cover your whole install — every workspace, and your
        backups. Confirm your owner password to continue.
      </p>
      {error && (
        <div className="mt-4 flex items-center gap-2 rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">
          <AlertTriangle className="size-3.5 shrink-0" />
          {error}
        </div>
      )}
      <form onSubmit={(e) => void verify(e)} className="mt-4 space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="owner_password">Owner password</Label>
          <Input
            id="owner_password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <Button type="submit" disabled={busy || !password}>
          {busy ? "Checking…" : "Unlock"}
        </Button>
      </form>
    </section>
  );
}
```

If `ApiError` has no `code` field, add one in `web/ui/src/lib/api.ts` — parse
the response body's `error` key alongside `message` and expose it. Every other
`ApiError` consumer keeps working, since the field is additive.

- [ ] **Step 4: Wrap the Owner tab**

In `SettingsPage.tsx` line ~462:

```tsx
            {section === "owner" && (
              <OwnerGate>
                <OwnerSections />
              </OwnerGate>
            )}
```

Add the import. In `web/ui/src/lib/session.ts`, add `owner_verified: boolean`
to the session response type (unused by `OwnerGate`, which reads the live 403,
but it keeps the type honest and is available for a future badge).

- [ ] **Step 5: Run the checks**

```bash
cd web/ui && npx tsc -b && npx oxlint && npx vitest run
```
Expected: PASS. Existing `OwnerSections` / `BackupSection` tests that mock the
admin endpoints with 200 keep passing — the gate is transparent when the probe
succeeds.

- [ ] **Step 6: Commit**

```bash
git add web/ui/src
git commit -m "feat(web/ui): prompt for the owner password before showing Owner settings"
```

---

### Task 4: Gate and manual verification

**Files:** none modified.

- [ ] **Step 1: Run the full gate**

Run: `make ci`
Expected: PASS.

- [ ] **Step 2: Prove the server-side guarantee by hand**

This is the check that distinguishes this implementation from a UI-only one.

```bash
make deploy && sleep 3

# Log in and keep the session cookie.
curl -sS -c /tmp/rk.jar -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"username":"<owner>","password":"<pw>"}' | head -c 200; echo

# With a valid session but no verification: must be 403.
curl -sS -b /tmp/rk.jar -o /dev/null -w '%{http_code}\n' \
  http://127.0.0.1:8080/api/v1/backup/config
# expect: 403

curl -sS -b /tmp/rk.jar http://127.0.0.1:8080/api/v1/backup/config
# expect: {"error":"owner_verification_required", ...}

# Verify, then the same request must succeed.
curl -sS -b /tmp/rk.jar -c /tmp/rk.jar -X POST \
  http://127.0.0.1:8080/api/v1/auth/owner-verify \
  -H 'content-type: application/json' -d '{"password":"<pw>"}'
# expect: {"ok":true,"verified_until":"..."}

curl -sS -b /tmp/rk.jar -o /dev/null -w '%{http_code}\n' \
  http://127.0.0.1:8080/api/v1/backup/config
# expect: 200

rm -f /tmp/rk.jar
```

- [ ] **Step 3: Check the UI**

Open Settings → Owner: the password prompt appears in place of the body. Enter
the password: workspaces, audit, and backup render. Switch to another Settings
tab and back: no second prompt (within the TTL). Other tabs never prompt.

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin feat/identity-source-of-truth
gh pr create --title "feat(web): require owner re-authentication for install-level settings" --body "$(cat <<'EOF'
Implements docs/superpowers/specs/2026-07-30-owner-reauth-gate-design.md.

/admin/*, /backup/* and workspace deletion were reachable by anyone holding a
logged-in owner session, however old — including POST /api/v1/backup/restore,
which swaps the whole install on the next boot.

- POST /api/v1/auth/owner-verify stamps owner_verified_at on the session;
  requireOwnerVerified 403s owner_verification_required when it is absent or
  older than 15 minutes.
- Enforced on the server, mirroring the existing screen lock, so a leaked cookie
  cannot curl past a UI-only gate. Workspace enter/leave stay ungated (entering
  already needs that workspace's master password).
- A route-coverage test enumerates s.echo.Routes() so a future route added to
  the owner group cannot silently skip the gate.
- The SPA renders an inline prompt in place of the Owner tab body on 403.

Not protection against someone who knows the owner password; nothing at this
layer can be.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
|---|---|
| `POST /api/v1/auth/owner-verify`, request/response shape | 1 |
| Username from the session, never the request | 1 (test) |
| `auth.Authenticate` reuse; 401 `invalid_password` | 1 |
| `owner_verify_failed` audit with real IP | 1 (test) |
| Session-stored stamp, mirroring `locked` | 1 |
| `requireOwnerVerified`, 403 `owner_verification_required` | 2 |
| 15-minute TTL as a constant | 1 (`ownerVerifyTTL`) |
| Ordered after `requireOwnerAPI` | 2 (test) |
| Coverage: `/admin/*`, `/backup/*`, `DELETE /workspaces/:id` | 2 |
| Ungated: enter/leave/list/create, change-password, auth routes | 2 (test) |
| `owner_verified` on the session payload | 1 |
| SPA inline prompt, refetch on success, no client timer | 3 |
| Unrelated 403 does not render the prompt | 3 (test) |
| Route-coverage test against `s.echo.Routes()` | 2 |
| Fail-closed on a session write error | 1 |
| Accepted cost: no rate limiting | recorded in the spec; no task (deliberate) |

**Placeholder scan:** two harness-verification instructions (Task 1 Step 1,
Task 3 Step 1) and one conditional fallback (Task 2 Step 1: unit-test
`isOwnerVerified` directly if the harness cannot mutate a session). Each names
the exact file to read and the exact substitution; the assertions themselves are
fully specified.

**Type consistency:** `ownerVerifyTTL` is defined once (Task 1 Step 3) and used
in the handler, the middleware, and two tests. `isOwnerVerified` /
`setOwnerVerified` / `ownerVerifiedAt` signatures match between definition and
every caller. The error code string `owner_verification_required` is identical in
the middleware (Task 2 Step 3), the Go tests, and `GATE_CODE` in `OwnerGate.tsx`
(Task 3 Step 3). `registerWorkspacesAPI(g, verified *echo.Group)` matches its
single call site in `setupAPIRoutes`.
