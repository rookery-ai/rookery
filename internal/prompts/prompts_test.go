package prompts

import (
	"strings"
	"testing"
)

// v3 markers that must be present wherever the coder writes Composio code.
const (
	v3Host    = "backend.composio.dev/api/v3"
	v3Connect = "connected_accounts"
)

// TestComposioSpecPresentInEveryPhase is the regression guard for the bug where
// the v3 spec lived only in the design conversation, so generation wrote v1 code.
// Every phase that produces or guides code must carry the v3 spec when Composio
// is enabled — and must not leak it when disabled.
func TestComposioSpecPresentInEveryPhase(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "fetch my gmail"}}

	phases := map[string]struct {
		enabled  string
		disabled string
	}{
		"design": {
			enabled:  BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x", ComposioEnabled: true}),
			disabled: BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x", ComposioEnabled: false}),
		},
		"create": {
			enabled:  BuildImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: true}),
			disabled: BuildImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: false}),
		},
		"edit": {
			enabled:  BuildEditImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: true}),
			disabled: BuildEditImplementationPrompt("x", history, ImplementationParams{ComposioEnabled: false}),
		},
	}

	for name, p := range phases {
		if !strings.Contains(p.enabled, v3Host) || !strings.Contains(p.enabled, v3Connect) {
			t.Errorf("[%s] enabled prompt missing v3 spec (%q / %q)", name, v3Host, v3Connect)
		}
		// The block warns against v1 — confirm that guidance travels too.
		if !strings.Contains(p.enabled, "v3") {
			t.Errorf("[%s] enabled prompt missing v3 guidance", name)
		}
		if strings.Contains(p.disabled, v3Host) {
			t.Errorf("[%s] disabled prompt should not contain the Composio block", name)
		}
	}
}

// TestConnectedPlatformsInGeneration verifies platform context reaches generation.
func TestConnectedPlatformsInGeneration(t *testing.T) {
	out := BuildImplementationPrompt("x", nil, ImplementationParams{ConnectedPlatforms: []string{"Telegram"}})
	if !strings.Contains(out, "Telegram") || !strings.Contains(out, "[CHAT]") {
		t.Errorf("generation prompt missing connected-platform guidance:\n%s", out)
	}
}
