package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

type setupData struct {
	*pageData
	Step        int // 1=password, 2=master_password, 3=profile, 4=connector, 5=composio(optional), 6=done
	BotUsername string
}

func (s *Server) showSetup(c echo.Context) error {
	u := c.Get("user").(*db.User)
	step := setupStep(u, s.db)
	var botUsername string
	if step == 6 {
		botUsername, _ = s.db.GetSetting(u.ID, "telegram_bot_username")
	}
	return c.Render(http.StatusOK, "auth/setup.html", &setupData{
		pageData:    s.page(c, "Setup Your Account"),
		Step:        step,
		BotUsername: botUsername,
	})
}

func (s *Server) handleSetup(c echo.Context) error {
	u := c.Get("user").(*db.User)
	action := c.FormValue("action")

	switch action {
	case "master_password":
		return s.handleSetupMasterPassword(c, u)
	case "profile":
		return s.handleSetupProfile(c, u)
	case "skip_profile":
		if err := profile.MarkComplete(s.db, u.ID); err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, "/setup")
	case "connector":
		// Connector setup is handled by /dashboard/connectors — just advance
		return c.Redirect(http.StatusFound, "/setup")
	case "skip_connector":
		if err := s.db.UpdateUserSetup(u.ID, u.EncryptedMasterPassword, u.SecretsSalt); err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, "/setup")
	case "composio_done", "skip_composio":
		// Mark the Composio step as seen so it doesn't appear again, then advance.
		_ = s.db.SetSetting(u.ID, "composio_setup_seen", "1")
		return c.Redirect(http.StatusFound, "/setup")
	case "finish":
		if err := s.db.UpdateUserSetup(u.ID, u.EncryptedMasterPassword, u.SecretsSalt); err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, "/dashboard")
	default:
		return c.Redirect(http.StatusFound, "/setup")
	}
}

func (s *Server) handleSetupMasterPassword(c echo.Context, u *db.User) error {
	masterPw := c.FormValue("master_password")
	confirm := c.FormValue("confirm")

	sd := &setupData{pageData: s.page(c, "Setup Your Account"), Step: 2}
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

	// Encrypt master password with system key for cron-triggered agent runs.
	encMasterPw, err := secrets.EncryptMasterPassword(masterPw, s.systemKey)
	if err != nil {
		return err
	}

	if err := s.db.UpdateUserSetup(u.ID, encMasterPw, salt); err != nil {
		return err
	}

	s.audit.Log(u.ID, "set_master_password", "user:"+u.ID, "", c.RealIP())

	// Don't mark needs_setup=0 yet; let user proceed to the profile step
	return c.Redirect(http.StatusFound, "/setup")
}

// handleSetupProfile saves the profile fields submitted from the setup wizard
// and marks the profile step complete, then advances to the connector step.
// All fields are optional — this never validates required-ness.
func (s *Server) handleSetupProfile(c echo.Context, u *db.User) error {
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
	if err := profile.Save(s.db, u.ID, p); err != nil {
		return err
	}
	if err := profile.MarkComplete(s.db, u.ID); err != nil {
		return err
	}
	s.audit.Log(u.ID, "update_profile", "user:"+u.ID, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/setup")
}

// setupStep determines which step of setup to show based on user state.
// Steps: 1=password, 2=master_password, 3=profile, 4=connector, 5=composio(optional), 6=done.
func setupStep(u *db.User, database *db.DB) int {
	if u.MustChangePassword {
		return 1
	}
	if u.SecretsSalt == "" {
		return 2
	}
	if database != nil && !profile.IsComplete(database, u.ID) {
		return 3
	}
	if database != nil {
		conns, _ := database.ListUserPlatformConnections(u.ID)
		if len(conns) == 0 {
			return 4
		}
	}
	// Step 5 (optional): Composio — show unless user already has the key or has dismissed this step.
	if database != nil {
		hasKey, _ := database.SecretExists(u.ID, "COMPOSIO_API_KEY")
		seen, _ := database.GetSetting(u.ID, "composio_setup_seen")
		if !hasKey && seen != "1" {
			return 5
		}
	}
	return 6 // Done
}

