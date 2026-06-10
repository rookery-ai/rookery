package web

import (
	"net/http"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type dashboardData struct {
	*pageData
	DisplayName  string
	AgentCount   int
	RecentRuns   []*db.AgentRunWithName
	Reminders    []*db.Reminder
	HasConnector bool
}

func (s *Server) showDashboard(c echo.Context) error {
	u := c.Get("user").(*db.User)

	agents, _ := s.db.ListAgents(u.ID)
	recentRuns, _ := s.db.RecentAgentRunsWithNames(u.ID, 5)
	reminders, _ := s.db.ListReminders(u.ID)

	dn, _ := s.db.GetSetting(u.ID, "display_name")
	if dn == "" {
		dn = u.Username
	}

	// Check if user has any platform connected
	_, err := s.db.GetPlatformConnection(u.ID, "telegram")
	hasConnector := err == nil

	return c.Render(http.StatusOK, "dashboard/home.html", &dashboardData{
		pageData:     s.page(c, "Dashboard"),
		DisplayName:  dn,
		AgentCount:   len(agents),
		RecentRuns:   recentRuns,
		Reminders:    reminders,
		HasConnector: hasConnector,
	})
}
