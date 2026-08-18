package agentdesigner

import (
	"errors"
	"strings"
	"testing"
)

// A build that dies on a provider error used to return a raw error and delete its
// working directory, calling neither recordGenerationFailure nor saveDraft. Observed:
// a build ran 488 seconds, failed, and left the draft's updated_at ELEVEN SECONDS
// OLDER than the build's own start — so the user watched eight minutes of "building",
// landed back on the plan, and was told nothing at all.
//
// The message must name the likely cause without echoing the provider's error text:
// buildErrClass exists precisely because a provider error can quote back the request
// that produced it, which CodeQL traced to the workspace's API key.
func TestHardFailureMessageIsActionableAndLeaksNothing(t *testing.T) {
	secret := "sk-workspace-secret-key"
	err := errors.New("coder api error: 502 from provider, request={\"key\":\"" + secret + "\"}")

	got := hardFailureMessage(err)

	if strings.Contains(got, secret) {
		t.Fatalf("the provider's error text leaked into a user-facing message: %q", got)
	}
	if got == "" {
		t.Fatal("a hard failure must always produce a message — silence is the bug")
	}
	if !strings.Contains(strings.ToLower(got), "try") && !strings.Contains(strings.ToLower(got), "again") {
		t.Errorf("the message does not tell the user what to do next: %q", got)
	}
}
