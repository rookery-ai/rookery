package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/vault"
	"github.com/labstack/echo/v4"
)

// registerKBAPI registers the JSON knowledge-base endpoints on the given group
// (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI +
// requireSetupCompleteAPI). Every handler here is a JSON port of the
// corresponding template handler in web/handlers_kb.go and reuses the exact
// same vault/helper calls — all path safety comes from vault.Resolve inside
// those calls, never re-implemented here.
//
// GET /api/v1/kb/raw re-registers the EXISTING s.rawKBNote handler UNCHANGED
// (it already serves a raw file download; no JSON envelope to apply).
func (s *Server) registerKBAPI(g *echo.Group) {
	g.GET("/kb/tree", s.apiKBTree)
	g.GET("/kb/note", s.apiGetKBNote)
	g.PUT("/kb/note", s.apiSaveKBNote)
	g.POST("/kb/new", s.apiNewKBNote)
	g.DELETE("/kb/note", s.apiDeleteKBNoteAPI)
	g.POST("/kb/rename", s.apiRenameKBNote)
	g.GET("/kb/search", s.apiSearchKB)
	g.GET("/kb/resolve", s.apiResolveKBLink)
	g.GET("/kb/raw", s.rawKBNote)
}

// kbSystemDirs are the top-level vault directories that are system-managed
// (reflected from the DB or otherwise not user-authored knowledge), mirroring
// internal/vault's kbManifestExcluded set (minus .kb, which vault.List never
// surfaces at all). The template browser has no explicit "system" flag of its
// own — it just relies on enrichKBDisplayNames to give these dirs friendlier
// labels — so this is this endpoint's own derivation: a root-level node whose
// name is one of these is marked system:true.
var kbSystemDirs = map[string]bool{
	"agents": true, "chats": true, "memory": true,
	"skills": true, "reminders": true, "inbox": true,
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

type apiKBNode struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	System      bool   `json:"system"`
}

type apiKBTreeResponse struct {
	Path  string      `json:"path"`
	Nodes []apiKBNode `json:"nodes"`
}

type apiKBNoteResponse struct {
	Path      string   `json:"path"`
	Content   string   `json:"content"`
	HTML      string   `json:"html"`
	Backlinks []string `json:"backlinks"`
}

type apiSaveKBNoteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type apiNewKBNoteRequest struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type apiRenameKBNoteRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type apiKBSearchHit struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// ── Handlers ─────────────────────────────────────────────────────────────────

// vaultErrStatus maps a vault error to an API status+code. vault.Resolve
// (called internally by every vault method) returns vault.ErrEscapes for any
// path-traversal/absolute-path attempt, which we surface as 400 invalid_path;
// a missing file/dir (os.ErrNotExist, e.g. reading a note that was never
// created) is 404 not_found; anything else is a generic 500.
func vaultErrStatus(err error) (int, string) {
	if errors.Is(err, vault.ErrEscapes) {
		return http.StatusBadRequest, "invalid_path"
	}
	if errors.Is(err, os.ErrNotExist) {
		return http.StatusNotFound, "not_found"
	}
	return http.StatusInternalServerError, "internal"
}

func (s *Server) kbUnavailable(c echo.Context) error {
	return jsonErr(c, http.StatusNotImplemented, "kb_unavailable", "knowledge base not available")
}

// apiKBTree ports showKB + enrichKBDisplayNames.
// GET /api/v1/kb/tree?path= → 200 {"path","nodes":[{name,display_name,path,is_dir,system}]}
func (s *Server) apiKBTree(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	_ = s.vault.EnsureScaffold(u.ID)

	rel := cleanKBParam(c.QueryParam("path"))
	nodes, err := s.vault.List(u.ID, rel)
	if err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not open folder: "+err.Error())
	}
	s.enrichKBDisplayNames(u.ID, rel, nodes)

	isRoot := rel == ""
	out := make([]apiKBNode, 0, len(nodes))
	for _, n := range nodes {
		display := n.DisplayName
		if display == "" {
			display = n.Name
		}
		out = append(out, apiKBNode{
			Name:        n.Name,
			DisplayName: display,
			Path:        n.Path,
			IsDir:       n.IsDir,
			System:      isRoot && n.IsDir && kbSystemDirs[n.Name],
		})
	}
	return c.JSON(http.StatusOK, apiKBTreeResponse{Path: rel, Nodes: out})
}

// apiGetKBNote ports viewKBNote (+editKBNote's raw-content read for non-markdown
// files). Markdown notes get rendered HTML + backlinks (via s.vault.Backlinks,
// same call viewKBNote makes); non-markdown files return content only, HTML
// empty and backlinks an empty array.
// GET /api/v1/kb/note?path= → 200 {"path","content","html","backlinks":[...]}
func (s *Server) apiGetKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	rel := cleanKBParam(c.QueryParam("path"))
	data, err := s.vault.ReadNote(u.ID, rel)
	if err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "could not open note: "+err.Error())
	}

	resp := apiKBNoteResponse{
		Path:      rel,
		Content:   string(data),
		Backlinks: []string{},
	}
	if strings.EqualFold(path.Ext(rel), ".md") {
		resp.HTML = string(s.renderMarkdown(u.ID, resp.Content))
		if back, err := s.vault.Backlinks(u.ID, rel); err == nil {
			resp.Backlinks = back
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// apiSaveKBNote ports handleSaveKBNote.
// PUT /api/v1/kb/note {path,content} → 200 {"ok":true}
func (s *Server) apiSaveKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiSaveKBNoteRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	rel := cleanKBParam(req.Path)
	if rel == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "a note path is required")
	}
	if !strings.Contains(path.Base(rel), ".") {
		rel += ".md" // default new notes to markdown, matching handleSaveKBNote
	}
	content := normalizeNewlines(req.Content)
	if err := s.vault.WriteNote(u.ID, rel, []byte(content)); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "save failed: "+err.Error())
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiNewKBNote ports handleNewKBNote. The template form takes dir+name+kind
// fields; the JSON shape here is a single {path,is_dir} — path is split into
// dir=path.Dir(path)/name=path.Base(path) to drive the exact same vault calls.
// POST /api/v1/kb/new {path,is_dir} → 201 {"ok":true}
func (s *Server) apiNewKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiNewKBNoteRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	rel := cleanKBParam(req.Path)
	if rel == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "a path is required")
	}

	if req.IsDir {
		// Represent a folder by seeding a hidden keep note so it persists and is
		// browsable, matching handleNewKBNote's kind=="folder" branch.
		if err := s.vault.WriteNote(u.ID, path.Join(rel, ".keep"), []byte("")); err != nil {
			status, code := vaultErrStatus(err)
			return jsonErr(c, status, code, "create folder failed: "+err.Error())
		}
		return c.JSON(http.StatusCreated, apiOKResponse{OK: true})
	}

	name := path.Base(rel)
	if !strings.Contains(name, ".") {
		rel += ".md"
	}
	if err := s.vault.WriteNote(u.ID, rel, []byte("# "+strings.TrimSuffix(name, ".md")+"\n\n")); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "create note failed: "+err.Error())
	}
	return c.JSON(http.StatusCreated, apiOKResponse{OK: true})
}

// apiDeleteKBNoteAPI ports handleDeleteKBNote.
// DELETE /api/v1/kb/note?path= → 200 {"ok":true}
func (s *Server) apiDeleteKBNoteAPI(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	rel := cleanKBParam(c.QueryParam("path"))
	if err := s.vault.Delete(u.ID, rel); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "delete failed: "+err.Error())
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiRenameKBNote ports handleRenameKBNote.
// POST /api/v1/kb/rename {from,to} → 200 {"ok":true}
func (s *Server) apiRenameKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	var req apiRenameKBNoteRequest
	if err := bindAPI(c, &req); err != nil {
		return err
	}
	from := cleanKBParam(req.From)
	to := cleanKBParam(req.To)
	if from == "" || to == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_path", "both source and destination are required")
	}
	if err := s.vault.Rename(u.ID, from, to); err != nil {
		status, code := vaultErrStatus(err)
		return jsonErr(c, status, code, "rename failed: "+err.Error())
	}
	return c.JSON(http.StatusOK, apiOKResponse{OK: true})
}

// apiResolveKBLink resolves a [[wikilink]] target to the note it points at,
// reusing the exact same LinkIndex the vault builds for backlinks/rendering
// (internal/vault/links.go) — no resolution logic is duplicated here.
// GET /api/v1/kb/resolve?link=<target> → 200 {"path":"notes/target.md"} or 404 not_found.
func (s *Server) apiResolveKBLink(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	link := strings.TrimSpace(c.QueryParam("link"))
	if link == "" {
		return jsonErr(c, http.StatusBadRequest, "invalid_link", "link is required")
	}
	idx, err := s.vault.BuildLinkIndex(u.ID)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "could not build link index: "+err.Error())
	}
	rel := idx.Resolve(link)
	if rel == "" {
		return jsonErr(c, http.StatusNotFound, "not_found", "no note matches ["+link+"]")
	}
	return c.JSON(http.StatusOK, map[string]string{"path": rel})
}

// apiSearchKB ports searchKB.
// GET /api/v1/kb/search?q= → 200 {"hits":[{path,line,snippet}]}
func (s *Server) apiSearchKB(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return s.kbUnavailable(c)
	}
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return jsonErr(c, http.StatusBadRequest, "empty_query", "q is required")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()
	hits, err := s.vault.NewSearcher().Search(ctx, u.ID, q)
	if err != nil {
		return jsonErr(c, http.StatusInternalServerError, "internal", "search failed: "+err.Error())
	}
	out := make([]apiKBSearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, apiKBSearchHit{Path: h.Path, Line: h.Line, Snippet: h.Snippet})
	}
	return c.JSON(http.StatusOK, map[string]any{"hits": out})
}
