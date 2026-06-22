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
// <vaultsBase>/<userID>/agents/<agentID>. All other agent path helpers build on
// this. The "agents" segment scopes each agent's writable area within the vault.
func AgentDir(vaultsBase, userID, agentID string) string {
	return filepath.Join(vaultsBase, userID, "agents", agentID)
}

// LoadManifest loads an agent's manifest from agent.json.
// Falls back to a minimal manifest synthesised from config.json for legacy python agents.
func LoadManifest(vaultsBase, userID, agentID string) (*AgentManifest, error) {
	dir := AgentDir(vaultsBase, userID, agentID)

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
func SaveManifest(vaultsBase, userID, agentID string, m *AgentManifest) error {
	dir := AgentDir(vaultsBase, userID, agentID)
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
func AgentDescPath(vaultsBase, userID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, userID, agentID), "AGENT.md")
}

// AgentMDPath is an alias for AgentDescPath kept for callers not yet updated.
// Deprecated: use AgentDescPath.
func AgentMDPath(vaultsBase, userID, agentID string) string {
	return AgentDescPath(vaultsBase, userID, agentID)
}

// AgentStatePath returns the path to an agent's state.json file.
func AgentStatePath(vaultsBase, userID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, userID, agentID), "state.json")
}

// AgentLogsDir returns the path to an agent's logs directory.
func AgentLogsDir(vaultsBase, userID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, userID, agentID), "logs")
}

// AgentLogPath returns a timestamped log file path for a new run. Run logs are
// markdown notes so they render in the knowledge-base UI like any other note.
func AgentLogPath(vaultsBase, userID, agentID string, t time.Time) string {
	return filepath.Join(AgentDir(vaultsBase, userID, agentID), "logs",
		"run_log_"+t.UTC().Format("20060102_150405")+".md")
}

// AgentCodePath returns the absolute path to an agent's main.py (legacy python agents only).
func AgentCodePath(vaultsBase, userID, agentID string) string {
	return filepath.Join(AgentDir(vaultsBase, userID, agentID), "main.py")
}
