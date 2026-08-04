package gateway

import (
	"strings"
	"testing"
)

// Linking is wizard step 4's job, uniform across platforms. A setup step that
// tells the user to DM the bot is how the false "OR just DM it after
// connecting" branch got in — it read plausibly and nothing checked it.
func TestSetupStepsDoNotInstructLinking(t *testing.T) {
	banned := []string{"/start", "dm it", "dm your bot", "dm the bot", "message the bot"}
	for _, spec := range CredSpecs() {
		for i, step := range spec.SetupSteps {
			lower := strings.ToLower(step)
			for _, b := range banned {
				if strings.Contains(lower, b) {
					t.Errorf("%s step %d instructs linking (%q): %q", spec.Platform, i+1, b, step)
				}
			}
		}
	}
}

// Without MESSAGE CONTENT INTENT the bot connects, reports healthy and receives
// every DM with an empty body — a silent failure worth pinning.
func TestDiscordSetupStepsNameTheMessageContentIntent(t *testing.T) {
	spec, ok := CredSpecFor("discord")
	if !ok {
		t.Fatal("discord spec not registered")
	}
	joined := strings.ToUpper(strings.Join(spec.SetupSteps, "\n"))
	if !strings.Contains(joined, "MESSAGE CONTENT INTENT") {
		t.Fatalf("discord steps never name the intent:\n%s", strings.Join(spec.SetupSteps, "\n"))
	}
}

// Every step should be one action. A step carrying several semicolons is the
// dense-instruction smell that made Slack's step 3 unfollowable.
func TestSetupStepsAreSingleActions(t *testing.T) {
	for _, spec := range CredSpecs() {
		for i, step := range spec.SetupSteps {
			if strings.Count(step, ";") > 1 {
				t.Errorf("%s step %d packs several actions: %q", spec.Platform, i+1, step)
			}
		}
	}
}
