package agentrunner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/coder"
)

// A provider outage must not read as a broken agent.
//
// OpenRouter answered 2xx with an empty body seven times over ten minutes. The
// run reached no model — no tokens, no tool calls, nothing partial — and the
// owner was shown the raw internal string "llm: empty response body (status
// 200)". Every other transient failure here (rate limit, quota) has a
// plain-English message and an instruction; this one fell through to
// err.Error(), which is accurate, unactionable, and looks like their agent's
// fault.
func TestFriendlyRunErrorExplainsAnEmptyProviderResponse(t *testing.T) {
	msg := FriendlyRunError(fmt.Errorf("coder generate: %w", coder.ErrProviderEmpty), "the coder")

	if strings.Contains(msg, "empty response body") || strings.Contains(msg, "status 200") {
		t.Errorf("the raw internal error still reaches the user: %q", msg)
	}
	// The two facts a reader needs: it is not their fault, and retrying is the move.
	if !strings.Contains(strings.ToLower(msg), "try again") {
		t.Errorf("no instruction to retry: %q", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "temporary") {
		t.Errorf("does not say the failure is temporary: %q", msg)
	}
}

// The generic branch must stay generic — a failure nobody has classified should
// still show its real error rather than being swallowed by a neighbouring case.
func TestFriendlyRunErrorStillSurfacesUnclassifiedErrors(t *testing.T) {
	msg := FriendlyRunError(fmt.Errorf("something nobody anticipated"), "the coder")
	if !strings.Contains(msg, "something nobody anticipated") {
		t.Errorf("an unclassified error lost its text: %q", msg)
	}
}
