package web

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
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
	// Full implementation in Phase 2 (re-encrypt all secrets with new key).
	p := s.page(c, "Settings")
	p.Success = "Master password change will be available after secrets service integration"
	return c.Render(http.StatusOK, "dashboard/settings.html", p)
}
