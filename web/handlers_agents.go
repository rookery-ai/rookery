package web

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/labstack/echo/v4"
	"github.com/robfig/cron/v3"
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
	Code     string // contents of main.py; empty if not yet generated
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
	if description == "" {
		p := s.page(c, "Create Agent")
		p.Error = "Description is required"
		return c.Render(http.StatusBadRequest, "dashboard/agent_new.html", p)
	}

	agentID := uuid.New().String()

	// Use the design flow to generate real agent code if available.
	if s.designFlow != nil {
		if err := s.designFlow.GenerateAndSave(context.Background(), u.ID, agentID, name, description); err != nil {
			p := s.page(c, "Create Agent")
			p.Error = "Agent generation failed: " + err.Error()
			return c.Render(http.StatusInternalServerError, "dashboard/agent_new.html", p)
		}
		s.audit.Log(u.ID, "create_agent", "agent:"+agentID, name, c.RealIP())
		return c.Redirect(http.StatusFound, "/dashboard/agents/"+agentID)
	}

	// Fallback: create a DB row without code (coder not available).
	agent := &db.Agent{
		ID:          agentID,
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

	s.audit.Log(u.ID, "create_agent", "agent:"+agentID, name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/agents/"+agentID)
}

func (s *Server) showAgentDetail(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	return s.renderAgentDetail(c, agent, u.ID, s.page(c, "Agent: "+agent.Name))
}

// agentsDir returns the agents base directory from config (or empty string in tests).
func (s *Server) agentsDir() string {
	if s.cfg == nil {
		return ""
	}
	return s.cfg.Data.Dir + "/agents"
}

// renderAgentDetail loads all data needed for the agent detail page and renders it.
func (s *Server) renderAgentDetail(c echo.Context, agent *db.Agent, userID string, p *pageData) error {
	schedule, _ := s.db.GetScheduleForAgent(agent.ID)
	runs, _ := s.db.ListAgentRuns(agent.ID, 10)
	var code string
	if dir := s.agentsDir(); dir != "" {
		if data, err := os.ReadFile(agentdesigner.AgentCodePath(dir, userID, agent.ID)); err == nil {
			code = string(data)
		}
	}
	return c.Render(http.StatusOK, "dashboard/agent_detail.html", &agentDetailData{
		pageData: p,
		Agent:    agent,
		Schedule: schedule,
		Runs:     runs,
		Code:     code,
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

	return s.renderAgentDetail(c, agent, u.ID, p)
}

func (s *Server) handleSaveSchedule(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	cronExpr := strings.TrimSpace(c.FormValue("cron_expr"))
	p := s.page(c, "Agent: "+agent.Name)

	if cronExpr == "" {
		p.Error = "Cron expression is required"
	} else {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		sched, parseErr := parser.Parse(cronExpr)
		if parseErr != nil {
			p.Error = "Invalid cron expression: " + parseErr.Error()
		} else {
			nextRun := sched.Next(time.Now())
			// Reuse existing schedule ID so ON CONFLICT(id) updates rather than inserts.
			existing, _ := s.db.GetScheduleForAgent(agent.ID)
			schedID := uuid.New().String()
			if existing != nil {
				schedID = existing.ID
			}
			row := &db.AgentSchedule{
				ID:        schedID,
				AgentID:   agent.ID,
				UserID:    u.ID,
				CronExpr:  cronExpr,
				NextRunAt: &nextRun,
				Enabled:   true,
			}
			if err := s.db.UpsertAgentSchedule(row); err != nil {
				p.Error = "Failed to save schedule: " + err.Error()
			} else {
				p.Success = "Schedule saved"
				s.audit.Log(u.ID, "save_schedule", "agent:"+id, cronExpr, c.RealIP())
			}
		}
	}

	return s.renderAgentDetail(c, agent, u.ID, p)
}

func (s *Server) handleDeleteSchedule(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	_ = s.db.DeleteAgentSchedule(id)
	s.audit.Log(u.ID, "delete_schedule", "agent:"+id, "", c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/agents/"+id)
}

func (s *Server) handleSaveCode(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	code := c.FormValue("code")
	p := s.page(c, "Agent: "+agent.Name)

	if code == "" {
		p.Error = "Code cannot be empty"
		return s.renderAgentDetail(c, agent, u.ID, p)
	}

	if err := agentdesigner.RunFullGuardrails(code, ""); err != nil {
		p.Error = "Code failed safety checks: " + err.Error()
		return s.renderAgentDetail(c, agent, u.ID, p)
	}

	dir := s.agentsDir()
	if dir == "" {
		p.Error = "Storage not configured"
		return s.renderAgentDetail(c, agent, u.ID, p)
	}

	codePath := agentdesigner.AgentCodePath(dir, u.ID, id)
	if err := os.MkdirAll(dir+"/"+u.ID+"/"+id, 0o750); err != nil {
		p.Error = "Failed to create agent directory: " + err.Error()
		return s.renderAgentDetail(c, agent, u.ID, p)
	}
	if err := os.WriteFile(codePath, []byte(code), 0o640); err != nil {
		p.Error = "Failed to save code: " + err.Error()
		return s.renderAgentDetail(c, agent, u.ID, p)
	}

	s.audit.Log(u.ID, "edit_agent_code", "agent:"+id, agent.Name, c.RealIP())
	p.Success = "Code saved"
	return s.renderAgentDetail(c, agent, u.ID, p)
}

