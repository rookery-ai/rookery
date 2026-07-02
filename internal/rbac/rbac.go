package rbac

import "github.com/ilijad1/simple-agents/internal/db"

// Known permissions.
const (
	PermBash         = "bash"
	PermWebBrowser   = "web-browser"
	PermSystemTools  = "system-tools"
	PermMCPServers   = "mcp-servers"
)

// CanPerform returns true if the user has been explicitly granted the given permission.
func CanPerform(database *db.DB, workspaceID, permission string) bool {
	perms, err := database.ListPermissions(workspaceID)
	if err != nil {
		return false
	}
	for _, p := range perms {
		if p == permission {
			return true
		}
	}
	return false
}
