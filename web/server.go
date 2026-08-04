package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/sessions"
	"github.com/ilijad1/rookery/internal/agentdesigner"
	"github.com/ilijad1/rookery/internal/agentrunner"
	"github.com/ilijad1/rookery/internal/audit"
	"github.com/ilijad1/rookery/internal/backup"
	"github.com/ilijad1/rookery/internal/chat"
	"github.com/ilijad1/rookery/internal/coder"
	"github.com/ilijad1/rookery/internal/config"
	"github.com/ilijad1/rookery/internal/connectors"
	"github.com/ilijad1/rookery/internal/db"
	"github.com/ilijad1/rookery/internal/gateway"
	"github.com/ilijad1/rookery/internal/memory"
	"github.com/ilijad1/rookery/internal/secrets"
	"github.com/ilijad1/rookery/internal/skilldesigner"
	"github.com/ilijad1/rookery/internal/skillstore"
	"github.com/ilijad1/rookery/internal/vault"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

const sessionName = "sa_session"

// Server is the HTTP server for the Rookery web UI.
type Server struct {
	// approval resolves parked public_write actions from the web UI. Nil when the
	// install has no approval service wired, in which case the endpoints say so
	// rather than 500ing.
	approval   ApprovalResolver
	echo       *echo.Echo
	cfg        *config.Config
	db         *db.DB
	store      *sessions.CookieStore
	echoMu     sync.Mutex
	echoNonces map[string]echoNonce
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

	// searchKeyVerify proves a search API key against the live provider before
	// it is stored. It is a field rather than a direct call so tests can save a
	// key without reaching the network — verifySearchKey is the production
	// implementation and is installed by NewServer.
	searchKeyVerify func(ctx context.Context, provider, key string) error

	// backupSched drives owner-level snapshots. Shared with the background
	// ticker started in serve so the "Back up now" button and the schedule run
	// the exact same path. Nil in tests, where the endpoint says so rather than
	// panicking.
	backupSched *backup.Scheduler

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
		// Use a fixed dev key if not configured; production MUST set ROOKERY_SESSION_KEY.
		sessionKey = []byte("change-me-in-production-32bytes!!")
	}

	store := sessions.NewCookieStore(sessionKey)
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	// Must resolve the key the SAME way main.go does. Using the hostname-derived
	// SystemKeyFromEnv here would diverge from the pinned <data_dir>/system.key
	// the moment a restore installed a recovered key — the server would then
	// fail to decrypt connector tokens and stored master passwords with no
	// visible cause. By the time serve constructs the Server the key file
	// already exists, so this is a read.
	var wsCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&wsCount); err != nil {
		return nil, fmt.Errorf("count workspaces: %w", err)
	}
	sysKey, err := secrets.SystemKey(cfg.Data.Dir, wsCount > 0)
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
		spec.Validate = func(v map[string]string) (gateway.BotIdentity, error) {
			return testTelegramToken(v["token"])
		}
		gateway.RegisterCredSpec(spec)
	}

	return s, nil
}

func (s *Server) Start(addr string) error {
	return s.echo.Start(addr)
}

// WithBackupScheduler attaches the owner-level backup scheduler, so the
// "Back up now" button runs the same path as the nightly ticker.
func (s *Server) WithBackupScheduler(b *backup.Scheduler) *Server { s.backupSched = b; return s }

// WithBridge attaches the loopback connector bridge so CLI chat coders can reach connectors.
func (s *Server) WithBridge(b *connectors.Bridge) *Server { s.connBridge = b; return s }

// WithKBBridge attaches the loopback KB bridge so CLI chat coders can reach
// save_to_kb-equivalent conversion + search (`rookery kb convert|search`).
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

	// Unauthenticated infrastructure endpoint — see apiHealthz. Registered
	// before the SPA catch-all so /healthz is never swallowed by it.
	s.echo.GET("/healthz", s.apiHealthz)
	// Unauthenticated by necessity: the "Test this URL" check is a server-to-
	// server fetch that carries no session cookie, so an authenticated endpoint
	// would fail identically whether the URL was right or wrong — inverting the
	// signal the test exists to give. Safe because it is not an oracle: it echoes
	// only a nonce this process issued, once, within 30 seconds, and 404s
	// otherwise. It reveals no configuration.
	s.echo.GET("/healthz/echo", s.handleEchoNonce)

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

// ownerVerifyTTL is how long one owner-password confirmation lasts.
//
// Chosen from the shape of real owner work: saving a backup config, running it,
// and listing the resulting snapshots is three or four requests over a couple of
// minutes, which should cost ONE password entry, not four. Short enough that a
// browser walked away from re-locks within a coffee break.
const ownerVerifyTTL = 15 * time.Minute

// ownerVerifiedAt returns when the owner last confirmed their password on this
// session. Stored as Unix seconds because gorilla's default session encoder
// handles primitives without registering a gob type for time.Time.
func (s *Server) ownerVerifiedAt(c echo.Context) (time.Time, bool) {
	sess, err := s.store.Get(c.Request(), sessionName)
	if err != nil {
		return time.Time{}, false
	}
	secs, ok := sess.Values["owner_verified_at"].(int64)
	if !ok || secs == 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0), true
}

// isOwnerVerified reports whether the owner has confirmed their password within
// the TTL. Absent means NOT verified, so an older session cookie can never be
// read as pre-verified.
func (s *Server) isOwnerVerified(c echo.Context) bool {
	at, ok := s.ownerVerifiedAt(c)
	return ok && time.Since(at) < ownerVerifyTTL
}

// setOwnerVerified stamps a fresh confirmation. Like setLocked, it deliberately
// leaves owner_id and active_workspace_id alone.
func (s *Server) setOwnerVerified(c echo.Context) error {
	sess, _ := s.store.Get(c.Request(), sessionName)
	sess.Values["owner_verified_at"] = time.Now().Unix()
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
		s.cfg.Coder.ClaudeBin, s.cfg.Coder.Timeout, s.cfg.Sandbox.Enabled,
		s.coderMode() == config.ModeFull).
		WithSecretsLookup(s.secretsLookup)
}

// coderMode returns the build's coder policy, defaulting to full. Nil-safe
// because tests construct a bare &Server{}; the config is the single source, so
// no parallel field is stored on Server.
func (s *Server) coderMode() string {
	if s.cfg == nil || s.cfg.Coder.Mode == "" {
		return config.ModeFull
	}
	return s.cfg.Coder.Mode
}

// sandboxEnabled reports whether Landlock confinement is switched on.
func (s *Server) sandboxEnabled() bool {
	return s.cfg != nil && s.cfg.Sandbox.Enabled
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
