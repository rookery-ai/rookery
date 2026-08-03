package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/profile"
	"github.com/ilijad1/rookery/internal/reminder"
	"github.com/labstack/echo/v4"
)

// registerHomeAPI registers the JSON reminders + inbox endpoints on the given
// group (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI +
// requireSetupCompleteAPI). The poll endpoints re-register the EXISTING
// s.handlePollReminders / s.handleInboxPoll handlers UNCHANGED — they already
// return JSON in their own legacy shape and must not be touched or rewrapped.
func (s *Server) registerHomeAPI(g *echo.Group) {
	g.GET("/reminders", s.apiListReminders)
	g.POST("/reminders", s.apiCreateReminder)
	g.DELETE("/reminders/:id", s.apiDeleteReminder)
	g.GET("/reminders/poll", s.handlePollReminders)

	g.GET("/inbox", s.apiListInbox)
	g.GET("/inbox/poll", s.handleInboxPoll)
	g.POST("/inbox/:id/read", s.apiMarkInboxRead)
	g.POST("/inbox/read-all", s.apiMarkAllInboxRead)
	g.DELETE("/inbox/:id", s.apiDeleteInboxMessage)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

// apiReminder mirrors db.Reminder. Note: Sent is a plain bool on db.Reminder
// (not a SentAt pointer/timestamp) — surfaced here verbatim.
type apiReminder struct {
	ID       string    `json:"id"`
	Message  string    `json:"message"`
	RemindAt time.Time `json:"remind_at"`
	Sent     bool      `json:"sent"`
}

func toAPIReminder(r *db.Reminder) apiReminder {
	return apiReminder{ID: r.ID, Message: r.Message, RemindAt: r.RemindAt, Sent: r.Sent}
}

type apiCreateReminderRequest struct {
	// Text is the single natural-language field ("remind me in 10 minutes to
	// call the doctor"). When present it wins; Message/When are the legacy
	// two-field form kept for back-compat.
	Text    string `json:"text"`
	Message string `json:"message"`
	When    string `json:"when"`
}

// apiInboxMessage mirrors db.InboxMessage with the FULL body (not the
// 160-char preview used by the poll endpoint) and a bool Read derived from
// ReadAt (nil = unread).
type apiInboxMessage struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	Trigger   string    `json:"trigger"`
	Status    string    `json:"status"`
	Body      string    `json:"body"`
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"created_at"`
}

func toAPIInboxMessage(m *db.InboxMessage) apiInboxMessage {
	return apiInboxMessage{
		ID:        m.ID,
		Source:    m.Source,
		AgentID:   m.AgentID,
		AgentName: m.AgentName,
		Trigger:   m.Trigger,
		Status:    m.Status,
		Body:      m.Body,
		Read:      m.ReadAt != nil,
		CreatedAt: m.CreatedAt,
	}
}

// ── Handlers: reminders ──────────────────────────────────────────────────────

// apiListReminders ports showReminders.
// GET /api/v1/reminders → 200 {"reminders":[{...}]}
func (s *Server) apiListReminders(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	rs, err := s.db.ListReminders(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiReminder, 0, len(rs))
	for _, r := range rs {
		out = append(out, toAPIReminder(r))
	}
	return c.JSON(http.StatusOK, map[string]any{"reminders": out})
}

// apiCreateReminder ports handleCreateReminder (web/handlers_misc.go:248-288),
// including the LLM time-parser fallback (buildLLMTimeParser +
// reminder.ParseNaturalTimeFull). Both of the template's error branches
// (LLM/parse failure, and "no time found" i.e. a zero remindAt) map to the
// same 400 unparseable_time code here — the template distinguished them only
// by message text, not by outcome.
func (s *Server) apiCreateReminder(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	var req apiCreateReminderRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	// Single-field path: one natural-language sentence carries both the time
	// and the message. reminder.ParseReminderText splits them (regex fast path,
	// LLM fallback) — the same resolver Telegram's /remind uses.
	if txt := strings.TrimSpace(req.Text); txt != "" {
		now := time.Now()
		loc := profile.LoadLocation(s.db, u.ID)
		llmFn := buildLLMTimeParser(s.coderForWorkspace(u.ID))
		remindAt, message, err := reminder.ParseReminderText(c.Request().Context(), txt, now, loc, llmFn, u.ID)
		if err != nil {
			return jsonErr(c, http.StatusBadRequest, "unparseable_time", `couldn't understand that; try "remind me in 10 minutes to call the doctor"`)
		}
		if remindAt.IsZero() {
			return jsonErr(c, http.StatusBadRequest, "no_time", `couldn't find a time in that — try adding one, e.g. "in 10 minutes" or "tomorrow at 3pm"`)
		}
		message = strings.TrimSpace(message)
		if message == "" {
			return jsonErr(c, http.StatusBadRequest, "missing_field", "what should I remind you about?")
		}
		r := &db.Reminder{ID: uuid.New().String(), WorkspaceID: u.ID, Message: message, RemindAt: remindAt}
		if err := s.db.CreateReminder(r); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
		}
		s.audit.Log(u.ID, "create_reminder", "reminder:"+r.ID, message, c.RealIP())
		return c.JSON(http.StatusCreated, toAPIReminder(r))
	}

	whenStr := strings.TrimSpace(req.When)

	if req.Message == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", "message is required")
	}
	if whenStr == "" {
		return jsonErr(c, http.StatusBadRequest, "missing_field", `when is required, e.g. "in 10 minutes", "tomorrow at 3pm"`)
	}

	now := time.Now()
	loc := profile.LoadLocation(s.db, u.ID)
	llmFn := buildLLMTimeParser(s.coderForWorkspace(u.ID))

	remindAt, _, err := reminder.ParseNaturalTimeFull(c.Request().Context(), whenStr, now, loc, llmFn, u.ID)
	if err != nil {
		return jsonErr(c, http.StatusBadRequest, "unparseable_time", `couldn't understand that time; try "in 10 minutes", "tomorrow at 3pm", "next Tuesday"`)
	}
	if remindAt.IsZero() {
		return jsonErr(c, http.StatusBadRequest, "unparseable_time", `no time found in "`+whenStr+`"; try "in 10 minutes", "tomorrow at 3pm", "next Friday"`)
	}

	r := &db.Reminder{
		ID:          uuid.New().String(),
		WorkspaceID: u.ID,
		Message:     req.Message,
		RemindAt:    remindAt,
	}
	if err := s.db.CreateReminder(r); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log(u.ID, "create_reminder", "reminder:"+r.ID, req.Message, c.RealIP())
	return c.JSON(http.StatusCreated, toAPIReminder(r))
}

// apiDeleteReminder ports handleDeleteReminder.
// DELETE /api/v1/reminders/:id → 200 {"ok":true}
func (s *Server) apiDeleteReminder(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	r, err := s.db.GetReminder(id)
	if err != nil || r.WorkspaceID != u.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "reminder not found")
	}
	if err := s.db.DeleteReminder(id); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// ── Handlers: inbox ──────────────────────────────────────────────────────────

// apiListInbox ports showInbox but returns the FULL body (not the 160-char
// preview used by handleInboxPoll). Accepts ?limit= (default 100) and
// ?offset= (default 0).
func (s *Server) apiListInbox(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	limit := 100
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := c.QueryParam("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	msgs, err := s.db.ListInboxMessages(u.ID, limit, offset)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	unread, err := s.db.UnreadInboxCount(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiInboxMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toAPIInboxMessage(m))
	}
	return c.JSON(http.StatusOK, map[string]any{"messages": out, "unread": unread})
}

// apiMarkInboxRead ports handleMarkInboxRead.
// POST /api/v1/inbox/:id/read → 200 {"ok":true}
func (s *Server) apiMarkInboxRead(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	_ = s.db.MarkInboxRead(id, u.ID) // ignore ErrNotFound — already read / gone (matches template)
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiMarkAllInboxRead ports handleMarkAllInboxRead.
// POST /api/v1/inbox/read-all → 200 {"ok":true}
func (s *Server) apiMarkAllInboxRead(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	_ = s.db.MarkAllInboxRead(u.ID)
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiDeleteInboxMessage ports handleDeleteInboxMessage.
// DELETE /api/v1/inbox/:id → 200 {"ok":true}
func (s *Server) apiDeleteInboxMessage(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	// The row is the whole record — notifications are not reflected into the
	// vault, so there is nothing else to drop. (Builds that did reflect them are
	// swept once at startup by vault.RemoveLegacyInboxNotes.)
	_ = s.db.DeleteInboxMessage(id, u.ID)
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}
