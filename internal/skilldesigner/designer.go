package skilldesigner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rookery-ai/rookery/internal/agentdesigner"
	"github.com/rookery-ai/rookery/internal/db"
	"github.com/rookery-ai/rookery/internal/skilllibrary"
	"github.com/rookery-ai/rookery/internal/skillstore"
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

// SaveSkill writes a generated skill's SKILL.md plus its full generated tree
// (scripts/, references/, and any other files the build produced) to the user's
// vault and upserts its DB row. If a skill with the same name already exists for
// the user, its files are overwritten in place and the description is updated;
// the row ID is preserved. The skill name must not collide with a core skill —
// the caller (the creator flow) enforces that before reaching here.
func (s *SkillSaver) SaveSkill(workspaceID, name, description, skillMD string, scripts map[string]string) (*db.Skill, error) {
	if skilllibrary.IsCoreSkill(name) {
		return nil, fmt.Errorf("the name %q is reserved by a core skill; choose a different name", name)
	}
	if err := agentdesigner.CheckEthics(skillMD, ""); err != nil {
		return nil, fmt.Errorf("guardrails: %w", err)
	}
	for filename, code := range scripts {
		if err := guardrailsForGeneratedFile(filename, code); err != nil {
			return nil, fmt.Errorf("guardrails (%s): %w", filename, err)
		}
	}

	skillDir := skillstore.SkillDir(s.skillsDir, workspaceID, name)
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		return nil, fmt.Errorf("create skill dir: %w", err)
	}

	// Wipe and recreate the generated subtrees so a revision that drops a file takes
	// effect — the generated set is the full intended set, not a merge with the prior
	// one. SKILL.md is rewritten below; everything else lives under these dirs.
	for _, sub := range []string{"scripts", "references"} {
		if err := os.RemoveAll(filepath.Join(skillDir, sub)); err != nil {
			return nil, fmt.Errorf("clear %s dir: %w", sub, err)
		}
	}
	names := make([]string, 0, len(scripts))
	for n := range scripts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		// Reject any path escape; generated files must stay inside the skill dir.
		clean := filepath.Clean(n)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return nil, fmt.Errorf("unsafe skill file path: %s", n)
		}
		dest := filepath.Join(skillDir, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
			return nil, fmt.Errorf("create skill subdir: %w", err)
		}
		mode := os.FileMode(0o640)
		if strings.HasSuffix(clean, ".sh") {
			mode = 0o750 // a shell helper must be executable to be invokable
		}
		if err := os.WriteFile(dest, []byte(scripts[n]), mode); err != nil {
			return nil, fmt.Errorf("write skill file %s: %w", n, err)
		}
	}

	// Guarantee the frontmatter a built-in skill carries. The generation prompt
	// asks for every field, but a weak model that omits one must not produce a
	// skill the UI renders differently from a core one — and must not fail the
	// save either, which would lose the whole design conversation.
	skillMD = NormalizeFrontmatter(skillMD, name)

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
