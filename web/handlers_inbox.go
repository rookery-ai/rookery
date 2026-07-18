package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/labstack/echo/v4"
)

// handleInboxPoll returns the unread count plus the few newest messages so the
// navbar badge (and a lightweight "what's new" toast) can update without a reload.
// Shared by the JSON inbox API.
func (s *Server) handleInboxPoll(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	unread, err := s.db.UnreadInboxCount(u.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	loc := profile.LoadLocation(s.db, u.ID)
	recent, _ := s.db.ListInboxMessages(u.ID, 5, 0)
	type item struct {
		ID        string `json:"id"`
		Source    string `json:"source"`
		AgentName string `json:"agent_name"`
		Trigger   string `json:"trigger"`
		Status    string `json:"status"`
		Read      bool   `json:"read"`
		Preview   string `json:"preview"`
		CreatedAt string `json:"created_at"`
	}
	items := make([]item, 0, len(recent))
	for _, m := range recent {
		preview := m.Body
		if len(preview) > 160 {
			preview = preview[:160] + "…"
		}
		items = append(items, item{
			ID: m.ID, Source: m.Source, AgentName: m.AgentName,
			Trigger: m.Trigger, Status: m.Status, Read: m.ReadAt != nil,
			Preview:   preview,
			CreatedAt: m.CreatedAt.In(loc).Format("Jan 2 15:04"),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"unread": unread, "recent": items})
}
