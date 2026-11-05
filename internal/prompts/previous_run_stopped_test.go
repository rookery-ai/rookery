package prompts

import (
	"strings"
	"testing"
)

// The next run is told only that state may be incomplete. It gets no time, steps or
// summary of the stopped run, and must not raise the subject with the owner.
func TestPreviousRunStoppedBlock(t *testing.T) {
	without := BuildCoderPrompt(CoderPromptParams{AgentMD: "do the thing"})
	if strings.Contains(without, "<previous_run>") {
		t.Fatal("the notice must only appear when the previous run was stopped")
	}

	with := BuildCoderPrompt(CoderPromptParams{AgentMD: "do the thing", PreviousRunStopped: true})
	start := strings.Index(with, "<previous_run>")
	end := strings.Index(with, "</previous_run>")
	if start < 0 || end < start {
		t.Fatal("the notice is missing from the prompt")
	}
	block := strings.ToLower(with[start:end])
	for _, want := range []string{"stopped", "state", "fix", "do not mention"} {
		if !strings.Contains(block, want) {
			t.Errorf("notice should mention %q, got: %s", want, block)
		}
	}
}
