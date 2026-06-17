package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/prompts"
	"github.com/ilijad1/simple-agents/internal/skillstore"
	"github.com/labstack/echo/v4"
)

type skillsPageData struct {
	*pageData
	Skills []*db.Skill
}

type skillDetailData struct {
	*pageData
	Skill   *db.Skill
	Content string
}

// ── Skills list ───────────────────────────────────────────────────────────────

func (s *Server) showSkills(c echo.Context) error {
	u := c.Get("user").(*db.User)
	skills, _ := s.db.ListSkills(u.ID)
	return c.Render(http.StatusOK, "dashboard/skills.html", &skillsPageData{
		pageData: s.page(c, "Skills"),
		Skills:   skills,
	})
}

func (s *Server) handleCreateSkill(c echo.Context) error {
	u := c.Get("user").(*db.User)

	renderErr := func(msg string) error {
		p := s.page(c, "Skills")
		p.Error = msg
		skills, _ := s.db.ListSkills(u.ID)
		return c.Render(http.StatusBadRequest, "dashboard/skills.html", &skillsPageData{pageData: p, Skills: skills})
	}

	if s.skills == nil {
		return renderErr("Skills store not configured")
	}

	file, fileHeader, fileErr := c.Request().FormFile("skill_zip")
	if fileErr == nil {
		// ── ZIP install path ──────────────────────────────────────────────────
		defer file.Close()
		if fileHeader.Size > 10*1024*1024 {
			return renderErr("Zip file too large (max 10 MB)")
		}
		data, err := io.ReadAll(file)
		if err != nil {
			return renderErr("Failed to read uploaded file: " + err.Error())
		}

		skillMD, rootFolder, err := skillstore.PeekZip(data)
		if err != nil {
			return renderErr(err.Error())
		}

		name, description := skillstore.ParseSkillMeta(skillMD)

		// Fallback: use root folder name as skill name.
		if name == "" && rootFolder != "" {
			name = skillstore.SanitizeName(rootFolder)
		}

		// Ask coder for anything still missing.
		if name == "" || description == "" {
			cn, cd := s.inferSkillMeta(c.Request().Context(), u.ID, skillMD)
			if name == "" {
				name = cn
			}
			if description == "" {
				description = cd
			}
		}

		if name == "" {
			return renderErr("Could not determine skill name from SKILL.md frontmatter or zip folder name")
		}

		skill, err := s.skills.InstallFromZip(u.ID, name, description, data)
		if err != nil {
			return renderErr("Failed to install skill: " + err.Error())
		}
		s.audit.Log(u.ID, "create_skill", "skill:"+skill.Name, skill.Name, c.RealIP())
		return c.Redirect(http.StatusFound, "/dashboard/skills/"+skill.ID)
	}

	// ── SKILL.md paste path ───────────────────────────────────────────────────
	content := strings.TrimSpace(c.FormValue("content"))
	if content == "" {
		return renderErr("SKILL.md content is required")
	}

	name, description := skillstore.ParseSkillMeta(content)

	if name == "" || description == "" {
		cn, cd := s.inferSkillMeta(c.Request().Context(), u.ID, content)
		if name == "" {
			name = cn
		}
		if description == "" {
			description = cd
		}
	}

	if name == "" {
		return renderErr("Could not determine skill name from SKILL.md frontmatter")
	}

	skill, err := s.skills.Create(u.ID, name, description, content)
	if err != nil {
		return renderErr("Failed to create skill: " + err.Error())
	}

	s.audit.Log(u.ID, "create_skill", "skill:"+name, name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/skills/"+skill.ID)
}

// inferSkillMeta calls the coder to extract name and description from SKILL.md content.
// Returns empty strings on failure (caller should handle gracefully).
func (s *Server) inferSkillMeta(ctx context.Context, userID, content string) (name, description string) {
	coder := s.coderForUser(userID)
	if coder == nil {
		return
	}

	prompt := prompts.BuildSkillMetaPrompt(content)

	result, err := coder.Generate(ctx, userID, prompt)
	if err != nil {
		slog.Warn("skillstore: coder inference failed", "err", err)
		return
	}

	// Extract JSON from result (may have surrounding text).
	text := result.Text
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return
	}

	var meta struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &meta); err != nil {
		slog.Warn("skillstore: could not parse coder JSON response", "err", err, "text", text)
		return
	}

	return skillstore.SanitizeName(meta.Name), meta.Description
}

// ── Skill detail ──────────────────────────────────────────────────────────────

func (s *Server) showSkillDetail(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	skill, err := s.db.GetSkill(id)
	if err != nil || skill.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "skill not found")
	}

	var content string
	if s.skills != nil {
		content, _ = s.skills.LoadContent(u.ID, skill.Name)
	}

	return c.Render(http.StatusOK, "dashboard/skill_detail.html", &skillDetailData{
		pageData: s.page(c, "Skill: "+skill.Name),
		Skill:    skill,
		Content:  content,
	})
}

func (s *Server) handleSaveSkill(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	skill, err := s.db.GetSkill(id)
	if err != nil || skill.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "skill not found")
	}

	content := c.FormValue("content")

	// Re-parse description from frontmatter on save.
	_, description := skillstore.ParseSkillMeta(content)

	if s.skills != nil {
		if err := s.skills.SaveContent(u.ID, skill.ID, skill.Name, description, content); err != nil {
			p := s.page(c, "Skill: "+skill.Name)
			p.Error = "Failed to save skill: " + err.Error()
			return c.Render(http.StatusInternalServerError, "dashboard/skill_detail.html", &skillDetailData{
				pageData: p, Skill: skill, Content: content,
			})
		}
	}

	if description != "" {
		skill.Description = description
	}
	p := s.page(c, "Skill: "+skill.Name)
	p.Success = "Skill saved"
	return c.Render(http.StatusOK, "dashboard/skill_detail.html", &skillDetailData{
		pageData: p, Skill: skill, Content: content,
	})
}

func (s *Server) handleDeleteSkill(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	skill, err := s.db.GetSkill(id)
	if err != nil || skill.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "skill not found")
	}

	if s.skills != nil {
		if err := s.skills.Delete(u.ID, skill.ID, skill.Name); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "delete failed: "+err.Error())
		}
	} else {
		_ = s.db.DeleteSkill(id)
	}

	s.audit.Log(u.ID, "delete_skill", "skill:"+id, skill.Name, c.RealIP())
	return c.Redirect(http.StatusFound, "/dashboard/skills")
}

// ── Agent CLAUDE.md and skills ────────────────────────────────────────────────

func (s *Server) handleSaveAgentMD(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	content := c.FormValue("agent_md")
	if err := agentdesigner.CheckEthics(content, ""); err != nil {
		return s.renderAgentDetailWithError(c, agent, u.ID, "AGENT.md failed safety check: "+err.Error())
	}

	dir := s.agentsDir()
	if err := os.WriteFile(agentdesigner.AgentDescPath(dir, u.ID, id), []byte(content), 0o640); err != nil {
		return s.renderAgentDetailWithError(c, agent, u.ID, "Write failed: "+err.Error())
	}

	p := s.page(c, "Agent: "+agent.Name)
	p.Success = "AGENT.md saved"
	return s.renderAgentDetail(c, agent, u.ID, p)
}

func (s *Server) handleSaveAgentSkills(c echo.Context) error {
	u := c.Get("user").(*db.User)
	id := c.Param("id")

	agent, err := s.db.GetAgent(id)
	if err != nil || agent.UserID != u.ID {
		return echo.NewHTTPError(http.StatusNotFound, "agent not found")
	}

	_ = c.Request().ParseForm()
	skillIDs := c.Request().Form["skill_ids"]
	if err := s.db.SetAgentSkills(agent.ID, skillIDs); err != nil {
		return s.renderAgentDetailWithError(c, agent, u.ID, "Failed to save skills: "+err.Error())
	}

	// Update manifest skills list.
	dir := s.agentsDir()
	if manifest, err := agentdesigner.LoadManifest(dir, u.ID, agent.ID); err == nil {
		var names []string
		for _, sid := range skillIDs {
			if sk, err := s.db.GetSkill(sid); err == nil {
				names = append(names, sk.Name)
			}
		}
		manifest.Skills = names
		_ = agentdesigner.SaveManifest(dir, u.ID, agent.ID, manifest)
	}

	p := s.page(c, "Agent: "+agent.Name)
	p.Success = "Skills saved"
	return s.renderAgentDetail(c, agent, u.ID, p)
}

func (s *Server) renderAgentDetailWithError(c echo.Context, agent *db.Agent, userID, msg string) error {
	p := s.page(c, "Agent: "+agent.Name)
	p.Error = msg
	return s.renderAgentDetail(c, agent, userID, p)
}
