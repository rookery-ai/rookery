package prompts

import (
	"strings"
	"testing"
)

func TestVettingPromptNamesTheNewRedFlags(t *testing.T) {
	p := BuildSkillVettingPrompt("s", "---\nname: s\n---\n", nil, "")
	for _, want := range []string{"secrets written to disk", "install sources that hide where they download from"} {
		if !strings.Contains(p, want) {
			t.Errorf("vetting prompt lacks %q", want)
		}
	}
}
