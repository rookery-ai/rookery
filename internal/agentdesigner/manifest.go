package agentdesigner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// AgentManifest describes an agent's secrets and skill dependencies.
// Written to agent.json; no type field — all agents use the coder path.
type AgentManifest struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	RequiredSecrets []string  `json:"required_secrets"` // for UI warnings
	Skills          []string  `json:"skills"`           // skill names declared by this agent
	CreatedAt       time.Time `json:"created_at"`
}

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

// LoadManifest loads an agent's manifest from agent.json.
// Falls back to a minimal manifest synthesised from config.json for legacy python agents.
func LoadManifest(vaultsBase, workspaceID, agentID string) (*AgentManifest, error) {
	dir := AgentDir(vaultsBase, workspaceID, agentID)

	data, err := os.ReadFile(filepath.Join(dir, "agent.json"))
	if err == nil {
		var m AgentManifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		return &m, nil
	}

	// Fall back to legacy config.json (old python agents).
	data, err = os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("no manifest found (agent.json or config.json)")
		}
		return nil, err
	}

	var legacy struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}

	return &AgentManifest{
		ID:        legacy.ID,
		Name:      legacy.Name,
		CreatedAt: legacy.CreatedAt,
	}, nil
}

// SaveManifest writes agent.json to the agent's directory.
func SaveManifest(vaultsBase, workspaceID, agentID string, m *AgentManifest) error {
	dir := AgentDir(vaultsBase, workspaceID, agentID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "agent.json"), data, 0o640)
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

// AgentStatePath returns the path to an agent's state.json file.
func AgentStatePath(vaultsBase, workspaceID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, workspaceID, agentID), "state.json")
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

// ReconcileSkillAttachmentsToDB is a one-time cutover: it makes the agent_skills
// DB table the single source of truth for an agent's skills and empties the
// legacy manifest.Skills field (AGENT.md is for the LLM, not the skill record).
//
// Per agent, only when the DB has no skill rows for that agent yet (so it never
// overwrites skills the designer/manual-handler already wrote to the DB):
//
//   - If manifest.Skills is a curated list (a real subset), copy it into the DB.
//   - If manifest.Skills carries the legacy "fallback to all installed skills"
//     signature (it contains every core skill name — produced by the old
//     designer behaviour when AGENT.md declared no "# Skills:" line), the agent
//     declared no skills, so the DB is left empty.
//
// Then manifest.Skills is cleared on disk. Idempotent: once manifest.Skills is
// empty, subsequent runs do nothing (the DB already holds the result).
func ReconcileSkillAttachmentsToDB(database skillDB, vaultsBase string, coreSkillNames []string) (int, error) {
	coreSet := make(map[string]bool, len(coreSkillNames))
	for _, n := range coreSkillNames {
		coreSet[n] = true
	}

	userDirs, err := os.ReadDir(vaultsBase)
	if err != nil {
		return 0, err
	}

	reconciled := 0
	for _, ud := range userDirs {
		if !ud.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(vaultsBase, ud.Name(), "agents"))
		if err != nil {
			continue // no agents dir for this user
		}
		for _, ed := range entries {
			if !ed.IsDir() {
				continue
			}
			agentID := ed.Name()

			m, _ := LoadManifest(vaultsBase, ud.Name(), agentID)
			if m == nil || len(m.Skills) == 0 {
				continue // nothing to migrate
			}

			// Only seed the DB when it has no rows for this agent — never overwrite
			// attachments the designer or manual handler already persisted.
			existing, _ := database.ListAgentSkillNames(agentID)
			if len(existing) == 0 {
				if !manifestIsFallbackBloat(m.Skills, coreSet) {
					_ = database.SetAgentSkills(agentID, m.Skills)
					reconciled++
				}
			}

			// Clear the legacy field so the DB is the only source going forward.
			m.Skills = nil
			_ = SaveManifest(vaultsBase, ud.Name(), agentID, m)
		}
	}
	return reconciled, nil
}

// manifestIsFallbackBloat reports whether a skill list carries the legacy
// "fallback to all installed skills" signature: it contains every core skill
// name. A curated subset never matches.
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

// skillDB is the DB surface ReconcileSkillAttachmentsToDB needs — a tiny slice
// of *db.DB so the agentdesigner package doesn't import internal/db (which would
// be a cycle: db → agentdesigner? it doesn't, but keeping it loose avoids any
// future import churn).
type skillDB interface {
	ListAgentSkillNames(agentID string) ([]string, error)
	SetAgentSkills(agentID string, skillNames []string) error
}
