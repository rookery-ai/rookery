package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/skilldesigner"
	"github.com/ilijad1/rookery/internal/skilllibrary"
	"github.com/ilijad1/rookery/internal/skillstore"
	"github.com/labstack/echo/v4"
)

// registerSkillsAPI registers the JSON CRUD endpoints plus the skill-designer
// family (re-registered UNCHANGED — those keep their legacy {"error":"string"}
// shapes per the API plan's global constraints) on the given group (already
// guarded by requireOwnerAPI + requireActiveWorkspaceAPI + requireSetupCompleteAPI).
func (s *Server) registerSkillsAPI(g *echo.Group) {
	// Design/SSE family: unchanged legacy handlers, unchanged legacy error shapes.
	// Registered before the param routes below for readability (Echo's radix
	// router already prioritizes static segments over params regardless of
	// registration order, but keeping this clean avoids surprises).
	g.POST("/skills/design", s.handleSkillDesignChat)
	g.POST("/skills/design/cancel", s.handleCancelSkillDesign)
	g.POST("/skills/design/resume", s.handleResumeSkillDraft)
	g.POST("/skills/design/dismiss", s.handleDismissSkillDraft)
	g.GET("/skills/design/progress", s.handleSkillDesignProgress)

	g.GET("/skills", s.apiListSkills)
	g.POST("/skills", s.apiCreateSkill)
	g.GET("/skills/core/:slug", s.apiGetCoreSkill)
	g.GET("/skills/:id", s.apiGetSkill)
	g.PUT("/skills/:id", s.apiSaveSkill)
	g.DELETE("/skills/:id", s.apiDeleteSkill)
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

// apiSkillMeta is the frontmatter metadata surfaced for BOTH core and user
// skills. It exists so the two kinds are described by the same fields parsed by
// the same parser: before this, only the core path parsed metadata at all, and
// then dropped it at the DTO boundary — so a built-in skill and a created one
// could not be shown side by side.
type apiSkillMeta struct {
	Category string   `json:"category"`
	Version  string   `json:"version"`
	Requires []string `json:"requires"`
}

// defaultSkillCategory is what an unset or unrecognised category renders as.
// The UI shows a category chip unconditionally, and an empty one reads as a bug.
const defaultSkillCategory = "Other"

// flattenRequires renders a skill's tool requirements as display strings, with
// the KIND encoded in the string rather than in the shape.
//
// The nesting ParseMeta returns (bins / anyBins / env) is a Go detail; making
// the SPA branch across three lists to render one line would put that detail in
// two languages. "a or b" and "$KEY" carry the same information in a form the
// UI can print directly.
func flattenRequires(m skilllibrary.SkillMeta) []string {
	// Initialised, not declared: a nil slice marshals to JSON null, and the
	// SPA's default-parameter fallback (`requires = []`) fires only on
	// undefined — never on null. A skill declaring no tooling therefore
	// crashed its own detail page on `requires.length`. Every empty slice
	// crossing this boundary must serialise as [].
	out := []string{}
	out = append(out, m.RequiresBins...)
	if len(m.AnyBins) > 0 {
		out = append(out, strings.Join(m.AnyBins, " or "))
	}
	for _, e := range m.RequiresEnv {
		out = append(out, "$"+e)
	}
	return out
}

// skillMetaDTO maps parsed frontmatter into the wire shape. Version stays empty
// when unset (the UI omits that chip), but Category always resolves.
func skillMetaDTO(m skilllibrary.SkillMeta) apiSkillMeta {
	cat := m.Category
	if cat == "" {
		cat = defaultSkillCategory
	}
	return apiSkillMeta{Category: cat, Version: m.Version, Requires: flattenRequires(m)}
}

// metaForContent parses a SKILL.md, degrading to the default category when the
// content is unavailable or its frontmatter is malformed. A skill with broken
// frontmatter must stay listable and viewable — that is exactly when the owner
// needs to open it.
func metaForContent(content string) apiSkillMeta {
	m, _ := skilllibrary.ParseMeta(content)
	return skillMetaDTO(m)
}

type apiSkillListItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	apiSkillMeta
}

type apiCoreSkillListItem struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	apiSkillMeta
}

// toAPISkillDraft maps an in-progress skill-creator draft into the JSON DTO.
func toAPISkillDraft(d *db.SkillDraft) map[string]any {
	if d == nil {
		return nil
	}
	return map[string]any{
		"skill_name": d.SkillName,
		"state":      d.State,
		"updated_at": d.UpdatedAt,
		"expires_at": d.ExpiresAt,
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// apiListSkills ports showSkills (web/handlers_skills.go). Works DB-only when
// s.skillFlow is nil (draft is simply omitted).
func (s *Server) apiListSkills(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	skills, err := s.db.ListSkills(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", err.Error())
	}
	out := make([]apiSkillListItem, 0, len(skills))
	for _, sk := range skills {
		item := apiSkillListItem{
			ID: sk.ID, Name: sk.Name, Description: sk.Description, CreatedAt: sk.InstalledAt,
			// Degrades cleanly when the skill store is not configured: the list
			// must never fail over missing metadata.
			apiSkillMeta: apiSkillMeta{Category: defaultSkillCategory},
		}
		if s.skills != nil {
			if content, err := s.skills.LoadContent(u.ID, sk.Name); err == nil {
				item.apiSkillMeta = metaForContent(content)
			}
		}
		out = append(out, item)
	}

	core := make([]apiCoreSkillListItem, 0)
	for _, m := range skilllibrary.LoadBundled() {
		core = append(core, apiCoreSkillListItem{
			Slug: m.Name, Name: m.Name, Description: m.Description,
			apiSkillMeta: skillMetaDTO(m),
		})
	}

	var draft *db.SkillDraft
	if s.skillFlow != nil {
		draft = s.skillFlow.HasDraft(u.ID)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"skills":      out,
		"core_skills": core,
		"draft":       toAPISkillDraft(draft),
	})
}

// apiGetCoreSkill ports showCoreSkill. Core skills are embedded (no DB row),
// so an unknown slug is a 404 regardless of any store configuration.
func (s *Server) apiGetCoreSkill(c echo.Context) error {
	slug := c.Param("slug")
	content, ok := skilllibrary.CoreSkillContent(slug)
	if !ok || !skilllibrary.IsCoreSkill(slug) {
		return jsonErr(c, http.StatusNotFound, "not_found", "core skill not found")
	}
	m, _ := skilllibrary.ParseMeta(content)
	meta := skillMetaDTO(m)
	return c.JSON(http.StatusOK, map[string]any{
		"slug":        slug,
		"content":     content,
		"description": m.Description,
		"category":    meta.Category,
		"version":     meta.Version,
		"requires":    meta.Requires,
	})
}

// apiGetSkill ports showSkillDetail. Content degrades to "" when s.skills is
// nil (matches the template's behavior — the store isn't strictly required to
// look up the skill's DB row and description).
func (s *Server) apiGetSkill(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	skill, err := s.db.GetSkill(id)
	if err != nil || skill.WorkspaceID != u.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "skill not found")
	}

	var content string
	if s.skills != nil {
		content, _ = s.skills.LoadContent(u.ID, skill.Name)
	}

	meta := metaForContent(content)
	return c.JSON(http.StatusOK, map[string]any{
		"id":          skill.ID,
		"name":        skill.Name,
		"description": skill.Description,
		"content":     content,
		"category":    meta.Category,
		"version":     meta.Version,
		"requires":    meta.Requires,
	})
}

// apiSaveSkill ports handleSaveSkill. Unlike the template (which silently
// no-ops the write when s.skills is nil), the API is explicit: a save that
// can't actually persist returns 503 not_configured rather than a false 200.
func (s *Server) apiSaveSkill(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	skill, err := s.db.GetSkill(id)
	if err != nil || skill.WorkspaceID != u.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "skill not found")
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}

	if s.skills == nil {
		return jsonErr(c, http.StatusServiceUnavailable, "not_configured", "skills store is not configured")
	}

	// Re-parse description from frontmatter on save.
	_, description := skillstore.ParseSkillMeta(req.Content)
	if err := s.skills.SaveContent(u.ID, skill.ID, skill.Name, description, req.Content); err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to save skill: "+err.Error())
	}
	if description != "" {
		skill.Description = description
	}

	return c.JSON(http.StatusOK, map[string]any{
		"id":          skill.ID,
		"name":        skill.Name,
		"description": skill.Description,
		"content":     req.Content,
	})
}

// apiDeleteSkill ports handleDeleteSkill, including the dangling agent_skills
// cleanup. When a store is configured, Store.Delete already performs that
// cleanup internally; the nil-store fallback path replicates it explicitly so
// the behavior doesn't depend on store availability.
func (s *Server) apiDeleteSkill(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	id := c.Param("id")

	skill, err := s.db.GetSkill(id)
	if err != nil || skill.WorkspaceID != u.ID {
		return jsonErr(c, http.StatusNotFound, "not_found", "skill not found")
	}

	if s.skills != nil {
		if err := s.skills.Delete(u.ID, skill.ID, skill.Name); err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", "delete failed: "+err.Error())
		}
	} else {
		_ = s.db.DeleteSkill(id)
		_ = s.db.DeleteAgentSkillsByName(u.ID, skill.Name)
	}

	s.audit.Log(u.ID, "delete_skill", "skill:"+id, skill.Name, c.RealIP())
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

// apiCreateSkill ports handleCreateSkill: accepts either multipart/form-data
// (ZIP upload via "skill_zip", or pasted content via the "content" field — the
// same field names the template form uses) or a JSON body {"content": "..."}.
func (s *Server) apiCreateSkill(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)

	if s.skills == nil {
		return jsonErr(c, http.StatusServiceUnavailable, "not_configured", "skills store is not configured")
	}

	ct := c.Request().Header.Get(echo.HeaderContentType)
	if strings.HasPrefix(ct, echo.MIMEMultipartForm) {
		return s.apiCreateSkillMultipart(c, u)
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "SKILL.md content is required")
	}
	return s.apiCreateSkillFromContent(c, u, content)
}

// apiCreateSkillMultipart handles the ZIP-upload and paste-form field paths
// under multipart/form-data — the same field names (skill_zip, content) the
// dashboard's import form posts.
func (s *Server) apiCreateSkillMultipart(c echo.Context, u *db.Workspace) error {
	file, fileHeader, fileErr := c.Request().FormFile("skill_zip")
	if fileErr == nil {
		defer file.Close()
		if fileHeader.Size > 10*1024*1024 {
			return jsonErr(c, http.StatusBadRequest, "invalid_request", "zip file too large (max 10 MB)")
		}
		data, err := io.ReadAll(file)
		if err != nil {
			return jsonErr(c, http.StatusBadRequest, "invalid_request", "failed to read uploaded file: "+err.Error())
		}

		skillMD, rootFolder, err := skillstore.PeekZip(data)
		if err != nil {
			return jsonErr(c, http.StatusBadRequest, "invalid_request", err.Error())
		}

		name, description := skillstore.ParseSkillMeta(skillMD)
		if name == "" && rootFolder != "" {
			name = skillstore.SanitizeName(rootFolder)
		}
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
			return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not determine skill name from SKILL.md frontmatter or zip folder name")
		}
		if skilllibrary.IsCoreSkill(name) {
			return jsonErr(c, http.StatusBadRequest, "reserved_name", fmt.Sprintf("%q is a core skill name — pick another", name))
		}

		skill, err := s.skills.InstallFromZip(u.ID, name, description, data)
		if err != nil {
			return jsonErr(c, http.StatusInternalServerError, "internal", "failed to install skill: "+err.Error())
		}
		s.audit.Log(u.ID, "create_skill", "skill:"+skill.Name, skill.Name, c.RealIP())
		content, _ := s.skills.LoadContent(u.ID, skill.Name)
		return c.JSON(http.StatusCreated, map[string]any{
			"id": skill.ID, "name": skill.Name, "description": skill.Description, "content": content,
		})
	}

	content := strings.TrimSpace(c.FormValue("content"))
	if content == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "SKILL.md content is required")
	}
	return s.apiCreateSkillFromContent(c, u, content)
}

// apiCreateSkillFromContent handles the pasted-SKILL.md path shared by both
// the JSON and multipart entry points.
func (s *Server) apiCreateSkillFromContent(c echo.Context, u *db.Workspace, content string) error {
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
		return jsonErr(c, http.StatusBadRequest, "invalid_request", "could not determine skill name from SKILL.md frontmatter")
	}
	if skilllibrary.IsCoreSkill(name) {
		return jsonErr(c, http.StatusBadRequest, "reserved_name", fmt.Sprintf("%q is a core skill name — pick another", name))
	}

	// An imported SKILL.md has the same frontmatter gap a generated one can:
	// only name + description are reliably present. Normalize so an imported
	// skill is described exactly like a built-in one in the list and the viewer.
	content = skilldesigner.NormalizeFrontmatter(content, name)

	skill, err := s.skills.Create(u.ID, name, description, content)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "failed to create skill: "+err.Error())
	}
	s.audit.Log(u.ID, "create_skill", "skill:"+skill.Name, skill.Name, c.RealIP())
	return c.JSON(http.StatusCreated, map[string]any{
		"id": skill.ID, "name": skill.Name, "description": skill.Description, "content": content,
	})
}
