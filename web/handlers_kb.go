package web

import (
	"bufio"
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/vault"
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
//
// Directories are resolved here rather than in kbDisplayTitle because a dir has
// no content to read a heading from — only agents/<id> has a DB row to name it.
// Files delegate to kbDisplayTitle so the tree and global search cannot drift
// apart on what a given path is called.
// kbSystemFolderLabels gives the vault's own top-level directories a
// human-readable label. They are created by the platform, not the user, so they
// render lowercase and indistinguishable from a folder the user made — "notes"
// beside "Project Plans".
//
// Presentation only: DisplayName never feeds navigation. Name and Path keep the
// real on-disk directory, so Resolve, rename guards and isProtectedPath are
// untouched by this.
var kbSystemFolderLabels = map[string]string{
	"notes":   "Notes",
	"memory":  "Memory",
	"skills":  "Skills",
	"agents":  "Agents",
	"chats":   "Chats",
	"uploads": "Uploads",
}

func (s *Server) enrichKBDisplayNames(workspaceID, parentPath string, nodes []vault.Node) {
	top := strings.Trim(parentPath, "/")
	for i := range nodes {
		n := &nodes[i]
		if n.IsDir {
			if top == "agents" {
				if agent, err := s.db.GetAgent(n.Name); err == nil {
					n.DisplayName = agent.Name
				}
			}
			// Top level only. A user folder deeper in the tree that happens to be
			// called "notes" is theirs, and renaming it in the UI would be a lie.
			if top == "" {
				if label, ok := kbSystemFolderLabels[n.Name]; ok {
					n.DisplayName = label
				}
			}
			continue
		}
		// Files inside an agent's own directory keep their REAL filename in the tree.
		// kbDisplayTitle qualifies them as "<Agent> — <stem>", which is right for a
		// search hit (it arrives with no folder context to say which agent it belongs
		// to) and wrong here: the tree already shows the agent name on the parent
		// folder, so the prefix is pure noise. Worse, the stem strips the extension —
		// AGENT.md rendered as "Digest — AGENT", state.md as "Digest — state", and
		// tools/fetch.py as "Digest — fetch". For a user's own note the filename IS
		// the title so dropping ".md" reads naturally, but these are real files whose
		// extension is part of their identity, and the browser was showing a name that
		// matched nothing on disk.
		if strings.HasPrefix(strings.Trim(n.Path, "/"), "agents/") {
			continue // empty DisplayName → the API falls back to n.Name
		}
		if title := s.kbDisplayTitle(workspaceID, n.Path); title != "" && title != kbPathStem(n.Path) {
			n.DisplayName = title
		}
	}
}

// kbDisplayTitle resolves a human-readable title for one vault FILE path.
//
// It is keyed on the FULL path, unlike enrichKBDisplayNames which switches on the
// immediate parent directory. That difference is the point: reflected notes are
// named after a UUID (`chats/<id>.md`, `inbox/<id>.md`,
// `agents/<id>/logs/run_<ts>.md`), so global search was showing raw UUIDs as
// result titles. A parent-dir switch cannot reach the agent-log case at all — its
// parent is `logs`, two levels below the `agents` dir that identifies it.
//
// Returns a best-effort title, falling back to the filename stem, so callers can
// use the result unconditionally.
func (s *Server) kbDisplayTitle(workspaceID, path string) string {
	rel := strings.Trim(path, "/")
	if rel == "" {
		return ""
	}
	parts := strings.Split(rel, "/")

	// agents/<agentID>/... — name the agent from the DB, since nothing in the
	// file itself identifies it. Run logs additionally carry their timestamp,
	// which is what distinguishes one run from the next.
	if parts[0] == "agents" && len(parts) >= 3 {
		name := parts[1]
		if agent, err := s.db.GetAgent(parts[1]); err == nil && agent.Name != "" {
			name = agent.Name
		}
		leaf := kbPathStem(rel)
		if parts[len(parts)-2] == "logs" {
			if stamp := strings.TrimPrefix(leaf, "run_"); stamp != leaf {
				return name + " — run " + stamp
			}
		}
		return name + " — " + leaf
	}

	// Reflected/system notes are UUID-named but carry a real "# " heading.
	switch parts[0] {
	case "chats", "memory":
		if title := kbReadFirstHeading(s.vault, workspaceID, rel); title != "" {
			return title
		}
	}
	return kbPathStem(rel)
}

// kbPathStem returns a path's filename without its extension — the plain-English
// name of a user-authored note, whose filename IS its title.
func kbPathStem(path string) string {
	name := path
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	return name
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
	// Serve a sniffed content type so an embedded <img src="/kb/raw?path=…">
	// actually renders (an image asset), rather than the flat text/plain this
	// used to always send. Falls back to text/plain for a textual/unknown file
	// so existing raw-.md downloads are unchanged.
	ct := detectContentType(rel, data)
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	if isImagePath(rel) {
		c.Response().Header().Set("Cache-Control", "private, max-age=3600")
	}
	return c.Blob(http.StatusOK, ct, data)
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
