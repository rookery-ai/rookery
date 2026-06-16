package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/labstack/echo/v4"
	"github.com/robfig/cron/v3"
)

type agentsPageData struct {
	*pageData
	Agents []*db.Agent
}

type agentDetailData struct {
	*pageData
	Agent          *db.Agent
	Schedule       *db.AgentSchedule
	Runs           []*db.AgentRun
	Manifest       *agentdesigner.AgentManifest
	AgentMD        string   // AGENT.md
	State          string   // state.json (read-only)
	Logs           []string // sorted log file names (newest first)
	LastLog        string   // content of most recent log
	AgentSkills    []*db.Skill
	AllSkills      []*db.Skill
	MissingSecrets []string
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

// handleDesignChat drives the conversational agent creation via JSON API.
// POST /dashboard/agents/design
// Body: {"name": "my-agent", "message": "..."}
// Response: {"response": "...", "done": false} or {"response": "...", "done": true, "agent_id": "..."}
func (s *Server) handleDesignChat(c echo.Context) error {
	u := c.Get("user").(*db.User)

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

	if s.designFlow == nil {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "agent designer not configured"})
	}

	ctx := c.Request().Context()

	// If no active session and a name is provided, start a new design session.
	if s.designFlow.GetSession(u.ID) == nil {
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required to start a new session"})
		}
		response, err := s.designFlow.StartDesign(ctx, u.ID, req.Name, req.Message)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]interface{}{
			"response": response,
			"done":     false,
		})
	}

	// Existing session: step the FSM.
	response, isDone, agentID, err := s.designFlow.Step(ctx, u.ID, req.Message)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	if isDone {
		s.audit.Log(u.ID, "create_agent", "agent:"+agentID, req.Name, c.RealIP())
		return c.JSON(http.StatusOK, map[string]interface{}{
			"response": response,
			"done":     true,
			"agent_id": agentID,
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"response": response,
		"done":     false,
	})
}

// handleCancelDesign cancels the active design session.
// POST /dashboard/agents/design/cancel
func (s *Server) handleCancelDesign(c echo.Context) error {
	u := c.Get("user").(*db.User)
	if s.designFlow != nil {
		s.designFlow.Cancel(u.ID)
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "cancelled"})
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
	allSkills, _ := s.db.ListSkills(userID)
	agentSkills, _ := s.db.ListSkillsForAgent(agent.ID)

	data := &agentDetailData{
		pageData:    p,
		Agent:       agent,
		Schedule:    schedule,
		Runs:        runs,
		AgentSkills: agentSkills,
		AllSkills:   allSkills,
	}

	dir := s.agentsDir()
	if dir != "" {
		manifest, _ := agentdesigner.LoadManifest(dir, userID, agent.ID)
		data.Manifest = manifest

		// Load AGENT.md (fall back to CLAUDE.md for legacy agents).
		if raw, err := os.ReadFile(agentdesigner.AgentDescPath(dir, userID, agent.ID)); err == nil {
			data.AgentMD = string(raw)
		} else if raw, err := os.ReadFile(agentdesigner.AgentMDPath(dir, userID, agent.ID)); err == nil {
			data.AgentMD = string(raw)
		}

		// Load state.json.
		if raw, err := os.ReadFile(agentdesigner.AgentStatePath(dir, userID, agent.ID)); err == nil {
			data.State = string(raw)
		}

		// List log files (newest first).
		logsDir := agentdesigner.AgentLogsDir(dir, userID, agent.ID)
		if entries, err := os.ReadDir(logsDir); err == nil {
			for i := len(entries) - 1; i >= 0; i-- {
				e := entries[i]
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".txt") {
					data.Logs = append(data.Logs, e.Name())
				}
			}
			// Load content of the most recent log.
			if len(data.Logs) > 0 {
				if raw, err := os.ReadFile(filepath.Join(logsDir, data.Logs[0])); err == nil {
					data.LastLog = string(raw)
				}
			}
		}

		// Compute missing secrets for manifest-declared requirements.
		if manifest != nil && len(manifest.RequiredSecrets) > 0 {
			knownNames, _ := s.db.ListSecretNames(userID)
			knownSet := make(map[string]bool, len(knownNames))
			for _, n := range knownNames {
				knownSet[n] = true
			}
			for _, req := range manifest.RequiredSecrets {
				if !knownSet[req] {
					data.MissingSecrets = append(data.MissingSecrets, req)
				}
			}
		}
	}

	return c.Render(http.StatusOK, "dashboard/agent_detail.html", data)
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
		// Decrypt the user's stored master password the same way the scheduler
		// does for cron runs, so manual "Run Now" gets secret injection too.
		// There is no password-entry field on this form — agent execution
		// (unlike viewing secret values) doesn't require live re-entry.
		var masterPw string
		if u.EncryptedMasterPassword != "" {
			if pw, err := secrets.DecryptMasterPassword(u.EncryptedMasterPassword, s.systemKey); err == nil {
				masterPw = pw
			}
		}
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


