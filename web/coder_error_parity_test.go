package web

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/agentrunner"
	codersvc "github.com/rookery-ai/rookery/internal/coder"
)

// knownCoderFailures is the set of coder failures that have a remedy a user can
// act on. Every one of them must be classified by BOTH user-facing surfaces.
//
// This list is the contract. Adding a sentinel to internal/coder and wiring it
// into one classifier is the mistake this test exists to catch: the surfaces are
// two independent switches in two packages, so nothing else makes a missing arm
// visible — it just renders as the generic fallback, which is exactly how a dead
// local model server came to report "see the server log for details" for as long
// as it did.
var knownCoderFailures = []error{
	codersvc.ErrRateLimited,
	codersvc.ErrUsageLimit,
	codersvc.ErrProviderEmpty,
	codersvc.ErrCoderUnreachable,
	codersvc.ErrTimeout,
}

// The two classifiers are deliberately NOT one function: a run says "it will
// retry on the next scheduled run" and a chat turn says "try again", and
// flattening that into shared copy with a mode flag would make both worse. What
// must not diverge is WHICH failures each one recognises, so that is what this
// pins.
func TestBothSurfacesClassifyEveryKnownCoderFailure(t *testing.T) {
	for _, sentinel := range knownCoderFailures {
		// Wrapped, because that is how these arrive in production — the runner
		// prefixes "coder generate: " — and errors.Is must see through it.
		err := fmt.Errorf("coder generate: %w: some detail", sentinel)

		if got := chatTurnFailureMessage(err); got == chatTurnGenericFailure {
			t.Errorf("chat surface does not classify %v — it fell through to the generic message", sentinel)
		}
		if got := agentrunner.FriendlyRunError(err, "the coder"); strings.HasPrefix(got, agentrunner.GenericRunFailurePrefix) {
			t.Errorf("agent-run surface does not classify %v — it fell through to the raw error", sentinel)
		}
	}
}

// An error nobody classified must still reach the generic arm on both surfaces.
// That fallback is deliberate, not an oversight: an unrecognised error is
// exactly the case where its contents are unknown, and a chat failure message is
// written into the transcript, reflected into the vault and relayable to a chat
// platform.
func TestAnUnknownFailureStillFallsThroughOnBothSurfaces(t *testing.T) {
	err := errors.New("something nobody anticipated")

	if got := chatTurnFailureMessage(err); got != chatTurnGenericFailure {
		t.Errorf("chat surface = %q, want the generic message for an unclassified error", got)
	}
	if got := agentrunner.FriendlyRunError(err, "the coder"); !strings.HasPrefix(got, agentrunner.GenericRunFailurePrefix) {
		t.Errorf("agent-run surface = %q, want the generic prefix for an unclassified error", got)
	}
}

// An unreachable coder must name what could not be reached on both surfaces.
// The sentence exists for its detail: without it this reads as "something went
// wrong", which is the state this whole change set replaced.
func TestBothSurfacesNameWhatCouldNotBeReached(t *testing.T) {
	err := fmt.Errorf("coder generate: %w: could not reach the model %q at %s",
		codersvc.ErrCoderUnreachable, "qwen3:8b", "http://localhost:11434/v1")

	for name, msg := range map[string]string{
		"chat":     chatTurnFailureMessage(err),
		"agentrun": agentrunner.FriendlyRunError(err, "the coder"),
	} {
		if !strings.Contains(msg, "qwen3:8b") || !strings.Contains(msg, "localhost:11434") {
			t.Errorf("%s surface = %q, want it to name the model and the endpoint", name, msg)
		}
		// The sentinel's own text is machine wording; repeating it inside the
		// sentence reads as a leaked internal string.
		if strings.Contains(msg, codersvc.ErrCoderUnreachable.Error()+":") {
			t.Errorf("%s surface = %q, want the detail without the sentinel prefix", name, msg)
		}
	}
}
