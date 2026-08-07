package agentrunner

import (
	"os"
	"strings"
	"testing"
)

// The agent-name prefix on chat notifications is applied at the three sites
// where SendOutput is the real chat sender (the chat /run handler, the
// scheduler, the web run tracker) and must NEVER be applied inside the runner.
//
// runCoderAgent reuses SendOutput as a COLLECTOR for child-agent recursion:
// the child's messages are gathered and fed to
// prompts.BuildChildAgentFollowUpPrompt, i.e. straight into the PARENT agent's
// LLM prompt. Prefixing here would inject "🤖 **name**" into model input rather
// than into chat, and the same objection applies to recordInbox (inbox_messages
// carries AgentName as its own column) and to OnProgress (the SSE view is
// already scoped to one agent's page).
//
// This is a source guard rather than a behavioural test because reproducing it
// behaviourally needs a full coder round trip, while the invariant itself is
// structural: the helper simply must not be reachable from this file.
func TestRunnerDoesNotApplyTheChatPrefix(t *testing.T) {
	code := goCodeWithoutComments(t, "runner.go")
	for _, banned := range []string{"AgentPrefixed", "🤖 **"} {
		if strings.Contains(code, banned) {
			t.Errorf("runner.go must not apply the chat prefix (found %q) — it would "+
				"leak into the child-agent collector, which feeds the parent's LLM prompt", banned)
		}
	}
}

// goCodeWithoutComments returns path's source with whole-line // comments
// removed, so a guard asserting on CODE is not tripped by a comment that
// explains the very rule being guarded.
func goCodeWithoutComments(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var b strings.Builder
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// The "produced no notification" warning must not repeat the agent name: the
// chat copy is already labelled by gateway.AgentPrefixed and the inbox copy
// carries AgentName as a column, so repeating it reads "🤖 weather … ⚠️ weather
// ran but produced no notification".
func TestNoNotificationWarningDoesNotRepeatTheAgentName(t *testing.T) {
	if strings.Contains(goCodeWithoutComments(t, "runner.go"), `"⚠️ %s ran but produced no notification`) {
		t.Error("the warning must not interpolate the agent name; the send site labels it")
	}
}
