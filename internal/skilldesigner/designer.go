package skilldesigner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/db"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
	"github.com/ilijad1/simple-agents/internal/skillstore"
)

// SkillSaver handles file I/O and DB writes for a completed skill. It mirrors
// agentdesigner.AgentDesigner but for skill folders: <vault>/skills/<name>/.
type SkillSaver struct {
	db        *db.DB
	skillsDir string // vaults base: <data>/vaults/ (skills at <base>/<workspaceID>/skills/<name>)
}

// NewSaver creates a SkillSaver. skillsDir is the vaults base directory.
func NewSaver(database *db.DB, skillsDir string) *SkillSaver {
	return &SkillSaver{db: database, skillsDir: skillsDir}
}

// SkillsDir returns the vaults base the saver writes under.
func (s *SkillSaver) SkillsDir() string { return s.skillsDir }

// SaveSkill writes a generated skill's SKILL.md (+ scripts/) to the user's vault
// and upserts its DB row. If a skill with the same name already exists for the
// user, its files are overwritten in place and the description is updated; the
// row ID is preserved. The skill name must not collide with a core skill — the
// caller (the creator flow) enforces that before reaching here.
func (s *SkillSaver) SaveSkill(workspaceID, name, description, skillMD string, scripts map[string]string) (*db.Skill, error) {
	if skilllibrary.IsCoreSkill(name) {
		return nil, fmt.Errorf("the name %q is reserved by a core skill; choose a different name", name)
	}
	if err := agentdesigner.CheckEthics(skillMD, ""); err != nil {
		return nil, fmt.Errorf("guardrails: %w", err)
	}
	for filename, code := range scripts {
		if err := agentdesigner.RunToolGuardrails(filename, code); err != nil {
			return nil, fmt.Errorf("guardrails (%s): %w", filename, err)
		}
	}

	skillDir := skillstore.SkillDir(s.skillsDir, workspaceID, name)
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		return nil, fmt.Errorf("create skill dir: %w", err)
	}

	// Wipe and recreate scripts/ so revisions that drop a script take effect —
	// the generated set is the full intended set, not a merge with the prior one.
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.RemoveAll(scriptsDir); err != nil {
		return nil, fmt.Errorf("clear scripts dir: %w", err)
	}
	if len(scripts) > 0 {
		if err := os.MkdirAll(scriptsDir, 0o750); err != nil {
			return nil, fmt.Errorf("create scripts dir: %w", err)
		}
		// Sort for deterministic write order; paths are relative to scripts/.
		names := make([]string, 0, len(scripts))
		for n := range scripts {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			// Reject any path escape; scripts must stay inside scripts/.
			clean := filepath.Clean(n)
			if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
				return nil, fmt.Errorf("unsafe script path: %s", n)
			}
			dest := filepath.Join(scriptsDir, clean)
			if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
				return nil, fmt.Errorf("create script subdir: %w", err)
			}
			if err := os.WriteFile(dest, []byte(scripts[n]), 0o640); err != nil {
				return nil, fmt.Errorf("write script %s: %w", n, err)
			}
		}
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o640); err != nil {
		return nil, fmt.Errorf("write SKILL.md: %w", err)
	}

	// Upsert the DB row: update description if the skill already exists, else insert.
	if existing, err := s.db.GetSkillByName(workspaceID, name); err == nil && existing != nil {
		if err := s.db.UpdateSkillDescription(existing.ID, description); err != nil {
			return nil, fmt.Errorf("db update skill: %w", err)
		}
		existing.Description = description
		return existing, nil
	}

	skill := &db.Skill{
		ID:          uuid.New().String(),
		WorkspaceID: workspaceID,
		Name:        name,
		Description: description,
		InstalledAt: time.Now().UTC(),
	}
	if err := s.db.CreateSkill(skill); err != nil {
		_ = os.RemoveAll(skillDir)
		return nil, fmt.Errorf("db insert skill: %w", err)
	}
	return skill, nil
}
