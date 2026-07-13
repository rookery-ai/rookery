package web

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/sessions"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/audit"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/memory"
	"github.com/ilijad1/simple-agents/internal/profile"
	"github.com/ilijad1/simple-agents/internal/secrets"
	"github.com/ilijad1/simple-agents/internal/skilldesigner"
	"github.com/ilijad1/simple-agents/internal/skillstore"
	"github.com/ilijad1/simple-agents/internal/vault"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

const sessionName = "sa_session"

// Server is the HTTP server for the Simple Agents web UI.
type Server struct {
	echo       *echo.Echo
	cfg        *config.Config
	db         *db.DB
	store      *sessions.CookieStore
	audit      *audit.Writer
	systemKey  []byte                  // 32-byte key for encrypting master passwords at rest
	gateway    *gateway.GatewayManager // may be nil in tests
	runner     *agentrunner.Runner     // may be nil in tests
	designer   *agentdesigner.AgentDesigner
	skills     *skillstore.Store
	designFlow *agentdesigner.Flow  // shared with Telegram gateway
	skillFlow  *skilldesigner.Flow  // conversational skill-creator (web + Telegram)
	homesDir   string               // per-user claude HOME directories
	vault      *vault.Vault         // per-user knowledge base
	memory     *memory.Store        // per-user structured context (injected into one-off chat)
	connectors *connectors.Registry // self-managed-OAuth connector registry (embedded data files)

	// runs tracks in-flight manual ("Run Now") agent runs so progress can be
	// streamed to the browser over SSE while the run executes on a detached
	// context that outlives the originating HTTP request. Keyed by agentID.
	runs   map[string]*agentRunState
	runsMu sync.Mutex
}

// NewServer wires up all routes and middleware.
// gatewayManager, runner and memStore may be nil (e.g. in tests).
func NewServer(cfg *config.Config, database *db.DB, gatewayManager *gateway.GatewayManager, runner *agentrunner.Runner, designer *agentdesigner.AgentDesigner, homesDir string, skillStore *skillstore.Store, designFlow *agentdesigner.Flow, skillFlow *skilldesigner.Flow, memStore *memory.Store) (*Server, error) {
	sessionKey := []byte(cfg.Server.SessionKey)
	if len(sessionKey) == 0 {
		// Use a fixed dev key if not configured; production MUST set SA_SESSION_KEY.
		sessionKey = []byte("change-me-in-production-32bytes!!")
	}

	store := sessions.NewCookieStore(sessionKey)
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	sysKey, err := secrets.SystemKeyFromEnv()
	if err != nil {
		return nil, fmt.Errorf("system key: %w", err)
	}

	s := &Server{
		echo:       echo.New(),
		cfg:        cfg,
		db:         database,
		store:      store,
		audit:      audit.New(database),
		systemKey:  sysKey,
		gateway:    gatewayManager,
		runner:     runner,
		designer:   designer,
		skills:     skillStore,
		designFlow: designFlow,
		skillFlow:  skillFlow,
		homesDir:   homesDir,
		vault:      vault.New(cfg.Data.Dir),
		memory:     memStore,
		runs:       make(map[string]*agentRunState),
	}

	connReg, err := connectors.LoadBundled()
	if err != nil {
		return nil, fmt.Errorf("load connectors: %w", err)
	}
	s.connectors = connReg

	s.echo.HideBanner = true
	s.echo.HidePort = true

	if err := s.setupTemplates(); err != nil {
		return nil, err
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s, nil
}

func (s *Server) Start(addr string) error {
	return s.echo.Start(addr)
}

// ── Templates ──────────────────────────────────────────────────────────────

type TemplateRenderer struct {
	tmpl *template.Template
}

func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.tmpl.ExecuteTemplate(w, name, data)
}

func (s *Server) setupTemplates() error {
	tmplDir := s.cfg.Server.TemplatesDir
	if tmplDir == "" {
		// Fall back to relative paths if not configured.
		for _, d := range []string{
			filepath.Join(filepath.Dir(os.Args[0]), "web/templates"),
			"web/templates",
			"templates",
		} {
			if _, err := os.Stat(d); err == nil {
				tmplDir = d
				break
			}
		}
	}
	if tmplDir == "" {
		return fmt.Errorf("web templates directory not found; set SA_TEMPLATES_DIR or templates_dir in config")
	}

	tmpl, err := parseTemplates(tmplDir)
	if err != nil {
		return err
	}
	s.echo.Renderer = &TemplateRenderer{tmpl: tmpl}
	return nil
}

func parseTemplates(dir string) (*template.Template, error) {
	funcMap := template.FuncMap{
		"truncate": func(s string, n int) string {
			if len(s) <= n {
				return s
			}
			return s[:n] + "..."
		},
		"derefInt": func(p *int) int {
			if p == nil {
				return -1
			}
			return *p
		},
		"not": func(b bool) bool { return !b },
		"initials": func(s string) string {
			if len(s) == 0 {
				return "?"
			}
			return string([]rune(s)[:1])
		},
		"fmtTZ": func(loc *time.Location, t time.Time, layout string) string {
			if loc == nil {
				loc = time.UTC
			}
			return t.In(loc).Format(layout)
		},
	}

	tmpl := template.New("").Funcs(funcMap)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Template name = relative path from dir (e.g. "auth/login.html")
		name, _ := filepath.Rel(dir, path)
		if _, err := tmpl.New(name).Parse(string(data)); err != nil {
			return err
		}
		return nil
	})
	return tmpl, err
}

// ── Middleware ─────────────────────────────────────────────────────────────

func (s *Server) setupMiddleware() {
	s.echo.Use(middleware.Recover())
	s.echo.HTTPErrorHandler = func(err error, c echo.Context) {
		slog.Error("http error", "path", c.Path(), "err", err)
		s.echo.DefaultHTTPErrorHandler(err, c)
	}
	s.echo.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: "${method} ${uri} ${status}\n",
	}))
}

// ── Routes ─────────────────────────────────────────────────────────────────

func (s *Server) setupRoutes() {
	// Static assets
	staticDir := s.cfg.Server.StaticDir
	if staticDir == "" {
		staticDir = "web/static"
	}
	s.echo.Static("/static", staticDir)

	// Public routes
	s.echo.GET("/", s.redirectRoot)
	s.echo.GET("/login", s.showLogin)
	s.echo.POST("/login", s.handleLogin)
	s.echo.GET("/logout", s.handleLogout)

	// Owner-authenticated routes (no active workspace required)
	authed := s.echo.Group("")
	authed.Use(s.requireOwner)
	authed.GET("/change-password", s.showChangePassword)
	authed.POST("/change-password", s.handleChangePassword)

	// Workspace context (owner logged in AND a workspace entered).
	// The create/onboarding wizard runs here so a not-yet-set-up workspace can
	// still be configured, but every other dashboard route requires setup complete.
	dash := s.echo.Group("/dashboard")
	dash.Use(s.requireOwner)
	dash.Use(s.requireActiveWorkspace)
	dash.GET("/setup", s.showSetup)
	dash.POST("/setup", s.handleSetup)
	dash.Use(s.requireSetupComplete)
	dash.GET("", s.showDashboard)
	dash.GET("/agents", s.showAgents)
	dash.GET("/agents/new", s.showNewAgent)
	dash.POST("/agents/design", s.handleDesignChat)
	dash.POST("/agents/design/cancel", s.handleCancelDesign)
	dash.POST("/agents/design/resume", s.handleResumeDraft)
	dash.POST("/agents/design/dismiss", s.handleDismissDraft)
	dash.GET("/agents/design/progress", s.handleDesignProgress)
	dash.GET("/agents/design/state", s.handleDesignState)
	dash.GET("/agents/:id", s.showAgentDetail)
	dash.GET("/agents/:id/edit", s.showEditAgent)
	dash.POST("/agents/:id/edit/start", s.handleStartEditDesign)
	dash.POST("/agents/:id/delete", s.handleDeleteAgent)
	dash.POST("/agents/:id/run", s.handleRunAgent)
	dash.GET("/agents/:id/run/progress", s.handleRunProgress)
	dash.POST("/agents/:id/schedule", s.handleSaveSchedule)
	dash.POST("/agents/:id/schedule/delete", s.handleDeleteSchedule)
	dash.POST("/agents/:id/agent-md", s.handleSaveAgentMD)
	dash.POST("/agents/:id/skills", s.handleSaveAgentSkills)
	dash.POST("/agents/:id/connections", s.handleSaveAgentConnections)
	dash.GET("/skills", s.showSkills)
	dash.GET("/skills/new", s.showNewSkill)
	dash.POST("/skills/design", s.handleSkillDesignChat)
	dash.POST("/skills/design/cancel", s.handleCancelSkillDesign)
	dash.POST("/skills/design/resume", s.handleResumeSkillDraft)
	dash.POST("/skills/design/dismiss", s.handleDismissSkillDraft)
	dash.GET("/skills/design/progress", s.handleSkillDesignProgress)
	dash.POST("/skills", s.handleCreateSkill)
	dash.GET("/skills/core/:slug", s.showCoreSkill)
	dash.GET("/skills/:id", s.showSkillDetail)
	dash.POST("/skills/:id", s.handleSaveSkill)
	dash.POST("/skills/:id/delete", s.handleDeleteSkill)
	dash.GET("/secrets", s.showSecrets)
	dash.POST("/secrets", s.handleCreateSecret)
	dash.POST("/secrets/:name/delete", s.handleDeleteSecret)
	dash.GET("/connectors", s.showConnectors)
	dash.POST("/connectors", s.handleSaveConnector)
	dash.POST("/connectors/:platform/delete", s.handleDeleteConnector)
	dash.POST("/connectors/:platform/test", s.handleTestConnector)
	// Self-managed OAuth service connections (Google/Gmail, etc.)
	dash.GET("/connectors/services", s.showServices)
	dash.POST("/connectors/services/:provider/creds", s.handleSaveProviderCreds)
	dash.POST("/connectors/services/:provider/connect", s.handleConnectService)
	dash.GET("/connectors/services/callback/:provider", s.handleOAuthCallback)
	dash.POST("/connectors/services/:id/delete", s.handleDeleteServiceConnection)
	dash.GET("/chats", s.showChats)
	dash.POST("/chats", s.handleCreateChat)
	dash.GET("/chats/:id", s.showChatDetail)
	dash.POST("/chats/:id/messages", s.handleChatMessage)
	dash.POST("/chats/:id/resume", s.handleResumeChat)
	dash.POST("/chats/:id/stop", s.handleStopChat)
	dash.POST("/chats/:id/delete", s.handleDeleteChat)
	dash.GET("/reminders", s.showReminders)
	dash.POST("/reminders", s.handleCreateReminder)
	dash.POST("/reminders/:id/delete", s.handleDeleteReminder)
	dash.GET("/reminders/poll", s.handlePollReminders)
	dash.GET("/inbox", s.showInbox)
	dash.GET("/inbox/poll", s.handleInboxPoll)
	dash.POST("/inbox/:id/read", s.handleMarkInboxRead)
	dash.POST("/inbox/read-all", s.handleMarkAllInboxRead)
	dash.POST("/inbox/:id/delete", s.handleDeleteInboxMessage)
	dash.GET("/memory", func(c echo.Context) error {
		return c.Redirect(http.StatusFound, "/dashboard/kb?path=memory")
	})
	dash.GET("/kb", s.showKB)
	dash.GET("/kb/view", s.viewKBNote)
	dash.GET("/kb/edit", s.editKBNote)
	dash.POST("/kb/save", s.handleSaveKBNote)
	dash.POST("/kb/new", s.handleNewKBNote)
	dash.POST("/kb/delete", s.handleDeleteKBNote)
	dash.POST("/kb/rename", s.handleRenameKBNote)
	dash.GET("/kb/search", s.searchKB)
	dash.GET("/kb/raw", s.rawKBNote)
	dash.GET("/settings", s.showSettings)
	dash.POST("/settings", s.handleSaveSettings)
	dash.POST("/settings/workspace", s.handleSaveWorkspaceMeta)
	dash.POST("/settings/coder", s.handleSaveWorkspaceCoder)
	dash.POST("/settings/master-password", s.handleChangeMasterPassword)

	// Owner management area (relabeled "Workspaces" in the UI). Owner logged in;
	// no active workspace required.
	admin := s.echo.Group("/admin")
	admin.Use(s.requireOwner)
	admin.GET("", s.showAdminDashboard)
	admin.GET("/workspaces", s.showAdminWorkspaces)
	admin.POST("/workspaces", s.handleAdminCreateWorkspace)
	admin.GET("/workspaces/:id", s.showAdminWorkspace)
	admin.POST("/workspaces/:id/enter", s.handleEnterWorkspace)
	admin.POST("/workspaces/:id/delete", s.handleAdminDeleteWorkspace)
	admin.POST("/workspaces/:id/permissions", s.handleAdminGrantPermission)
	admin.POST("/workspaces/:id/permissions/:perm/revoke", s.handleAdminRevokePermission)
	admin.GET("/settings", s.showAdminSettings)
	admin.POST("/settings", s.handleAdminSaveSettings)
	admin.GET("/audit", s.showAuditLog)

	// Leaving the active workspace (back to the owner's workspace list).
	s.echo.POST("/workspace/leave", s.handleLeaveWorkspace, s.requireOwner)
}

// ── Auth helpers ───────────────────────────────────────────────────────────

// currentOwner returns the logged-in owner from the session, if any.
func (s *Server) currentOwner(c echo.Context) (*db.Owner, bool) {
	sess, err := s.store.Get(c.Request(), sessionName)
	if err != nil {
		return nil, false
	}
	ownerID, ok := sess.Values["owner_id"].(string)
	if !ok || ownerID == "" {
		return nil, false
	}
	o, err := s.db.GetOwner()
	if err != nil || o == nil || o.ID != ownerID {
		return nil, false
	}
	return o, true
}

// activeWorkspace returns the workspace the owner has currently entered, if any.
func (s *Server) activeWorkspace(c echo.Context) (*db.Workspace, bool) {
	sess, err := s.store.Get(c.Request(), sessionName)
	if err != nil {
		return nil, false
	}
	wsID, ok := sess.Values["active_workspace_id"].(string)
	if !ok || wsID == "" {
		return nil, false
	}
	w, err := s.db.GetWorkspaceByID(wsID)
	if err != nil {
		return nil, false
	}
	return w, true
}

// setOwnerSession marks the owner as logged in.
func (s *Server) setOwnerSession(c echo.Context, ownerID string) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	sess.Values["owner_id"] = ownerID
	return sess.Save(c.Request(), c.Response())
}

// setActiveWorkspace records the entered workspace. Switching requires re-entering
// the target workspace's master password (see handleEnterWorkspace), so this simply
// replaces the single active workspace id.
func (s *Server) setActiveWorkspace(c echo.Context, wsID string) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	sess.Values["active_workspace_id"] = wsID
	return sess.Save(c.Request(), c.Response())
}

// clearActiveWorkspace leaves the current workspace without logging the owner out.
func (s *Server) clearActiveWorkspace(c echo.Context) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	delete(sess.Values, "active_workspace_id")
	return sess.Save(c.Request(), c.Response())
}

func (s *Server) clearSession(c echo.Context) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	sess.Options.MaxAge = -1
	return sess.Save(c.Request(), c.Response())
}

// ── Middleware handlers ────────────────────────────────────────────────────

// requireOwner ensures the owner is logged in and injects "owner" into context.
func (s *Server) requireOwner(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		o, ok := s.currentOwner(c)
		if !ok {
			return c.Redirect(http.StatusFound, "/login")
		}
		if o.MustChangePassword && c.Path() != "/change-password" {
			return c.Redirect(http.StatusFound, "/change-password")
		}
		c.Set("owner", o)
		return next(c)
	}
}

// requireActiveWorkspace ensures a workspace is entered and injects "workspace".
func (s *Server) requireActiveWorkspace(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		w, ok := s.activeWorkspace(c)
		if !ok {
			return c.Redirect(http.StatusFound, "/admin")
		}
		c.Set("workspace", w)
		return next(c)
	}
}

func (s *Server) requireSetupComplete(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		w := c.Get("workspace").(*db.Workspace)
		if w.NeedsSetup && c.Path() != "/dashboard/setup" {
			return c.Redirect(http.StatusFound, "/dashboard/setup")
		}
		return next(c)
	}
}

// ── Utility ────────────────────────────────────────────────────────────────

func (s *Server) redirectRoot(c echo.Context) error {
	if _, ok := s.currentOwner(c); ok {
		return c.Redirect(http.StatusFound, "/admin")
	}
	return c.Redirect(http.StatusFound, "/login")
}

// coderForWorkspace returns a Coder configured from the workspace's inlined coder
// settings, falling back to the system defaults when unset.
func (s *Server) coderForWorkspace(workspaceID string) *coder.Coder {
	w, _ := s.db.GetWorkspaceByID(workspaceID)
	return coder.ForWorkspace(w, s.homesDir, s.cfg.Data.Dir, s.vault,
		s.cfg.Coder.ClaudeBin, s.cfg.Coder.Timeout, s.cfg.Sandbox.Enabled).
		WithSecretsLookup(s.secretsLookup)
}

// secretsLookup resolves a single named secret for a workspace at run time. The
// API coder uses it to fetch its provider API key lazily on every call.
func (s *Server) secretsLookup(ctx context.Context, workspaceID, name string) (string, error) {
	w, err := s.db.GetWorkspaceByID(workspaceID)
	if err != nil || w == nil || w.EncryptedMasterPassword == "" {
		return "", err
	}
	masterPw, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		return "", err
	}
	svc := secrets.New(s.db, workspaceID, masterPw, w.SecretsSalt)
	return svc.Get(ctx, name)
}

type pageData struct {
	Owner      *db.Owner
	Workspace  *db.Workspace   // active workspace (nil on owner-only pages)
	Workspaces []*db.Workspace // all workspaces, for the switcher dropdown
	Title      string
	Error      string
	Success    string
	Data       any
	UserLoc    *time.Location
}

func (s *Server) page(c echo.Context, title string) *pageData {
	o, _ := s.currentOwner(c)
	p := &pageData{Owner: o, Title: title, UserLoc: time.UTC}
	if o != nil {
		p.Workspaces, _ = s.db.ListWorkspaces()
	}
	if w, ok := s.activeWorkspace(c); ok {
		p.Workspace = w
		p.UserLoc = profile.LoadLocation(s.db, w.ID)
	}
	return p
}
