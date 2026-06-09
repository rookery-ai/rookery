package web

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
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

	s.audit.Log(u.ID, "run_agent", "agent:"+id, agent.Name, c.RealIP())

	p := s.page(c, "Agent: "+agent.Name)

	if s.runner == nil {
		p.Error = "Agent runner is not configured"
	} else {
		masterPw := c.FormValue("master_password")
		var outputLines []string
		send := func(msg string) { outputLines = append(outputLines, msg) }

		runErr := s.runner.Run(c.Request().Context(), agentrunner.RunInput{
			AgentID:    agent.ID,
			UserID:     u.ID,
			Trigger:    "manual",
			MasterPw:   masterPw,
			SendOutput: send,
		})
		if runErr != nil {
			p.Error = "Run failed: " + runErr.Error()
		} else if len(outputLines) > 0 {
			p.Success = strings.Join(outputLines, "\n")
		} else {
			p.Success = "Agent completed with no output."
		}
	}

	schedule, _ := s.db.GetScheduleForAgent(id)
	runs, _ := s.db.ListAgentRuns(id, 10)
	return c.Render(http.StatusOK, "dashboard/agent_detail.html", &agentDetailData{
		pageData: p,
		Agent:    agent,
		Schedule: schedule,
		Runs:     runs,
	})
}

