package web

import (
	"net/http"
	"strings"

	"github.com/ilijad1/rookery/internal/db"
	"github.com/labstack/echo/v4"
)

type searchItem struct {
	Title   string `json:"title"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	URL     string `json:"url,omitempty"`
}
type searchGroup struct {
	Kind  string       `json:"kind"`
	Items []searchItem `json:"items"`
}

const searchGroupLimit = 5

func (s *Server) apiGlobalSearch(c echo.Context) error {
	u := c.Get("workspace").(*db.Workspace)
	q := strings.TrimSpace(c.QueryParam("q"))
	if q == "" {
		return jsonErr(c, http.StatusBadRequest, "empty_query", "q is required")
	}
	lq := strings.ToLower(q)
	match := func(fields ...string) bool {
		for _, f := range fields {
			if strings.Contains(strings.ToLower(f), lq) {
				return true
			}
		}
		return false
	}
	var groups []searchGroup
	add := func(kind string, items []searchItem) {
		if len(items) > searchGroupLimit {
			items = items[:searchGroupLimit]
		}
		if len(items) > 0 {
			groups = append(groups, searchGroup{Kind: kind, Items: items})
		}
	}

	// Notes: full-text via the vault searcher (ripgrep or Go fallback).
	//
	// Title is RESOLVED, not the raw path: the searcher returns every matching
	// vault file, including reflected notes whose filename is a UUID
	// (chats/<id>.md, inbox/<id>.md, agents/<id>/logs/run_<ts>.md). Showing the
	// path alone left those results reading as bare UUIDs. The full path still
	// travels in Path for the client to render underneath.
	if hits, err := s.vault.NewSearcher().Search(c.Request().Context(), u.ID, q); err == nil {
		var items []searchItem
		for _, h := range hits {
			title := s.kbDisplayTitle(u.ID, h.Path)
			if title == "" {
				title = h.Path
			}
			items = append(items, searchItem{Title: title, Path: h.Path, Line: h.Line,
				Snippet: h.Snippet, URL: "/kb?path=" + h.Path})
		}
		add("notes", items)
	}

	agents, _ := s.db.ListAgents(u.ID)
	var items []searchItem
	for _, a := range agents {
		if match(a.Name, a.Description) {
			items = append(items, searchItem{Title: a.Name, ID: a.ID, URL: "/agents/" + a.ID})
		}
	}
	add("agents", items)

	chats, _ := s.db.ListChats(u.ID)
	items = nil
	for _, ch := range chats {
		if match(ch.Name) {
			items = append(items, searchItem{Title: ch.Name, ID: ch.ID, URL: "/chats/" + ch.ID})
		}
	}
	add("chats", items)

	skills, _ := s.db.ListSkills(u.ID)
	items = nil
	for _, sk := range skills {
		if match(sk.Name, sk.Description) {
			items = append(items, searchItem{Title: sk.Name, ID: sk.ID, URL: "/skills/" + sk.ID})
		}
	}
	add("skills", items)

	conns, _ := s.db.ListServiceConnections(c.Request().Context(), u.ID)
	items = nil
	for _, cn := range conns {
		if match(cn.Provider, cn.AccountLabel, cn.AccountIdentity) {
			items = append(items, searchItem{Title: cn.Provider + " · " + cn.AccountLabel, ID: cn.ID, URL: "/connections"})
		}
	}
	add("connections", items)

	names, _ := s.db.ListSecretNames(u.ID)
	items = nil
	for _, n := range names {
		if match(n) {
			items = append(items, searchItem{Title: n, URL: "/secrets"})
		}
	}
	add("secrets", items)

	rems, _ := s.db.ListReminders(u.ID)
	items = nil
	for _, r := range rems {
		if match(r.Message) {
			items = append(items, searchItem{Title: r.Message, ID: r.ID, URL: "/"})
		}
	}
	add("reminders", items)

	if groups == nil {
		groups = []searchGroup{}
	}
	return c.JSON(http.StatusOK, map[string]any{"query": q, "groups": groups})
}

func (s *Server) registerSearchAPI(g *echo.Group) {
	g.GET("/search", s.apiGlobalSearch)
}
