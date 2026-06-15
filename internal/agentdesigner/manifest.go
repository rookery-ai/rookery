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

// LoadManifest loads an agent's manifest from agent.json.
// Falls back to a minimal manifest synthesised from config.json for legacy python agents.
func LoadManifest(agentsDir, userID, agentID string) (*AgentManifest, error) {
	dir := filepath.Join(agentsDir, userID, agentID)

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
func SaveManifest(agentsDir, userID, agentID string, m *AgentManifest) error {
	dir := filepath.Join(agentsDir, userID, agentID)
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
func AgentDescPath(agentsDir, userID, agentID string) string {
	return filepath.Join(agentsDir, userID, agentID, "AGENT.md")
}

// AgentMDPath is an alias for AgentDescPath kept for callers not yet updated.
// Deprecated: use AgentDescPath.
func AgentMDPath(agentsDir, userID, agentID string) string {
	return AgentDescPath(agentsDir, userID, agentID)
}

// AgentStatePath returns the path to an agent's state.json file.
func AgentStatePath(agentsDir, userID, agentID string) string {
	return filepath.Join(agentsDir, userID, agentID, "state.json")
}

// AgentLogsDir returns the path to an agent's logs directory.
func AgentLogsDir(agentsDir, userID, agentID string) string {
	return filepath.Join(agentsDir, userID, agentID, "logs")
}

// AgentLogPath returns a timestamped log file path for a new run.
func AgentLogPath(agentsDir, userID, agentID string, t time.Time) string {
	return filepath.Join(agentsDir, userID, agentID, "logs",
		"run_log_"+t.UTC().Format("20060102_150405")+".txt")
}

// AgentCodePath returns the absolute path to an agent's main.py (legacy python agents only).
func AgentCodePath(agentsDir, userID, agentID string) string {
	return filepath.Join(agentsDir, userID, agentID, "main.py")
}
