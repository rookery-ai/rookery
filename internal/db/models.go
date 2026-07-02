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
	CoderKind         string // "local" (a host CLI binary) or "api" (future: direct provider API)
	CoderBin          string // coder binary name/path when CoderKind == "local"
	CoderTimeoutS     int    // 0 = use system default
	CoderBackendType  string // '' = auto-detect, 'claude', or 'generic'
	CoderProvider     string // reserved for future API coder
	CoderModel        string // reserved for future API coder
	CoderAPIKeySecret string // reserved for future API coder (secret name)

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
	ID             string
	WorkspaceID    string
	Platform       string
	EncryptedToken string
	Active         bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
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
	StartedAt   time.Time
	FinishedAt  *time.Time
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
	WorkspaceID      string
	AgentID          string // freshly-minted for create; existing agent's ID for edit
	AgentName        string
	IsEdit           bool
	State            string // "designing" or "verifying"
	HistoryJSON      string
	PendingAgentMD   string
	PendingToolsJSON string
	UpdatedAt        time.Time
	ExpiresAt        time.Time
}
