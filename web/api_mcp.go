package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/mcp"
	"github.com/rookery-ai/rookery/internal/secrets"
)

// registerMCPAPI registers the MCP server endpoints on the workspace-scoped group
// (already guarded by requireOwnerAPI + requireActiveWorkspaceAPI +
// requireSetupCompleteAPI). MCP servers are per-workspace, exactly like service
// connections.
func (s *Server) registerMCPAPI(g *echo.Group) {
	g.GET("/mcp/servers", s.apiListMCPServers)
	g.POST("/mcp/servers", s.apiCreateMCPServer)
	g.GET("/mcp/servers/:id", s.apiGetMCPServer)
	g.PUT("/mcp/servers/:id", s.apiUpdateMCPServer)
	g.DELETE("/mcp/servers/:id", s.apiDeleteMCPServer)
	g.POST("/mcp/servers/:id/test", s.apiTestMCPServer)
	g.POST("/mcp/servers/:id/sync", s.apiSyncMCPServer)
	g.GET("/mcp/servers/:id/tools", s.apiListMCPServerTools)
	g.PUT("/mcp/servers/:id/tools/:toolID", s.apiUpdateMCPTool)

	// Per-agent binding, mirroring /agents/:id/connections. A build sees every
	// enabled server; a run sees only what the agent is bound to.
	g.GET("/agents/:id/mcp", s.apiGetAgentMCPServers)
	g.PUT("/agents/:id/mcp", s.apiSaveAgentMCPServers)
}

func (s *Server) apiGetAgentMCPServers(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	all, err := s.db.ListMCPServers(w.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	boundIDs, err := s.db.ListAgentMCPServerIDs(ctx, c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	servers := []apiMCPServer{}
	for _, m := range all {
		tools, _ := s.db.ListMCPTools(ctx, m.ID)
		active := 0
		for _, t := range tools {
			if t.Enabled && !t.Missing {
				active++
			}
		}
		servers = append(servers, mcpServerDTO(m, len(tools), active))
	}
	if boundIDs == nil {
		boundIDs = []string{}
	}
	return c.JSON(http.StatusOK, echo.Map{"servers": servers, "attached": boundIDs})
}

type agentMCPInput struct {
	ServerIDs []string `json:"server_ids"`
}

func (s *Server) apiSaveAgentMCPServers(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	var in agentMCPInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "bad request body"})
	}
	// Validate every id against THIS workspace before writing: an agent must not be
	// bindable to another tenant's server by posting a guessed id.
	own := map[string]bool{}
	rows, err := s.db.ListMCPServers(w.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	for _, m := range rows {
		own[m.ID] = true
	}
	valid := make([]string, 0, len(in.ServerIDs))
	for _, id := range in.ServerIDs {
		if own[id] {
			valid = append(valid, id)
		}
	}
	if err := s.db.SetAgentMCPServers(ctx, c.Param("id"), valid); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	s.audit.Log(w.ID, "bind_agent_mcp", "agent:"+c.Param("id"), "", c.RealIP())
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

// ── DTOs ─────────────────────────────────────────────────────────────────────

// apiMCPServer never carries the credential, not even redacted — a field that exists
// is a field a future handler can accidentally populate.
type apiMCPServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	URL         string `json:"url"`
	AuthKind    string `json:"auth_kind"`
	HeaderName  string `json:"header_name"`
	HasToken    bool   `json:"has_token"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
	LastError   string `json:"last_error"`
	SyncedAt    string `json:"synced_at"`
	ToolCount   int    `json:"tool_count"`
	ActiveTools int    `json:"active_tools"`
}

type apiMCPTool struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ToolName     string `json:"tool_name"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	ReadOnly     bool   `json:"read_only"`
	ApprovalMode string `json:"approval_mode"`
	Enabled      bool   `json:"enabled"`
	Missing      bool   `json:"missing"`
}

func mcpServerDTO(m *db.MCPServer, total, active int) apiMCPServer {
	return apiMCPServer{
		ID: m.ID, Name: m.Name, Slug: m.Slug, URL: m.URL,
		AuthKind: m.AuthKind, HeaderName: m.HeaderName,
		HasToken: m.EncryptedToken != "", Enabled: m.Enabled,
		Status: m.Status, LastError: m.LastError, SyncedAt: m.ToolsSyncedAt,
		ToolCount: total, ActiveTools: active,
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) apiListMCPServers(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	rows, err := s.db.ListMCPServers(w.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	// Initialised as an empty slice, never nil: a nil slice marshals to JSON null,
	// and a TypeScript default parameter substitutes only for undefined. That exact
	// mismatch once unmounted a whole route (see flattenRequires).
	out := []apiMCPServer{}
	for _, m := range rows {
		tools, _ := s.db.ListMCPTools(c.Request().Context(), m.ID)
		active := 0
		for _, t := range tools {
			if t.Enabled && !t.Missing {
				active++
			}
		}
		out = append(out, mcpServerDTO(m, len(tools), active))
	}
	return c.JSON(http.StatusOK, echo.Map{"servers": out})
}

func (s *Server) apiGetMCPServer(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	m, err := s.db.GetMCPServer(c.Request().Context(), w.ID, c.Param("id"))
	if err != nil {
		return mcpNotFound(c, err)
	}
	tools, _ := s.db.ListMCPTools(c.Request().Context(), m.ID)
	active := 0
	for _, t := range tools {
		if t.Enabled && !t.Missing {
			active++
		}
	}
	return c.JSON(http.StatusOK, echo.Map{"server": mcpServerDTO(m, len(tools), active)})
}

type mcpServerInput struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	AuthKind   string `json:"auth_kind"`
	HeaderName string `json:"header_name"`
	Token      string `json:"token"`
	Enabled    *bool  `json:"enabled"`
}

// validate rejects the input shapes that would otherwise fail later with a worse
// message — an empty header name for header auth being the one a user hits most.
func (in *mcpServerInput) validate() string {
	in.Name = strings.TrimSpace(in.Name)
	in.URL = strings.TrimSpace(in.URL)
	in.HeaderName = strings.TrimSpace(in.HeaderName)
	if in.AuthKind == "" {
		in.AuthKind = "none"
	}
	if in.Name == "" {
		return "a name is required"
	}
	if in.URL == "" {
		return "a server URL is required"
	}
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return "the URL must start with http:// or https://"
	}
	switch in.AuthKind {
	case "none", "bearer", "header":
	default:
		return "auth kind must be none, bearer or header"
	}
	if in.AuthKind == "header" && in.HeaderName == "" {
		return "a header name is required for header authentication"
	}
	return ""
}

func (s *Server) apiCreateMCPServer(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	var in mcpServerInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "bad request body"})
	}
	if msg := in.validate(); msg != "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": msg})
	}

	ctx := c.Request().Context()
	slug, err := mcp.UniqueSlug(ctx, s.db, w.ID, in.Name, "")
	if err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}
	enc := ""
	if in.Token != "" {
		// Sealed with the SYSTEM key rather than the workspace master password: the
		// background sync and cron runs must decrypt it with no human present.
		enc, err = secrets.EncryptWithSystemKey(in.Token, s.systemKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "could not store the credential"})
		}
	}
	m := &db.MCPServer{
		ID: uuid.NewString(), WorkspaceID: w.ID, Name: in.Name, Slug: slug,
		URL: in.URL, Transport: "http", AuthKind: in.AuthKind, HeaderName: in.HeaderName,
		EncryptedToken: enc, Enabled: true, Status: db.MCPStatusActive,
	}
	if err := s.db.CreateMCPServer(ctx, m); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	s.audit.Log(w.ID, "add_mcp_server", "mcp:"+m.ID, in.URL, c.RealIP())
	return c.JSON(http.StatusOK, echo.Map{"server": mcpServerDTO(m, 0, 0)})
}

func (s *Server) apiUpdateMCPServer(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	m, err := s.db.GetMCPServer(ctx, w.ID, c.Param("id"))
	if err != nil {
		return mcpNotFound(c, err)
	}
	var in mcpServerInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "bad request body"})
	}
	if msg := in.validate(); msg != "" {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": msg})
	}
	m.Name, m.URL, m.AuthKind, m.HeaderName = in.Name, in.URL, in.AuthKind, in.HeaderName
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	// Switching to no authentication DISCARDS the stored credential. It is never sent
	// once auth_kind is "none" (applyAuth returns early), but keeping ciphertext at
	// rest for an auth mode that cannot use it is a credential the owner believes they
	// removed.
	if in.AuthKind == "none" {
		m.EncryptedToken = ""
		m.HeaderName = ""
		if err := s.db.UpdateMCPServerClearToken(ctx, m); err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
		}
		s.audit.Log(w.ID, "update_mcp_server", "mcp:"+m.ID, "", c.RealIP())
		return s.apiGetMCPServer(c)
	}
	// An empty token means "leave the stored one alone", so editing a server's URL
	// does not force the owner to retype a credential they cannot read back.
	if in.Token != "" {
		enc, err := secrets.EncryptWithSystemKey(in.Token, s.systemKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "could not store the credential"})
		}
		m.EncryptedToken = enc
	} else {
		m.EncryptedToken = ""
	}
	if err := s.db.UpdateMCPServer(ctx, m); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	s.audit.Log(w.ID, "update_mcp_server", "mcp:"+m.ID, "", c.RealIP())
	return s.apiGetMCPServer(c)
}

func (s *Server) apiDeleteMCPServer(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	if err := s.db.DeleteMCPServer(c.Request().Context(), w.ID, c.Param("id")); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	s.audit.Log(w.ID, "delete_mcp_server", "mcp:"+c.Param("id"), "", c.RealIP())
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

// apiTestMCPServer and apiSyncMCPServer are the same operation from the user's point
// of view — "does this work, and what can it do" — so Test performs a real sync
// rather than a bare handshake.
//
// That is deliberate: a handshake-only check would pass against a server whose tool
// list we cannot use, and the tool list IS the review step where the owner reads the
// descriptions before anything is enabled.
func (s *Server) apiTestMCPServer(c echo.Context) error { return s.apiSyncMCPServer(c) }

func (s *Server) apiSyncMCPServer(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	m, err := s.db.GetMCPServer(ctx, w.ID, c.Param("id"))
	if err != nil {
		return mcpNotFound(c, err)
	}
	if s.mcpClient == nil {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"error": "MCP is not available on this server"})
	}

	// The BoundServer is assembled here rather than through mcp.BoundServersFor,
	// which deliberately drops a server with no enabled tools — and on a FIRST sync
	// that is every server, since the tools it would filter on do not exist yet.
	srv := mcp.BoundServer{
		ID: m.ID, WorkspaceID: m.WorkspaceID, Name: m.Name, Slug: m.Slug, URL: m.URL,
		AuthKind: m.AuthKind, HeaderName: m.HeaderName,
	}
	if m.EncryptedToken != "" {
		tok, err := secrets.DecryptWithSystemKey(m.EncryptedToken, s.systemKey)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": "stored credential could not be read"})
		}
		srv.Token = tok
	}

	rep, err := mcp.Sync(ctx, s.db, s.mcpClient, srv)
	if err != nil {
		// 200 with an error field: the sync ran and the SERVER refused or was
		// unreachable. A 5xx would read as "Rookery is broken", which sends the owner
		// looking in the wrong place.
		fresh, _ := s.db.GetMCPServer(ctx, w.ID, m.ID)
		status := db.MCPStatusUnreachable
		if fresh != nil {
			status = fresh.Status
		}
		return c.JSON(http.StatusOK, echo.Map{"error": err.Error(), "status": status})
	}
	s.audit.Log(w.ID, "sync_mcp_server", "mcp:"+m.ID, "", c.RealIP())
	return c.JSON(http.StatusOK, echo.Map{
		"discovered": rep.Discovered,
		"added":      rep.Added,
		"missing":    rep.Missing,
		"held_back":  rep.HeldBack,
		"status":     db.MCPStatusActive,
	})
}

func (s *Server) apiListMCPServerTools(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	m, err := s.db.GetMCPServer(ctx, w.ID, c.Param("id"))
	if err != nil {
		return mcpNotFound(c, err)
	}
	rows, err := s.db.ListMCPTools(ctx, m.ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	out := []apiMCPTool{}
	for _, t := range rows {
		out = append(out, apiMCPTool{
			ID: t.ID, Name: t.Name, ToolName: t.ToolName, Title: t.Title,
			Description: t.Description, ReadOnly: t.ReadOnly,
			ApprovalMode: t.ApprovalMode, Enabled: t.Enabled, Missing: t.Missing,
		})
	}
	return c.JSON(http.StatusOK, echo.Map{"tools": out, "cap": mcp.MaxEnabledToolsPerServer})
}

type mcpToolInput struct {
	Enabled      *bool  `json:"enabled"`
	ReadOnly     *bool  `json:"read_only"`
	ApprovalMode string `json:"approval_mode"`
}

func (s *Server) apiUpdateMCPTool(c echo.Context) error {
	w := c.Get("workspace").(*db.Workspace)
	ctx := c.Request().Context()
	m, err := s.db.GetMCPServer(ctx, w.ID, c.Param("id"))
	if err != nil {
		return mcpNotFound(c, err)
	}
	tool, err := s.db.GetMCPTool(ctx, m.ID, c.Param("toolID"))
	if err != nil {
		return mcpNotFound(c, err)
	}
	var in mcpToolInput
	if err := c.Bind(&in); err != nil {
		return c.JSON(http.StatusBadRequest, echo.Map{"error": "bad request body"})
	}
	enabled, readOnly, mode := tool.Enabled, tool.ReadOnly, tool.ApprovalMode
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	// Enforce the per-server cap here too, not only at first sync. The cap protects a
	// shared budget — every tool an agent is offered competes for its attention, so
	// one server's eightieth tool degrades its use of every OTHER tool, connector
	// actions included. A cap that only applied to sync would be trivially exceeded by
	// ticking boxes, which is exactly how the documented limit would become a lie.
	if enabled && !tool.Enabled {
		n, err := s.db.CountEnabledMCPTools(ctx, m.ID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
		}
		if n >= mcp.MaxEnabledToolsPerServer {
			return c.JSON(http.StatusBadRequest, echo.Map{
				"error": fmt.Sprintf(
					"%s already has the maximum of %d tools switched on. Turn one off before enabling another.",
					m.Name, mcp.MaxEnabledToolsPerServer),
			})
		}
	}
	if in.ReadOnly != nil {
		readOnly = *in.ReadOnly
	}
	if in.ApprovalMode != "" {
		mode = in.ApprovalMode
	}
	if err := s.db.UpdateMCPToolSettings(ctx, m.ID, tool.ID, enabled, readOnly, mode); err != nil {
		return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, echo.Map{"ok": true})
}

func mcpNotFound(c echo.Context, err error) error {
	if errors.Is(err, db.ErrNotFound) {
		return c.JSON(http.StatusNotFound, echo.Map{"error": "not found"})
	}
	return c.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
}
