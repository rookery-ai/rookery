package web

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/profile"
)

// registerDashboardAPI registers the home-dashboard endpoint on the given
// group (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI +
// requireSetupCompleteAPI).
func (s *Server) registerDashboardAPI(g *echo.Group) {
	g.GET("/dashboard", s.apiGetDashboard)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

// apiDashboardRun mirrors toAPIRun's derived status but as a typed struct
// (recent_runs is a homogeneous, agent-name-joined list, unlike the agent
// detail page's per-agent run list).
type apiDashboardRun struct {
	ID         string     `json:"id"`
	AgentID    string     `json:"agent_id"`
	AgentName  string     `json:"agent_name"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func toAPIDashboardRun(r *db.AgentRunWithName) apiDashboardRun {
	status := "running"
	if r.ExitCode != nil {
		if *r.ExitCode == 0 {
			status = "success"
		} else {
			status = "failed"
		}
	}
	return apiDashboardRun{
		ID:         r.ID,
		AgentID:    r.AgentID,
		AgentName:  r.AgentName,
		Status:     status,
		Trigger:    r.Trigger,
		StartedAt:  r.StartedAt,
		FinishedAt: r.FinishedAt,
	}
}

// apiDashboardUpcoming mirrors a ScheduleWithName, trimmed to what the home
// page's "Next up" card needs.
type apiDashboardUpcoming struct {
	AgentID   string     `json:"agent_id"`
	AgentName string     `json:"agent_name"`
	CronExpr  string     `json:"cron_expr"`
	NextRunAt *time.Time `json:"next_run_at"`
}

func toAPIDashboardUpcoming(s *db.ScheduleWithName) apiDashboardUpcoming {
	return apiDashboardUpcoming{
		AgentID:   s.AgentID,
		AgentName: s.AgentName,
		CronExpr:  s.CronExpr,
		NextRunAt: s.NextRunAt,
	}
}

type apiDashboardResponse struct {
	DisplayName      string                 `json:"display_name"`
	AgentCount       int                    `json:"agent_count"`
	ActiveAgentCount int                    `json:"active_agent_count"`
	RecentRuns       []apiDashboardRun      `json:"recent_runs"`
	Upcoming         []apiDashboardUpcoming `json:"upcoming"`
	HasConnector     bool                   `json:"has_connector"`
}

// ── Handler ──────────────────────────────────────────────────────────────────

// apiGetDashboard ports showDashboard (web/handlers_dashboard.go) plus the
// upcoming-schedules + active-agent-count fields the SPA home page needs.
// GET /api/v1/dashboard → 200 apiDashboardResponse
func (s *Server) apiGetDashboard(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	agents, err := s.db.ListAgents(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	activeCount := 0
	for _, a := range agents {
		if a.Active {
			activeCount++
		}
	}

	recentRuns, err := s.db.RecentAgentRunsWithNames(u.ID, 10)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	runsOut := make([]apiDashboardRun, 0, len(recentRuns))
	for _, r := range recentRuns {
		runsOut = append(runsOut, toAPIDashboardRun(r))
	}

	schedules, err := s.db.ListWorkspaceSchedulesWithNames(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	upcomingOut := make([]apiDashboardUpcoming, 0, len(schedules))
	for _, sc := range schedules {
		upcomingOut = append(upcomingOut, toAPIDashboardUpcoming(sc))
	}

	prof := profile.Load(s.db, u.ID)
	dn := prof.DisplayName
	if dn == "" {
		dn = u.Name
	}

	// Any connected chat platform (Telegram/Discord/Slack) counts — the old
	// template only checked telegram; generalized here since multiple
	// platforms are now supported.
	conns, err := s.db.ListWorkspacePlatformConnections(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	hasConnector := len(conns) > 0

	return c.JSON(http.StatusOK, apiDashboardResponse{
		DisplayName:      dn,
		AgentCount:       len(agents),
		ActiveAgentCount: activeCount,
		RecentRuns:       orEmpty(runsOut),
		Upcoming:         orEmpty(upcomingOut),
		HasConnector:     hasConnector,
	})
}
