package skilllibrary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ilijad1/simple-agents/internal/agentdesigner"
	"github.com/ilijad1/simple-agents/internal/skilllibrary"
	"github.com/stretchr/testify/require"
)

const skillsRoot = "skills"

// minDescriptionLen is the shortest description that can plausibly state BOTH what a
// skill does and the phrases that trigger it. Shorter than this and the skill is
// effectively invisible to the designer and to SelectSkills, which match on exactly
// this text. Raise a failing skill's description; do not lower this floor.
const minDescriptionLen = 80

// Every embedded core skill must parse, be self-consistent, and hold to the same bar as
// a user-generated skill. This is the guard against the drift that shipped pdf/ and docx/
// with a documented scripts/ directory that did not exist.
func TestCoreCatalogInvariants(t *testing.T) {
	entries, err := os.ReadDir(skillsRoot)
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := e.Name()
		t.Run(dir, func(t *testing.T) {
			content, ok := skilllibrary.CoreSkillContent(dir)
			require.True(t, ok, "skill dir %q is not loadable via CoreSkillContent", dir)

			// ParseMeta returns (SkillMeta, body) — it does not error; an unparseable
			// frontmatter surfaces as an empty Name, which the next assertion catches.
			meta, body := skilllibrary.ParseMeta(content)
			require.NotEmpty(t, strings.TrimSpace(body), "SKILL.md must have a body, not just frontmatter")
			require.NotEmpty(t, meta.Name, "name is required")
			require.NotEmpty(t, meta.Description, "description is required")
			require.Equal(t, dir, meta.Name,
				"frontmatter name must equal the directory name (agent_skills is keyed by name)")
			require.False(t, seen[meta.Name], "duplicate skill name %q", meta.Name)
			seen[meta.Name] = true

			// A description is the trigger signal — a one-liner with no trigger phrases
			// makes the skill invisible to both the designer and the selector. The floor
			// is named rather than inline so the intent survives the next reader.
			require.GreaterOrEqual(t, len(meta.Description), minDescriptionLen,
				"description must state what the skill does AND when it triggers")

			// Every scripts/ path the body references must exist.
			for _, ref := range referencedScriptPaths(content) {
				_, statErr := os.Stat(filepath.Join(skillsRoot, dir, ref))
				require.NoError(t, statErr, "SKILL.md references %q but it does not exist", ref)
			}

			// Every shipped script must pass the skill guardrail profile.
			scriptsDir := filepath.Join(skillsRoot, dir, "scripts")
			walkErr := filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					if os.IsNotExist(err) {
						return nil // no scripts/ is fine
					}
					return err
				}
				if info.IsDir() {
					return nil
				}
				data, readErr := os.ReadFile(path)
				require.NoError(t, readErr)
				require.NoError(t,
					agentdesigner.RunToolGuardrails(info.Name(), string(data), agentdesigner.ProfileSkillScript),
					"shipped script %q must pass the skill guardrail profile", path)
				return nil
			})
			if !os.IsNotExist(walkErr) {
				require.NoError(t, walkErr)
			}
		})
	}

	require.NotEmpty(t, seen, "the catalog must not be empty")
}

// referencedScriptPaths finds scripts/<file> paths mentioned in a SKILL.md body.
func referencedScriptPaths(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, field := range strings.Fields(content) {
		f := strings.Trim(field, "`'\"(),.:;*")
		if !strings.HasPrefix(f, "scripts/") || strings.HasSuffix(f, "/") {
			continue
		}
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// LoadBundled must agree with what is on disk.
func TestLoadBundledMatchesDisk(t *testing.T) {
	entries, err := os.ReadDir(skillsRoot)
	require.NoError(t, err)
	onDisk := 0
	for _, e := range entries {
		if e.IsDir() {
			onDisk++
		}
	}
	require.Len(t, skilllibrary.LoadBundled(), onDisk)
}
