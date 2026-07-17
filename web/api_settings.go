package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/llm"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// registerSettingsAPI registers the settings + setup + coder JSON endpoints on
// the given group (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI
// + requireSetupCompleteAPI — the latter exempts "/api/v1/setup" so the wizard
// works before setup completes).
func (s *Server) registerSettingsAPI(g *echo.Group) {
	g.GET("/settings", s.apiGetSettings)
	g.PUT("/settings/profile", s.apiPutSettingsProfile)
	g.PUT("/settings/workspace", s.apiPutSettingsWorkspace)
	g.PUT("/settings/coder", s.apiPutSettingsCoder)
	g.POST("/settings/coder/test", s.handleSmokeCoder) // unchanged, just re-registered
	g.PUT("/settings/master-password", s.apiPutSettingsMasterPassword)

	g.GET("/setup", s.apiGetSetup)
	g.POST("/setup", s.apiPostSetup)
}

// ── Shared catalog builder ───────────────────────────────────────────────────

// apiCoderCatalogEntry mirrors the JSON shape the settings-page/setup-wizard
// coder-form JS already expects (see coderCatalogJSON in handlers_misc.go).
type apiCoderCatalogEntry struct {
	Name        string `json:"name"`
	Base        string `json:"base"`
	Model       string `json:"model"`
	Docs        string `json:"docs"`
	RequiresKey bool   `json:"requiresKey"`
	Custom      bool   `json:"custom"`
	HasKey      bool   `json:"hasKey"`
}

// coderCatalogSlice builds the direct-LLM-API provider catalog as a plain slice.
// This is the single source used both by coderCatalogJSON (template.JS-wrapped,
// for the settings/setup templates) and apiGetSettings (plain JSON array).
func (s *Server) coderCatalogSlice(secretNames []string) []apiCoderCatalogEntry {
	have := make(map[string]bool, len(secretNames))
	for _, n := range secretNames {
		have[n] = true
	}
	cat := coder.APIProviders()
	out := make([]apiCoderCatalogEntry, 0, len(cat))
	for _, p := range cat {
		out = append(out, apiCoderCatalogEntry{
			Name: p.Name, Base: llm.DefaultBaseURL(p.Name), Model: p.ModelPlaceholder,
			Docs: p.DocsURL, RequiresKey: p.RequiresKey, Custom: p.Custom,
			HasKey: have[coder.CoderKeySecretName(p.Name)],
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

	detected := coder.DetectInstalled()
	detOut := make([]apiDetectedCoderDTO, 0, len(detected))
	for _, d := range detected {
		detOut = append(detOut, apiDetectedCoderDTO{Name: d.Name, Bin: d.Bin, BackendType: d.BackendType})
	}

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
	})
}

// ── PUT /api/v1/settings/profile ─────────────────────────────────────────────

func (s *Server) apiPutSettingsProfile(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	var req apiProfileDTO
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	prof := profile.Profile{
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Location:    req.Location,
		Timezone:    req.Timezone,
		Tone:        req.Tone, // no tone_custom precedence in JSON — take tone as-is
		Language:    req.Language,
		Notes:       req.Notes,
	}
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
	if err := s.db.UpdateWorkspaceMeta(w.ID, req.Name, req.About); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save: "+err.Error())
	}
	s.audit.Log(w.ID, "update_workspace_meta", "workspace:"+w.ID, "", c.RealIP())
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
		detected := coder.DetectInstalled()
		detOut := make([]apiDetectedCoderDTO, 0, len(detected))
		for _, d := range detected {
			detOut = append(detOut, apiDetectedCoderDTO{Name: d.Name, Bin: d.Bin, BackendType: d.BackendType})
		}
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
	case 5:
		resp["platforms"] = s.connectorPlatformList(w)
	case 7:
		botUsername, _ := s.db.GetSetting(w.ID, "telegram_bot_username")
		resp["bot_username"] = botUsername
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
		plan := coder.PlanKeySecret(provider, strings.TrimSpace(req.CoderAPIKey), w.CoderAPIKeySecret)
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
		return jsonErr(c, http.StatusBadRequest, "invalid_connector_config", err.Error())
	}
	s.audit.Log(w.ID, "connect_platform", "platform:"+req.Platform, "", c.RealIP())
	if botStartErr != nil {
		return jsonErr(c, http.StatusBadRequest, "bot_start_failed", "Connector saved but bot failed to start: "+botStartErr.Error())
	}
	_ = identity
	return s.apiSetupOK(c, w)
}
