package gateway

import (
	"strings"
	"testing"
)

// Linking is wizard step 4's job, uniform across platforms. A setup step that
// tells the user to DM the bot is how the false "OR just DM it after
// connecting" branch got in — it read plausibly and nothing checked it.
func TestSetupStepsDoNotInstructLinking(t *testing.T) {
	specs := CredSpecs()
	// Without this, a registration failure would empty the list and both
	// checks below would pass by vacuum — the exact "green against a broken
	// implementation" failure these tests exist to prevent.
	if len(specs) == 0 {
		t.Fatal("no CredSpecs registered — the guard would pass vacuously")
	}
	banned := []string{
		"/start",
		"dm it", "dm the bot", "dm your bot", "dm the app",
		"open a dm", "send a dm",
		"message the bot", "text the bot", "chat with the bot",
		"reply to the bot", "ping the bot",
	}
	for _, spec := range specs {
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

// TestSetupStepsHaveNoMultiClauseSemicolons is a regression pin, not a general
// "one action per step" check. Slack's old step 3 chained four actions with
// semicolons and was unfollowable; this fails the build if that specific
// pattern returns. It deliberately does NOT catch comma-joined actions —
// detecting those without false-positiving on ordinary prose is not something
// a substring rule can do, and a check that overstates its own coverage is
// worse than one that states it plainly.
func TestSetupStepsHaveNoMultiClauseSemicolons(t *testing.T) {
	specs := CredSpecs()
	// Without this, a registration failure would empty the list and both
	// checks below would pass by vacuum — the exact "green against a broken
	// implementation" failure these tests exist to prevent.
	if len(specs) == 0 {
		t.Fatal("no CredSpecs registered — the guard would pass vacuously")
	}
	for _, spec := range specs {
		for i, step := range spec.SetupSteps {
			if strings.Count(step, ";") > 1 {
				t.Errorf("%s step %d packs several actions: %q", spec.Platform, i+1, step)
			}
		}
	}
}
