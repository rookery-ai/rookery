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

type WorkspacePermission struct {
	ID          string
	WorkspaceID string
	Permission  string
	GrantedBy   string
	GrantedAt   time.Time
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
}

type AgentRun struct {
	ID          string
	AgentID     string
	WorkspaceID string
	Trigger     string // "chat" | "cron" | "manual"
	ExitCode    *int
	Stdout      string
	Stderr      string
	// Token usage, populated by the API coder (direct LLM provider). Zero for
	// CLI coders (claude-code/opencode/codex/cursor), which don't report it.
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	StartedAt        time.Time
	FinishedAt       *time.Time
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

type MCPServer struct {
	ID          string
	WorkspaceID string
	Name        string
	URL         string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
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
	UpdatedAt                  time.Time
	ExpiresAt                  time.Time
}
