package web

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/rookery-ai/rookery/internal/budget"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/profile"
)

// maxLimitTurns is the highest per-run turn limit: the engine's hard ceiling is
// 150 turns, so a limit of 150 or more could never be the thing that stops a run.
const maxLimitTurns = 149

type limitRun struct {
	Tokens int64   `json:"tokens"`
	Turns  int64   `json:"turns"`
	USD    float64 `json:"usd"`
}

type limitPeriod struct {
	Tokens int64   `json:"tokens"`
	USD    float64 `json:"usd"`
}

type limitSpend struct {
	Tokens      int64   `json:"tokens"`
	USD         float64 `json:"usd"`
	USDReported bool    `json:"usd_reported"`
}

type agentLimitsBody struct {
	Run   limitRun    `json:"run"`
	Day   limitPeriod `json:"day"`
	Month limitPeriod `json:"month"`
}

type agentLimitsDTO struct {
	agentLimitsBody
	Enforced         bool `json:"enforced"`
	DollarsSupported bool `json:"dollars_supported"`
	Spend            struct {
		Day   limitSpend `json:"day"`
		Month limitSpend `json:"month"`
	} `json:"spend"`
	// PausedUntil is an explicit null (never omitted) unless a pause is in force.
	PausedUntil *string `json:"paused_until"`
	PauseReason string  `json:"pause_reason"`
}

// activePause reports the agent's pause only while it is still in the future; a
// lapsed pause is history and must not render as a live one.
func activePause(a *db.Agent, now time.Time) (time.Time, string, bool) {
	if a.BudgetPausedUntil == nil || !a.BudgetPausedUntil.After(now) {
		return time.Time{}, "", false
	}
	return *a.BudgetPausedUntil, a.BudgetPauseReason, true
}

// agentLimitsDTO is the one builder behind GET/PUT limits and the agent detail's
// "limits" block, so the three cannot drift.
func (s *Server) agentLimitsDTO(_ context.Context, agent *db.Agent, workspaceID string) agentLimitsDTO {
	var out agentLimitsDTO
	if l, err := s.db.GetAgentLimits(agent.ID); err == nil {
		out.Run = limitRun{Tokens: l.RunTokens, Turns: l.RunTurns, USD: l.RunUSD}
		out.Day = limitPeriod{Tokens: l.DayTokens, USD: l.DayUSD}
		out.Month = limitPeriod{Tokens: l.MonthTokens, USD: l.MonthUSD}
	}
	if s.runner != nil {
		e := s.runner.AgentEnforcement(workspaceID, agent.ID)
		out.Enforced, out.DollarsSupported = e.Enforced, e.DollarsSupported
	}
	now := time.Now()
	_, loc := profile.ScheduleZone(s.db, workspaceID)
	if loc == nil {
		loc = time.Local
	}
	fill := func(p budget.Period, dst *limitSpend) {
		sp, err := s.db.AgentSpendSince(agent.ID, budget.PeriodStart(p, now, loc))
		if err != nil {
			dst.USDReported = true
			return
		}
		*dst = limitSpend{Tokens: sp.Tokens, USD: sp.USD, USDReported: !sp.CostUnreported}
	}
	fill(budget.PeriodDay, &out.Spend.Day)
	fill(budget.PeriodMonth, &out.Spend.Month)
	if until, reason, ok := activePause(agent, now); ok {
		u := until.UTC().Format(time.RFC3339)
		out.PausedUntil = &u
		out.PauseReason = reason
	}
	return out
}

// GET /api/v1/agents/:id/limits
func (s *Server) apiGetAgentLimits(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}
	return c.JSON(http.StatusOK, s.agentLimitsDTO(c.Request().Context(), agent, u.ID))
}

func validMoney(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

// PUT /api/v1/agents/:id/limits — saving clears any pause: the owner has just
// changed the terms the pause was set under, and a stale pause would keep the
// agent stopped by a limit that no longer exists.
func (s *Server) apiSaveAgentLimits(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}
	var req agentLimitsBody
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if req.Run.Tokens < 0 || req.Run.Turns < 0 || req.Day.Tokens < 0 || req.Month.Tokens < 0 ||
		!validMoney(req.Run.USD) || !validMoney(req.Day.USD) || !validMoney(req.Month.USD) {
		return jsonErr(c, http.StatusBadRequest, "invalid_limits", "limits must not be negative")
	}
	if req.Run.Turns > maxLimitTurns {
		return jsonErr(c, http.StatusBadRequest, "invalid_limits", "turns limit must be 149 or less")
	}
	if err := s.db.SaveAgentLimits(db.AgentLimits{
		AgentID: agent.ID, RunTokens: req.Run.Tokens, RunTurns: req.Run.Turns, RunUSD: req.Run.USD,
		DayTokens: req.Day.Tokens, DayUSD: req.Day.USD, MonthTokens: req.Month.Tokens, MonthUSD: req.Month.USD,
	}); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	if err := s.db.ClearAgentBudgetPause(agent.ID); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	s.audit.Log(u.ID, "save_agent_limits", "agent:"+agent.ID, agent.Name, c.RealIP())
	fresh, err := s.getOwnedAgent(u.ID, agent.ID)
	if err != nil {
		fresh = agent
	}
	return c.JSON(http.StatusOK, s.agentLimitsDTO(c.Request().Context(), fresh, u.ID))
}
