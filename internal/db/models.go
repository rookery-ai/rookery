package db

import "time"

type User struct {
	ID                      string
	Username                string
	PasswordHash            string
	Role                    string // "admin" | "user"
	EncryptedMasterPassword string
	SecretsSalt             string
	NeedsSetup              bool
	MustChangePassword      bool
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type UserPermission struct {
	ID         string
	UserID     string
	Permission string
	GrantedBy  string
	GrantedAt  time.Time
}

type PlatformConnection struct {
	ID             string
	UserID         string
	Platform       string
	EncryptedToken string
	Active         bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type PlatformIdentity struct {
	ID             string
	UserID         string
	Platform       string
	PlatformUserID string
	LinkedAt       time.Time
}

type Agent struct {
	ID          string
	UserID      string
	Name        string
	Description string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AgentSchedule struct {
	ID         string
	AgentID    string
	UserID     string
	CronExpr   string
	NextRunAt  *time.Time
	LastRunAt  *time.Time
	Enabled    bool
	CreatedAt  time.Time
}

type AgentRun struct {
	ID         string
	AgentID    string
	UserID     string
	Trigger    string // "chat" | "cron" | "manual"
	ExitCode   *int
	Stdout     string
	Stderr     string
	StartedAt  time.Time
	FinishedAt *time.Time
}

type Secret struct {
	ID         string
	UserID     string
	Name       string
	Ciphertext string
	Nonce      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ChatSession struct {
	ID        string
	UserID    string
	AgentID   *string
	Name      string
	Platform  string
	Active    bool
	CreatedAt time.Time
	LastSeen  time.Time
}

type Reminder struct {
	ID         string
	UserID     string
	Message    string
	RemindAt   time.Time
	Recurrence string
	Sent       bool
	CreatedAt  time.Time
}

type UserSetting struct {
	ID        string
	UserID    string
	Key       string
	Value     string
	UpdatedAt time.Time
}

type MCPServer struct {
	ID        string
	UserID    string
	Name      string
	URL       string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AuditLog struct {
	ID        string
	UserID    *string
	Action    string
	Target    string
	Detail    string
	IPAddress string
	CreatedAt time.Time
}
