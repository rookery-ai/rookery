package gateway

import "testing"

func TestAgentPrefixedLabelsTheMessage(t *testing.T) {
	got := AgentPrefixed("weather", "25°C, clear sky")
	want := "🤖 **weather**\n\n25°C, clear sky"
	if got != want {
		t.Fatalf("AgentPrefixed = %q, want %q", got, want)
	}
}

func TestAgentPrefixedLeavesBlankInputsAlone(t *testing.T) {
	if got := AgentPrefixed("", "body"); got != "body" {
		t.Fatalf("an empty agent name must not add a prefix, got %q", got)
	}
	if got := AgentPrefixed("weather", "   "); got != "   " {
		t.Fatalf("a blank body must pass through, got %q", got)
	}
}

// A message must never be labelled twice. Three call sites apply this helper;
// if any two ever overlap, the second must be a no-op rather than stacking a
// second header onto the user's message.
func TestAgentPrefixedDoesNotDoublePrefix(t *testing.T) {
	once := AgentPrefixed("weather", "body")
	if twice := AgentPrefixed("weather", once); twice != once {
		t.Fatalf("double prefix: %q", twice)
	}
}

// The prefix rides the same neutral-CommonMark path every adapter renders on
// send, so it must not contain platform-specific escaping.
func TestAgentPrefixedIsNeutralCommonMark(t *testing.T) {
	got := AgentPrefixed("my agent", "hello")
	if want := "🤖 **my agent**\n\nhello"; got != want {
		t.Fatalf("AgentPrefixed = %q, want %q", got, want)
	}
}
