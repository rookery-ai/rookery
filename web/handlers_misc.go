package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/prompts"
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
		loc := profile.LoadLocation(s.db, u.ID)
		name = "Session " + time.Now().In(loc).Format("2006-01-02 15:04")
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
	renderErr := func(msg string) error {
		p.Error = msg
		rs, _ := s.db.ListReminders(u.ID)
		return c.Render(http.StatusBadRequest, "dashboard/reminders.html", &remindersPageData{pageData: p, Reminders: rs})
	}

	if message == "" {
		return renderErr("Reminder message is required")
	}
	if whenStr == "" {
		return renderErr(`When would you like to be reminded? Try: "in 10 minutes", "tomorrow at 3pm", "next Friday morning"`)
	}

	now := time.Now()
	loc := profile.LoadLocation(s.db, u.ID)
	llmFn := buildLLMTimeParser(s.coderForUser(u.ID))

	remindAt, _, err := reminder.ParseNaturalTimeFull(c.Request().Context(), whenStr, now, loc, llmFn, u.ID)
	if err != nil {
		return renderErr(`Couldn't understand that time. Try: "in 10 minutes", "tomorrow at 3pm", "next Tuesday", "July 15 at 2pm"`)
	}
	if remindAt.IsZero() {
		return renderErr(`No time found in "` + whenStr + `". Try: "in 10 minutes", "tomorrow at 3pm", "next Friday"`)
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

// buildLLMTimeParser returns a reminder.TimeParserFunc backed by the given coder.
// It calls BuildReminderParsePrompt and parses the JSON response via ParseLLMReminderJSON.
func buildLLMTimeParser(coderSvc *coder.Coder) reminder.TimeParserFunc {
	if coderSvc == nil {
		return nil
	}
	return func(ctx context.Context, userID, input string, now time.Time, loc *time.Location) (time.Time, string, error) {
		tz := "UTC"
		if loc != nil {
			tz = loc.String()
		}
		nowStr := now.In(loc).Format("2006-01-02 15:04 MST")
		prompt := prompts.BuildReminderParsePrompt(input, nowStr, tz)
		result, err := coderSvc.WithNoTools().Generate(ctx, userID, prompt)
		if err != nil {
			return time.Time{}, input, err
		}
		when, msg, err := reminder.ParseLLMReminderJSON(result.Text, now)
		return when, msg, err
	}
}

// handlePollReminders returns due unsent reminders for the current user.
// For web-only users (no platform connected) it also marks them sent — this IS the delivery.
// For users with Telegram connected, it returns them for info display but does NOT mark sent
// so the server-side tick() can still deliver via Telegram.
func (s *Server) handlePollReminders(c echo.Context) error {
	u := c.Get("user").(*db.User)
	due, err := s.db.ListDueReminders(time.Now())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	hasPlatform := s.db.HasPlatformIdentity(u.ID)
	type item struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	var result []item
	for _, r := range due {
		if r.UserID != u.ID {
			continue
		}
		result = append(result, item{ID: r.ID, Message: r.Message})
		// Only mark sent here for web-only users. Platform users get marked sent
		// by the server-side reminder tick() after Telegram delivery.
		if !hasPlatform {
			_ = s.db.MarkReminderSent(r.ID)
		}
	}
	if result == nil {
		result = []item{}
	}
	return c.JSON(http.StatusOK, result)
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
