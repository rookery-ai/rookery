package composioassets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHelperFilesWritesBothFilesToAgentToolsDir(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := WriteHelperFiles(toolsDir); err != nil {
		t.Fatalf("WriteHelperFiles: %v", err)
	}

	helper, err := os.ReadFile(filepath.Join(toolsDir, HelperFilename))
	if err != nil {
		t.Fatalf("read %s: %v", HelperFilename, err)
	}
	if !strings.Contains(string(helper), "def composio_execute") {
		t.Fatalf("%s missing composio_execute", HelperFilename)
	}
	if !strings.Contains(string(helper), "def list_tools") {
		t.Fatalf("%s missing list_tools (generic discovery)", HelperFilename)
	}
	// Regression guard: errors must be classified (network vs server-error), never
	// collapsed into one vague "unreachable" message that misattributes a Composio-side
	// 5xx as a network problem.
	if !strings.Contains(string(helper), "class ComposioConnectionError") {
		t.Fatalf("%s missing ComposioConnectionError (network-failure classification)", HelperFilename)
	}
	if !strings.Contains(string(helper), "class ComposioServerError") {
		t.Fatalf("%s missing ComposioServerError (persistent-5xx classification)", HelperFilename)
	}

	discover, err := os.ReadFile(filepath.Join(toolsDir, DiscoverFilename))
	if err != nil {
		t.Fatalf("read %s: %v", DiscoverFilename, err)
	}
	if !strings.Contains(string(discover), "from composio_helper import") {
		t.Fatalf("%s does not import the helper", DiscoverFilename)
	}
}

func TestWriteHelperFilesWritesToSkillScriptsDir(t *testing.T) {
	dir := t.TempDir()
	scriptsDir := filepath.Join(dir, "scripts")
	if err := WriteHelperFiles(scriptsDir); err != nil {
		t.Fatalf("WriteHelperFiles: %v", err)
	}
	for _, name := range []string{HelperFilename, DiscoverFilename} {
		if _, err := os.Stat(filepath.Join(scriptsDir, name)); err != nil {
			t.Fatalf("expected %s under scripts/: %v", name, err)
		}
	}
}

func TestWriteHelperFilesIsIdempotentAndSelfHeals(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	if err := WriteHelperFiles(toolsDir); err != nil {
		t.Fatalf("WriteHelperFiles (first): %v", err)
	}

	// Simulate a prior LLM-corrupted copy (e.g. a model that "helpfully" rewrote it
	// despite instructions, or an older/broken version from before this package existed).
	corrupted := []byte("# corrupted by a coder that ignored instructions\n")
	if err := os.WriteFile(filepath.Join(toolsDir, HelperFilename), corrupted, 0o640); err != nil {
		t.Fatalf("simulate corruption: %v", err)
	}

	if err := WriteHelperFiles(toolsDir); err != nil {
		t.Fatalf("WriteHelperFiles (second, re-seed): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(toolsDir, HelperFilename))
	if err != nil {
		t.Fatalf("read after re-seed: %v", err)
	}
	if strings.Contains(string(got), "corrupted") {
		t.Fatalf("re-seeding did not overwrite the corrupted copy")
	}
	if !strings.Contains(string(got), "def composio_execute") {
		t.Fatalf("re-seeded file missing composio_execute")
	}
}

func TestIsSeededFilename(t *testing.T) {
	cases := map[string]bool{
		HelperFilename:   true,
		DiscoverFilename: true,
		"gmail_draft.py": false,
		"other.py":       false,
	}
	for name, want := range cases {
		if got := IsSeededFilename(name); got != want {
			t.Errorf("IsSeededFilename(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBuildPhaseConstantsMatchWhatComposioHelperChecks(t *testing.T) {
	// The Python side reads these exact strings (BUILD_PHASE_ENV_VAR /
	// BUILD_PHASE_GENERATION in assets/composio_helper.py) — if either drifts from the
	// Go constants, the build-time send-guard silently stops firing. Cheap to assert
	// both sides agree rather than relying on manual sync.
	data, err := assetsFS.ReadFile("assets/" + HelperFilename)
	if err != nil {
		t.Fatalf("read embedded helper: %v", err)
	}
	src := string(data)
	if !strings.Contains(src, `BUILD_PHASE_ENV_VAR = "`+BuildPhaseEnvVar+`"`) {
		t.Fatalf("composio_helper.py's BUILD_PHASE_ENV_VAR does not match composioassets.BuildPhaseEnvVar (%q)", BuildPhaseEnvVar)
	}
	if !strings.Contains(src, `BUILD_PHASE_GENERATION = "`+BuildPhaseGeneration+`"`) {
		t.Fatalf("composio_helper.py's BUILD_PHASE_GENERATION does not match composioassets.BuildPhaseGeneration (%q)", BuildPhaseGeneration)
	}
}
