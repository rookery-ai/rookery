package web

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/sessions"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/audit"
	"github.com/ilijad1/simple-agents/internal/auth"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/memory"
	"github.com/ilijad1/simple-agents/internal/secrets"
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
	memory     *memory.Store           // may be nil in tests
	designer   *agentdesigner.AgentDesigner
	skills     *skillstore.Store
	designFlow *agentdesigner.Flow // shared with Telegram gateway
	homesDir   string              // per-user claude HOME directories
	vault      *vault.Vault        // per-user knowledge base
}

// NewServer wires up all routes and middleware.
// gatewayManager and runner may be nil (e.g. in tests).
func NewServer(cfg *config.Config, database *db.DB, gatewayManager *gateway.GatewayManager, runner *agentrunner.Runner, designer *agentdesigner.AgentDesigner, homesDir string, memStore *memory.Store, skillStore *skillstore.Store, designFlow *agentdesigner.Flow) (*Server, error) {
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
		memory:     memStore,
		designer:   designer,
		skills:     skillStore,
		designFlow: designFlow,
		homesDir:   homesDir,
		vault:      vault.New(cfg.Data.Dir),
	}

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

	// Authenticated routes (any role)
	authed := s.echo.Group("")
	authed.Use(s.requireAuth)
	authed.GET("/setup", s.showSetup)
	authed.POST("/setup", s.handleSetup)
	authed.GET("/change-password", s.showChangePassword)
	authed.POST("/change-password", s.handleChangePassword)

	// User dashboard (admins are blocked and redirected to /admin)
	dash := s.echo.Group("/dashboard")
	dash.Use(s.requireAuth)
	dash.Use(s.requireUserOnly)
	dash.Use(s.requireSetupComplete)
	dash.GET("", s.showDashboard)
	dash.GET("/agents", s.showAgents)
	dash.GET("/agents/new", s.showNewAgent)
	dash.POST("/agents/design", s.handleDesignChat)
	dash.POST("/agents/design/cancel", s.handleCancelDesign)
	dash.POST("/agents/design/resume", s.handleResumeDraft)
	dash.POST("/agents/design/dismiss", s.handleDismissDraft)
	dash.GET("/agents/design/progress", s.handleDesignProgress)
	dash.GET("/agents/:id", s.showAgentDetail)
	dash.GET("/agents/:id/edit", s.showEditAgent)
	dash.POST("/agents/:id/edit/start", s.handleStartEditDesign)
	dash.POST("/agents/:id/delete", s.handleDeleteAgent)
	dash.POST("/agents/:id/run", s.handleRunAgent)
	dash.POST("/agents/:id/schedule", s.handleSaveSchedule)
	dash.POST("/agents/:id/schedule/delete", s.handleDeleteSchedule)
	dash.POST("/agents/:id/agent-md", s.handleSaveAgentMD)
	dash.POST("/agents/:id/skills", s.handleSaveAgentSkills)
	dash.GET("/skills", s.showSkills)
	dash.POST("/skills", s.handleCreateSkill)
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
	dash.GET("/composio", s.showComposio)
	dash.GET("/sessions", s.showSessions)
	dash.POST("/sessions", s.handleCreateSession)
	dash.POST("/sessions/:id/stop", s.handleStopSession)
	dash.POST("/sessions/:id/delete", s.handleDeleteSession)
	dash.GET("/reminders", s.showReminders)
	dash.POST("/reminders", s.handleCreateReminder)
	dash.POST("/reminders/:id/delete", s.handleDeleteReminder)
	dash.GET("/memory", s.showMemory)
	dash.POST("/memory", s.handleUpdateMemory)
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
	dash.POST("/settings/master-password", s.handleChangeMasterPassword)

	// Admin routes
	admin := s.echo.Group("/admin")
	admin.Use(s.requireAuth)
	admin.Use(s.requireAdmin)
	admin.GET("", s.showAdminDashboard)
	admin.GET("/users", s.showAdminUsers)
	admin.POST("/users", s.handleAdminCreateUser)
	admin.GET("/users/:id", s.showAdminUser)
	admin.POST("/users/:id/permissions", s.handleAdminGrantPermission)
	admin.POST("/users/:id/permissions/:perm/revoke", s.handleAdminRevokePermission)
	admin.POST("/users/:id/reset-password", s.handleAdminResetPassword)
	admin.POST("/users/:id/coder", s.handleAdminAssignCoder)
	admin.POST("/users/:id/coder/unassign", s.handleAdminUnassignCoder)
	admin.GET("/coders", s.showAdminCoders)
	admin.POST("/coders", s.handleAdminCreateCoder)
	admin.GET("/coders/:id", s.showAdminCoder)
	admin.POST("/coders/:id", s.handleAdminUpdateCoder)
	admin.POST("/coders/:id/delete", s.handleAdminDeleteCoder)
	admin.GET("/settings", s.showAdminSettings)
	admin.POST("/settings", s.handleAdminSaveSettings)
	admin.GET("/audit", s.showAuditLog)
}

// ── Auth helpers ───────────────────────────────────────────────────────────

func (s *Server) currentUser(c echo.Context) (*db.User, bool) {
	sess, err := s.store.Get(c.Request(), sessionName)
	if err != nil {
		return nil, false
	}
	userID, ok := sess.Values["user_id"].(string)
	if !ok || userID == "" {
		return nil, false
	}
	u, err := s.db.GetUserByID(userID)
	if err != nil {
		return nil, false
	}
	return u, true
}

func (s *Server) setSession(c echo.Context, userID string) error {
	// Ignore decode error: Get always returns a usable session even for stale/invalid cookies.
	sess, _ := s.store.Get(c.Request(), sessionName)
	sess.Values["user_id"] = userID
	return sess.Save(c.Request(), c.Response())
}

func (s *Server) clearSession(c echo.Context) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	sess.Options.MaxAge = -1
	return sess.Save(c.Request(), c.Response())
}

// ── Middleware handlers ────────────────────────────────────────────────────

func (s *Server) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		u, ok := s.currentUser(c)
		if !ok {
			return c.Redirect(http.StatusFound, "/login")
		}
		// Force password change before anything else
		if u.MustChangePassword && c.Path() != "/change-password" {
			return c.Redirect(http.StatusFound, "/change-password")
		}
		c.Set("user", u)
		return next(c)
	}
}

func (s *Server) requireSetupComplete(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		u := c.Get("user").(*db.User)
		if u.NeedsSetup && c.Path() != "/setup" {
			return c.Redirect(http.StatusFound, "/setup")
		}
		return next(c)
	}
}

func (s *Server) requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		u := c.Get("user").(*db.User)
		if u.Role != auth.RoleAdmin {
			return echo.NewHTTPError(http.StatusForbidden, "admin access required")
		}
		return next(c)
	}
}

func (s *Server) requireUserOnly(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		u := c.Get("user").(*db.User)
		if u.Role == auth.RoleAdmin {
			return c.Redirect(http.StatusFound, "/admin")
		}
		return next(c)
	}
}

// ── Utility ────────────────────────────────────────────────────────────────

func (s *Server) redirectRoot(c echo.Context) error {
	u, ok := s.currentUser(c)
	if ok {
		if u.Role == auth.RoleAdmin {
			return c.Redirect(http.StatusFound, "/admin")
		}
		return c.Redirect(http.StatusFound, "/dashboard")
	}
	return c.Redirect(http.StatusFound, "/login")
}

// coderForUser returns a Coder for the given user. If the user has a coder
// profile assigned, its settings are used; otherwise the system defaults apply.
func (s *Server) coderForUser(userID string) *coder.Coder {
	dataDir := s.cfg.Data.Dir
	if profile, err := s.db.GetUserCoder(userID); err == nil && profile != nil {
		timeout := time.Duration(profile.TimeoutS) * time.Second
		return coder.New(profile.ClaudeBin, timeout, s.homesDir, dataDir).
			WithBackendType(profile.BackendType).
			WithSandbox(s.cfg.Sandbox.Enabled)
	}
	return coder.New(s.cfg.Coder.ClaudeBin, s.cfg.Coder.Timeout, s.homesDir, dataDir).
		WithSandbox(s.cfg.Sandbox.Enabled)
}

type pageData struct {
	User    *db.User
	Title   string
	Error   string
	Success string
	Data    any
}

func (s *Server) page(c echo.Context, title string) *pageData {
	u, _ := s.currentUser(c)
	return &pageData{User: u, Title: title}
}
