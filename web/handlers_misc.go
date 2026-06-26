package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/chat"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/reminder"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
)

// ── Chats ───────────────────────────────────────────────────────────────────

type chatsPageData struct {
	*pageData
	Chats []*db.Chat
}

func (s *Server) showChats(c echo.Context) error {
	u := c.Get("user").(*db.User)
	chats, _ := s.db.ListChats(u.ID)
	return c.Render(http.StatusOK, "dashboard/chats.html", &chatsPageData{
		pageData: s.page(c, "Chats"),
		Chats:    chats,
	})
}

func (s *Server) handleCreateChat(c echo.Context) error {
	u := c.Get("user").(*db.User)
	name := c.FormValue("name")
	if name == "" {
		loc := profile.LoadLocation(s.db, u.ID)
		name = "Chat " + time.Now().In(loc).Format("2006-01-02 15:04")
	}
	ch := &db.Chat{
		ID:       uuid.New().String(),
		UserID:   u.ID,
		Name:     name,
		Platform: "web",
		Active:   true,
	}
	if err := s.db.CreateChat(ch); err != nil {
		return err
	}
	s.audit.Log(u.ID, "create_chat", "chat:"+ch.ID, name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats/"+ch.ID)
}

type chatDetailPageData struct {
	*pageData
	Chat     *db.Chat
	Messages []db.ChatMessage
}

// showChatDetail renders a chat's full message history plus a composer.
func (s *Server) showChatDetail(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	msgs, _ := s.db.ListChatMessages(id)
	return c.Render(http.StatusOK, "dashboard/chat_detail.html", &chatDetailPageData{
		pageData: s.page(c, "Chat"),
		Chat:    ch,
		Messages: msgs,
	})
}

// handleChatMessage sends one user message through the coder one-off-chat path,
// persists both turns, and returns the assistant reply as JSON. Used by the
// chat detail page's AJAX composer (mirrors the agent-designer chat flow).
func (s *Server) handleChatMessage(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.UserID != u.ID {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "chat not found"})
	}

	// Accept JSON {message} (AJAX composer) or a form-encoded "message" field.
	var text string
	if strings.HasPrefix(c.Request().Header.Get("Content-Type"), "application/json") {
		var body struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(c.Request().Body).Decode(&body)
		text = strings.TrimSpace(body.Message)
	} else {
		text = strings.TrimSpace(c.FormValue("message"))
	}
	if text == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "empty message"})
	}

	history, _ := s.db.ListChatMessages(id)

	// System context: a read+write knowledge-base instruction (so the chat can retrieve
	// and edit notes on demand) + the user's always-on identity context (profile/memory/
	// agents/MCP). The coder runs with its CWD set to the vault root, which the sandbox
	// grants read+write access to, and the file toolset Read/Write/Edit/Glob/Grep.
	root := s.vault.Root(u.ID)
	sysCtx := prompts.BuildChatSystemPrompt(root) + chat.BuildUserContext(s.db, s.memory, u.ID)

	// Re-activate the chat if it had been stopped, so history keeps flowing.
	if !ch.Active {
		_ = s.db.ResumeChat(id)
	}

	coder := s.coderForUser(u.ID).WithDir(root).WithAllowedTools("Read,Write,Edit,Glob,Grep")
	result, err := coder.Chat(c.Request().Context(), u.ID, history, sysCtx, text)
	if err != nil {
		// Don't persist on failure — the client already shows the user bubble,
		// and a refresh clears the failed attempt (matches agent-designer behavior).
		return c.JSON(http.StatusOK, map[string]string{"error": "Couldn't reach " + coder.Name() + ": " + err.Error()})
	}

	_ = s.db.AddChatMessage(id, "user", text)
	_ = s.db.AddChatMessage(id, "assistant", result.Text)
	_ = s.db.TouchChat(id)
	return c.JSON(http.StatusOK, map[string]string{"response": result.Text})
}

// handleResumeChat re-activates a previously stopped chat.
func (s *Server) handleResumeChat(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	_ = s.db.ResumeChat(id)
	s.audit.Log(u.ID, "resume_chat", "chat:"+id, ch.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats/"+id)
}

func (s *Server) handleStopChat(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	_ = s.db.StopChat(id)
	s.audit.Log(u.ID, "stop_chat", "chat:"+id, ch.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats")
}

func (s *Server) handleDeleteChat(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")
	ch, err := s.db.GetChat(id)
	if err != nil || ch.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "chat not found")
	}
	_ = s.db.DeleteChat(id)
	s.audit.Log(u.ID, "delete_chat", "chat:"+id, ch.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/chats")
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
