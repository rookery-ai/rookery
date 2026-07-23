package web

import (
	"strings"
	"testing"
)

// TestAPIParityInventory is the sub-plan-1 merge gate: every planned /api/v1
// route from the spec's §12 inventory must be registered. Adding a UI surface
// in later sub-plans without its API row failing here first is a process bug.
func TestAPIParityInventory(t *testing.T) {
	s, _ := newAPITestServer(t)
	want := []string{
		"GET /api/v1/auth/session", "POST /api/v1/auth/login", "POST /api/v1/auth/logout",
		"POST /api/v1/auth/change-password",
		"GET /api/v1/workspaces", "POST /api/v1/workspaces", "POST /api/v1/workspaces/:id/enter",
		"POST /api/v1/workspaces/leave", "DELETE /api/v1/workspaces/:id",
		"GET /api/v1/workspaces/:id/permissions", "PUT /api/v1/workspaces/:id/permissions",
		"GET /api/v1/admin/overview", "GET /api/v1/admin/audit",
		"GET /api/v1/admin/settings", "PUT /api/v1/admin/settings",
		"GET /api/v1/agents", "GET /api/v1/agents/:id", "DELETE /api/v1/agents/:id",
		"POST /api/v1/agents/:id/run", "GET /api/v1/agents/:id/run/progress",
		"PUT /api/v1/agents/:id/schedule", "DELETE /api/v1/agents/:id/schedule",
		"PUT /api/v1/agents/:id/agent-md", "PUT /api/v1/agents/:id/skills",
		"PUT /api/v1/agents/:id/connections",
		"POST /api/v1/agents/design", "POST /api/v1/agents/design/cancel",
		"POST /api/v1/agents/design/resume", "POST /api/v1/agents/design/dismiss",
		"GET /api/v1/agents/design/progress", "GET /api/v1/agents/design/state",
		"POST /api/v1/agents/:id/edit/start",
		"GET /api/v1/skills", "POST /api/v1/skills", "GET /api/v1/skills/core/:slug",
		"GET /api/v1/skills/:id", "PUT /api/v1/skills/:id", "DELETE /api/v1/skills/:id",
		"POST /api/v1/skills/design", "POST /api/v1/skills/design/cancel",
		"POST /api/v1/skills/design/resume", "POST /api/v1/skills/design/dismiss",
		"GET /api/v1/skills/design/progress",
		"GET /api/v1/secrets", "POST /api/v1/secrets", "DELETE /api/v1/secrets/:name",
		"GET /api/v1/search-keys", "PUT /api/v1/search-keys", "DELETE /api/v1/search-keys/:provider",
		"GET /api/v1/connectors", "POST /api/v1/connectors",
		"DELETE /api/v1/connectors/:platform", "POST /api/v1/connectors/:platform/test",
		"GET /api/v1/services", "POST /api/v1/services/:provider/creds",
		"POST /api/v1/services/:provider/connect", "POST /api/v1/services/:provider/apikey",
		"DELETE /api/v1/services/:id",
		"GET /api/v1/chats", "POST /api/v1/chats", "GET /api/v1/chats/:id",
		"POST /api/v1/chats/:id/messages", "POST /api/v1/chats/:id/resume",
		"POST /api/v1/chats/:id/stop", "DELETE /api/v1/chats/:id",
		"GET /api/v1/reminders", "POST /api/v1/reminders", "DELETE /api/v1/reminders/:id",
		"GET /api/v1/reminders/poll",
		"GET /api/v1/inbox", "GET /api/v1/inbox/poll", "POST /api/v1/inbox/:id/read",
		"POST /api/v1/inbox/read-all", "DELETE /api/v1/inbox/:id",
		"GET /api/v1/kb/tree", "GET /api/v1/kb/note", "PUT /api/v1/kb/note",
		"POST /api/v1/kb/new", "DELETE /api/v1/kb/note", "POST /api/v1/kb/rename",
		"GET /api/v1/kb/search", "GET /api/v1/kb/resolve", "GET /api/v1/kb/raw",
		"PUT /api/v1/kb/order", "POST /api/v1/kb/upload",
		"PUT /api/v1/kb/icon", "GET /api/v1/kb/folders",
		"GET /api/v1/kb/export", "GET /api/v1/kb/export/formats",
		"POST /api/v1/kb/asset", "GET /api/v1/kb/assets",
		"GET /api/v1/settings", "PUT /api/v1/settings/profile", "PUT /api/v1/settings/workspace",
		"PUT /api/v1/settings/coder", "POST /api/v1/settings/coder/test",
		"PUT /api/v1/settings/master-password",
		"GET /api/v1/setup", "POST /api/v1/setup",
		"GET /api/v1/search",
		"GET /api/v1/dashboard",
	}
	have := make(map[string]bool)
	for _, r := range s.echo.Routes() {
		have[r.Method+" "+r.Path] = true
	}
	var missing []string
	for _, w := range want {
		if !have[w] {
			missing = append(missing, w)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing %d /api/v1 routes:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
