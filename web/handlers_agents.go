package web

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
)

type agentsPageData struct {
	*pageData
	Agents []*db.Agent
}

type agentDetailData struct {
	*pageData
	Agent    *db.Agent
	Schedule *db.AgentSchedule
	Runs     []*db.AgentRun
}

func (s *Server) showAgents(c echo.Context) error {
	u := c.Get("user").(*db.User)
	agents, _ := s.db.ListAgents(u.ID)
	return c.Render(http.StatusOK, "dashboard/agents.html", &agentsPageData{
		pageData: s.page(c, "My Agents"),
		Agents:   agents,
	})
}

func (s *Server) showNewAgent(c echo.Context) error {
	return c.Render(http.StatusOK, "dashboard/agent_new.html", s.page(c, "Create Agent"))
}

func (s *Server) handleNewAgent(c echo.Context) error {
	u := c.Get("user").(*db.User)
	name := c.FormValue("name")
	description := c.FormValue("description")

	if name == "" {
		p := s.page(c, "Create Agent")
		p.Error = "Agent name is required"
		return c.Render(http.StatusBadRequest, "dashboard/agent_new.html", p)
	}

	agent := &db.Agent{
		ID:          uuid.New().String(),
		UserID:      u.ID,
		Name:        name,
		Description: description,
		Active:      true,
	}

	if err := s.db.CreateAgent(agent); err != nil {
		p := s.page(c, "Create Agent")
		p.Error = "Failed to create agent: " + err.Error()
		return c.Render(http.StatusInternalServerError, "dashboard/agent_new.html", p)
	}

	s.audit.Log(u.ID, "create_agent", "agent:"+agent.ID, name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/agents/"+agent.ID)
}

func (s *Server) showAgentDetail(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	schedule, _ := s.db.GetScheduleForAgent(id)
	runs, _ := s.db.ListAgentRuns(id, 10)

	return c.Render(http.StatusOK, "dashboard/agent_detail.html", &agentDetailData{
		pageData: s.page(c, "Agent: "+agent.Name),
		Agent:    agent,
		Schedule: schedule,
		Runs:     runs,
	})
}

func (s *Server) handleDeleteAgent(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	if err := s.db.DeleteAgent(id); err != nil {
		return err
	}

	s.audit.Log(u.ID, "delete_agent", "agent:"+id, agent.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/agents")
}

func (s *Server) handleRunAgent(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	// Agent runner integration happens in Phase 6.
	// For now, record a pending run and redirect.
	run := &db.AgentRun{
		ID:      uuid.New().String(),
		AgentID: agent.ID,
		UserID:  u.ID,
		Trigger: "manual",
	}
	_ = s.db.CreateAgentRun(run)

	s.audit.Log(u.ID, "run_agent", "agent:"+id, agent.Name, c.RealIP())

	p := s.page(c, "Agent: "+agent.Name)
	p.Success = "Agent run queued (runner not yet connected)"
	schedule, _ := s.db.GetScheduleForAgent(id)
	runs, _ := s.db.ListAgentRuns(id, 10)
	return c.Render(http.StatusOK, "dashboard/agent_detail.html", &agentDetailData{
		pageData: p,
		Agent:    agent,
		Schedule: schedule,
		Runs:     runs,
	})
}
