package coder

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rookery-ai/rookery/internal/llm"
)

// An unreachable provider must arrive as ErrCoderUnreachable carrying a detail
// that names the model, the endpoint and the provider.
//
// The detail is the whole point. The failure used to fall through to
// mapProviderErr's default arm, and every downstream classifier renders an
// unclassified error as its generic "see the server log" sentence — which is
// what a user reported as chat hanging and then saying nothing. A classified
// error may carry specifics precisely because we generated them ourselves: the
// vague-by-default rule exists for errors whose contents we do not know.
func TestAnUnreachableProviderNamesTheEndpoint(t *testing.T) {
	c := New("claude", time.Minute, t.TempDir(), t.TempDir()).
		WithAPIConfig("ollama", "qwen3:8b", "http://localhost:11434/v1", "CODER_KEY_OLLAMA")

	err := c.mapProviderErr(llm.ErrUnreachable)
	if !errors.Is(err, ErrCoderUnreachable) {
		t.Fatalf("err = %v, want ErrCoderUnreachable", err)
	}
	for _, want := range []string{"qwen3:8b", "http://localhost:11434/v1", "ollama"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not name %q", err.Error(), want)
		}
	}
}

// A CLI coder whose binary is not installed is the same class of failure — the
// thing that was supposed to answer is not there — so it maps to the same
// sentinel, with a detail naming the binary rather than an endpoint. One
// sentinel and not two, because each downstream classifier then needs one arm
// and the specificity lives where the configuration is.
func TestAMissingCoderBinaryIsUnreachable(t *testing.T) {
	c := New("opencode", time.Minute, t.TempDir(), t.TempDir())

	err := c.mapCLIErr(&exec.Error{Name: "opencode", Err: exec.ErrNotFound})
	if !errors.Is(err, ErrCoderUnreachable) {
		t.Fatalf("err = %v, want ErrCoderUnreachable", err)
	}
	if !strings.Contains(err.Error(), "opencode") {
		t.Errorf("message %q does not name the binary", err.Error())
	}
}

// Everything mapProviderErr already classified must keep its own sentinel — the
// new arm must not swallow a rate limit or an auth failure, whose remedies are
// entirely different.
func TestExistingProviderClassificationsAreUnchanged(t *testing.T) {
	c := New("claude", time.Minute, t.TempDir(), t.TempDir())
	cases := []struct {
		in   error
		want error
	}{
		{llm.ErrRateLimit, ErrRateLimited},
		{llm.ErrQuotaExhausted, ErrUsageLimit},
		{llm.ErrAuth, ErrAPIAuth},
		{llm.ErrEmptyResponse, ErrProviderEmpty},
	}
	for _, tc := range cases {
		if got := c.mapProviderErr(tc.in); !errors.Is(got, tc.want) {
			t.Errorf("mapProviderErr(%v) = %v, want %v", tc.in, got, tc.want)
		}
		if errors.Is(c.mapProviderErr(tc.in), ErrCoderUnreachable) {
			t.Errorf("mapProviderErr(%v) was swallowed by the unreachable arm", tc.in)
		}
	}
}

// A CLI failure that is NOT a missing binary keeps its existing shape: a coder
// that ran and exited non-zero has reached the thing it was calling, so calling
// it unreachable would be a lie that sends the user to check an installation
// that is fine.
func TestAnOrdinaryCLIFailureIsNotUnreachable(t *testing.T) {
	c := New("claude", time.Minute, t.TempDir(), t.TempDir())
	if err := c.mapCLIErr(errors.New("exit status 1")); errors.Is(err, ErrCoderUnreachable) {
		t.Errorf("an ordinary non-zero exit was classified as unreachable: %v", err)
	}
}
