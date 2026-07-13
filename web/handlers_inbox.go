package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/labstack/echo/v4"
)

// inboxPageData renders the inbox list page.
type inboxPageData struct {
	*pageData
	Messages []*db.InboxMessage
	Unread   int
}

// showInbox renders the cross-agent notification inbox, newest first.
func (s *Server) showInbox(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	msgs, _ := s.db.ListInboxMessages(u.ID, 100, 0)
	unread, _ := s.db.UnreadInboxCount(u.ID)
	if msgs == nil {
		msgs = []*db.InboxMessage{}
	}
	return c.Render(http.StatusOK, "dashboard/inbox.html", &inboxPageData{
		pageData: s.page(c, "Inbox"),
		Messages: msgs,
		Unread:   unread,
	})
}

// handleInboxPoll returns the unread count plus the few newest messages so the
// navbar badge (and a lightweight "what's new" toast) can update without a reload.
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

// handleMarkInboxRead marks a single inbox message read (workspace-scoped).
func (s *Server) handleMarkInboxRead(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	_ = s.db.MarkInboxRead(id, u.ID) // ignore ErrNotFound — already read / gone
	return c.Redirect(http.StatusSeeOther, "/dashboard/inbox")
}

// handleMarkAllInboxRead marks every unread inbox message in the workspace read.
func (s *Server) handleMarkAllInboxRead(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	_ = s.db.MarkAllInboxRead(u.ID)
	return c.Redirect(http.StatusSeeOther, "/dashboard/inbox")
}

// handleDeleteInboxMessage deletes a single inbox message (workspace-scoped).
func (s *Server) handleDeleteInboxMessage(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")
	_ = s.db.DeleteInboxMessage(id, u.ID)
	return c.Redirect(http.StatusSeeOther, "/dashboard/inbox")
}
