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

			// Every scripts/ path the body references must exist — except in the two
			// meta skills, whose entire subject IS the skill format. skill-creator and
			// skill-vetter show example paths for the skill the reader is authoring
			// ("write scripts/extract.py"), not files they ship themselves. Without this
			// carve-out the check forces them to teach with filenames elided, which makes
			// the one prompt that shapes every generated skill worse at its job.
			for _, ref := range referencedScriptPaths(content) {
				if dir == "skill-creator" || dir == "skill-vetter" {
					break
				}
				_, statErr := os.Stat(filepath.Join(skillsRoot, dir, ref))
				require.NoError(t, statErr, "SKILL.md references %q but it does not exist", ref)
			}

			// Every shipped script must pass the skill guardrail profile.
			//
			// NOTE: this loop currently iterates nothing. TestCoreSkillsShipNoScripts
			// guarantees no core skill has a scripts/ dir, so the check is aspirational —
			// it exists so that if runtime materialization is ever built and core skills
			// start shipping scripts, they are held to the same bar as user skills from
			// the first commit. The widened ProfileSkillScript has no dogfood in the
			// shipped catalog today; it is covered by unit tests only.
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

// TestCoreSkillsShipNoScripts pins an architectural constraint that is invisible from the
// source tree alone.
//
// A core skill reaches an agent through skilllibrary.CoreSkillContent, which returns the
// embedded SKILL.md text and nothing else — agentrunner.loadDeclaredSkillContent has no
// path that materializes a core skill's scripts/ onto disk, and this package's own docs
// state there is "no disk seeding". So a shipped scripts/helper.py is dead weight: the
// body could tell an agent to run `python3 scripts/helper.py`, and at run time that file
// would not exist in its working directory.
//
// User skills are the opposite — they live in the vault on disk, scripts included — which
// is why the skill guardrails still validate script content.
//
// Core skills must therefore teach through inline commands and snippets the agent can run
// or adapt directly. Lifting this restriction means materializing the embedded scripts at
// run time; until that exists, shipping them would promise a file that never arrives.
func TestCoreSkillsShipNoScripts(t *testing.T) {
	entries, err := os.ReadDir(skillsRoot)
	require.NoError(t, err)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		scriptsDir := filepath.Join(skillsRoot, e.Name(), "scripts")
		if _, statErr := os.Stat(scriptsDir); statErr == nil {
			t.Errorf("core skill %q ships a scripts/ directory, but core-skill scripts are "+
				"never written to disk at run time — teach through inline snippets instead, "+
				"or implement runtime materialization first", e.Name())
		}
	}
}

// ── metadata shape ──────────────────────────────────────────────────────────

// The canonical frontmatter puts a skill's requirements DIRECTLY under
// metadata — `metadata.requires` and, as its SIBLING, `metadata.install`.
// There is no vendor segment between them.
func TestParseMetaFlatForm(t *testing.T) {
	meta, body := skilllibrary.ParseMeta(`---
name: flat
description: a skill declaring its requirements under metadata directly
metadata:
  requires:
    bins: [pdfinfo, pdftotext]
    anyBins: [pdftotext, pandoc]
    env: [SOME_API_KEY]
  install:
    - kind: binary
      bin: pdfinfo
      url: https://example.test/poppler.tar.gz
      strip: 1
    - kind: pip
      package: pdfplumber
---

Body text.`)

	require.Equal(t, "flat", meta.Name)
	require.Equal(t, []string{"pdfinfo", "pdftotext"}, meta.RequiresBins)
	require.Equal(t, []string{"pdftotext", "pandoc"}, meta.AnyBins)
	require.Equal(t, []string{"SOME_API_KEY"}, meta.RequiresEnv)
	require.Len(t, meta.Install, 2)
	require.Equal(t, "binary", meta.Install[0].Kind)
	require.Equal(t, "pdfinfo", meta.Install[0].Bin)
	require.Equal(t, 1, meta.Install[0].Strip)
	require.Equal(t, "pdfplumber", meta.Install[1].Package)
	require.Equal(t, "Body text.", body)
}

// A skill imported from ClawHub carries the legacy metadata.openclaw.* nesting.
// Dropping support would not fail loudly — the skill would parse with EMPTY
// requirements, and the runner would then tell the agent an installed tool is
// missing. So the legacy form stays readable.
func TestParseMetaLegacyOpenclawFormStillParses(t *testing.T) {
	meta, _ := skilllibrary.ParseMeta(`---
name: legacy
description: an imported skill still using the vendor-namespaced nesting
metadata:
  openclaw:
    requires:
      bins: [pandoc]
      env: [OLD_KEY]
    install:
      - kind: pip
        package: pypdf
---

Body.`)

	require.Equal(t, []string{"pandoc"}, meta.RequiresBins)
	require.Equal(t, []string{"OLD_KEY"}, meta.RequiresEnv)
	require.Len(t, meta.Install, 1)
	require.Equal(t, "pypdf", meta.Install[0].Package)
}

// Precedence is per FIELD, not all-or-nothing: a half-converted file (flat
// `requires`, legacy `install`) must still yield both. An all-or-nothing rule
// would silently drop the install list here.
func TestParseMetaFlatWinsPerField(t *testing.T) {
	meta, _ := skilllibrary.ParseMeta(`---
name: mixed
description: flat requires alongside a legacy install block
metadata:
  requires:
    bins: [flatbin]
  openclaw:
    requires:
      bins: [legacybin]
    install:
      - kind: pip
        package: leftover
---

Body.`)

	require.Equal(t, []string{"flatbin"}, meta.RequiresBins, "flat requires must win")
	require.Len(t, meta.Install, 1, "legacy install must survive when there is no flat one")
	require.Equal(t, "leftover", meta.Install[0].Package)
}

// Nothing this platform SHIPS may use the legacy nesting — core skills are the
// worked examples the skill designer's generated output is compared against,
// and the skill-creator/skill-vetter bodies are literally read by the model as
// the format to follow. One straggler teaches the wrong shape.
func TestCoreSkillsUseFlatMetadata(t *testing.T) {
	entries, err := os.ReadDir(skillsRoot)
	require.NoError(t, err)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(skillsRoot, e.Name(), "SKILL.md"))
		require.NoError(t, readErr)
		require.NotContains(t, string(raw), "openclaw",
			"core skill %q still references the legacy metadata.openclaw nesting; "+
				"requirements belong directly under metadata (requires + sibling install)", e.Name())
	}
}
