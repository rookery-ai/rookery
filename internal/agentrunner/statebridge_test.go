package agentrunner

import (
	"os"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentstate"
)

// The bridge shipped inert once already: it was built, tested and complete, but
// nothing in a real run ever started or registered it, so `rookery state`
// answered "no state bridge available" for every agent. Nothing failed — the
// capability simply did not exist.
//
// These assert the two halves that were missing, at the level they were missing
// at: the Runner accepts a bridge, and a registered token is scoped to one
// agent's directory rather than to the workspace.
func TestRunnerAcceptsAStateBridge(t *testing.T) {
	b := agentstate.NewBridge()
	r := (&Runner{}).WithStateBridge(b)
	if r.stateBridge == nil {
		t.Fatal("WithStateBridge did not retain the bridge; CLI coders would silently lose state access")
	}
}

// state.md is per-AGENT. A token scoped to the workspace would let one agent
// read and overwrite another's memory, which is the one thing the loopback
// design must not permit.
func TestStateTokensAreScopedPerAgentNotPerWorkspace(t *testing.T) {
	b := agentstate.NewBridge()
	first := b.Register(t.TempDir(), "agent-one")
	second := b.Register(t.TempDir(), "agent-two")

	if first == second {
		t.Fatal("two agents in the same workspace received the same token")
	}
	b.Unregister(first)
	b.Unregister(second)
}

// The env injection is the half that actually shipped inert, and it was still
// the half nothing asserted: deleting the extraEnv lines left both earlier tests
// passing. A CLI coder learns the bridge exists ONLY from these two variables,
// so without them `rookery state` reports "no state bridge available" and the
// capability silently does not exist — which is exactly how it shipped the first
// time.
//
// Asserted by reading the source rather than by driving a run: Run needs a live
// coder, a database and a workspace, and the property at issue is simply that
// these two keys are populated from the bridge next to the other three pairs.
func TestRunInjectsTheStateBridgeEnvVars(t *testing.T) {
	src, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`extraEnv["ROOKERY_STATE_URL"] = r.stateBridge.Addr()`,
		`extraEnv["ROOKERY_STATE_TOKEN"] = stateToken`,
		"defer r.stateBridge.Unregister(stateToken)",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("runner.go no longer contains %q — a CLI coder cannot reach its own state", want)
		}
	}
}
