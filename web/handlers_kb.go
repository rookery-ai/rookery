package web

import (
	"bufio"
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/vault"
	"github.com/labstack/echo/v4"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// md is the shared markdown renderer. Raw HTML in notes is left escaped (goldmark
// "unsafe" is NOT enabled) so a note can never inject script — the vault is the
// user's own content but defence-in-depth is cheap.
var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

type kbCrumb struct {
	Name string
	Path string
}

type kbBrowseData struct {
	*pageData
	Path    string
	Crumbs  []kbCrumb
	Nodes   []vault.Node
	Query   string
	Results []vault.SearchHit
}

type kbViewData struct {
	*pageData
	Path       string
	NoteTitle  string
	HTML       template.HTML
	Raw        string
	IsMarkdown bool
	Backlinks  []kbCrumb
	ParentPath string
}

type kbEditData struct {
	*pageData
	Path       string
	Content    string
	ParentPath string
}

// showKB renders the file-tree browser for a directory in the user's vault.
func (s *Server) showKB(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "knowledge base not available")
	}
	_ = s.vault.EnsureScaffold(u.ID)

	rel := cleanKBParam(c.QueryParam("path"))
	nodes, err := s.vault.List(u.ID, rel)
	if err != nil {
		return s.renderKBError(c, "Could not open folder: "+err.Error())
	}
	s.enrichKBDisplayNames(u.ID, rel, nodes)
	p := s.page(c, "Knowledge Base")
	return c.Render(http.StatusOK, "dashboard/kb_browse.html", &kbBrowseData{
		pageData: p,
		Path:     rel,
		Crumbs:   kbBreadcrumbs(rel),
		Nodes:    nodes,
	})
}

// enrichKBDisplayNames resolves human-readable names for system-managed vault dirs.
// It mutates nodes in place; failures are silently ignored (raw name is left as-is).
func (s *Server) enrichKBDisplayNames(workspaceID, parentPath string, nodes []vault.Node) {
	top := strings.Trim(parentPath, "/")
	for i := range nodes {
		n := &nodes[i]
		switch top {
		case "agents":
			if n.IsDir {
				if agent, err := s.db.GetAgent(n.Name); err == nil {
					n.DisplayName = agent.Name
				}
			}
		case "memory", "chats", "reminders":
			if !n.IsDir {
				if title := kbReadFirstHeading(s.vault, workspaceID, n.Path); title != "" {
					n.DisplayName = title
				}
			}
		}
	}
}

// kbReadFirstHeading reads the first markdown heading line from a vault note
// and returns its text (stripped of leading "# "), capped at 80 chars.
func kbReadFirstHeading(v *vault.Vault, workspaceID, relPath string) string {
	data, err := v.ReadNote(workspaceID, relPath)
	if err != nil {
		return ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			title := strings.TrimPrefix(line, "# ")
			if len(title) > 80 {
				title = title[:80] + "…"
			}
			return title
		}
	}
	return ""
}

// viewKBNote renders a single note: markdown is converted to HTML with working
// [[wikilinks]]; other files are shown as raw text.
func (s *Server) viewKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	if s.vault == nil {
		return echo.NewHTTPError(http.StatusNotImplemented, "knowledge base not available")
	}
	rel := cleanKBParam(c.QueryParam("path"))
	data, err := s.vault.ReadNote(u.ID, rel)
	if err != nil {
		return s.renderKBError(c, "Could not open note: "+err.Error())
	}
	p := s.page(c, "Knowledge Base")
	vd := &kbViewData{
		pageData:   p,
		Path:       rel,
		NoteTitle:  path.Base(rel),
		Raw:        string(data),
		IsMarkdown: strings.EqualFold(path.Ext(rel), ".md"),
		ParentPath: path.Dir(rel),
	}
	if vd.ParentPath == "." {
		vd.ParentPath = ""
	}
	if vd.IsMarkdown {
		vd.HTML = s.renderMarkdown(u.ID, string(data))
		if back, err := s.vault.Backlinks(u.ID, rel); err == nil {
			for _, b := range back {
				vd.Backlinks = append(vd.Backlinks, kbCrumb{Name: b, Path: b})
			}
		}
	}
	return c.Render(http.StatusOK, "dashboard/kb_view.html", vd)
}

// editKBNote shows a raw-text editor for a note (or a blank one for a new note).
func (s *Server) editKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	rel := cleanKBParam(c.QueryParam("path"))
	var content string
	if rel != "" {
		if data, err := s.vault.ReadNote(u.ID, rel); err == nil {
			content = string(data)
		}
	}
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	return c.Render(http.StatusOK, "dashboard/kb_edit.html", &kbEditData{
		pageData:   s.page(c, "Edit note"),
		Path:       rel,
		Content:    content,
		ParentPath: parent,
	})
}

// handleSaveKBNote writes a note's content. All path handling goes through the
// vault's Resolve, so traversal outside the user's vault is impossible.
func (s *Server) handleSaveKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	rel := cleanKBParam(c.FormValue("path"))
	if rel == "" {
		return s.renderKBError(c, "A note path is required.")
	}
	if !strings.Contains(path.Base(rel), ".") {
		rel += ".md" // default new notes to markdown
	}
	content := normalizeNewlines(c.FormValue("content"))
	if err := s.vault.WriteNote(u.ID, rel, []byte(content)); err != nil {
		return s.renderKBError(c, "Save failed: "+err.Error())
	}
	return c.Redirect(http.StatusFound, "/dashboard/kb/view?path="+url.QueryEscape(rel))
}

// handleNewKBNote creates a new note or folder under a directory.
func (s *Server) handleNewKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	dir := cleanKBParam(c.FormValue("dir"))
	name := strings.TrimSpace(c.FormValue("name"))
	kind := c.FormValue("kind")
	if name == "" {
		return s.redirectKB(c, dir)
	}
	name = strings.ReplaceAll(name, "/", "-") // a name is a single segment
	rel := path.Join(dir, name)

	if kind == "folder" {
		// Represent a folder by seeding a hidden keep note so it persists and is
		// browsable; then return to it.
		if err := s.vault.WriteNote(u.ID, path.Join(rel, ".keep"), []byte("")); err != nil {
			return s.renderKBError(c, "Create folder failed: "+err.Error())
		}
		return s.redirectKB(c, rel)
	}
	if !strings.Contains(name, ".") {
		rel += ".md"
	}
	if err := s.vault.WriteNote(u.ID, rel, []byte("# "+strings.TrimSuffix(name, ".md")+"\n\n")); err != nil {
		return s.renderKBError(c, "Create note failed: "+err.Error())
	}
	return c.Redirect(http.StatusFound, "/dashboard/kb/edit?path="+url.QueryEscape(rel))
}

// handleDeleteKBNote removes a note or folder and returns to its parent.
func (s *Server) handleDeleteKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	rel := cleanKBParam(c.FormValue("path"))
	if err := s.vault.Delete(u.ID, rel); err != nil {
		return s.renderKBError(c, "Delete failed: "+err.Error())
	}
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	return s.redirectKB(c, parent)
}

// handleRenameKBNote moves a note/folder to a new path.
func (s *Server) handleRenameKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	from := cleanKBParam(c.FormValue("from"))
	to := cleanKBParam(c.FormValue("to"))
	if from == "" || to == "" {
		return s.renderKBError(c, "Both source and destination are required.")
	}
	if err := s.vault.Rename(u.ID, from, to); err != nil {
		return s.renderKBError(c, "Rename failed: "+err.Error())
	}
	return c.Redirect(http.StatusFound, "/dashboard/kb/view?path="+url.QueryEscape(to))
}

// searchKB runs a keyword search over the user's vault.
func (s *Server) searchKB(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	q := strings.TrimSpace(c.QueryParam("q"))
	p := s.page(c, "Search knowledge base")
	bd := &kbBrowseData{pageData: p, Query: q, Crumbs: kbBreadcrumbs("")}
	if q != "" {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()
		hits, err := s.vault.NewSearcher().Search(ctx, u.ID, q)
		if err != nil {
			p.Error = "Search failed: " + err.Error()
		}
		bd.Results = hits
	}
	return c.Render(http.StatusOK, "dashboard/kb_browse.html", bd)
}

// rawKBNote serves a note's bytes as plain text (for download / external tools).
func (s *Server) rawKBNote(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	rel := cleanKBParam(c.QueryParam("path"))
	data, err := s.vault.ReadNote(u.ID, rel)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "note not found")
	}
	return c.Blob(http.StatusOK, "text/plain; charset=utf-8", data)
}

// renderMarkdown rewrites [[wikilinks]] to KB viewer links, then converts the
// markdown to sanitised HTML.
func (s *Server) renderMarkdown(workspaceID, content string) template.HTML {
	if idx, err := s.vault.BuildLinkIndex(workspaceID); err == nil {
		content = idx.RenderHTMLLinks(content, func(rel string) string {
			return "/dashboard/kb/view?path=" + url.QueryEscape(rel)
		})
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return template.HTML("<pre>" + template.HTMLEscapeString(content) + "</pre>")
	}
	return template.HTML(buf.String())
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (s *Server) redirectKB(c echo.Context, dir string) error {
	if dir == "" {
		return c.Redirect(http.StatusFound, "/dashboard/kb")
	}
	return c.Redirect(http.StatusFound, "/dashboard/kb?path="+url.QueryEscape(dir))
}

func (s *Server) renderKBError(c echo.Context, msg string) error {
	u := c.Get("workspace").(*db.Workspace)
	_ = s.vault.EnsureScaffold(u.ID)
	nodes, _ := s.vault.List(u.ID, "")
	p := s.page(c, "Knowledge Base")
	p.Error = msg
	return c.Render(http.StatusOK, "dashboard/kb_browse.html", &kbBrowseData{
		pageData: p, Path: "", Crumbs: kbBreadcrumbs(""), Nodes: nodes,
	})
}

// cleanKBParam normalises a user-supplied path parameter to a vault-relative slash
// path. Final containment is still enforced by vault.Resolve; this just tidies it.
func cleanKBParam(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "/")
	if p == "." {
		return ""
	}
	return p
}

func kbBreadcrumbs(rel string) []kbCrumb {
	crumbs := []kbCrumb{{Name: "🏠 Vault", Path: ""}}
	if rel == "" {
		return crumbs
	}
	var acc string
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" {
			continue
		}
		if acc == "" {
			acc = seg
		} else {
			acc = acc + "/" + seg
		}
		crumbs = append(crumbs, kbCrumb{Name: seg, Path: acc})
	}
	return crumbs
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
