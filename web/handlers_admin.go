package web

import (
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/sandbox"
	"github.com/rookery-ai/rookery/internal/secrets"
)

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

// adminSettingsData is the system-settings view model, populated by
// loadAdminSettings and consumed by the JSON admin API (api_workspaces.go).
//
// Both fields are runtime STATUS, read live from config and the kernel — there
// is deliberately nothing settable here. See apiAdminSettings for why the four
// former fields (claude_bin, coder_timeout, agent_timeout, memory_mb) went away.
type adminSettingsData struct {
	SandboxOn     bool // sandbox enabled in config
	LandlockReady bool // kernel actually supports Landlock
}

func (s *Server) loadAdminSettings() *adminSettingsData {
	return &adminSettingsData{
		SandboxOn:     s.cfg.Sandbox.Enabled,
		LandlockReady: sandbox.Supported(),
	}
}
