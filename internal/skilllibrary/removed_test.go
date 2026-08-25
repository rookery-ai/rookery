package skilllibrary

import (
	"strings"
	"testing"
)

// removedSkills were deleted deliberately and must stay deleted.
//
// This mirrors connectors.TestRemovedProvidersStayRemoved, and exists for the
// same reason: the obvious fix for "the playwright skill is missing" is to write
// the file back. Doing so would ship a skill that teaches a model to hand-write
// Playwright in Python — against a native browser tool that now does the same
// job properly, with the sandbox, the address guard, the secret redaction and
// the acting grants that a hand-rolled script has none of.
//
// A skill and a tool competing for the same job is worse than either alone: the
// weak models this platform runs pick badly between them, which was the whole
// reason the tool was built.
var removedSkills = map[string]string{
	"playwright-browser": "superseded by the native browser_* tools (see " +
		"docs/superpowers/specs/2026-08-25-browser-automation-design.md)",
}

func TestRemovedSkillsStayRemoved(t *testing.T) {
	for _, m := range LoadBundled() {
		if why, gone := removedSkills[m.Name]; gone {
			t.Errorf("%q was re-added; it was removed because it is %s", m.Name, why)
		}
	}
}

// Nothing may point users at a skill that no longer exists. A dangling
// reference in another skill's body is invisible until a model follows it and
// finds nothing.
func TestNoBundledSkillReferencesARemovedOne(t *testing.T) {
	for _, m := range LoadBundled() {
		body, ok := CoreSkillContent(m.Name)
		if !ok {
			t.Fatalf("CoreSkillContent(%q) returned nothing", m.Name)
		}
		for removed := range removedSkills {
			if strings.Contains(body, removed) {
				t.Errorf("skill %q still refers to the removed skill %q", m.Name, removed)
			}
		}
	}
}
