package web

import (
	"net/http"
	"strconv"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// Wizard steps (workspace onboarding, owner-driven):
//   1=basics (name+about) 2=master_password 3=coder 4=profile
//   5=connector 6=composio(optional) 7=done
type setupData struct {
	*pageData
	Step           int
	BotUsername    string
	DetectedCoders []coder.Installed
}

func (s *Server) showSetup(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	step := setupStep(w, s.db)
	sd := &setupData{
		pageData: s.page(c, "Set Up Workspace"),
		Step:     step,
	}
	switch step {
	case 3:
		sd.DetectedCoders = coder.DetectInstalled()
	case 7:
		sd.BotUsername, _ = s.db.GetSetting(w.ID, "telegram_bot_username")
	}
	return c.Render(http.StatusOK, "auth/setup.html", sd)
}

func (s *Server) handleSetup(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	action := c.FormValue("action")

	switch action {
	case "basics":
		return s.handleSetupBasics(c, w)
	case "master_password":
		return s.handleSetupMasterPassword(c, w)
	case "coder":
		return s.handleSetupCoder(c, w)
	case "skip_coder":
		_ = s.db.SetSetting(w.ID, "wizard_coder_done", "1")
		return c.Redirect(http.StatusFound, "/dashboard/setup")
	case "profile":
		return s.handleSetupProfile(c, w)
	case "skip_profile":
		if err := profile.MarkComplete(s.db, w.ID); err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, "/dashboard/setup")
	case "connector":
		return s.handleSetupConnector(c, w)
	case "skip_connector":
		_ = s.db.SetSetting(w.ID, "wizard_connector_skipped", "1")
		return c.Redirect(http.StatusFound, "/dashboard/setup")
	case "composio_done", "skip_composio":
		_ = s.db.SetSetting(w.ID, "composio_setup_seen", "1")
		return c.Redirect(http.StatusFound, "/dashboard/setup")
	case "finish":
		if err := s.db.MarkWorkspaceSetupComplete(w.ID); err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, "/dashboard")
	default:
		return c.Redirect(http.StatusFound, "/dashboard/setup")
	}
}

func (s *Server) handleSetupBasics(c echo.Context, w *db.Workspace) error {
	name := c.FormValue("name")
	about := c.FormValue("about")
	if name == "" {
		name = w.Name
	}
	if err := s.db.UpdateWorkspaceMeta(w.ID, name, about); err != nil {
		return err
	}
	_ = s.db.SetSetting(w.ID, "wizard_basics_done", "1")
	return c.Redirect(http.StatusFound, "/dashboard/setup")
}

func (s *Server) handleSetupMasterPassword(c echo.Context, w *db.Workspace) error {
	masterPw := c.FormValue("master_password")
	confirm := c.FormValue("confirm")

	sd := &setupData{pageData: s.page(c, "Set Up Workspace"), Step: 2}
	if len(masterPw) < 8 {
		sd.Error = "Master password must be at least 8 characters"
		return c.Render(http.StatusBadRequest, "auth/setup.html", sd)
	}
	if masterPw != confirm {
		sd.Error = "Passwords do not match"
		return c.Render(http.StatusBadRequest, "auth/setup.html", sd)
	}

	salt, err := auth.GenerateSecretsSalt()
	if err != nil {
		return err
	}

	// Encrypt master password with system key for cron-triggered agent runs and
	// for the enter/switch access gate. needs_setup stays 1 until the wizard finishes.
	encMasterPw, err := secrets.EncryptMasterPassword(masterPw, s.systemKey)
	if err != nil {
		return err
	}
	if err := s.db.UpdateWorkspaceMasterPassword(w.ID, encMasterPw, salt); err != nil {
		return err
	}

	s.audit.Log(w.ID, "set_master_password", "workspace:"+w.ID, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/setup")
}

// handleSetupConnector saves the Telegram token during onboarding. It runs on the
// setup-exempt route so the save isn't blocked by requireSetupComplete (needs_setup
// is still 1 mid-wizard); posting to /dashboard/connectors would just redirect back.
func (s *Server) handleSetupConnector(c echo.Context, w *db.Workspace) error {
	platform := c.FormValue("platform")
	token := c.FormValue("token")
	if platform == "" || token == "" {
		return c.Redirect(http.StatusFound, "/dashboard/setup")
	}
	sd := &setupData{pageData: s.page(c, "Set Up Workspace"), Step: 5}
	botUsername, botStartErr, err := s.saveConnector(w.ID, platform, token)
	if err != nil {
		sd.Error = err.Error()
		return c.Render(http.StatusBadRequest, "auth/setup.html", sd)
	}
	s.audit.Log(w.ID, "connect_platform", "platform:"+platform, "", c.RealIP())
	if botStartErr != nil {
		sd.Error = "Connector saved but bot failed to start: " + botStartErr.Error()
		return c.Render(http.StatusBadRequest, "auth/setup.html", sd)
	}
	_ = botUsername
	return c.Redirect(http.StatusFound, "/dashboard/setup")
}

func (s *Server) handleSetupCoder(c echo.Context, w *db.Workspace) error {
	bin := c.FormValue("coder_bin")
	backend := c.FormValue("coder_backend_type")
	timeoutS := 0
	if v, err := strconv.Atoi(c.FormValue("coder_timeout_s")); err == nil && v > 0 {
		timeoutS = v
	}
	if err := s.db.UpdateWorkspaceCoder(w.ID, "local", bin, timeoutS, backend, "", "", ""); err != nil {
		return err
	}
	_ = s.db.SetSetting(w.ID, "wizard_coder_done", "1")
	s.audit.Log(w.ID, "configure_coder", "workspace:"+w.ID, bin, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/setup")
}

// handleSetupProfile saves the persona profile fields and marks the step complete.
// All fields are optional.
func (s *Server) handleSetupProfile(c echo.Context, w *db.Workspace) error {
	tone := c.FormValue("tone_custom")
	if tone == "" {
		tone = c.FormValue("tone")
	}
	p := profile.Profile{
		DisplayName: c.FormValue("display_name"),
		Email:       c.FormValue("email"),
		Location:    c.FormValue("location"),
		Timezone:    c.FormValue("timezone"),
		Tone:        tone,
		Language:    c.FormValue("language"),
		Notes:       c.FormValue("notes"),
	}
	if err := profile.Save(s.db, w.ID, p); err != nil {
		return err
	}
	if err := profile.MarkComplete(s.db, w.ID); err != nil {
		return err
	}
	s.audit.Log(w.ID, "update_profile", "workspace:"+w.ID, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/setup")
}

// setupStep determines which wizard step to show based on workspace state.
func setupStep(w *db.Workspace, database *db.DB) int {
	if database == nil {
		return 7
	}
	if v, _ := database.GetSetting(w.ID, "wizard_basics_done"); v != "1" {
		return 1
	}
	if w.SecretsSalt == "" {
		return 2
	}
	if v, _ := database.GetSetting(w.ID, "wizard_coder_done"); v != "1" {
		return 3
	}
	if !profile.IsComplete(database, w.ID) {
		return 4
	}
	conns, _ := database.ListWorkspacePlatformConnections(w.ID)
	if skipped, _ := database.GetSetting(w.ID, "wizard_connector_skipped"); len(conns) == 0 && skipped != "1" {
		return 5
	}
	// Step 6 (optional): Composio — unless the workspace has the key or dismissed it.
	hasKey, _ := database.SecretExists(w.ID, "COMPOSIO_API_KEY")
	seen, _ := database.GetSetting(w.ID, "composio_setup_seen")
	if !hasKey && seen != "1" {
		return 6
	}
	return 7 // Done
}
