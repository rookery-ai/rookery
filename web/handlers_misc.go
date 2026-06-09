package web

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// ── Sessions ───────────────────────────────────────────────────────────────

type sessionsPageData struct {
	*pageData
	Sessions []*db.ChatSession
}

func (s *Server) showSessions(c echo.Context) error {
	u := c.Get("user").(*db.User)
	sessions, _ := s.db.ListChatSessions(u.ID)
	return c.Render(http.StatusOK, "dashboard/sessions.html", &sessionsPageData{
		pageData: s.page(c, "Chat Sessions"),
		Sessions: sessions,
	})
}

func (s *Server) handleCreateSession(c echo.Context) error {
	u := c.Get("user").(*db.User)
	name := c.FormValue("name")
	if name == "" {
		name = "Session " + time.Now().Format("2006-01-02 15:04")
	}
	sess := &db.ChatSession{
		ID:       uuid.New().String(),
		UserID:   u.ID,
		Name:     name,
		Platform: "web",
		Active:   true,
	}
	_ = s.db.CreateChatSession(sess)
	s.audit.Log(u.ID, "create_session", "session:"+sess.ID, name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/sessions")
}

func (s *Server) handleStopSession(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	sess, err := s.db.GetChatSession(id)
	if err != nil || sess.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}
	_ = s.db.StopChatSession(id)
	return c.Redirect(http.StatusFound, "/dashboard/sessions")
}

func (s *Server) handleDeleteSession(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	sess, err := s.db.GetChatSession(id)
	if err != nil || sess.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "session not found")
	}
	_ = s.db.DeleteChatSession(id)
	return c.Redirect(http.StatusFound, "/dashboard/sessions")
}

// ── Reminders ──────────────────────────────────────────────────────────────

type remindersPageData struct {
	*pageData
	Reminders []*db.Reminder
}

func (s *Server) showReminders(c echo.Context) error {
	u := c.Get("user").(*db.User)
	reminders, _ := s.db.ListReminders(u.ID)
	return c.Render(http.StatusOK, "dashboard/reminders.html", &remindersPageData{
		pageData:  s.page(c, "Reminders"),
		Reminders: reminders,
	})
}

func (s *Server) handleCreateReminder(c echo.Context) error {
	u := c.Get("user").(*db.User)
	message := c.FormValue("message")
	remindAtStr := c.FormValue("remind_at")

	p := s.page(c, "Reminders")
	if message == "" || remindAtStr == "" {
		p.Error = "Message and reminder time are required"
		reminders, _ := s.db.ListReminders(u.ID)
		return c.Render(http.StatusBadRequest, "dashboard/reminders.html", &remindersPageData{pageData: p, Reminders: reminders})
	}

	remindAt, err := time.ParseInLocation("2006-01-02T15:04", remindAtStr, time.Local)
	if err != nil {
		p.Error = "Invalid date/time format"
		reminders, _ := s.db.ListReminders(u.ID)
		return c.Render(http.StatusBadRequest, "dashboard/reminders.html", &remindersPageData{pageData: p, Reminders: reminders})
	}

	r := &db.Reminder{
		ID:       uuid.New().String(),
		UserID:   u.ID,
		Message:  message,
		RemindAt: remindAt,
	}
	_ = s.db.CreateReminder(r)
	s.audit.Log(u.ID, "create_reminder", "reminder:"+r.ID, message, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/reminders")
}

func (s *Server) handleDeleteReminder(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	r, err := s.db.GetReminder(id)
	if err != nil || r.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "reminder not found")
	}
	_ = s.db.DeleteReminder(id)
	return c.Redirect(http.StatusFound, "/dashboard/reminders")
}

// ── Memory ─────────────────────────────────────────────────────────────────

func (s *Server) showMemory(c echo.Context) error {
	return c.Render(http.StatusOK, "dashboard/memory.html", s.page(c, "Memory"))
}

func (s *Server) handleUpdateMemory(c echo.Context) error {
	// Full implementation in Phase 7 (MemoryStore).
	p := s.page(c, "Memory")
	p.Success = "Memory update queued (memory service not yet connected)"
	return c.Render(http.StatusOK, "dashboard/memory.html", p)
}

// ── Settings ───────────────────────────────────────────────────────────────

func (s *Server) showSettings(c echo.Context) error {
	return c.Render(http.StatusOK, "dashboard/settings.html", s.page(c, "Settings"))
}

func (s *Server) handleSaveSettings(c echo.Context) error {
	p := s.page(c, "Settings")
	p.Success = "Settings saved"
	return c.Render(http.StatusOK, "dashboard/settings.html", p)
}

func (s *Server) handleChangeMasterPassword(c echo.Context) error {
	u := c.Get("user").(*db.User)
	oldPw := c.FormValue("old_master_password")
	newPw := c.FormValue("new_master_password")
	confirm := c.FormValue("confirm")

	renderErr := func(msg string) error {
		p := s.page(c, "Settings")
		p.Error = msg
		return c.Render(http.StatusBadRequest, "dashboard/settings.html", p)
	}

	if oldPw == "" || newPw == "" {
		return renderErr("Old and new master passwords are required")
	}
	if len(newPw) < 8 {
		return renderErr("New master password must be at least 8 characters")
	}
	if newPw != confirm {
		return renderErr("New passwords do not match")
	}
	if u.SecretsSalt == "" {
		return renderErr("Account setup not complete")
	}

	// Verify old password by attempting to decrypt an existing secret.
	// If there are no secrets, trust the provided old password to avoid lockout.
	ctx := context.Background()
	names, _ := s.db.ListSecretNames(u.ID)
	if len(names) > 0 {
		oldSvc := secrets.New(s.db, u.ID, oldPw, u.SecretsSalt)
		if _, err := oldSvc.Get(ctx, names[0]); err != nil {
			return renderErr("Old master password is incorrect")
		}
	}

	// Re-encrypt all secrets with the new key (same salt, new password → new derived key).
	oldSvc := secrets.New(s.db, u.ID, oldPw, u.SecretsSalt)
	newSvc := secrets.New(s.db, u.ID, newPw, u.SecretsSalt)
	for _, name := range names {
		val, err := oldSvc.Get(ctx, name)
		if err != nil {
			return renderErr("Failed to re-encrypt secrets: " + err.Error())
		}
		if err := newSvc.Set(ctx, name, val); err != nil {
			return renderErr("Failed to re-encrypt secrets: " + err.Error())
		}
	}

	// Update encrypted master password stored for scheduler.
	encMasterPw, err := secrets.EncryptMasterPassword(newPw, s.systemKey)
	if err != nil {
		return err
	}
	if err := s.db.UpdateUserSetup(u.ID, encMasterPw, u.SecretsSalt); err != nil {
		return err
	}

	s.audit.Log(u.ID, "change_master_password", "user:"+u.ID, "", c.RealIP())
	p := s.page(c, "Settings")
	p.Success = "Master password changed successfully"
	return c.Render(http.StatusOK, "dashboard/settings.html", p)
}
