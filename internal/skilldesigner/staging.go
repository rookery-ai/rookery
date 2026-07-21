package skilldesigner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
)

// ErrNoSkillMD means the build produced no SKILL.md the flow can identify — either none
// exists, or several nested candidates make the choice ambiguous.
var ErrNoSkillMD = errors.New("no SKILL.md found in the staging dir")

// LocateSkillRoot finds the directory that holds the generated SKILL.md.
//
// The prompt tells the model to write SKILL.md at the root of its working directory, but
// a weak model sometimes nests it one level down under a folder named after the skill —
// because that IS the published layout (<name>/SKILL.md) that skill-creator documents.
// Discarding an otherwise valid build over a directory level is the wrong trade, so a
// single unambiguous nested candidate is accepted and its directory becomes the skill
// root. Zero candidates, or more than one, is a soft failure: guessing which of two
// skills the user meant would be worse than asking.
func LocateSkillRoot(stagingDir string) (string, error) {
	if fi, err := os.Stat(filepath.Join(stagingDir, "SKILL.md")); err == nil && !fi.IsDir() {
		return stagingDir, nil
	}

	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return "", ErrNoSkillMD
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(stagingDir, e.Name())
		if fi, err := os.Stat(filepath.Join(child, "SKILL.md")); err == nil && !fi.IsDir() {
			found = append(found, child)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", ErrNoSkillMD
}

// ReadSkillTree reads every shipping file under the skill root, keyed by its
// forward-slash path relative to that root. SKILL.md is excluded (the caller reads it
// separately) and build-time test artifacts are dropped.
//
// The old reader took only top-level scripts/*.py, so a .sh helper, a nested library
// module, or a references/ doc was silently lost between the staging dir and the saved
// skill. The skill format allows all three, so the whole tree travels.
func ReadSkillTree(skillRoot string) (map[string]string, error) {
	out := map[string]string{}
	scriptRoot := filepath.Join(skillRoot, "scripts")

	err := filepath.WalkDir(skillRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip dotfile dirs (e.g. the API engine's .sa_out spill dir).
			if path != skillRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := filepath.Rel(skillRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" {
			return nil
		}
		if agentdesigner.IsTestArtifact(path, d.Name(), scriptRoot) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
