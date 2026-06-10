package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

type setupData struct {
	*pageData
	Step        int // 1=password, 2=master_password, 3=connector, 4=done
	BotUsername string
}

func (s *Server) showSetup(c echo.Context) error {
	u := c.Get("user").(*db.User)
	step := setupStep(u, s.db)
	var botUsername string
	if step == 4 {
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
	case "connector":
		// Connector setup is handled by /dashboard/connectors — just advance
		return c.Redirect(http.StatusFound, "/setup?step=4")
	case "skip_connector":
		if err := s.db.UpdateUserSetup(u.ID, u.EncryptedMasterPassword, u.SecretsSalt); err != nil {
			return err
		}
		return c.Redirect(http.StatusFound, "/dashboard")
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

	// Don't mark needs_setup=0 yet; let user proceed to connector step
	return c.Redirect(http.StatusFound, "/setup?step=3")
}

// setupStep determines which step of setup to show based on user state.
// The db parameter is used to check whether the user has connected a platform yet.
func setupStep(u *db.User, database *db.DB) int {
	if u.MustChangePassword {
		return 1
	}
	if u.SecretsSalt == "" {
		return 2
	}
	// Show step 4 (done) if user already has a platform connection.
	if database != nil {
		conns, _ := database.ListUserPlatformConnections(u.ID)
		if len(conns) > 0 {
			return 4
		}
	}
	return 3
}

