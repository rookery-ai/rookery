package prompts

import (
	"strings"
	"testing"
)

// The designer told a user to "save your login email and password in Secrets
// under TOOLSHOP_EMAIL and TOOLSHOP_PASSWORD" — secrets that already existed —
// because nothing told it which names were stored. It must see the NAMES (never
// values) and be told to reuse a matching one instead of asking for it again.
func TestDesignPromptListsStoredSecretNames(t *testing.T) {
	got := BuildDesignSystemPrompt(DesignSystemParams{
		AgentName:   "Toolshop restock",
		SecretNames: []string{"CARD_NUMBER", "CODER_KEY_OPENROUTER", "SEARCH_KEY_BRAVE", "TOOLSHOP_EMAIL"},
	})
	for _, want := range []string{"TOOLSHOP_EMAIL", "CARD_NUMBER"} {
		if !strings.Contains(got, want) {
			t.Errorf("stored secret %s not shown to the designer", want)
		}
	}
	// The platform's own keys are not credentials an agent should be built around.
	for _, hidden := range []string{"CODER_KEY_OPENROUTER", "SEARCH_KEY_BRAVE"} {
		if strings.Contains(got, hidden) {
			t.Errorf("platform-internal secret %s leaked into the design prompt", hidden)
		}
	}
	if !strings.Contains(got, "do not ask the user to add it again") {
		t.Error("the designer is not told to reuse an existing secret")
	}
}

func TestDesignPromptSaysWhenNoSecretsAreStored(t *testing.T) {
	got := BuildDesignSystemPrompt(DesignSystemParams{AgentName: "x"})
	if !strings.Contains(got, "No secrets are stored yet") {
		t.Error("an empty store is not stated, so the designer cannot tell 'none' from 'not told'")
	}
}
