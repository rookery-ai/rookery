// Package skillstore manages per-user Agent Skills (agentskills.io format).
// Each skill is a directory containing SKILL.md plus optional scripts/, references/, assets/.
package skillstore

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ilijad1/simple-agents/internal/db"
)

var validSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

const maxZipExtractSize = 10 * 1024 * 1024 // 10 MB total extracted

// Store manages skills on disk and in the database.
type Store struct {
	db        *db.DB
	skillsDir string // vaults base: <data>/vaults (skills live at <base>/<workspaceID>/skills/<name>)
}

// New creates a Store. skillsDir is the vaults base directory.
func New(database *db.DB, skillsDir string) *Store {
	return &Store{db: database, skillsDir: skillsDir}
}

// SkillDir returns a single skill's directory inside the user's vault:
// <vaultsBase>/<workspaceID>/skills/<name>. Used by both this package and the agent
// runner so skill paths are computed in exactly one place.
func SkillDir(vaultsBase, workspaceID, name string) string {
	return filepath.Join(vaultsBase, workspaceID, "skills", name)
}

func (s *Store) skillDir(workspaceID, name string) string {
	return SkillDir(s.skillsDir, workspaceID, name)
}

// ParseSkillMeta extracts name and description from SKILL.md frontmatter.
// Handles both YAML-style ("key: value") and space/tab-delimited ("key    value") formats.
func ParseSkillMeta(content string) (name, description string) {
	text := strings.TrimSpace(content)
	if !strings.HasPrefix(text, "---") {
		return
	}
	rest := text[3:]
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return
	}
	front := rest[:endIdx]

	for _, line := range strings.Split(front, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var k, v string
		if idx := strings.Index(line, ": "); idx > 0 {
			// YAML-style: "key: value"
			k = strings.TrimSpace(line[:idx])
			v = strings.TrimSpace(line[idx+2:])
		} else {
			// Space/tab-delimited: "key    value"
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			k = fields[0]
			v = strings.TrimSpace(strings.TrimSpace(line)[len(fields[0]):])
		}
		switch strings.ToLower(k) {
		case "name":
			name = sanitizeSkillName(v)
		case "description":
			description = v
		}
	}
	return
}

// PeekZip reads the SKILL.md content and root folder name from a zip without extracting it.
func PeekZip(data []byte) (skillMD, rootFolder string, err error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", fmt.Errorf("invalid zip archive: %w", err)
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		slashed := filepath.ToSlash(f.Name)
		parts := strings.SplitN(slashed, "/", 2)

		var isSkillMD bool
		if len(parts) == 2 && strings.EqualFold(parts[1], "SKILL.md") {
			rootFolder = parts[0]
			isSkillMD = true
		} else if strings.EqualFold(slashed, "SKILL.md") {
			isSkillMD = true
		}

		if isSkillMD {
			rc, e := f.Open()
			if e != nil {
				return "", rootFolder, fmt.Errorf("open SKILL.md in zip: %w", e)
			}
			raw, e := io.ReadAll(io.LimitReader(rc, maxZipExtractSize))
			rc.Close()
			if e != nil {
				return "", rootFolder, e
			}
			return string(raw), rootFolder, nil
		}
	}
	return "", "", fmt.Errorf("SKILL.md not found in zip (expected at root or inside a single folder)")
}

// Create installs a new skill from a pasted SKILL.md. name and description must already be resolved.
func (s *Store) Create(workspaceID, name, description, content string) (*db.Skill, error) {
	if !validSkillName.MatchString(name) {
		return nil, fmt.Errorf("skill name %q is invalid (must be lowercase alphanumeric + hyphens, 3-64 chars)", name)
	}

	skillDir := s.skillDir(workspaceID, name)
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		return nil, fmt.Errorf("create skill dir: %w", err)
	}
	for _, sub := range []string{"scripts", "references", "assets"} {
		_ = os.MkdirAll(filepath.Join(skillDir, sub), 0o750)
	}

	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o640); err != nil {
		return nil, fmt.Errorf("write SKILL.md: %w", err)
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

// InstallFromZip extracts a zip archive and installs it as a skill.
// name and description must already be resolved by the caller.
func (s *Store) InstallFromZip(workspaceID, name, description string, data []byte) (*db.Skill, error) {
	if !validSkillName.MatchString(name) {
		return nil, fmt.Errorf("skill name %q is invalid (must be lowercase alphanumeric + hyphens, 3-64 chars)", name)
	}

	if existing, err := s.db.GetSkillByName(workspaceID, name); err == nil && existing != nil {
		return nil, fmt.Errorf("skill %q already exists", name)
	}

	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip archive: %w", err)
	}

	// Detect root prefix.
	var rootPrefix string
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.SplitN(filepath.ToSlash(f.Name), "/", 2)
		if len(parts) == 2 {
			rootPrefix = parts[0] + "/"
		}
		break
	}

	skillDir := s.skillDir(workspaceID, name)
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		return nil, fmt.Errorf("create skill dir: %w", err)
	}

	var totalSize int64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(filepath.ToSlash(f.Name), rootPrefix)
		if rel == "" {
			continue
		}

		clean := filepath.Clean(filepath.FromSlash(rel))
		if strings.HasPrefix(clean, "..") {
			continue
		}

		totalSize += int64(f.UncompressedSize64)
		if totalSize > maxZipExtractSize {
			_ = os.RemoveAll(skillDir)
			return nil, fmt.Errorf("zip exceeds 10 MB extracted size limit")
		}

		destPath := filepath.Join(skillDir, clean)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o750); err != nil {
			_ = os.RemoveAll(skillDir)
			return nil, fmt.Errorf("create dir: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			_ = os.RemoveAll(skillDir)
			return nil, err
		}
		fileData, err := io.ReadAll(io.LimitReader(rc, maxZipExtractSize))
		rc.Close()
		if err != nil {
			_ = os.RemoveAll(skillDir)
			return nil, err
		}
		if err := os.WriteFile(destPath, fileData, 0o640); err != nil {
			_ = os.RemoveAll(skillDir)
			return nil, err
		}
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

// LoadContent reads the SKILL.md file for a skill identified by name.
func (s *Store) LoadContent(workspaceID, skillName string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.skillDir(workspaceID, skillName), "SKILL.md"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("skill %q not found on disk", skillName)
		}
		return "", err
	}
	return string(data), nil
}

// SaveContent overwrites the SKILL.md file for a skill. Also updates description in DB.
func (s *Store) SaveContent(workspaceID, skillID, skillName, description, content string) error {
	skillDir := s.skillDir(workspaceID, skillName)
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o640); err != nil {
		return err
	}
	return s.db.UpdateSkillDescription(skillID, description)
}

// Delete removes a skill from DB and disk.
func (s *Store) Delete(workspaceID, skillID, skillName string) error {
	if err := s.db.DeleteSkill(skillID); err != nil {
		return err
	}
	// Drop any agent attachments referencing this skill by name so they don't dangle.
	_ = s.db.DeleteAgentSkillsByName(workspaceID, skillName)
	_ = os.RemoveAll(s.skillDir(workspaceID, skillName))
	return nil
}

// List returns all skills for a user (DB only, fast).
func (s *Store) List(workspaceID string) ([]*db.Skill, error) {
	return s.db.ListSkills(workspaceID)
}

// SanitizeName lowercases and replaces spaces/underscores with hyphens.
func SanitizeName(s string) string { return sanitizeSkillName(s) }

func sanitizeSkillName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}
