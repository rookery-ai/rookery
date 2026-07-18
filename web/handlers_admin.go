package web

import (
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/sandbox"
	"github.com/ilijad1/simple-agents/internal/secrets"
)

// allPermissions / validPermissions are the workspace-permission universe, shared
// with the JSON admin API (api_workspaces.go) which validates grants against them.
var allPermissions = []string{"bash", "web-browser", "system-tools", "mcp-servers"}
var validPermissions = allPermissions

// verifyWorkspaceMasterPassword decrypts the stored (system-key encrypted) master
// password and compares it to the supplied one. The stored form must remain (the
// scheduler decrypts it for headless cron runs), so this is an access gate, not the
// encryption key itself. Shared by the JSON API's enter-workspace endpoint.
func (s *Server) verifyWorkspaceMasterPassword(w *db.Workspace, password string) bool {
	if password == "" || w.EncryptedMasterPassword == "" {
		return false
	}
	stored, err := secrets.DecryptMasterPassword(w.EncryptedMasterPassword, s.systemKey)
	if err != nil {
		return false
	}
	return stored == password
}

// adminSettingsData is the system-settings view model, populated by loadAdminSettings
// and consumed by the JSON admin API (api_workspaces.go).
type adminSettingsData struct {
	ClaudeBin     string
	CoderTimeout  string
	AgentTimeout  string
	MemoryMB      string
	SandboxOn     bool // sandbox enabled in config
	LandlockReady bool // kernel actually supports Landlock
}

func (s *Server) loadAdminSettings() *adminSettingsData {
	get := func(key, fallback string) string {
		if v, err := s.db.GetSystemSetting(key); err == nil && v != "" {
			return v
		}
		return fallback
	}
	return &adminSettingsData{
		ClaudeBin:     get("claude_bin", "claude"),
		CoderTimeout:  get("coder_timeout", "120"),
		AgentTimeout:  get("agent_timeout", "300"),
		MemoryMB:      get("memory_mb", "256"),
		SandboxOn:     s.cfg.Sandbox.Enabled,
		LandlockReady: sandbox.Supported(),
	}
}
