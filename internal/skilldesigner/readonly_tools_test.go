package skilldesigner

import (
	"os"
	"strings"
	"testing"
)

// TestSkillDesignConversationUsesTheReadOnlyProfile mirrors the agent designer.
// The two share one front end and have drifted before; a capability one has and
// the other lacks is exactly the inconsistency nobody finds until it costs a build.
func TestSkillDesignConversationUsesTheReadOnlyProfile(t *testing.T) {
	body := mustReadFlow(t)
	if !strings.Contains(body, "WithReadOnlyTools()") {
		t.Error("skill design conversation does not use WithReadOnlyTools")
	}
	if !strings.Contains(body, "WithReadOnlyTools().WithDir(") {
		t.Error("the read-only design coder does not set its working directory to the vault root")
	}
}

// TestSkillVetterStaysTextOnly is a CARVE-OUT, not an oversight.
//
// The vetting pass audits generated skill content for exfiltration of vault
// notes, USER.md, SOUL.md and secrets. Handing the auditor file and network
// tools would give the audited content a way to act, so it keeps WithNoTools
// while the conversation beside it does not.
func TestSkillVetterStaysTextOnly(t *testing.T) {
	body := mustReadFlow(t)
	if !strings.Contains(body, "WithNoTools().Chat(ctx, workspaceID, nil, vetterBody, userMsg)") {
		t.Error("the skill vetter no longer runs text-only")
	}
}

func mustReadFlow(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("flow.go")
	if err != nil {
		t.Fatalf("read flow.go: %v", err)
	}
	return string(src)
}
