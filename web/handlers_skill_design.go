package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

// handleSkillDesignChat drives the conversational skill-creator via JSON API.
// POST /dashboard/skills/design
// Body: {"name": "my-skill", "message": "..."}
// Response: {"response": "...", "done": false, "state": "...", "generation_failed": false}
// or {"response": "...", "done": true, "skill_id": "..."}
func (s *Server) handleSkillDesignChat(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	var req struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request"})
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Message = strings.TrimSpace(req.Message)

	if req.Message == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
	}
	if s.skillFlow == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "skill designer not configured"})
	}

	ctx := c.Request().Context()

	// No active session + a name → start a new design session.
	if s.skillFlow.GetSession(u.ID) == nil {
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required to start a new session"})
		}
		response, err := s.skillFlow.StartDesign(ctx, u.ID, req.Name, req.Message)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		state, generationFailed := "", false
		if sess := s.skillFlow.GetSession(u.ID); sess != nil {
			state = sess.State.String()
			generationFailed = sess.GenerationFailed
		}
		return c.JSON(http.StatusOK, map[string]interface{}{
			"response":          response,
			"done":              false,
			"state":             state,
			"generation_failed": generationFailed,
		})
	}

	auditName := req.Name
	if sess := s.skillFlow.GetSession(u.ID); sess != nil && sess.SkillName != "" {
		auditName = sess.SkillName
	}

	response, isDone, skillID, err := s.skillFlow.Step(ctx, u.ID, req.Message)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if isDone {
		s.audit.Log(u.ID, "create_skill", "skill:"+skillID, auditName, c.RealIP())
		return c.JSON(http.StatusOK, map[string]interface{}{
			"response": response,
			"done":     true,
			"skill_id": skillID,
		})
	}

	state, generationFailed := "", false
	if sess := s.skillFlow.GetSession(u.ID); sess != nil {
		state = sess.State.String()
		generationFailed = sess.GenerationFailed
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"response":          response,
		"done":              false,
		"state":             state,
		"generation_failed": generationFailed,
	})
}

// POST /dashboard/skills/design/cancel
func (s *Server) handleCancelSkillDesign(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.skillFlow != nil {
		s.skillFlow.Cancel(u.ID)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "cancelled"})
}

// POST /dashboard/skills/design/resume
func (s *Server) handleResumeSkillDraft(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.skillFlow == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "skill designer not configured"})
	}
	resp, err := s.skillFlow.ResumeDraft(c.Request().Context(), u.ID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	type histEntry struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	sess := s.skillFlow.GetSession(u.ID)
	out := map[string]interface{}{
		"response":          resp,
		"state":             "",
		"history":           []histEntry{},
		"skill_id":          "",
		"skill_name":        "",
		"generation_failed": false,
	}
	if sess != nil {
		out["generation_failed"] = sess.GenerationFailed
		hist := make([]histEntry, 0, len(sess.History))
		for _, m := range sess.History {
			hist = append(hist, histEntry{Role: m.Role, Content: m.Content})
		}
		out["state"] = sess.State.String()
		out["history"] = hist
		out["skill_name"] = sess.SkillName
	}
	return c.JSON(http.StatusOK, out)
}

// POST /dashboard/skills/design/dismiss
func (s *Server) handleDismissSkillDraft(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.skillFlow != nil {
		_ = s.skillFlow.DismissDraft(u.ID)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// handleSkillDesignProgress streams skill-generation milestone events via SSE.
// GET /dashboard/skills/design/progress
func (s *Server) handleSkillDesignProgress(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	reqCtx := c.Request().Context()

	if s.skillFlow == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no skill design flow"})
	}

	var ch <-chan string
	for i := 0; i < 150; i++ {
		select {
		case <-reqCtx.Done():
			return nil
		default:
		}
		if c2, ok := s.skillFlow.GetProgressChan(u.ID); ok {
			ch = c2
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if ch == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "no active generation"})
	}

	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	for {
		select {
		case <-reqCtx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			w.Flush()
		}
	}
}
