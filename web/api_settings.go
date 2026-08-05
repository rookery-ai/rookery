package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ilijad1/rookery/internal/auth"
	"github.com/ilijad1/rookery/internal/coder"
	"github.com/ilijad1/rookery/internal/config"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
	"github.com/ilijad1/rookery/internal/llm"
	"github.com/ilijad1/rookery/internal/profile"
	"github.com/ilijad1/rookery/internal/secrets"
	"github.com/labstack/echo/v4"
)

// detectedCoders returns the installed CLI coders, or an empty slice in slim
// mode. Short-circuiting matters twice over: the probe hits the host filesystem
// on every settings load, and in slim mode a coder that happens to be installed
// still cannot be used, so listing it would be a lie.
//
// Always returns a non-nil slice so the JSON field marshals as [] not null.
func (s *Server) detectedCoders() []apiDetectedCoderDTO {
	out := []apiDetectedCoderDTO{}
	if s.coderMode() == config.ModeSlim {
		return out
	}
	for _, d := range coder.DetectInstalled() {
		out = append(out, apiDetectedCoderDTO{Name: d.Name, Bin: d.Bin, BackendType: d.BackendType})
	}
	return out
}

// rejectLocalInSlim guards the write path. The SPA hides the local option in
// slim mode, but a stale tab or a plain curl would otherwise still persist
// coder_kind=local and produce a workspace that can never run.
func (s *Server) rejectLocalInSlim(kind string) error {
	if s.coderMode() == config.ModeSlim && kind == "local" {
		return echo.NewHTTPError(http.StatusBadRequest,
			"this build has no CLI coder (ROOKERY_CODER_MODE=slim) — choose the API engine")
	}
	return nil
}

// registerSettingsAPI registers the settings + setup + coder JSON endpoints on
// the given group (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI
// + requireSetupCompleteAPI — the latter exempts "/api/v1/setup" so the wizard
// works before setup completes).
func (s *Server) registerSettingsAPI(g *echo.Group) {
	g.GET("/settings", s.apiGetSettings)
	g.PUT("/settings/profile", s.apiPutSettingsProfile)
	g.PUT("/settings/workspace", s.apiPutSettingsWorkspace)
	g.PUT("/settings/workspace/icon", s.apiPutSettingsWorkspaceIcon)
	g.PUT("/settings/coder", s.apiPutSettingsCoder)
	g.POST("/settings/coder/test", s.handleSmokeCoder) // unchanged, just re-registered
	g.PUT("/settings/master-password", s.apiPutSettingsMasterPassword)

	g.GET("/setup", s.apiGetSetup)
	g.POST("/setup", s.apiPostSetup)

	// Setup-scoped mirrors of two connector endpoints. Every /connectors route
	// sits on this same group behind requireSetupCompleteAPI, so the wizard
	// 403s on all of them while needs_setup is true — which is why onboarding
	// could never run the test-and-link steps the connections page runs.
	//
	// Mirroring exactly these two (read + test) rather than exempting the
	// /connectors group keeps a half-configured workspace away from the
	// delete, re-save and unlink endpoints that share it. The guard exempts by
	// PREFIX ("/api/v1/setup"), so these need no change to it.
	g.GET("/setup/platforms", s.apiSetupPlatforms)
	g.POST("/setup/platforms/:platform/test", s.apiSetupTestPlatform)
}

// apiSetupPlatforms serves the same catalog as apiListConnectors, reachable
// while the setup wizard is still running.
// GET /api/v1/setup/platforms → {"platforms":[...]}
func (s *Server) apiSetupPlatforms(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	return c.JSON(http.StatusOK, apiConnectorListResponse{Platforms: s.connectorPlatformList(w)})
}

// apiSetupTestPlatform is apiTestConnector for the setup wizard: it proves the
// saved token still authenticates before the wizard asks the operator to go
// send /start. POST /api/v1/setup/platforms/:platform/test
func (s *Server) apiSetupTestPlatform(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	platform := c.Param("platform")

	if _, ok := gateway.CredSpecFor(platform); !ok {
		return jsonErr(c, http.StatusNotFound, "not_found", "unknown platform: "+platform)
	}

	identity, err := s.testConnectorIdentity(w.ID, platform)
	if err != nil {
		return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: false, Error: err.Error()})
	}
	return c.JSON(http.StatusOK, apiTestConnectorResponse{OK: true, Identity: identity.Username})
}

// ── Shared catalog builder ───────────────────────────────────────────────────

// apiCoderCatalogEntry mirrors the JSON shape the SPA settings/setup pages
// consume (see coderCatalogSlice below — the sole surviving builder).
type apiCoderCatalogEntry struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Base        string `json:"base"`
	Model       string `json:"model"`
	Docs        string `json:"docs"`
	RequiresKey bool   `json:"requiresKey"`
	Custom      bool   `json:"custom"`
	HasKey      bool   `json:"hasKey"`
	Group       string `json:"group"` // coder.GroupHosted | coder.GroupLocal
}

// coderCatalogSlice builds the direct-LLM-API provider catalog as a plain slice.
// This is the single source used by apiGetSettings and apiGetSetup (plain JSON
// array) — the template-era coderCatalogJSON wrapper was removed in the SPA
// cutover (Task 8).
func (s *Server) coderCatalogSlice(secretNames []string) []apiCoderCatalogEntry {
	have := make(map[string]bool, len(secretNames))
	for _, n := range secretNames {
		have[n] = true
	}
	cat := coder.APIProviders()
	out := make([]apiCoderCatalogEntry, 0, len(cat))
	for _, p := range cat {
		out = append(out, apiCoderCatalogEntry{
			Name: p.Name, Label: p.Label, Base: llm.DefaultBaseURL(p.Name),
			Model: p.ModelPlaceholder,
			Docs:  p.DocsURL, RequiresKey: p.RequiresKey, Custom: p.Custom,
			HasKey: have[coder.CoderKeySecretName(p.Name)],
			Group:  p.Group,
		})
	}
	return out
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type apiProfileDTO struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Location    string `json:"location"`
	Timezone    string `json:"timezone"`
	Tone        string `json:"tone"`
	Language    string `json:"language"`
	Notes       string `json:"notes"`
}

type apiWorkspaceMetaDTO struct {
	Name  string `json:"name"`
	About string `json:"about"`
}

type apiCoderConfigDTO struct {
	Kind         string `json:"kind"`
	Bin          string `json:"bin"`
	TimeoutS     int    `json:"timeout_s"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	BaseURL      string `json:"base_url"`
	APIKeySecret string `json:"api_key_secret"`
}

type apiDetectedCoderDTO struct {
	Name        string `json:"name"`
	Bin         string `json:"bin"`
	BackendType string `json:"backend_type"`
}

type apiAPIProviderDTO struct {
	Name             string `json:"name"`
	Label            string `json:"label"`
	Schema           string `json:"schema"`
	ModelPlaceholder string `json:"model_placeholder"`
	DocsURL          string `json:"docs_url"`
	RequiresKey      bool   `json:"requires_key"`
	Custom           bool   `json:"custom"`
}

// ── GET /api/v1/settings ─────────────────────────────────────────────────────

func (s *Server) apiGetSettings(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	prof := profile.Load(s.db, w.ID)
	dn := prof.DisplayName
	if dn == "" {
		dn = w.Name
	}
	secretNames, _ := s.db.ListSecretNames(w.ID)
	if secretNames == nil {
		secretNames = []string{}
	}

	detOut := s.detectedCoders()

	providers := coder.APIProviders()
	provOut := make([]apiAPIProviderDTO, 0, len(providers))
	for _, p := range providers {
		provOut = append(provOut, apiAPIProviderDTO{
			Name: p.Name, Label: p.Label, Schema: p.Schema, ModelPlaceholder: p.ModelPlaceholder,
			DocsURL: p.DocsURL, RequiresKey: p.RequiresKey, Custom: p.Custom,
		})
	}

	return c.JSON(http.StatusOK, map[string]any{
		"profile": apiProfileDTO{
			DisplayName: dn, Email: prof.Email, Location: prof.Location,
			Timezone: prof.Timezone, Tone: prof.Tone, Language: prof.Language, Notes: prof.Notes,
		},
		"workspace": apiWorkspaceMetaDTO{Name: w.Name, About: w.About},
		"coder": apiCoderConfigDTO{
			Kind: w.CoderKind, Bin: w.CoderBin, TimeoutS: w.CoderTimeoutS,
			Provider: w.CoderProvider, Model: w.CoderModel, BaseURL: w.CoderBaseURL,
			APIKeySecret: w.CoderAPIKeySecret,
		},
		"detected_coders": detOut,
		"api_providers":   provOut,
		"coder_catalog":   s.coderCatalogSlice(secretNames),
		"secret_names":    secretNames,
		// Build policy, not host state: slim means this build has no CLI coder
		// at all, so the SPA must not offer the local engine.
		"coder_mode": s.coderMode(),
	})
}

// ── PUT /api/v1/settings/profile ─────────────────────────────────────────────

func (s *Server) apiPutSettingsProfile(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	var req apiProfileDTO
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	// Only the two fields code actually reads are editable here. Tone, language,
	// location, email and background live in memory/ABOUT.md and memory/STYLE.md,
	// which the owner edits in the knowledge base — Settings must not offer a
	// second place to change them.
	//
	// Load-then-overwrite rather than constructing a fresh Profile: profile.Save
	// writes EVERY field unconditionally (deliberately, so a caller can clear
	// one), so a two-field struct would blank the other five on the first save.
	// Those five are the seed values the startup identity backfill reads, and
	// blanking them is unrecoverable — the same reason the workspace handler
	// above passes w.About through instead of req.About.
	prof := profile.Load(s.db, w.ID)
	prof.DisplayName = req.DisplayName
	prof.Timezone = req.Timezone
	if err := profile.Save(s.db, w.ID, prof); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save settings: "+err.Error())
	}
	s.audit.Log(w.ID, "update_settings", "workspace:"+w.ID, "profile", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// ── PUT /api/v1/settings/workspace ───────────────────────────────────────────

func (s *Server) apiPutSettingsWorkspace(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	var req apiWorkspaceMetaDTO
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if req.Name == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "workspace name is required")
	}
	// req.About is deliberately ignored: memory/ABOUT.md is the source of truth
	// for what this workspace is about, and workspaces.about survives only as the
	// seed value the startup backfill reads. Passing req.About through would let
	// a rename blank it. Ignored rather than rejected so an older SPA build still
	// sending the field does not start failing.
	if err := s.db.UpdateWorkspaceMeta(w.ID, req.Name, w.About); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save: "+err.Error())
	}
	s.audit.Log(w.ID, "update_workspace_meta", "workspace:"+w.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// ── PUT /api/v1/settings/workspace/icon ──────────────────────────────────────

// workspaceIcons is the catalog of preset workspace images. The DB stores only
// a SLUG; the artwork itself lives in the SPA, so a picture never has to be
// uploaded, stored, served, or size-checked, and the set stays identical for
// every workspace.
//
// Validated server-side rather than trusted from the client: the value is
// echoed back into every session response and rendered by the SPA, so an
// arbitrary string is untrusted input, not a preference. An unknown slug is
// rejected outright instead of being stored and silently falling back at
// render time — a setting that appears to save but never shows is worse than
// an error. Keep in sync with web/ui/src/lib/workspaceIcons.tsx.
var workspaceIcons = map[string]bool{
	"aurora": true, "orbit": true, "prism": true, "meadow": true,
	"ember": true, "tide": true, "dusk": true, "grove": true,
	"signal": true, "quartz": true, "bloom": true, "slate": true,
	"cascade": true, "lattice": true, "forum": true, "spring": true,
	"nova": true, "eclipse": true, "surge": true, "venn": true,
	"summit": true, "monolith": true, "waning": true, "clinic": true,
	"strata": true, "beacon": true, "sprout": true, "voyage": true,
}

type apiWorkspaceIconRequest struct {
	Icon string `json:"icon"`
}

func (s *Server) apiPutSettingsWorkspaceIcon(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	var req apiWorkspaceIconRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	icon := strings.TrimSpace(req.Icon)
	// "" is legitimate — it clears the image and restores the initial-letter
	// monogram, which is also what a workspace starts life with.
	if icon != "" && !workspaceIcons[icon] {
		return jsonErr(c, http.StatusBadRequest, "invalid_icon", "unknown workspace icon")
	}
	if err := s.db.UpdateWorkspaceIcon(w.ID, icon); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save: "+err.Error())
	}
	s.audit.Log(w.ID, "update_workspace_icon", "workspace:"+w.ID, icon, c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// ── PUT /api/v1/settings/coder ───────────────────────────────────────────────

type apiCoderRequest struct {
	Kind     string `json:"kind"`
	Bin      string `json:"bin"`
	TimeoutS int    `json:"timeout_s"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	APIKey   string `json:"api_key"`
}

func (s *Server) apiPutSettingsCoder(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	var req apiCoderRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	// The SPA hides the local engine in slim mode, but a stale tab or a plain
	// curl would otherwise still persist a coder kind this build cannot run.
	if err := s.rejectLocalInSlim(req.Kind); err != nil {
		return err
	}
	f := coderForm{
		Kind:     req.Kind,
		Bin:      req.Bin,
		TimeoutS: strconv.Itoa(req.TimeoutS),
		Provider: req.Provider,
		Model:    req.Model,
		BaseURL:  req.BaseURL,
		APIKey:   req.APIKey,
	}
	userErrMsg, err := s.saveWorkspaceCoderCore(w, f)
	if userErrMsg != "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_coder_config", userErrMsg)
	}
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}

	if w2, err := s.db.GetWorkspaceByID(w.ID); err == nil {
		w = w2
	}
	detail := w.CoderBin
	if w.CoderKind == "api" {
		detail = w.CoderProvider + "/" + w.CoderModel
	}
	s.audit.Log(w.ID, "configure_coder", "workspace:"+w.ID, w.CoderKind+":"+detail, c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// ── PUT /api/v1/settings/master-password ─────────────────────────────────────

func (s *Server) apiPutSettingsMasterPassword(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	var req struct {
		Current     string `json:"current"`
		NewPassword string `json:"new_password"`
		Confirm     string `json:"confirm"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	if req.Current == "" || req.NewPassword == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "current and new_password are required")
	}
	if len(req.NewPassword) < 8 {
		return jsonErr(c, http.StatusBadRequest, "password_too_short", "new master password must be at least 8 characters")
	}
	if req.NewPassword != req.Confirm {
		return jsonErr(c, http.StatusBadRequest, "password_mismatch", "new passwords do not match")
	}

	userErrMsg, err := s.changeMasterPasswordCore(w, req.Current, req.NewPassword)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	if userErrMsg != "" {
		if userErrMsg == msgWrongMasterPassword {
			return jsonErr(c, http.StatusUnauthorized, "wrong_master_password", userErrMsg)
		}
		return jsonErr(c, http.StatusBadRequest, "invalid_master_password_change", userErrMsg)
	}

	s.audit.Log(w.ID, "change_master_password", "workspace:"+w.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// ── GET/POST /api/v1/setup ───────────────────────────────────────────────────

// apiSetupRequest is a flat superset of every wizard step's form fields (the
// step number picks which subset the handler reads), plus a "skip" flag for
// the three skippable steps (coder/profile/connector).
type apiSetupRequest struct {
	Step int  `json:"step"`
	Skip bool `json:"skip"`

	// step 1 — basics
	Name  string `json:"name"`
	About string `json:"about"`

	// step 2 — master_password
	MasterPassword string `json:"master_password"`
	Confirm        string `json:"confirm"`

	// step 3 — coder
	CoderKind     string `json:"coder_kind"`
	CoderBin      string `json:"coder_bin"`
	CoderTimeoutS int    `json:"coder_timeout_s"`
	CoderProvider string `json:"coder_provider"`
	CoderModel    string `json:"coder_model"`
	CoderBaseURL  string `json:"coder_base_url"`
	CoderAPIKey   string `json:"coder_api_key"`

	// step 4 — profile
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Location    string `json:"location"`
	Timezone    string `json:"timezone"`
	Tone        string `json:"tone"`
	Language    string `json:"language"`
	Notes       string `json:"notes"`

	// step 5 — connector
	Platform string            `json:"platform"`
	Fields   map[string]string `json:"fields"`
}

func (s *Server) apiGetSetup(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	step := setupStep(w, s.db)
	resp := map[string]any{
		"step":        step,
		"needs_setup": w.NeedsSetup,
	}
	switch step {
	case 3:
		secretNames, _ := s.db.ListSecretNames(w.ID)
		detOut := s.detectedCoders()
		providers := coder.APIProviders()
		provOut := make([]apiAPIProviderDTO, 0, len(providers))
		for _, p := range providers {
			provOut = append(provOut, apiAPIProviderDTO{
				Name: p.Name, Label: p.Label, Schema: p.Schema, ModelPlaceholder: p.ModelPlaceholder,
				DocsURL: p.DocsURL, RequiresKey: p.RequiresKey, Custom: p.Custom,
			})
		}
		resp["detected_coders"] = detOut
		resp["api_providers"] = provOut
		resp["coder_catalog"] = s.coderCatalogSlice(secretNames)
		resp["coder_mode"] = s.coderMode()
	case 5:
		resp["platforms"] = s.connectorPlatformList(w)
	case 7:
		// Was telegram_bot_username, which saveConnector writes ONLY for
		// Telegram (web/handlers_connectors.go) — so a Discord install reached
		// Done with an empty bot name, no linking instruction, and no mention
		// of Discord at all. Read the platform-keyed state the connectors list
		// already builds instead, and let the SPA name the real platform.
		for _, p := range s.connectorPlatformList(w) {
			if !p.Connected {
				continue
			}
			resp["platform"] = p.Platform
			resp["platform_label"] = p.Label
			resp["bot_identity"] = p.Identity
			resp["linked"] = p.Linked
			resp["linked_identity"] = p.LinkedIdentity
			resp["dm_url"] = p.DMURL
			resp["invite_url"] = p.InviteURL
			resp["bot_online"] = p.BotOnline
			break
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// apiSetupOK reloads the workspace, recomputes the wizard step, and returns the
// standard {"ok":true,"next_step":n} envelope — mirrors the template wizard's
// "always redirect back to /dashboard/setup, which recomputes the step" pattern.
func (s *Server) apiSetupOK(c echo.Context, w *db.Workspace) error {
	if w2, err := s.db.GetWorkspaceByID(w.ID); err == nil {
		w = w2
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "next_step": setupStep(w, s.db)})
}

func (s *Server) apiPostSetup(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	if !w.NeedsSetup {
		// Setup already finished — a live session must not be able to replay a
		// setup step (e.g. step 2 rotating the master password) outside the
		// Settings flow, which requires proving the CURRENT password. GET stays
		// open (harmless: it only reads/recomputes the step).
		return jsonErr(c, http.StatusForbidden, "setup_complete", "setup is already complete — use Settings")
	}
	var req apiSetupRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	switch req.Step {
	case 1:
		return s.apiSetupBasics(c, w, req)
	case 2:
		return s.apiSetupMasterPassword(c, w, req)
	case 3:
		if req.Skip {
			_ = s.db.SetSetting(w.ID, "wizard_coder_done", "1")
			return s.apiSetupOK(c, w)
		}
		return s.apiSetupCoder(c, w, req)
	case 4:
		if req.Skip {
			if err := profile.MarkComplete(s.db, w.ID); err != nil {
				return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
			}
			return s.apiSetupOK(c, w)
		}
		return s.apiSetupProfile(c, w, req)
	case 5:
		if req.Skip {
			_ = s.db.SetSetting(w.ID, "wizard_connector_skipped", "1")
			return s.apiSetupOK(c, w)
		}
		return s.apiSetupConnector(c, w, req)
	case 7:
		if err := s.db.MarkWorkspaceSetupComplete(w.ID); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
		// Seeded here rather than at step 1 or 4: both of those are skippable,
		// and step 7 is the one point every wizard path passes through.
		s.seedIdentityFiles(w.ID, "setup_complete")
		return s.apiSetupOK(c, w)
	default:
		return jsonErr(c, http.StatusBadRequest, "invalid_step", "unknown or missing step")
	}
}

// apiSetupBasics ports handleSetupBasics (web/handlers_setup.go).
func (s *Server) apiSetupBasics(c echo.Context, w *db.Workspace, req apiSetupRequest) error {
	name := req.Name
	if name == "" {
		name = w.Name
	}
	if err := s.db.UpdateWorkspaceMeta(w.ID, name, req.About); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	_ = s.db.SetSetting(w.ID, "wizard_basics_done", "1")
	return s.apiSetupOK(c, w)
}

// apiSetupMasterPassword ports handleSetupMasterPassword. Unlike the template
// handler, this one guards against a destructive re-post: the SPA wizard lets
// the user navigate Back to step 2 after step 3 has already written a secret
// (e.g. a coder API key) under the current salt. Re-generating the salt at
// that point would leave those secrets permanently undecryptable, even though
// the step itself "succeeds".
//
// Once secrets exist, a re-post is handled by resubmitPasswordOverExistingSecrets
// (shared with handleSetupMasterPassword): resubmitting the SAME password is a
// no-op; a DIFFERENT password re-encrypts every existing secret in place (via
// changeMasterPasswordCore) instead of silently discarding the change.
func (s *Server) apiSetupMasterPassword(c echo.Context, w *db.Workspace, req apiSetupRequest) error {
	if len(req.MasterPassword) < 8 {
		return jsonErr(c, http.StatusBadRequest, "password_too_short", "master password must be at least 8 characters")
	}
	if req.MasterPassword != req.Confirm {
		return jsonErr(c, http.StatusBadRequest, "password_mismatch", "passwords do not match")
	}

	if w.SecretsSalt != "" {
		if names, _ := s.db.ListSecretNames(w.ID); len(names) > 0 {
			userErrMsg, err := s.resubmitPasswordOverExistingSecrets(w, req.MasterPassword)
			if err != nil {
				return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
			}
			if userErrMsg != "" {
				return jsonErr(c, http.StatusBadRequest, "invalid_master_password_change", userErrMsg)
			}
			s.audit.Log(w.ID, "set_master_password", "workspace:"+w.ID, "resubmit", c.RealIP())
			return s.apiSetupOK(c, w)
		}
	}

	salt, err := auth.GenerateSecretsSalt()
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	encMasterPw, err := secrets.EncryptMasterPassword(req.MasterPassword, s.systemKey)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	if err := s.db.UpdateWorkspaceMasterPassword(w.ID, encMasterPw, salt); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}

	s.audit.Log(w.ID, "set_master_password", "workspace:"+w.ID, "", c.RealIP())
	return s.apiSetupOK(c, w)
}

// apiSetupCoder ports handleSetupCoder (web/handlers_setup.go) — deliberately
// NOT reusing saveWorkspaceCoderCore: the wizard step has its own error copy
// ("Provider and model are required...", "Set a master password (step 2)...")
// and its own (looser) audit-detail format (bare kind, not "kind:detail"),
// which differ from the settings page on purpose.
func (s *Server) apiSetupCoder(c echo.Context, w *db.Workspace, req apiSetupRequest) error {
	kind := req.CoderKind
	// Same guard as apiPutSettingsCoder: the wizard is a separate write path
	// into the same field, so it needs its own check.
	if err := s.rejectLocalInSlim(kind); err != nil {
		return err
	}
	timeoutS := 0
	if req.CoderTimeoutS > 0 {
		timeoutS = req.CoderTimeoutS
	}

	if kind == "api" {
		provider := req.CoderProvider
		model := strings.TrimSpace(req.CoderModel)
		baseURL := strings.TrimSpace(req.CoderBaseURL)
		if provider == "" || model == "" {
			return jsonErr(c, http.StatusBadRequest, "invalid_coder_config", "Provider and model are required for an API coder")
		}
		if provider == "generic" && baseURL == "" {
			return jsonErr(c, http.StatusBadRequest, "invalid_coder_config", "A base URL is required for a Custom provider")
		}
		// Same provider-matched precedence as saveWorkspaceCoderCore: a pasted
		// key always wins; otherwise prefer a secret that already matches THIS
		// provider's reserved name (CODER_KEY_<PROVIDER>) over w.CoderAPIKeySecret,
		// which may be a stale reference left over from a DIFFERENT provider
		// (switching openai -> openrouter mid-wizard must not silently keep
		// CODER_KEY_OPENAI); only fall back to the current reference when no
		// provider-matched secret exists.
		currentSecret := w.CoderAPIKeySecret
		if strings.TrimSpace(req.CoderAPIKey) == "" {
			if names, lerr := s.db.ListSecretNames(w.ID); lerr == nil {
				want := coder.CoderKeySecretName(provider)
				for _, n := range names {
					if n == want {
						currentSecret = want
						break
					}
				}
			}
		}
		plan := coder.PlanKeySecret(provider, strings.TrimSpace(req.CoderAPIKey), currentSecret)
		if plan.Err != "" {
			return jsonErr(c, http.StatusBadRequest, "invalid_coder_config", plan.Err)
		}
		if plan.WriteSecret {
			if w.SecretsSalt == "" || w.EncryptedMasterPassword == "" {
				return jsonErr(c, http.StatusBadRequest, "invalid_coder_config", "Set a master password (step 2) before configuring an API coder")
			}
			masterPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
			if err != nil {
				return jsonErr(c, http.StatusBadRequest, "invalid_coder_config", "Could not decrypt master password — re-run setup")
			}
			if err := secrets.New(s.db, w.ID, masterPw, w.SecretsSalt).Set(context.Background(), plan.SecretName, plan.WriteValue); err != nil {
				return jsonErr(c, http.StatusInternalServerError, "internal", "Failed to store API key: "+err.Error())
			}
		}
		if err := s.db.UpdateWorkspaceCoder(w.ID, "api", "", timeoutS, "api", provider, model, plan.SecretName, baseURL); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
	} else {
		bin := req.CoderBin
		backend := coder.BackendForBin(bin)
		if err := s.db.UpdateWorkspaceCoder(w.ID, "local", bin, timeoutS, backend, "", "", "", ""); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
	}
	_ = s.db.SetSetting(w.ID, "wizard_coder_done", "1")
	s.audit.Log(w.ID, "configure_coder", "workspace:"+w.ID, kind, c.RealIP())
	return s.apiSetupOK(c, w)
}

// apiSetupProfile ports handleSetupProfile. tone_custom precedence doesn't
// apply to JSON (no separate free-text-vs-picker fields) — tone is taken as-is.
func (s *Server) apiSetupProfile(c echo.Context, w *db.Workspace, req apiSetupRequest) error {
	p := profile.Profile{
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Location:    req.Location,
		Timezone:    req.Timezone,
		Tone:        req.Tone,
		Language:    req.Language,
		Notes:       req.Notes,
	}
	if err := profile.Save(s.db, w.ID, p); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	if err := profile.MarkComplete(s.db, w.ID); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log(w.ID, "update_profile", "workspace:"+w.ID, "", c.RealIP())
	return s.apiSetupOK(c, w)
}

// apiSetupConnector ports handleSetupConnector. Unlike the form-driven template
// (which reads CredSpec field keys off the request), the JSON client sends the
// field map directly — the platform's CredSpec is only consulted downstream by
// saveConnector.
func (s *Server) apiSetupConnector(c echo.Context, w *db.Workspace, req apiSetupRequest) error {
	values := req.Fields
	if values == nil {
		values = map[string]string{}
	}
	if req.Platform == "" || values["token"] == "" {
		// Mirrors the template's silent no-op when required fields are absent.
		return s.apiSetupOK(c, w)
	}
	identity, botStartErr, err := s.saveConnector(w.ID, req.Platform, values)
	if err != nil {
		if errors.Is(err, ErrBotAlreadyConnected) {
			return jsonErr(c, http.StatusConflict, "bot_already_connected", err.Error())
		}
		return jsonErr(c, http.StatusBadRequest, "invalid_connector_config", err.Error())
	}
	s.audit.Log(w.ID, "connect_platform", "platform:"+req.Platform, "", c.RealIP())
	if botStartErr != nil {
		return jsonErr(c, http.StatusBadRequest, "bot_start_failed", "Connector saved but bot failed to start: "+botStartErr.Error())
	}
	_ = identity
	return s.apiSetupOK(c, w)
}
