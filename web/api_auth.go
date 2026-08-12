package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/auth"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/profile"
)

type apiWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Icon is a preset image slug the SPA resolves to a bundled graphic.
	// Empty means the UI falls back to the workspace name's initial.
	Icon       string    `json:"icon"`
	About      string    `json:"about"`
	NeedsSetup bool      `json:"needs_setup"`
	CreatedAt  time.Time `json:"created_at"`
}

func toAPIWorkspace(w *db.Workspace) apiWorkspace {
	return apiWorkspace{
		ID: w.ID, Name: w.Name, Icon: w.Icon, About: w.About,
		NeedsSetup: w.NeedsSetup, CreatedAt: w.CreatedAt,
	}
}

func (s *Server) registerAuthAPI(g *echo.Group) {
	g.GET("/auth/session", s.apiAuthSession)
	g.POST("/auth/login", s.apiLogin)
	g.POST("/auth/logout", s.apiLogout)
	g.POST("/auth/change-password", s.apiChangePassword, s.requireOwnerAPI)
	g.POST("/auth/lock", s.apiLock, s.requireOwnerAPI)
	g.POST("/auth/unlock", s.apiUnlock, s.requireOwnerAPI)
	g.POST("/auth/owner-verify", s.apiOwnerVerify, s.requireOwnerAPI)
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
	// Timezone travels with the session (not /api/v1/settings) because the SPA
	// already loads and caches this payload once, and the settings endpoint
	// re-probes the host filesystem for installed coders on every call. Always
	// present as a key — the SPA treats "" as "use the browser's own zone".
	out["timezone"] = ""
	if w, ok := s.activeWorkspace(c); ok {
		out["workspace"] = toAPIWorkspace(w)
		out["timezone"] = profile.Load(s.db, w.ID).Timezone
	}
	// Reported so a reload lands back on the lock screen. The lock is a server
	// flag, not a client overlay, so this is the SPA's only way to know.
	out["locked"] = s.isLocked(c)
	// Reported for the same reason "locked" is: the SPA already loads and caches
	// this payload once, so a reload lands in the right state without a probe.
	out["owner_verified"] = s.isOwnerVerified(c)
	return c.JSON(http.StatusOK, out)
}

// ── Screen lock ─────────────────────────────────────────────────────────────
//
// Locking leaves the session's owner_id AND active_workspace_id in place — the
// point is to blank the screen without giving up the entered workspace, which
// would otherwise cost a master-password re-entry anyway.
//
// The flag lives on the session rather than in the browser because a lock a
// reload clears is theatre: the API would still answer every request, and
// "unlock requires the master password" would not be true of anything. While
// locked, apiLockGate 423s the owner- and workspace-scoped routes; session,
// unlock and logout stay reachable so the SPA can render the lock screen and
// the user can always escape it.
//
// The threat model is someone walking up to an unattended screen, not someone
// who already holds the session cookie.

func (s *Server) apiLock(c echo.Context) error {
	o := c.Get("owner").(*db.Owner)
	if err := s.setLocked(c, true); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log("", "lock_ui", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "locked": true})
}

func (s *Server) apiUnlock(c echo.Context) error {
	o := c.Get("owner").(*db.Owner)
	var req struct {
		MasterPassword string `json:"master_password"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	w, ok := s.activeWorkspace(c)
	if !ok {
		// Locked with no workspace entered: nothing tenant-scoped to protect,
		// so clearing the flag returns the owner to the workspace picker
		// rather than stranding them at a prompt no password can satisfy.
		if err := s.setLocked(c, false); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"ok": true, "locked": false})
	}
	// Same check as entering the workspace: compare against the system-key
	// encrypted copy. Nothing about secret handling changes — the master
	// password is not held in the session before or after this.
	if !s.verifyWorkspaceMasterPassword(w, req.MasterPassword) {
		s.audit.Log(w.ID, "unlock_failed", "owner:"+o.ID, "", c.RealIP())
		return jsonErr(c, http.StatusUnauthorized, "invalid_master_password", "wrong master password")
	}
	if err := s.setLocked(c, false); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log(w.ID, "unlock_ui", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "locked": false})
}

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
		// Fail CLOSED: a failed stamp just means the owner tries again. (Unlike
		// the connector approval Parker, where failing closed would silently
		// halt an autonomous agent nobody gated.)
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log("", "owner_verified", "owner:"+o.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{
		"ok":             true,
		"verified_until": time.Now().Add(ownerVerifyTTL).UTC().Format(time.RFC3339),
	})
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
