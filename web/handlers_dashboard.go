package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type dashboardData struct {
	*pageData
	AgentCount   int
	RecentRuns   []*db.AgentRun
	Reminders    []*db.Reminder
	HasConnector bool
}

func (s *Server) showDashboard(c echo.Context) error {
	u := c.Get("user").(*db.User)

	agents, _ := s.db.ListAgents(u.ID)
	recentRuns, _ := s.db.RecentAgentRuns(u.ID, 5)
	reminders, _ := s.db.ListReminders(u.ID)

	// Check if user has any platform connected
	_, err := s.db.GetPlatformConnection(u.ID, "telegram")
	hasConnector := err == nil

	return c.Render(http.StatusOK, "dashboard/home.html", &dashboardData{
		pageData:     s.page(c, "Dashboard"),
		AgentCount:   len(agents),
		RecentRuns:   recentRuns,
		Reminders:    reminders,
		HasConnector: hasConnector,
	})
}
