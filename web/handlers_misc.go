package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/reminder"
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
	whenStr := strings.TrimSpace(c.FormValue("when"))

	p := s.page(c, "Reminders")
	if message == "" || whenStr == "" {
		p.Error = "Message and reminder time are required"
		reminders, _ := s.db.ListReminders(u.ID)
		return c.Render(http.StatusBadRequest, "dashboard/reminders.html", &remindersPageData{pageData: p, Reminders: reminders})
	}

	remindAt, err := reminder.ParseNaturalTime(whenStr, time.Now(), profile.LoadLocation(s.db, u.ID))
	if err != nil {
		p.Error = `Couldn't parse that time. Try: "in 10 minutes", "tomorrow at 3pm", "next Tuesday"`
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

type memoryPageData struct {
	*pageData
	Entries []memoryEntry
}

type memoryEntry struct {
	ID      string
	Content string
	Date    string
}

func (s *Server) showMemory(c echo.Context) error {
	u := c.Get("user").(*db.User)
	p := s.page(c, "Memory")
	if s.memory == nil {
		return c.Render(http.StatusOK, "dashboard/memory.html", &memoryPageData{pageData: p})
	}
	entries, _ := s.memory.List(u.ID)
	var rows []memoryEntry
	for _, e := range entries {
		rows = append(rows, memoryEntry{
			ID:      e.ID,
			Content: e.Content,
			Date:    e.CreatedAt.Format("Jan 2, 2006"),
		})
	}
	return c.Render(http.StatusOK, "dashboard/memory.html", &memoryPageData{pageData: p, Entries: rows})
}

func (s *Server) handleUpdateMemory(c echo.Context) error {
	u := c.Get("user").(*db.User)
	content := c.FormValue("content")
	action := c.FormValue("action")
	entryID := c.FormValue("entry_id")

	p := s.page(c, "Memory")

	if action == "delete" && entryID != "" {
		if s.memory != nil {
			_ = s.memory.Delete(u.ID, entryID)
		}
		return c.Redirect(http.StatusFound, "/dashboard/memory")
	}

	if content == "" {
		p.Error = "Memory content cannot be empty"
	} else if s.memory != nil {
		if _, err := s.memory.Append(u.ID, content); err != nil {
			p.Error = "Failed to save: " + err.Error()
		} else {
			p.Success = "Memory saved"
		}
	}

	entries, _ := s.memory.List(u.ID)
	var rows []memoryEntry
	for _, e := range entries {
		rows = append(rows, memoryEntry{ID: e.ID, Content: e.Content, Date: e.CreatedAt.Format("Jan 2, 2006")})
	}
	return c.Render(http.StatusOK, "dashboard/memory.html", &memoryPageData{pageData: p, Entries: rows})
}

// ── Settings ───────────────────────────────────────────────────────────────

type settingsPageData struct {
	*pageData
	DisplayName string
	Email       string
	Location    string
	Timezone    string
	Tone        string
	Language    string
	Notes       string
}

func (s *Server) showSettings(c echo.Context) error {
	u := c.Get("user").(*db.User)
	prof := profile.Load(s.db, u.ID)
	dn := prof.DisplayName
	if dn == "" {
		dn = u.Username
	}
	return c.Render(http.StatusOK, "dashboard/settings.html", &settingsPageData{
		pageData:    s.page(c, "Settings"),
		DisplayName: dn,
		Email:       prof.Email,
		Location:    prof.Location,
		Timezone:    prof.Timezone,
		Tone:        prof.Tone,
		Language:    prof.Language,
		Notes:       prof.Notes,
	})
}

func (s *Server) handleSaveSettings(c echo.Context) error {
	u := c.Get("user").(*db.User)
	tone := c.FormValue("tone_custom")
	if tone == "" {
		tone = c.FormValue("tone")
	}
	prof := profile.Profile{
		DisplayName: c.FormValue("display_name"),
		Email:       c.FormValue("email"),
		Location:    c.FormValue("location"),
		Timezone:    c.FormValue("timezone"),
		Tone:        tone,
		Language:    c.FormValue("language"),
		Notes:       c.FormValue("notes"),
	}
	p := s.page(c, "Settings")

	if err := profile.Save(s.db, u.ID, prof); err != nil {
		p.Error = "Failed to save settings: " + err.Error()
	} else {
		s.audit.Log(u.ID, "update_settings", "user:"+u.ID, "profile", c.RealIP())
		p.Success = "Settings saved"
	}

	dn := prof.DisplayName
	if dn == "" {
		dn = u.Username
	}
	return c.Render(http.StatusOK, "dashboard/settings.html", &settingsPageData{
		pageData:    p,
		DisplayName: dn,
		Email:       prof.Email,
		Location:    prof.Location,
		Timezone:    prof.Timezone,
		Tone:        prof.Tone,
		Language:    prof.Language,
		Notes:       prof.Notes,
	})
}

func (s *Server) handleChangeMasterPassword(c echo.Context) error {
	u := c.Get("user").(*db.User)
	oldPw := c.FormValue("current")
	newPw := c.FormValue("new_password")
	confirm := c.FormValue("confirm")

	renderErr := func(msg string) error {
		prof := profile.Load(s.db, u.ID)
		dn := prof.DisplayName
		if dn == "" {
			dn = u.Username
		}
		p := s.page(c, "Settings")
		p.Error = msg
		return c.Render(http.StatusBadRequest, "dashboard/settings.html", &settingsPageData{
			pageData: p, DisplayName: dn, Email: prof.Email, Location: prof.Location,
			Timezone: prof.Timezone, Tone: prof.Tone, Language: prof.Language, Notes: prof.Notes,
		})
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
	prof := profile.Load(s.db, u.ID)
	dn := prof.DisplayName
	if dn == "" {
		dn = u.Username
	}
	p := s.page(c, "Settings")
	p.Success = "Master password changed successfully"
	return c.Render(http.StatusOK, "dashboard/settings.html", &settingsPageData{
		pageData: p, DisplayName: dn, Email: prof.Email, Location: prof.Location,
		Timezone: prof.Timezone, Tone: prof.Tone, Language: prof.Language, Notes: prof.Notes,
	})
}
