package agentdesigner

import (
	"os"
	"strings"
	"testing"
)

// TestDesignConversationUsesTheReadOnlyProfile pins that the agent design
// conversation is offered the read-only tools rather than none.
//
// It is a source-level assertion because callCoder is unexported and takes its
// coder from an injected factory, so there is no seam to observe the profile
// through without building a whole Flow. The same technique packaging/scripts_test.go
// uses for the shell installers, and for the same reason: what it guards is that the
// two designers do not drift apart, which CLAUDE.md records as a recurring cost.
func TestDesignConversationUsesTheReadOnlyProfile(t *testing.T) {
	body := mustReadFlow(t)
	if !strings.Contains(body, "WithReadOnlyTools()") {
		t.Error("agent design conversation does not use WithReadOnlyTools")
	}
	if strings.Contains(body, "coderSvc.WithNoTools().Chat(") {
		t.Error("agent design conversation still calls WithNoTools().Chat")
	}
}

// TestDesignConversationRunsInTheVault guards the CLI engine specifically: with
// no WithDir the subprocess runs in the per-workspace claude-home, which holds
// coder credentials, rather than the vault the tools are meant to read.
func TestDesignConversationRunsInTheVault(t *testing.T) {
	if !strings.Contains(mustReadFlow(t), "WithReadOnlyTools().WithDir(") {
		t.Error("the read-only design coder does not set its working directory to the vault root")
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
