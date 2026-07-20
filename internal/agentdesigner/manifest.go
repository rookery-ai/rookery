package agentdesigner

import (
	"path/filepath"
	"time"
)

// AgentDir returns an agent's own directory inside the user's vault:
// <vaultsBase>/<workspaceID>/agents/<agentID>. All other agent path helpers build on
// this. The "agents" segment scopes each agent's writable area within the vault.
func AgentDir(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(vaultsBase, workspaceID, "agents", agentID)
}

// DraftAgentDir returns the working directory for a create-mode agent that is
// still being designed/built (before finalize). It is named draft_<slug> from the
// agent's NAME — not the opaque agent UUID — so a work-in-progress agent is
// recognizable in the KB browser. The dir is kept there until the user finalizes
// the agent (finalize reconstitutes the real agent at AgentDir(<uuid>) from the
// captured content and removes this dir), discards the draft, or deletes the
// agent — a failed/blocked build never removes it. The slug is stable for a given
// name so a resumed draft's next generation iterates in the same dir.
func DraftAgentDir(vaultsBase, workspaceID, agentName string) string {
	return filepath.Join(vaultsBase, workspaceID, "agents", "draft_"+slugifyAgentName(agentName))
}

// AgentDescPath returns the path to an agent's AGENT.md description file.
func AgentDescPath(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "AGENT.md")
}

// AgentMDPath is an alias for AgentDescPath kept for callers not yet updated.
// Deprecated: use AgentDescPath.
func AgentMDPath(vaultsBase, workspaceID, agentID string) string {
	return AgentDescPath(vaultsBase, workspaceID, agentID)
}

// AgentLogsDir returns the path to an agent's logs directory.
func AgentLogsDir(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "logs")
}

// AgentLogPath returns a timestamped log file path for a new run. Run logs are
// markdown notes so they render in the knowledge-base UI like any other note.
func AgentLogPath(vaultsBase, workspaceID, agentID string, t time.Time) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "logs",
		"run_log_"+t.UTC().Format("20060102_150405")+".md")
}

// AgentCodePath returns the absolute path to an agent's main.py (legacy python agents only).
func AgentCodePath(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "main.py")
}

// manifestIsFallbackBloat reports whether a skill list carries the legacy
// "fallback to all installed skills" signature: it contains every core skill
// name. A curated subset never matches.
//
// Used by MigrateAgentFilesToMarkdown (migrate_files.go) when reconciling a
// legacy agent.json's Skills field into the agent_skills DB table during the
// one-time state.json→state.md / agent.json-deletion migration. The manifest
// type this originally guarded (AgentManifest/ReconcileSkillAttachmentsToDB)
// is gone — that one-time reconciliation is now absorbed into the migration.
func manifestIsFallbackBloat(skills []string, coreSet map[string]bool) bool {
	if len(coreSet) == 0 {
		return false
	}
	present := make(map[string]bool, len(skills))
	for _, s := range skills {
		present[s] = true
	}
	for name := range coreSet {
		if !present[name] {
			return false
		}
	}
	return true
}

// skillDB is the DB surface the agent_skills reconciliation needs (used by
// MigrateAgentFilesToMarkdown in migrate_files.go) — a tiny slice of *db.DB so
// the agentdesigner package doesn't need a wider dependency.
type skillDB interface {
	ListAgentSkillNames(agentID string) ([]string, error)
	SetAgentSkills(agentID string, skillNames []string) error
}
