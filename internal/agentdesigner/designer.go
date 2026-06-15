package agentdesigner

import (
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

// SaveAgent writes agent files to disk and inserts a DB row.
// agentMD is the full AGENT.md content.
// tools is a map of filename→content for Python helper scripts (written to tools/).
// skills is a slice of skill names declared by the agent.
// requiredSecrets is a slice of secret names required by the agent.
func (d *AgentDesigner) SaveAgent(userID, agentID, name, description string, agentMD string, tools map[string]string, skills []string, requiredSecrets []string) error {
	if err := CheckEthics(agentMD, ""); err != nil {
		return fmt.Errorf("guardrails: %w", err)
	}
	for filename, code := range tools {
		if err := RunFullGuardrails(code, ""); err != nil {
			return fmt.Errorf("guardrails (%s): %w", filename, err)
		}
	}

	dir := filepath.Join(d.agentsDir, userID, agentID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o750); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o750); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(agentMD), 0o640); err != nil {
		return fmt.Errorf("write AGENT.md: %w", err)
	}

	for filename, code := range tools {
		dest := filepath.Join(dir, "tools", filepath.Base(filename))
		if err := os.WriteFile(dest, []byte(code), 0o640); err != nil {
			return fmt.Errorf("write tool %s: %w", filename, err)
		}
	}

	// Write state.json only if it doesn't exist yet.
	statePath := filepath.Join(dir, "state.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		if err := os.WriteFile(statePath, []byte("{}"), 0o640); err != nil {
			return fmt.Errorf("write state.json: %w", err)
		}
	}

	manifest := &AgentManifest{
		ID:              agentID,
		Name:            name,
		RequiredSecrets: requiredSecrets,
		Skills:          skills,
		CreatedAt:       time.Now().UTC(),
	}
	if err := SaveManifest(d.agentsDir, userID, agentID, manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	if err := d.db.CreateAgent(&db.Agent{
		ID:          agentID,
		UserID:      userID,
		Name:        name,
		Description: description,
		Active:      true,
	}); err != nil {
		return fmt.Errorf("db insert: %w", err)
	}

	return nil
}
