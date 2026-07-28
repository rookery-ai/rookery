package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/sessions"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/agentrunner"
	"github.com/ilijad1/simple-agents/internal/audit"
	"github.com/ilijad1/simple-agents/internal/chat"
	"github.com/ilijad1/simple-agents/internal/coder"
	"github.com/ilijad1/simple-agents/internal/config"
	"github.com/ilijad1/simple-agents/internal/connectors"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/gateway"
	"github.com/ilijad1/simple-agents/internal/memory"
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
	// approval resolves parked public_write actions from the web UI. Nil when the
	// install has no approval service wired, in which case the endpoints say so
	// rather than 500ing.
	approval   ApprovalResolver
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
	designFlow *agentdesigner.Flow   // shared with Telegram gateway
	skillFlow  *skilldesigner.Flow   // conversational skill-creator (web + Telegram)
	homesDir   string                // per-user claude HOME directories
	vault      *vault.Vault          // per-user knowledge base
	memory     *memory.Store         // per-user structured context (injected into one-off chat)
	connectors *connectors.Registry  // self-managed-OAuth connector registry (embedded data files)
	connStore  connectors.TokenStore // token store for connector execution (chat + services UI)
	connBridge *connectors.Bridge    // loopback bridge so CLI chat coders can reach connectors
	kbBridge   *vault.Bridge         // loopback bridge so CLI chat coders can reach KB convert/search
	titleGen   chat.TitleGenerator   // optional; auto-titles a chat from its first exchange

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
	s.connStore = &connectors.DBTokenStore{DB: s.db, SystemKey: s.systemKey, Reg: s.connectors, OAuth: connectors.OAuthClient{}}

	s.echo.HideBanner = true
	s.echo.HidePort = true

	s.setupMiddleware()
	s.setupRoutes()

	if spec, ok := gateway.CredSpecFor("telegram"); ok && spec.Validate == nil {
		spec.Validate = func(v map[string]string) (string, error) { return testTelegramToken(v["token"]) }
		gateway.RegisterCredSpec(spec)
	}

	return s, nil
}

func (s *Server) Start(addr string) error {
	return s.echo.Start(addr)
}

// WithBridge attaches the loopback connector bridge so CLI chat coders can reach connectors.
func (s *Server) WithBridge(b *connectors.Bridge) *Server { s.connBridge = b; return s }

// WithKBBridge attaches the loopback KB bridge so CLI chat coders can reach
// save_to_kb-equivalent conversion + search (`simple-agents kb convert|search`).
func (s *Server) WithKBBridge(b *vault.Bridge) *Server { s.kbBridge = b; return s }

// WithTitleGenerator enables one-time content-based auto-titling of chats.
func (s *Server) WithTitleGenerator(g chat.TitleGenerator) *Server {
	s.titleGen = g
	return s
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
	// The entire template UI has been deleted — the embedded SPA (served at / by
	// setupSPARoutes) plus the JSON API (/api/v1, setupAPIRoutes) are the only two
	// HTTP surfaces. The one exception below is the OAuth callback.
	//
	// FROZEN: this exact path is the redirect URI registered in external OAuth
	// apps (Google, GitHub, …), so it must NOT change even though the rest of the
	// /dashboard template tree is gone. It is registered standalone (not the JSON
	// API) because the provider redirects a browser here and we finish with an
	// HTTP redirect, not a JSON body. It reads c.Get("workspace"), so it carries
	// the same owner → active-workspace → setup-complete guard chain the old
	// /dashboard group applied.
	s.echo.GET("/dashboard/connectors/services/callback/:provider", s.handleOAuthCallback,
		s.requireOwner, s.requireActiveWorkspace, s.requireSetupComplete)

	s.setupAPIRoutes()
	s.setupSPARoutes()
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

// isLocked reports whether the owner has locked the UI. Absent means unlocked,
// so an older session cookie is never read as locked.
func (s *Server) isLocked(c echo.Context) bool {
	sess, err := s.store.Get(c.Request(), sessionName)
	if err != nil {
		return false
	}
	locked, _ := sess.Values["locked"].(bool)
	return locked
}

// setLocked sets or clears the screen lock. It deliberately leaves owner_id and
// active_workspace_id alone: locking must not cost the entered workspace.
func (s *Server) setLocked(c echo.Context, locked bool) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	if locked {
		sess.Values["locked"] = true
	} else {
		delete(sess.Values, "locked")
	}
	return sess.Save(c.Request(), c.Response())
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

// ApprovalResolver is the subset of *approval.Service the web layer needs. An
// interface so web does not import internal/approval (which imports the whole
// connector layer) purely to resolve two endpoints.
type ApprovalResolver interface {
	Approve(ctx context.Context, workspaceID, id string) (*db.PendingAction, error)
	Reject(ctx context.Context, workspaceID, id string) (*db.PendingAction, error)
}

// WithApproval wires the approval resolver used by the /api/v1/approvals endpoints.
func (s *Server) WithApproval(a ApprovalResolver) *Server {
	s.approval = a
	return s
}
