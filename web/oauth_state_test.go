package web

import (
	"strings"
	"testing"
	"time"
)

// The state payload must round-trip 4-, 5- and 6-field shapes. The older shapes
// still arrive during the 10-minute TTL after a deploy.
func TestStatePayloadShapesRoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now()
	for _, payload := range []string{
		"ws~google~default~nonce",
		"ws~google~default~nonce~aW5wdXRz",
		"ws~google~default~nonce~aW5wdXRz~https://agents.example.com/dashboard/connectors/services/callback/google",
	} {
		got, ok := verifyState(key, signState(key, payload, now), now)
		if !ok || got != payload {
			t.Fatalf("round-trip failed for %q (ok=%v got=%q)", payload, ok, got)
		}
		if n := len(strings.Split(got, "~")); n < 4 || n > 6 {
			t.Fatalf("unexpected field count %d", n)
		}
	}
}

func TestStateRejectsTamperedPayload(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now()
	tok := signState(key, "ws~google~default~nonce~~https://evil.example.com/cb", now)
	// Flip a byte in the encoded token.
	b := []byte(tok)
	b[len(b)/2] ^= 0x01
	if _, ok := verifyState(key, string(b), now); ok {
		t.Fatalf("tampered state must not verify")
	}
}

// redirectURIFromState is the accessor the callback uses; it must tolerate the
// older shapes by returning "" rather than panicking on a short slice.
func TestRedirectURIFromState(t *testing.T) {
	cases := []struct{ payload, want string }{
		{"ws~google~default~nonce", ""},
		{"ws~google~default~nonce~aW5w", ""},
		{"ws~google~default~nonce~aW5w~https://a.example.com/cb", "https://a.example.com/cb"},
		{"ws~google~default~nonce~~https://a.example.com/cb", "https://a.example.com/cb"},
	}
	for _, tc := range cases {
		if got := redirectURIFromState(strings.Split(tc.payload, "~")); got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.payload, got, tc.want)
		}
	}
}

// A pinned URI that disagrees with what we would compute now must still be USED,
// not rejected: the user has already granted consent, and the pinned string is
// the one the provider validated. Rejecting would bounce them into a loop.
func TestPinnedURIWinsOverCurrentComputation(t *testing.T) {
	pinned := "https://old.example.com/dashboard/connectors/services/callback/google"
	parts := strings.Split("ws~google~default~nonce~~"+pinned, "~")
	if got := redirectURIFromState(parts); got != pinned {
		t.Fatalf("pinned URI must be returned verbatim, got %q", got)
	}
}
