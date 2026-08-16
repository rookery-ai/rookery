package coder

import (
	"context"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/llm"
	"github.com/rookery-ai/rookery/internal/prompts"
)

// The API engine used to send the PROTOCOL kickoff on every runAPI call, so a
// one-shot text call — the KB rewrite panel, skill-metadata extraction, reminder
// parsing, Ping — was explicitly told to wrap its answer in [CHAT], and a
// well-behaved model did. The reported symptom was a stray marker in the KB
// panel; the quieter half was the two JSON callers failing to parse and falling
// back without a word.
//
// It is API-engine-only: a CLI coder's Generate never sees this message, so the
// same install exhibits it or not depending on coder_kind.
func TestRunAPIPicksTheKickoffByToolAvailability(t *testing.T) {
	for _, tt := range []struct {
		name        string
		noTools     bool
		wantKickoff string
	}{
		{"text-only call gets the plain-content kickoff", true, prompts.APIEngineTextKickoffMessage},
		{"a tool-using run still gets the output protocol", false, prompts.APIEngineKickoffMessage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			testFake.calls = 0
			testFake.script = func(_ int, req llm.Request) (*llm.Response, error) {
				got = lastUserMessage(req)
				return &llm.Response{Content: "done"}, nil
			}

			c := newTestCoder(t, t.TempDir())
			if tt.noTools {
				c = c.WithNoTools()
			}
			if _, err := c.Generate(context.Background(), "ws1", "do the thing"); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if got != tt.wantKickoff {
				t.Errorf("kickoff = %q,\nwant %q", got, tt.wantKickoff)
			}
		})
	}
}

// Both halves are pinned because deleting the protocol clause outright would
// look like a tidy-up and would break every agent run: agentrunner.parseCoderOutput
// reads exactly these markers.
func TestKickoffMessagesDisagreeAboutTheProtocol(t *testing.T) {
	if !strings.Contains(prompts.APIEngineKickoffMessage, "[CHAT]") {
		t.Error("the tool-using kickoff no longer asks for the output protocol — agent runs depend on it")
	}
	for _, marker := range []string{"[CHAT]", "[STATE]", "[SILENT]"} {
		if strings.Contains(prompts.APIEngineTextKickoffMessage, marker) {
			t.Errorf("the text-only kickoff mentions %s — that is the whole bug", marker)
		}
	}
}
