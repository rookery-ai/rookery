package prompts

import (
	"strings"
	"testing"
)

// A chat once spent over ten minutes rephrasing a web search for a code that
// existed nowhere it could reach. The engine now caps fruitless searching, but
// the model should not need the cap: it is told, up front, when to give up and
// what a useful "I could not find it" answer contains.
func TestChatPromptSaysWhenToStopLooking(t *testing.T) {
	for _, backend := range []string{"api", "claude"} {
		p := BuildChatSystemPrompt("/vault", backend, nil, nil, "", nil, false)
		if !strings.Contains(p, "## When to stop looking") {
			t.Fatalf("backend %q: chat prompt has no stop-looking guidance", backend)
		}
		if !strings.Contains(p, "could not find") {
			t.Fatalf("backend %q: the guidance must say to report what could not be found", backend)
		}
	}
}

func TestStopLookingGuidanceIsChatOnly(t *testing.T) {
	p := BuildCoderPrompt(CoderPromptParams{AgentMD: "do things"})
	if strings.Contains(p, "## When to stop looking") {
		t.Fatal("the stop-looking section is chat guidance; agent runs must not carry it")
	}
}
