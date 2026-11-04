package prompts

import (
	"strings"
	"testing"
)

// The acting block must steer a model away from repeating a call the page has
// already answered, such as re-filling a field or walking a dropdown with arrows.
func TestBrowserBlockSaysReadInsteadOfRepeating(t *testing.T) {
	for _, backend := range []string{BackendToolCalling, BackendFullCoder} {
		b := browserToolsBlock(backend, true, false)
		for _, want := range []string{"re-read the page instead of", "filled from ${NAME}", "never walk it with arrow keys"} {
			if !strings.Contains(b, want) {
				t.Errorf("%s: block lacks %q", backend, want)
			}
		}
	}
	// A reading-only surface (chat) is not told about acting.
	if strings.Contains(browserToolsBlock(BackendToolCalling, false, false), "re-read the page instead of") {
		t.Error("acting guidance leaked into the read-only block")
	}
}
