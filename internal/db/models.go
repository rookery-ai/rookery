package db

import "time"

// Owner is the single account that installs and operates the platform. The owner
// logs in, then enters a Workspace. There is exactly one owner row.
type Owner struct {
	ID                 string
	Username           string
	PasswordHash       string
	MustChangePassword bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Workspace is a fully isolated tenant (own vault, home, secrets, agents,
// connector, coder config). It replaces the old per-user account. Workspaces have
// no login of their own — the owner enters them with the workspace master password.
type Workspace struct {
	ID                      string
	Name                    string
	About                   string // "what is this workspace about" — injected into LLM context
	Icon                    string // slug of a preset workspace image; "" = fall back to the name's initial
	EncryptedMasterPassword string
	SecretsSalt             string

	// Inlined coder config (moved from the old admin-level coders pool).
	CoderKind         string // "local" (a host CLI binary) or "api" (direct provider API)
	CoderBin          string // coder binary name/path when CoderKind == "local"
	CoderTimeoutS     int    // 0 = use system default
	CoderBackendType  string // local: '' auto-detect | 'claude' | 'opencode' | 'codex' | 'gemini' | 'cursor' | 'generic'; api kind: 'api'
	CoderProvider     string // API coder: llm registry name (openai/openrouter/anthropic/generic)
	CoderModel        string // API coder: model id, e.g. "gpt-4o", "anthropic/claude-3.5-sonnet"
	CoderAPIKeySecret string // API coder: name of the secret holding the provider API key
	CoderBaseURL      string // API coder: optional base URL override (for generic OpenAI-compatible)

	NeedsSetup bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type PlatformConnection struct {
	ID              string
	WorkspaceID     string
	Platform        string
	EncryptedToken  string
	EncryptedConfig string
	Active          bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type PlatformIdentity struct {
	ID             string
	WorkspaceID    string
	Platform       string
	PlatformUserID string
	LinkedAt       time.Time
}

type Agent struct {
	ID          string
	WorkspaceID string
	Name        string
	Description string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// BrowserIrreversible is the owner's permission for actions that cannot be
	// undone — paying, ordering, transferring, deleting. It is the ONLY browser
	// permission: an agent may read and interact with pages without any grant,
	// because it can already do the equivalent with bash and curl.
	//
	// BrowserNeedsIrreversible is not a permission but a FINDING: this agent's
	// work involves such an action. It is what decides whether the permission is
	// shown on the agent's page at all, so an agent that only reads never
	// presents its owner with a switch about payments.
	BrowserIrreversible      bool
	BrowserNeedsIrreversible bool
}

type Skill struct {
	ID          string
	WorkspaceID string
	Name        string
	Description string
	InstalledAt time.Time
}

// SkillDraft is a persisted in-progress skill-creator session, used to resume
// after a page reload, browser close, or server restart. One per workspace.
type SkillDraft struct {
	WorkspaceID        string
	SkillName          string
	State              string // "designing" or "verifying"
	HistoryJSON        string
	PendingSkillMD     string
	PendingScriptsJSON string
	VettingReport      string
	UpdatedAt          time.Time
	ExpiresAt          time.Time
}

type AgentSchedule struct {
	ID          string
	AgentID     string
	WorkspaceID string
	CronExpr    string
	NextRunAt   *time.Time
	LastRunAt   *time.Time
	Enabled     bool
	CreatedAt   time.Time
	// Timezone is the IANA zone the cron expression is read in. EMPTY means the
	// host's local zone, which is what every expression was evaluated in before
	// this column existed — so an unset value is not "unknown", it is an
	// explicit "behave exactly as before". See scheduler.scheduleLocation.
	Timezone string
}

type AgentRun struct {
	ID          string
	AgentID     string
	WorkspaceID string
	Trigger     string // "chat" | "cron" | "manual"
	ExitCode    *int
	Stdout      string
	Stderr      string
	// Transcript is the run's captured tool calls and coder turns, as JSON.
	// Populated only by GetAgentRun — ListAgentRuns and RecentAgentRuns leave it
	// empty rather than carry every run's full transcript into a list view.
	Transcript string
	// Silent records that the run emitted [SILENT]. Without it a deliberately
	// quiet run and a broken one are identically shaped here (exit 0, empty
	// stdout) and the interface can only render the same nothing for both.
	Silent bool
	// Token usage, populated by the API coder (direct LLM provider). Zero for
	// CLI coders (claude-code/opencode/codex/cursor), which don't report it.
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	// CachedTokens is the part of PromptTokens served from the provider's prompt
	// cache; CostUSD is what the provider said the run cost. The *Reported flags
	// separate "reported as zero" from "not reported at all" — a CLI coder
	// reports neither, and showing its runs as "$0.00" would read as free.
	CachedTokens  int
	CacheReported bool
	CostUSD       float64
	CostReported  bool
	StartedAt     time.Time
	FinishedAt    *time.Time
}

type Secret struct {
	ID          string
	WorkspaceID string
	Name        string
	Ciphertext  string
	Nonce       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Chat struct {
	ID          string
	WorkspaceID string
	AgentID     *string
	Name        string
	Platform    string
	Active      bool
	CreatedAt   time.Time
	LastSeen    time.Time
}

type ChatMessage struct {
	ID        int64
	ChatID    string
	Role      string // "user" or "assistant"
	Content   string
	CreatedAt time.Time
}

type Reminder struct {
	ID          string
	WorkspaceID string
	Message     string
	RemindAt    time.Time
	Recurrence  string
	Sent        bool
	CreatedAt   time.Time
}

// InboxMessage is one delivered notification in the cross-agent inbox feed: an
// agent run's actual output/error message (source "agent_run") or a fired
// reminder (source "reminder"). The body is exactly what was sent to the user.
type InboxMessage struct {
	ID          string
	WorkspaceID string
	Source      string // "agent_run" | "reminder"
	AgentID     string // empty for reminders
	AgentName   string // denormalized; empty for reminders
	RefID       string // run_id or reminder_id
	Trigger     string // cron|manual|chat; empty for reminders
	Body        string
	Status      string     // "ok" | "error"
	ReadAt      *time.Time // nil = unread
	CreatedAt   time.Time
}

// Unread reports whether the notification has not been read yet. Exposed for
// templates (Go's `not` builtin requires a bool, so a nil *time.Time can't be
// negated directly in a template).
func (m InboxMessage) Unread() bool { return m.ReadAt == nil }

type WorkspaceSetting struct {
	ID          string
	WorkspaceID string
	Key         string
	Value       string
	UpdatedAt   time.Time
}

// MCPServer is one Model Context Protocol server the owner added by URL.
//
// Nothing about an MCP server ships in the binary: unlike a connector, both the
// server AND its action list come from outside. The owner supplies URL + credential;
// the server itself supplies its tools (cached in mcp_tools). See internal/mcp.
type MCPServer struct {
	ID          string
	WorkspaceID string
	Name        string
	// Slug is the tool-name namespace — exposed tools are mcp__<slug>__<tool>. It is
	// ours rather than the server's serverInfo.name, which the MCP spec states is not
	// guaranteed unique across servers.
	Slug      string
	URL       string
	Transport string // "http"
	AuthKind  string // none|bearer|header
	// HeaderName is the header the credential is sent in when AuthKind is "header".
	HeaderName string
	// EncryptedToken is sealed with the SYSTEM key, not the workspace master password:
	// the background refresh and cron runs must decrypt it headlessly.
	EncryptedToken string
	Enabled        bool
	// Status is ACTIVE, NEEDS_AUTH or UNREACHABLE. Only a definitive 401 produces
	// NEEDS_AUTH; transport and 5xx failures produce UNREACHABLE, which neither
	// alerts nor removes the server from the retry path.
	Status        string
	LastError     string
	ToolsSyncedAt string
	// ToolsTTLMs is the server-declared catalog lifetime from tools/list, or 0 when
	// the server did not supply one (then the fixed default interval applies).
	ToolsTTLMs int
	ServerInfo string // JSON: serverInfo + negotiated protocol version
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MCPTool is one tool discovered from a server's tools/list.
//
// It is a cache of a remote response, but not a disposable one: ReadOnly,
// ApprovalMode and Enabled are authored by the owner and must survive a re-sync,
// which is why reconcile upserts on (server_id, name) instead of replacing the set.
type MCPTool struct {
	ID       string
	ServerID string
	// Name is the server's own tool name, verbatim — what tools/call is invoked with.
	// MCP permits dots and up to 128 characters here; an LLM tool name permits
	// neither, hence ToolName.
	Name string
	// ToolName is the slugged, truncated, uniqueness-suffixed name exposed to the
	// model. Stored rather than recomputed so an upstream rename cannot silently
	// re-point a name the model already learned mid-run.
	ToolName    string
	Title       string
	Description string
	InputSchema string
	// ReadOnly is seeded from the server's readOnlyHint annotation and then owned by
	// the owner. The MCP spec requires clients to treat annotations as untrusted, so
	// the hint is only a default; Execute's build-phase guard reads this field.
	ReadOnly     bool
	ApprovalMode string // auto|approve
	Enabled      bool
	// Missing marks a tool that vanished upstream. It is marked rather than deleted
	// so the owner's overrides survive a server restart.
	Missing   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AuditLog struct {
	ID          string
	WorkspaceID *string // active workspace context; owner is the implicit actor
	Action      string
	Target      string
	Detail      string
	IPAddress   string
	CreatedAt   time.Time
}

// AgentDraft is a persisted in-progress agent creation/edit session, used to
// resume after a page reload, browser close, or server restart. One per workspace.
type AgentDraft struct {
	WorkspaceID                string
	AgentID                    string // freshly-minted for create; existing agent's ID for edit
	AgentName                  string
	IsEdit                     bool
	State                      string // "designing" or "verifying"
	HistoryJSON                string
	PendingAgentMD             string
	PendingToolsJSON           string
	PendingUsedConnectionsJSON string
	// PendingUsedMCPServersJSON is the sibling of PendingUsedConnectionsJSON for MCP
	// auto-bind: the server ids a build actually called, so the binding survives a
	// restart or a resumed keep-as-is draft.
	PendingUsedMCPServersJSON string
	UpdatedAt                 time.Time
	ExpiresAt                 time.Time
}
