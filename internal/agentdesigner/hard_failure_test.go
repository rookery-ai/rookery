package agentdesigner

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rookery-ai/rookery/internal/coder"
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

// hardFailureMessage used to ignore its argument entirely, so EVERY error reaching it was
// reported as "the model provider dropped the connection … type approve to try again". For
// a rejected or expired API key that is a confident wrong diagnosis pointing at a retry
// that can never succeed: the key has to be fixed first, and no number of rebuilds does it.
// The diagnosis comes from buildErrClass, so the provider's own text is still never read.
func TestHardFailureMessageDiagnosesARejectedKeyRatherThanGuessing(t *testing.T) {
	secret := "sk-workspace-secret-key"
	err := fmt.Errorf("coder api error: 401 request={\"key\":\"%s\"}: %w", secret, coder.ErrAPIAuth)

	got := hardFailureMessage(err)

	if strings.Contains(got, secret) {
		t.Fatalf("the provider's error text leaked into a user-facing message: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "dropped the") {
		t.Errorf("an auth failure was misdiagnosed as a dropped connection: %q", got)
	}
	lower := strings.ToLower(got)
	if !strings.Contains(lower, "key") || !strings.Contains(lower, "settings") {
		t.Errorf("the message must name the rejected key and where to fix it: %q", got)
	}
	if !strings.Contains(lower, "won't help") && !strings.Contains(lower, "will not help") {
		t.Errorf("the message must say that retrying cannot fix this: %q", got)
	}
}

// The other class whose REMEDY differs: a slim build has no local coder at all, so the fix
// is switching the workspace to the api coder kind. Retrying changes nothing there either.
func TestHardFailureMessageNamesADisabledLocalCoder(t *testing.T) {
	got := hardFailureMessage(fmt.Errorf("build: %w", coder.ErrLocalCoderDisabled))

	if strings.Contains(strings.ToLower(got), "dropped the") {
		t.Errorf("a disabled local coder was misdiagnosed as a dropped connection: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "coder settings") {
		t.Errorf("the message must point at coder settings, where the fix is: %q", got)
	}
}

// An unclassified error keeps the observed common case's wording — a provider drop, which a
// retry really can fix. A case per class is exactly what this switch must not become.
func TestHardFailureMessageKeepsTheProviderDropWordingForAnUnknownError(t *testing.T) {
	got := hardFailureMessage(errors.New("something nobody has classified"))

	if !strings.Contains(got, "dropped the") {
		t.Errorf("an unclassified error should keep the provider-drop wording: %q", got)
	}
	if !strings.Contains(got, "approve") {
		t.Errorf("the generic message must still offer the retry: %q", got)
	}
}

// Every branch of hardFailureMessage must tell the user what to do next, not only the
// default one. TestHardFailureMessageIsActionableAndLeaksNothing lands on the generic
// case, so the two diagnosing branches - added later, in one commit - had nothing
// asserting they were actionable at all. They were not equally so: the auth case
// originally ended at "until the key is fixed in coder settings" and named no way to
// resume, while its sibling written beside it ended "type approve to try again".
func TestEveryHardFailureBranchNamesAWayForward(t *testing.T) {
	secret := "sk-workspace-secret-key"
	leak := "request={\"key\":\"" + secret + "\"}"

	for _, c := range []struct {
		name string
		err  error
	}{
		{"auth", fmt.Errorf("coder api error: 401 %s: %w", leak, coder.ErrAPIAuth)},
		{"local_coder_disabled", fmt.Errorf("coder: %s: %w", leak, coder.ErrLocalCoderDisabled)},
		{"other", errors.New("coder api error: 502 " + leak)},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := hardFailureMessage(c.err)

			if strings.Contains(got, secret) {
				t.Fatalf("the provider error text leaked into a user-facing message: %q", got)
			}
			// "approve" is the word the designer own approval trigger accepts, so a
			// message that names a fix without naming it leaves the user with a repaired
			// key and no idea how to resume.
			if !strings.Contains(strings.ToLower(got), "approve") {
				t.Errorf("%s: the message never tells the user how to resume: %q", c.name, got)
			}
		})
	}
}
