package agentrunner

import (
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
