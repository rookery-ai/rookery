package web

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/ilijad1/rookery/internal/db"
)

// registerApprovalRoutes wires the approval gate's control surface. Without these the
// gate is unreachable: SetAgentConnectionApprovalMode has no other caller, and a
// parked post is unresolvable on an install with no chat platform connected.
func (s *Server) registerApprovalRoutes(g *echo.Group) {
	g.PUT("/agents/:id/connections/:connID/approval", s.apiSetConnectionApproval)
	g.GET("/approvals", s.apiListApprovals)
	g.POST("/approvals/:id/approve", s.apiApproveAction)
	g.POST("/approvals/:id/reject", s.apiRejectAction)
}

// apiSetConnectionApproval toggles the gate for one agent+connection binding.
func (s *Server) apiSetConnectionApproval(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()

	agent, err := s.db.GetAgent(c.Param("id"))
	if err != nil || agent == nil || agent.WorkspaceID != w.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}

	var req struct {
		Mode string `json:"mode"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if req.Mode != db.ApprovalModeAuto && req.Mode != db.ApprovalModeApprove {
		return jsonErr(c, http.StatusBadRequest, "invalid_mode",
			`mode must be "auto" or "approve"`)
	}

	// The connection must belong to this workspace AND be bound to this agent —
	// otherwise the write silently no-ops and the UI shows a toggle that does nothing.
	bound, _ := s.db.ListAgentConnections(ctx, agent.ID)
	found := false
	for _, cn := range bound {
		if cn.ID == c.Param("connID") {
			found = true
			break
		}
	}
	if !found {
		return jsonErr(c, http.StatusNotFound, "not_bound",
			"that connection is not attached to this agent")
	}

	if err := s.db.SetAgentConnectionApprovalMode(ctx, agent.ID, c.Param("connID"), req.Mode); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "mode": req.Mode})
}

// apiListApprovals returns the workspace's parked calls. Defaults to pending only —
// the queue is a to-do list, not an audit log.
func (s *Server) apiListApprovals(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	status := c.QueryParam("status")
	if status == "" {
		status = db.PendingStatusPending
	}
	if status == "all" {
		status = ""
	}
	rows, err := s.db.ListPendingActions(c.Request().Context(), w.ID, status, 100)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]map[string]any, 0, len(rows))
	for _, p := range rows {
		out = append(out, map[string]any{
			"id":         p.ID,
			"agent_id":   p.AgentID,
			"agent_name": p.AgentName,
			"provider":   p.Provider,
			"action":     p.Action,
			"summary":    p.Summary,
			"status":     p.Status,
			"error":      p.Error,
			"created_at": p.CreatedAt,
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"approvals": out})
}

func (s *Server) apiApproveAction(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	if s.approval == nil {
		return jsonErr(c, http.StatusServiceUnavailable, "unavailable", "approvals are not configured")
	}
	p, err := s.approval.Approve(c.Request().Context(), w.ID, c.Param("id"))
	if err != nil {
		// A nil row means the claim failed: already approved, rejected, or expired.
		// A non-nil row with an error means the claim WON and the send failed — the
		// ticket is spent, which the caller must not read as "try again".
		if p == nil {
			return jsonErr(c, http.StatusConflict, "not_pending",
				"that request is no longer pending — it may already have been resolved or expired")
		}
		return c.JSON(http.StatusOK, map[string]any{"ok": false, "status": db.PendingStatusFailed, "error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "status": db.PendingStatusApproved})
}

func (s *Server) apiRejectAction(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	if s.approval == nil {
		return jsonErr(c, http.StatusServiceUnavailable, "unavailable", "approvals are not configured")
	}
	if _, err := s.approval.Reject(c.Request().Context(), w.ID, c.Param("id")); err != nil {
		return jsonErr(c, http.StatusConflict, "not_pending",
			"that request is no longer pending — it may already have been resolved or expired")
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true, "status": db.PendingStatusRejected})
}

// connectionApprovalModes reports each attached connection's gate, keyed by
// connection id. Absent or unreadable entries default to "auto" so the UI never
// renders a toggle as ON because a read failed.
func (s *Server) connectionApprovalModes(agentID string, connIDs []string) map[string]string {
	out := make(map[string]string, len(connIDs))
	ctx := context.Background()
	for _, id := range connIDs {
		mode, err := s.db.AgentConnectionApprovalMode(ctx, agentID, id)
		if err != nil || mode == "" {
			mode = db.ApprovalModeAuto
		}
		out[id] = mode
	}
	return out
}
