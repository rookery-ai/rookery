package web

import (
	"bufio"
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strings"

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

// enrichKBDisplayNames resolves human-readable names for system-managed vault dirs.
// It mutates nodes in place; failures are silently ignored (raw name is left as-is).
// Shared by the JSON API's KB tree endpoint.
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
		case "memory", "chats", "reminders", "inbox":
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

// rawKBNote serves a note's bytes as plain text (for download / external tools).
// Shared by the JSON API.
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
// markdown to sanitised HTML. Shared by the JSON API's note-read endpoint.
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

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
