package skilllibrary

import (
	"strings"
	"testing"
)

func vetterBody(t *testing.T) string {
	t.Helper()
	body, ok := CoreSkillContent("skill-vetter")
	if !ok {
		t.Fatal("skill-vetter is missing")
	}
	return body
}

// A secret persisted to disk outlives the run in a home every agent shares, so the
// vetter blocks it and names the fix.
func TestVetterBlocksSecretsWrittenToDisk(t *testing.T) {
	body := vetterBody(t)
	for _, want := range []string{"Secret written to disk", "Settings → Secrets"} {
		if !strings.Contains(body, want) {
			t.Errorf("skill-vetter lacks %q", want)
		}
	}
}

// Declared pip dependencies installed through install_packages are the design, not a
// red flag. Rating every "package install" HIGH would refuse every skill that does the
// right thing — the regression this pins against.
func TestVetterDoesNotBlockDeclaredDependencies(t *testing.T) {
	body := vetterBody(t)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "| 🔴 HIGH") && strings.Contains(line, "package installs") {
			t.Errorf("HIGH row still blocks every package install: %s", line)
		}
	}
	for _, want := range []string{"#### Dependencies", "install_packages", "hides where it downloads from"} {
		if !strings.Contains(body, want) {
			t.Errorf("skill-vetter lacks %q", want)
		}
	}
}
