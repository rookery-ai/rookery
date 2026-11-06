package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/rename"
)

// apiRenameResult is what a rename reports. Both slices are always arrays, because a
// TypeScript default substitutes only for undefined, never for a JSON null.
type apiRenameResult struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	StillMentions []string `json:"still_mentions"`
	Warnings      []string `json:"warnings"`
	EnvRemoved    bool     `json:"env_removed"`
}

func renamePlanError(c echo.Context, kind, name string, err error) error {
	switch {
	case errors.Is(err, rename.ErrInvalidName):
		msg := strings.TrimPrefix(err.Error(), rename.ErrInvalidName.Error()+": ")
		return jsonErr(c, http.StatusBadRequest, "invalid_name", msg)
	case errors.Is(err, db.ErrNameTaken):
		return jsonErr(c, http.StatusConflict, "name_taken",
			fmt.Sprintf("The name %q is already in use by another %s.", name, kind))
	}
	return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
}

// firstRunningAgent returns the name of a running agent among ids, or "". A run holds
// its prompt from the moment it starts, so it would go on using the old name.
func (s *Server) firstRunningAgent(ids []string) string {
	for _, id := range ids {
		if s.isAgentRunning(id) {
			if a, err := s.db.GetAgent(id); err == nil {
				return a.Name
			}
			return "An agent"
		}
	}
	return ""
}

// agentDesignOpen reports an open design session. It holds AGENT.md and the skill list
// in memory and would save them back with the old name, and working out whether this
// particular session mentions the name would be a lot of machinery for a rare moment
// that one click on Cancel resolves. A draft with no live session needs no refusal: the
// rename rewrites it.
func (s *Server) agentDesignOpen(workspaceID string) bool {
	return s.designFlow != nil && s.designFlow.GetSession(workspaceID) != nil
}

const agentDesignOpenMsg = "An agent is being designed or edited in this workspace, and it holds its own copy of these names. Finish or cancel it, then rename."

func (s *Server) renameResponse(c echo.Context, id string, r *rename.Result) error {
	return c.JSON(http.StatusOK, apiRenameResult{
		ID: id, Name: r.Name, StillMentions: r.StillMentions, Warnings: r.Warnings, EnvRemoved: r.EnvRemoved,
	})
}

// apiRenameAgent renames an agent.
//
// PUT /api/v1/agents/:id/name {"name": "..."}
func (s *Server) apiRenameAgent(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	agent, err := s.getOwnedAgent(u.ID, c.Param("id"))
	if err != nil {
		return jsonErr(c, http.StatusNotFound, "not_found", "agent not found")
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if s.vault == nil {
		return jsonErr(c, http.StatusServiceUnavailable, "not_configured", "the knowledge base is not configured")
	}

	plan, err := rename.New(s.db, s.vault, s.homesDir).PrepareAgent(u.ID, agent, req.Name)
	if err != nil {
		return renamePlanError(c, "agent", strings.TrimSpace(req.Name), err)
	}
	if !plan.Unchanged {
		if running := s.firstRunningAgent(plan.ReferencingAgentIDs); running != "" {
			return jsonErr(c, http.StatusConflict, "agent_running",
				fmt.Sprintf("%q is running and refers to this name. Wait for it to finish, then rename.", running))
		}
		if s.agentDesignOpen(u.ID) {
			return jsonErr(c, http.StatusConflict, "design_in_progress", agentDesignOpenMsg)
		}
	}

	result, err := plan.Apply()
	if err != nil {
		return renamePlanError(c, "agent", plan.NewName, err)
	}
	if !plan.Unchanged {
		s.audit.Log(u.ID, "rename_agent", "agent:"+agent.ID, agent.Name+" → "+result.Name, c.RealIP())
	}
	return s.renameResponse(c, agent.ID, result)
}

// skillDraftHolds reports a skill design holding either name. Saving a skill overwrites
// a same-named one in place, so a draft named after the new name would replace the skill
// just renamed onto it.
func (s *Server) skillDraftHolds(workspaceID string, names ...string) bool {
	holds := func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	if s.skillFlow != nil {
		if sess := s.skillFlow.GetSession(workspaceID); sess != nil && holds(sess.SkillName) {
			return true
		}
	}
	dr, err := s.db.GetSkillDraft(workspaceID)
	return err == nil && dr != nil && holds(dr.SkillName)
}

// apiRenameSkill renames a user skill. Core skills have no row here, so they are a 404.
//
// PUT /api/v1/skills/:id/name {"name": "..."}
func (s *Server) apiRenameSkill(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	skill, err := s.db.GetSkill(c.Param("id"))
	if err != nil || skill.WorkspaceID != u.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "skill not found")
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	if s.vault == nil {
		return jsonErr(c, http.StatusServiceUnavailable, "not_configured", "the knowledge base is not configured")
	}

	plan, err := rename.New(s.db, s.vault, s.homesDir).PrepareSkill(u.ID, skill, req.Name)
	if err != nil {
		return renamePlanError(c, "skill", strings.TrimSpace(req.Name), err)
	}
	if !plan.Unchanged {
		if running := s.firstRunningAgent(plan.ReferencingAgentIDs); running != "" {
			return jsonErr(c, http.StatusConflict, "agent_running",
				fmt.Sprintf("%q uses this skill and is running. Wait for it to finish, then rename.", running))
		}
		if s.agentDesignOpen(u.ID) {
			return jsonErr(c, http.StatusConflict, "design_in_progress", agentDesignOpenMsg)
		}
		if s.skillDraftHolds(u.ID, skill.Name, plan.NewName) {
			return jsonErr(c, http.StatusConflict, "design_in_progress",
				"A skill draft is using this name, and saving it would overwrite the skill. Finish or dismiss the draft, then rename.")
		}
	}

	result, err := plan.Apply()
	if err != nil {
		return renamePlanError(c, "skill", plan.NewName, err)
	}
	if !plan.Unchanged {
		s.audit.Log(u.ID, "rename_skill", "skill:"+skill.ID, skill.Name+" → "+result.Name, c.RealIP())
	}
	return s.renameResponse(c, skill.ID, result)
}
