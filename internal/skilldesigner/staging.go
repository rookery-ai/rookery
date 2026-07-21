package skilldesigner

import (
	"errors"
	"os"
	"path/filepath"
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
