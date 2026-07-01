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
	agentsDir string // vaults base: <data>/vaults/ (agent dirs at <base>/<userID>/agents/<agentID>)
}

// NewDesigner creates an AgentDesigner. agentsDir is the vaults base directory.
func NewDesigner(database *db.DB, agentsDir string) *AgentDesigner {
	return &AgentDesigner{db: database, agentsDir: agentsDir}
}

// SaveAgent writes a brand-new agent's files to disk and inserts a DB row.
// agentMD is the full AGENT.md content.
// tools is a map of filename→content for Python helper scripts (written to tools/).
// skills is a slice of skill names declared by the agent.
// requiredSecrets is a slice of secret names required by the agent.
func (d *AgentDesigner) SaveAgent(userID, agentID, name, description string, agentMD string, tools map[string]string, skills []string, requiredSecrets []string) error {
	if err := d.writeAgentContent(userID, agentID, name, agentMD, tools, skills, requiredSecrets); err != nil {
		return err
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

	// Persist the coder's declared skills to the DB (the source of truth for an
	// agent's skills). Must run after CreateAgent so the agents row exists for the
	// agent_skills FK.
	if err := d.db.SetAgentSkills(agentID, skills); err != nil {
		return fmt.Errorf("set agent skills: %w", err)
	}

	return nil
}

// UpdateAgent overwrites an existing agent's files in place and updates its DB row.
// Identity (ID, name) is immutable here — only content (AGENT.md, tools, manifest
// metadata, description) changes. Used by the edit flow's finalize step.
func (d *AgentDesigner) UpdateAgent(userID, agentID, name, description string, agentMD string, tools map[string]string, skills []string, requiredSecrets []string) error {
	if err := d.writeAgentContent(userID, agentID, name, agentMD, tools, skills, requiredSecrets); err != nil {
		return err
	}

	if err := d.db.UpdateAgentDescription(agentID, description); err != nil {
		return fmt.Errorf("db update: %w", err)
	}

	// Reconcile the agent's skill attachments in the DB (source of truth).
	if err := d.db.SetAgentSkills(agentID, skills); err != nil {
		return fmt.Errorf("set agent skills: %w", err)
	}

	return nil
}

// writeAgentContent is shared by SaveAgent and UpdateAgent: guardrails, file writes,
// and manifest. It never touches the agents DB row — callers do that themselves
// (INSERT vs UPDATE) since that's the only part that differs between create and edit.
func (d *AgentDesigner) writeAgentContent(userID, agentID, name string, agentMD string, tools map[string]string, skills []string, requiredSecrets []string) error {
	if err := CheckEthics(agentMD, ""); err != nil {
		return fmt.Errorf("guardrails: %w", err)
	}
	for filename, code := range tools {
		if err := RunToolGuardrails(filename, code); err != nil {
			return fmt.Errorf("guardrails (%s): %w", filename, err)
		}
	}

	dir := AgentDir(d.agentsDir, userID, agentID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	// Wipe and recreate tools/ so edits that remove a script take effect — the coder
	// always regenerates the full intended set, so the directory must reflect exactly
	// the incoming map, not a merge with whatever was there before.
	toolsDir := filepath.Join(dir, "tools")
	if err := os.RemoveAll(toolsDir); err != nil {
		return fmt.Errorf("clear tools dir: %w", err)
	}
	if err := os.MkdirAll(toolsDir, 0o750); err != nil {
		return fmt.Errorf("create tools dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o750); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte(agentMD), 0o640); err != nil {
		return fmt.Errorf("write AGENT.md: %w", err)
	}

	// Write the full project tree (nested dirs, tests, requirements.txt, …). The
	// map keys are paths relative to tools/; safety is enforced in WriteToolsTree.
	if err := WriteToolsTree(toolsDir, tools); err != nil {
		return err
	}

	// Write state.json only if it doesn't exist yet — never clobber a running agent's
	// persisted state, whether this is a fresh create or an edit of a live agent.
	statePath := filepath.Join(dir, "state.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		if err := os.WriteFile(statePath, []byte("{}"), 0o640); err != nil {
			return fmt.Errorf("write state.json: %w", err)
		}
	}

	// Preserve the original creation timestamp across edits.
	createdAt := time.Now().UTC()
	if existing, err := LoadManifest(d.agentsDir, userID, agentID); err == nil && existing != nil && !existing.CreatedAt.IsZero() {
		createdAt = existing.CreatedAt
	}

	manifest := &AgentManifest{
		ID:              agentID,
		Name:            name,
		RequiredSecrets: requiredSecrets,
		Skills:          nil, // skills live in the agent_skills DB table (source of truth); AGENT.md is for the LLM.
		CreatedAt:       createdAt,
	}
	if err := SaveManifest(d.agentsDir, userID, agentID, manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return nil
}
