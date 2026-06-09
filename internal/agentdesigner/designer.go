package agentdesigner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ilijad1/simple-agents/internal/db"
)

// AgentDesigner handles file I/O and DB writes for completed agents.
type AgentDesigner struct {
	db        *db.DB
	agentsDir string // root dir: <data>/agents/
}

// NewDesigner creates an AgentDesigner.
func NewDesigner(database *db.DB, agentsDir string) *AgentDesigner {
	return &AgentDesigner{db: database, agentsDir: agentsDir}
}

// SaveAgent runs guardrails, writes agent files to disk, and inserts a DB row.
// Call this only after the user approves the generated code.
func (d *AgentDesigner) SaveAgent(userID, agentID, name, description, code string) error {
	if err := RunFullGuardrails(code, ""); err != nil {
		return fmt.Errorf("guardrails: %w", err)
	}

	dir := filepath.Join(d.agentsDir, userID, agentID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(code), 0o640); err != nil {
		return fmt.Errorf("write main.py: %w", err)
	}

	cfg := agentConfig{
		ID:          agentID,
		Name:        name,
		Description: description,
		EntryPoint:  "main.py",
		Version:     "1",
		CreatedAt:   time.Now().UTC(),
	}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfgData, 0o640); err != nil {
		return fmt.Errorf("write config.json: %w", err)
	}

	return d.db.CreateAgent(&db.Agent{
		ID:          agentID,
		UserID:      userID,
		Name:        name,
		Description: description,
		Active:      true,
	})
}

// AgentCodePath returns the absolute path to an agent's main.py.
func AgentCodePath(agentsDir, userID, agentID string) string {
	return filepath.Join(agentsDir, userID, agentID, "main.py")
}

// ─── Internal ─────────────────────────────────────────────────────────────────

type agentConfig struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	EntryPoint  string    `json:"entry_point"`
	Version     string    `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
}
